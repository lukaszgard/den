// Package aws implements a service.Service for AWS SSO sessions.
// Each SSO session from ~/.aws/config becomes a row in the Connect list,
// showing token TTL and allowing login via the standard service controls.
package aws

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/process"
	"github.com/lukaszgard/den/internal/service"
)

// Service implements service.Service for an AWS SSO session.
type Service struct {
	mu sync.RWMutex
	// name is what the UI shows; session is the [sso-session X] section name.
	// They differ when den.yaml gives the session a display name, and only
	// session may ever be passed to `aws sso login --sso-session` or used as a
	// token-cache key.
	name        string
	session     string
	description string
	startURL    string
	ssoRegion   string
	awsCfgPath  string
	ssoCacheDir string
	profile     string // first profile referencing this session (for export-credentials)
	state       service.RuntimeState
	updates     chan service.StatusUpdate
	cancel      context.CancelFunc
	logs        *process.RingBuffer
}

// New creates an AWS SSO service. It reads the token cache on creation so the
// Connect list shows the current SSO token status immediately.
func New(name, session, description, startURL, ssoRegion, awsCfgPath, ssoCacheDir, profile string) *Service {
	if name == "" {
		name = session
	}
	s := &Service{
		name:        name,
		session:     session,
		description: description,
		startURL:    startURL,
		ssoRegion:   ssoRegion,
		awsCfgPath:  awsCfgPath,
		ssoCacheDir: ssoCacheDir,
		profile:     profile,
		updates:     make(chan service.StatusUpdate, 32),
		state:       service.RuntimeState{Status: service.StatusIdle},
		logs:        process.NewRingBuffer(200),
	}
	s.checkToken()
	return s
}

func (s *Service) Info() service.ServiceInfo {
	return service.ServiceInfo{Name: s.name, Type: service.TypeAWS}
}

// Session returns the [sso-session X] name, which is what identifies this
// service to the AWS CLI regardless of the display name.
func (s *Service) Session() string { return s.session }

// StartURL returns the session's sso_start_url, for opening the SSO portal.
func (s *Service) StartURL() string { return s.startURL }

// Description satisfies service.Describer so a curated description shows in the
// TUI without any extra wiring.
func (s *Service) Description() string { return s.description }

// State reports a token that has run out as idle. Connected only means the cache
// held a valid token when it was last read, and a token found at startup has no
// login of den's watching it, so without this it stayed connected for good and
// connecting again answered "already running". TokenExpires stays, so the row
// still says the token expired.
func (s *Service) State() service.RuntimeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.state
	if st.Status == service.StatusConnected && st.TokenExpires != nil && time.Until(*st.TokenExpires) <= 0 {
		st.Status = service.StatusIdle
	}
	return st
}

func (s *Service) Logs() []string { return s.logs.Lines() }

func (s *Service) LogSeq() uint64 { return s.logs.Seq() }

func (s *Service) ConnectCommand() string {
	return fmt.Sprintf("aws sso login --sso-session %s", s.session)
}

// RefreshToken re-reads the SSO token cache and updates the displayed state.
// It does NOT trigger a new SSO login.
func (s *Service) RefreshToken(_ context.Context) error {
	s.checkToken()
	s.sendUpdate()
	return nil
}

func (s *Service) Start(ctx context.Context) (<-chan service.StatusUpdate, error) {
	s.mu.Lock()
	if s.state.Status == service.StatusConnecting {
		s.mu.Unlock()
		return s.updates, fmt.Errorf("already logging in")
	}
	s.mu.Unlock()

	// End the previous login's expiry watch: its next tick would re-read the
	// stale cache and set this login idle while the browser is still open.
	loginCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	previous := s.cancel
	s.cancel = cancel
	s.mu.Unlock()
	if previous != nil {
		previous()
	}

	go s.run(loginCtx)
	return s.updates, nil
}

func (s *Service) Stop(_ context.Context) error {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	s.setState(service.RuntimeState{Status: service.StatusIdle})
	return nil
}

func (s *Service) run(ctx context.Context) {
	s.setState(service.RuntimeState{Status: service.StatusConnecting})
	s.log("starting SSO login for session: " + s.session)

	args := []string{"sso", "login", "--sso-session", s.session}
	s.log(fmt.Sprintf("running: aws %s", strings.Join(args, " ")))

	out, err := exec.CommandContext(ctx, "aws", args...).CombinedOutput() //nolint:gosec
	if err != nil {
		if ctx.Err() != nil {
			s.log("SSO login cancelled")
			s.setState(service.RuntimeState{Status: service.StatusIdle})
			return
		}
		s.log(fmt.Sprintf("SSO login failed: %v: %s", err, strings.TrimSpace(string(out))))
		// The exit status alone ("exit status 255") says nothing; the CLI's
		// last line does ("The SSO session ... does not exist").
		msg := fmt.Sprintf("SSO login failed: %v", err)
		if line := lastLine(string(out)); line != "" {
			msg += ": " + line
		}
		s.setState(service.RuntimeState{
			Status: service.StatusError,
			Error:  errors.New(msg),
		})
		return
	}

	s.log("SSO login successful")

	// Write den cache for precise expiry if we have a profile
	if s.profile != "" {
		issuedAt := time.Now()
		expiresAt, err := config.ExportCredentials(s.profile)
		if err == nil && expiresAt != nil {
			_ = config.WriteDenCache(s.ssoCacheDir, s.session, issuedAt, *expiresAt)
			s.log(fmt.Sprintf("token expires at %s", expiresAt.Format(time.RFC3339)))
		}
	}

	s.checkToken()
	s.sendUpdate()

	// Monitor token expiry in background
	s.tokenMonitorLoop(ctx)
}

// lastLine is the last non-empty line of the CLI's output.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func (s *Service) tokenMonitorLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.RLock()
			expires := s.state.TokenExpires
			s.mu.RUnlock()

			if expires != nil && time.Until(*expires) <= 0 {
				s.log("SSO token expired")
				s.setState(service.RuntimeState{Status: service.StatusIdle})
				return
			}
			// Re-read cache in case token was refreshed externally
			s.checkToken()
			s.sendUpdate()
		}
	}
}

// defaultTokenTTL is the assumed lifetime when a cache entry gives an expiry but
// no usable issue time. SSO tokens are hours long, so the generic 15-minute
// default would render the TTL bar permanently near-empty.
const defaultTokenTTL = 12 * time.Hour

// checkToken reads the SSO token cache and updates internal state.
//
// It sets TokenTTL as well as TokenExpires: without a TTL the detail overlay
// falls back to a 15-minute bar (see components.TokenBar), which is wrong by
// nearly two orders of magnitude for an SSO token. setState replaces the whole
// RuntimeState, so run() wipes both on StatusConnecting and checkToken puts them
// back after login.
func (s *Service) checkToken() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Try den cache first (more precise expiry)
	denCache := config.ReadDenCache(s.ssoCacheDir)
	if entry, ok := denCache.Sessions[s.session]; ok && time.Until(entry.ExpiresAt) > 0 {
		s.state.TokenExpires = &entry.ExpiresAt
		s.state.TokenTTL = tokenTTL(entry.IssuedAt, entry.ExpiresAt)
		s.state.Status = service.StatusConnected
		return
	}

	// Fall back to AWS SSO cache
	tok := config.ReadSSOToken(s.session, s.startURL, s.ssoCacheDir)
	if tok != nil && time.Until(tok.ExpiresAt) > 0 {
		s.state.TokenExpires = &tok.ExpiresAt
		s.state.TokenTTL = tokenTTL(tok.IssuedAt, tok.ExpiresAt)
		s.state.Status = service.StatusConnected
		return
	}

	s.state.TokenExpires = nil
	s.state.TokenTTL = 0
	s.state.Status = service.StatusIdle
}

// tokenTTL is the token's full lifetime, used to scale the TTL bar. The issue
// time can be missing or bogus (the AWS CLI cache has no issue field, so it is
// inferred from the file mtime), so fall back rather than render a negative bar.
func tokenTTL(issuedAt, expiresAt time.Time) time.Duration {
	if issuedAt.IsZero() {
		return defaultTokenTTL
	}
	if ttl := expiresAt.Sub(issuedAt); ttl > 0 {
		return ttl
	}
	return defaultTokenTTL
}

func (s *Service) log(msg string) {
	s.logs.Add(msg)
}

func (s *Service) setState(state service.RuntimeState) {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
	s.sendUpdate()
}

func (s *Service) sendUpdate() {
	s.mu.RLock()
	st := s.state
	s.mu.RUnlock()
	select {
	case s.updates <- service.StatusUpdate{Info: s.Info(), State: st}:
	default:
	}
}
