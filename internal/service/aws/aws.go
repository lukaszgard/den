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

	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc/types"

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

	// newOIDC is IAM Identity Center's token API in a region; tests swap in a
	// fake. CreateToken is unsigned, so it needs no credentials.
	newOIDC func(region string) ssocreds.CreateTokenAPIClient
	// renewMu makes renewals take turns: a refresh token can be spent once,
	// so two at the same moment would fail one of them.
	renewMu  sync.Mutex
	renewErr string // the last renewal failure logged
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
		newOIDC: func(region string) ssocreds.CreateTokenAPIClient {
			return ssooidc.New(ssooidc.Options{Region: region})
		},
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

// RefreshToken renews an expired access token from the refresh token, then
// re-reads the SSO token cache and updates the displayed state. It never
// starts a new login: one that cannot be renewed stays expired.
func (s *Service) RefreshToken(ctx context.Context) error {
	// A login in progress owns the state: the TUI asks every minute, and the
	// cache holds no new token until the browser step is done.
	if s.State().Status == service.StatusConnecting {
		return nil
	}
	s.renew(ctx)
	s.checkToken()
	s.sendUpdate()
	return nil
}

// renewTimeout bounds one CreateToken call, so a hung one does not hold
// renewMu over the renewals after it.
const renewTimeout = 30 * time.Second

// renew swaps the session's expired access token for a new one while the
// refresh token is good, as the CLI and SDKs do on their next call. The token
// lasts an hour, and when nothing used AWS in that hour the file stayed
// expired: every reader of it (the Secrets pane, den doctor, tools outside
// den) took the login for over. It never opens a browser.
//
// The SDK rewrites the file in the CLI's format. It only refreshes a token
// that has expired, which is why this waits for the expiry.
func (s *Service) renew(ctx context.Context) {
	if s.session == "" {
		return
	}
	path := config.SSOTokenPath(s.ssoCacheDir, s.session)
	s.renewMu.Lock()
	defer s.renewMu.Unlock()
	// Read under the lock: the renewal before this one spent the refresh
	// token in the file it read, and rewrote the file.
	tok, ok := config.LoadSSOToken(path)
	now := time.Now()
	if !ok || !now.After(tok.ExpiresAt) || !tok.RefreshableUntil.After(now) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, renewTimeout)
	defer cancel()
	bearer, err := ssocreds.NewSSOTokenProvider(s.newOIDC(s.ssoRegion), path).RetrieveBearerToken(ctx)
	if err != nil {
		if sessionEnded(err) {
			s.recordEnd(path, tok)
		}
		// A session that ended fails the same way every minute, so it is
		// logged once.
		if msg := err.Error(); msg != s.renewErr {
			s.renewErr = msg
			s.log("could not renew the SSO token: " + msg)
		}
		return
	}
	s.renewErr = ""
	s.log("renewed the SSO token, valid until " + bearer.Expires.Local().Format("15:04"))
}

// sessionEnded reports whether IAM Identity Center refused the refresh token
// or the client it was issued to: the session is over until the next login.
// A failure on the way (no network, a timeout, a throttle) says nothing about
// the session.
func sessionEnded(err error) bool {
	var (
		grant        *types.InvalidGrantException
		expired      *types.ExpiredTokenException
		unauthorized *types.UnauthorizedClientException
		client       *types.InvalidClientException
	)
	return errors.As(err, &grant) || errors.As(err, &expired) ||
		errors.As(err, &unauthorized) || errors.As(err, &client)
}

// recordEnd ends den's record of the session at the refused token's expiry.
// The file alone cannot say the session is over: it keeps a refresh token
// and a client registration that lasts months, so every reader took it for a
// live login. config.FindSSOLogin counts a login whose record of den's ran
// out, and whose file was not written since, as over; a new login, by den or
// the CLI, writes the file and counts again.
//
// tok is the file as read before the renewal. When the file changed since,
// another den or the CLI renewed the login and spent the refresh token den
// sent, which is why it was refused: that is not the end.
func (s *Service) recordEnd(path string, tok config.SSOToken) {
	again, ok := config.LoadSSOToken(path)
	if !ok || !again.IssuedAt.Equal(tok.IssuedAt) || !again.ExpiresAt.Equal(tok.ExpiresAt) {
		return
	}
	// FindSSOLogin compares the file's mod time with the record's end, so the
	// end is no earlier than the last write.
	end := tok.ExpiresAt
	if tok.IssuedAt.After(end) {
		end = tok.IssuedAt
	}
	issued := tok.IssuedAt
	if entry, ok := config.ReadDenCache(s.ssoCacheDir).Sessions[s.session]; ok {
		issued = entry.IssuedAt
	}
	if err := config.WriteDenCache(s.ssoCacheDir, s.session, issued, end); err != nil {
		s.log("could not record the end of the SSO session: " + err.Error())
	}
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
			// Renewed before the cache is read: past den's record of the
			// login, the state comes from the hour-long access token.
			s.renew(ctx)
			// Re-read cache in case token was refreshed externally
			s.checkToken()
			s.sendUpdate()
			if s.State().Status != service.StatusConnected {
				s.log("SSO token expired")
				return
			}
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
//
// Whether the login is live is config.FindSSOLogin's answer, the rule the
// Secrets pane and den doctor use. The pane once read den's record first, so
// after `aws sso logout` it counted down hours while every secret said the
// login had expired. A live login with no known end (a refresh token, no
// record of den's) is connected without an expiry.
func (s *Service) checkToken() {
	s.mu.Lock()
	defer s.mu.Unlock()

	den := config.ReadDenCache(s.ssoCacheDir)
	login := config.FindSSOLogin(s.session, s.startURL, config.ListSSOTokens(s.ssoCacheDir), den, time.Now())
	s.state.TokenExpires = nil
	s.state.TokenTTL = 0
	if !login.Live {
		s.state.Status = service.StatusIdle
		return
	}
	s.state.Status = service.StatusConnected
	if login.Until.IsZero() {
		return
	}
	until := login.Until
	s.state.TokenExpires = &until
	if entry, ok := den.Sessions[s.session]; ok && entry.ExpiresAt.Equal(until) {
		s.state.TokenTTL = tokenTTL(entry.IssuedAt, until)
	} else if tok := config.ReadSSOToken(s.session, s.startURL, s.ssoCacheDir); tok != nil {
		s.state.TokenTTL = tokenTTL(tok.IssuedAt, until)
	} else {
		s.state.TokenTTL = defaultTokenTTL
	}
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
