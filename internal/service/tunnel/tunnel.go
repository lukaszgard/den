// Package tunnel implements a service.Service that runs an arbitrary SSH tunnel command.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/process"
	"github.com/lukaszgard/den/internal/service"
)

// The TUI discovers the r/m/H/v key bindings by type-asserting to these, so
// losing one would silently drop a toggle from the panes rather than fail to build.
var (
	_ service.Reconnectable   = (*Service)(nil)
	_ service.Monitorable     = (*Service)(nil)
	_ service.HealthCheckable = (*Service)(nil)
	_ service.VPNBound        = (*Service)(nil)
)

// Service implements service.Service for an SSH tunnel defined by a raw shell command.
type Service struct {
	mu      sync.RWMutex
	spec    config.TunnelSpec
	sup     *process.Supervisor
	state   service.RuntimeState
	updates chan service.StatusUpdate
	cancel  context.CancelFunc
	// done is closed when the loop started by Start has exited, so Stop can
	// wait for it; nil before the first Start. Guarded by mu.
	done chan struct{}
	// The runtime toggles. All are guarded by mu and seeded from the spec;
	// flipping one never writes back to den.yaml, so a restart restores the file's
	// values. healthCheck only gates the probe — spec.HealthCheck keeps the target
	// so the TUI can still show it while the check is switched off; autoConnect
	// likewise leaves spec.AutoConnect.VPN in place.
	reconnect   bool
	monitor     bool
	healthCheck bool
	autoConnect bool
	localPort   int // first -D/-L port in the command, shown as the tunnel's local port

	// lastOutput is the most recent non-empty line the tunnel command printed,
	// kept apart from den's own log lines so error messages quote ssh itself.
	lastOutput string // guarded by mu
}

const (
	// readyTimeout bounds how long a new tunnel may stay "connecting" before den
	// gives up on it; ssh alone can take over a minute to fail an unreachable host.
	readyTimeout = 30 * time.Second
	readyPoll    = 500 * time.Millisecond
	// healthRetryEvery spaces out health checks while connecting: each failed
	// attempt makes ssh log a "channel open failed" line.
	healthRetryEvery = 3 * time.Second
	// noForwardGrace is how long a command with nothing to probe must stay up
	// before it counts as connected.
	noForwardGrace   = 2 * time.Second
	monitorEvery     = 15 * time.Second
	monitorMaxMisses = 2
	stopGrace        = 3 * time.Second
	stopWait         = 5 * time.Second
)

// New creates a tunnel Service from the given spec.
func New(spec config.TunnelSpec) *Service {
	var localPort int
	if forwards := ParseForwards(spec.Command); len(forwards) > 0 {
		localPort = forwards[0].Port
	}
	return &Service{
		spec:        spec,
		reconnect:   spec.Reconnect,
		monitor:     spec.Monitor,
		healthCheck: spec.HealthCheck != "",
		autoConnect: spec.AutoConnect.On(),
		localPort:   localPort,
		sup:         process.NewSupervisor(200),
		updates:     make(chan service.StatusUpdate, 32),
		state:       service.RuntimeState{Status: service.StatusIdle},
	}
}

func (s *Service) Info() service.ServiceInfo {
	return service.ServiceInfo{Name: s.spec.Name, Type: service.TypeTunnel, Environment: ""}
}

func (s *Service) State() service.RuntimeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.state
	st.Reconnect = s.reconnect
	st.LocalPort = s.localPort
	return st
}

func (s *Service) LogWriter() io.Writer { return s.sup.LogWriter() }

func (s *Service) Logs() []string { return s.sup.Logs() }

func (s *Service) LogSeq() uint64 { return s.sup.LogSeq() }

func (s *Service) ConnectCommand() string { return s.spec.Command }

func (s *Service) RefreshToken(_ context.Context) error { return nil }

// Description returns the tunnel's description from the config, or "".
func (s *Service) Description() string { return s.spec.Description }

// HealthCheckTarget returns the host:port configured for the SOCKS probe, or ""
// when none is configured. It reports the target even while the check is
// switched off, so the TUI can show what would be probed.
func (s *Service) HealthCheckTarget() string { return s.spec.HealthCheck }

// HealthCheckEnabled reports whether the configured target is actually probed.
func (s *Service) HealthCheckEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.healthCheck
}

// activeHealthCheck is the target the probe should dial right now: the configured
// host:port, or "" when nothing is configured or the check is switched off. Every
// probe path goes through this rather than reading spec.HealthCheck directly.
func (s *Service) activeHealthCheck() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.healthCheck {
		return ""
	}
	return s.spec.HealthCheck
}

// Monitoring reports whether the connection checks keep running while connected.
func (s *Service) Monitoring() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.monitor
}

// ReconnectEnabled reports whether the tunnel restarts itself after a drop.
func (s *Service) ReconnectEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reconnect
}

// ToggleReconnect flips the runtime reconnect setting and returns the new value.
// It applies at the next drop: a retry already waiting out its backoff still runs.
func (s *Service) ToggleReconnect() bool {
	s.mu.Lock()
	s.reconnect = !s.reconnect
	enabled := s.reconnect
	s.mu.Unlock()
	s.sendUpdate()
	return enabled
}

// ToggleMonitor flips the runtime monitor setting and returns the new value. The
// monitor loop reads it each tick, so it takes effect within monitorEvery.
func (s *Service) ToggleMonitor() bool {
	s.mu.Lock()
	s.monitor = !s.monitor
	enabled := s.monitor
	s.mu.Unlock()
	s.sendUpdate()
	return enabled
}

// ToggleHealthCheck flips the runtime health-check setting and returns the new
// value. It is inert when no target is configured — there is nothing to probe —
// and applies at the next check: the next monitor tick, else the next connect.
func (s *Service) ToggleHealthCheck() bool {
	if s.spec.HealthCheck == "" {
		return false
	}
	s.mu.Lock()
	s.healthCheck = !s.healthCheck
	enabled := s.healthCheck
	s.mu.Unlock()
	s.sendUpdate()
	return enabled
}

// BoundVPN returns the VPN named in auto_connect, or "" when there is none.
func (s *Service) BoundVPN() string {
	if s.spec.AutoConnect == nil {
		return ""
	}
	return s.spec.AutoConnect.VPN
}

// AutoConnect reports whether the TUI follows the bound VPN: connecting the
// tunnel when it comes up and stopping it when it goes down.
func (s *Service) AutoConnect() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.autoConnect
}

// ToggleAutoConnect flips the runtime auto-connect setting and returns the new
// value. It is inert without a bound VPN, and applies at the VPN's next change.
func (s *Service) ToggleAutoConnect() bool {
	if s.BoundVPN() == "" {
		return false
	}
	s.mu.Lock()
	s.autoConnect = !s.autoConnect
	enabled := s.autoConnect
	s.mu.Unlock()
	s.sendUpdate()
	return enabled
}

func (s *Service) Start(ctx context.Context) (<-chan service.StatusUpdate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state.Status {
	case service.StatusConnecting, service.StatusConnected, service.StatusReconnecting:
		// Reconnecting counts as running: its loop is alive, and a second one
		// would fight it over the same supervisor.
		return s.updates, service.ErrAlreadyRunning
	}

	tunnelCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.cancel = cancel
	s.done = done
	go func() {
		defer close(done)
		s.run(tunnelCtx)
	}()
	return s.updates, nil
}

// Stop tears the tunnel down and waits, bounded by ctx and stopWait, for the
// loop to exit, so a Start right after it cannot run a second loop.
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()

	s.setState(service.RuntimeState{Status: service.StatusStopping})
	if cancel != nil {
		cancel()
	}
	if err := s.sup.StopGraceful(stopGrace); err != nil {
		return err
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
		case <-time.After(stopWait):
		}
	}
	s.setState(service.RuntimeState{Status: service.StatusIdle})
	return nil
}

func (s *Service) run(ctx context.Context) {
	backoff := 5 * time.Second
	for {
		s.sup.Drain()
		wasConnected := s.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}

		s.mu.RLock()
		shouldReconnect := s.reconnect
		s.mu.RUnlock()
		if !shouldReconnect {
			return
		}

		if wasConnected {
			backoff = 5 * time.Second
		}

		reconnectAt := time.Now().Add(backoff)
		s.setState(service.RuntimeState{
			Status:      service.StatusReconnecting,
			ReconnectAt: &reconnectAt,
		})
		s.log(fmt.Sprintf("reconnecting in %v...", backoff.Round(time.Second)))

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff = min(backoff*2, 60*time.Second)
	}
}

// runOnce executes a single tunnel attempt. Returns true if the tunnel reached
// StatusConnected before exiting (used to reset the backoff on reconnect).
func (s *Service) runOnce(ctx context.Context) bool {
	s.setState(service.RuntimeState{Status: service.StatusConnecting})
	s.log("starting tunnel...")

	forwards := ParseForwards(s.spec.Command)
	if err := s.preflight(forwards); err != nil {
		s.fail(err)
		return false
	}

	s.mu.Lock()
	s.lastOutput = ""
	s.mu.Unlock()
	s.log(fmt.Sprintf("running: sh -c %q", s.spec.Command))
	// A process group lets a stop take down ssh itself, not just the sh wrapping
	// it — an orphaned ssh would keep its port and fail the next preflight.
	opts := process.StartOptions{ProcessGroup: true, OnLine: s.recordOutput}
	shell, args := platform.ShellCommand(s.spec.Command)
	if err := s.sup.StartWith(ctx, opts, shell, args...); err != nil {
		s.setState(service.RuntimeState{
			Status: service.StatusError,
			Error:  err,
		})
		return false
	}
	done := s.sup.Done()

	if err := s.waitReady(ctx, forwards, done); err != nil {
		_ = s.sup.StopGraceful(stopGrace)
		<-done
		if ctx.Err() == nil {
			s.fail(err)
		}
		return false
	}

	now := time.Now()
	s.mu.Lock()
	s.state = service.RuntimeState{
		Status:     service.StatusConnected,
		StartedAt:  &now,
		ConnectCmd: s.spec.Command,
	}
	s.mu.Unlock()
	s.sendUpdate()

	// Start the loop whenever there is anything to check, not only when monitoring
	// is on right now: it re-reads the flag every tick, so the user can switch
	// monitoring on mid-connection without restarting the tunnel.
	dropped := make(chan error, 1)
	if len(forwards) > 0 || s.spec.HealthCheck != "" {
		monitorCtx, stopMonitor := context.WithCancel(ctx)
		defer stopMonitor()
		go s.monitorLoop(monitorCtx, forwards, dropped)
	}

	// Wait on done rather than the events channel: events drop when the buffer
	// is full, so a chatty tunnel could lose its own exit event.
	<-done
	if ctx.Err() != nil {
		return true
	}

	var err error
	select {
	case reason := <-dropped:
		err = fmt.Errorf("connection check failed %d times in a row: %w", monitorMaxMisses, reason)
	default:
		err = s.exitError("tunnel exited")
	}
	s.setState(service.RuntimeState{
		Status: service.StatusError,
		Error:  err,
	})
	return true
}

// preflight rejects a start the readiness check could not judge: a port that
// something else already listens on would pass the check for the wrong process.
func (s *Service) preflight(forwards []Forward) error {
	if s.activeHealthCheck() != "" {
		if _, ok := socksForward(forwards); !ok {
			return errNoSocks
		}
	}
	for _, f := range forwards {
		if service.Reachable(f.ProbeAddr(), portCheckTimeout) {
			return fmt.Errorf("port %d is already in use — is another ssh/tunnel still running?", f.Port)
		}
	}
	return nil
}

// waitReady holds the tunnel in "connecting" until check passes, the process
// exits, or readyTimeout elapses. ssh opens its -D/-L listeners only after it
// has authenticated, so a listening port means the session is really up.
func (s *Service) waitReady(ctx context.Context, forwards []Forward, done <-chan struct{}) error {
	if len(forwards) == 0 && s.activeHealthCheck() == "" {
		s.log(fmt.Sprintf("no -D/-L ports to check — treating the tunnel as up once it has run for %v", noForwardGrace))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return s.exitError("tunnel exited before it was ready")
		case <-time.After(noForwardGrace):
			return nil
		}
	}

	ports := make([]string, len(forwards))
	for i, f := range forwards {
		ports[i] = fmt.Sprintf(":%d", f.Port)
	}
	msg := "waiting for " + strings.Join(ports, ", ") + " to accept connections"
	if hc := s.activeHealthCheck(); hc != "" {
		msg += ", then " + hc + " through the SOCKS proxy"
	}
	s.log(msg + "...")

	deadline := time.After(readyTimeout)
	ticker := time.NewTicker(readyPoll)
	defer ticker.Stop()
	var healthErr error
	var healthDue time.Time // zero: check as soon as the ports are up
	for {
		// Re-read the toggle each round: switching the health check off mid-connect
		// drops the probe from the readiness test instead of stalling until timeout.
		hc := s.activeHealthCheck()
		err := checkPorts(forwards)
		if err == nil && hc != "" {
			if !time.Now().Before(healthDue) {
				healthErr = s.checkHealth(forwards)
				healthDue = time.Now().Add(healthRetryEvery)
			}
			err = healthErr
		}
		if err == nil {
			s.log("verified: " + strings.Join(ports, ", ") + " listening")
			if hc != "" {
				s.log("verified: " + hc + " reachable through the tunnel")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return s.exitError("tunnel exited before it was ready")
		case <-deadline:
			return fmt.Errorf("tunnel not ready after %v: %v%s", readyTimeout, err, s.outputSuffix())
		case <-ticker.C:
		}
	}
}

// monitor re-runs check every monitorEvery while the tunnel is up. ssh can stay
// alive long after its connection has died, so a run of failures reports the
// reason on dropped and stops the process, letting the reconnect loop take over.
func (s *Service) monitorLoop(ctx context.Context, forwards []Forward, dropped chan<- error) {
	ticker := time.NewTicker(monitorEvery)
	defer ticker.Stop()
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		// Read the toggle every tick so switching monitoring off stops the checks
		// without tearing the loop down, and switching it back on starts clean.
		if !s.Monitoring() {
			misses = 0
			continue
		}
		err := s.check(forwards)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			if misses > 0 {
				s.log("connection check recovered")
			}
			misses = 0
			continue
		}
		misses++
		s.log(fmt.Sprintf("connection check failed (%d/%d): %v", misses, monitorMaxMisses, err))
		if misses >= monitorMaxMisses {
			dropped <- err
			_ = s.sup.StopGraceful(stopGrace)
			return
		}
	}
}

// recordOutput remembers the tunnel command's latest line for error messages.
func (s *Service) recordOutput(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	s.mu.Lock()
	s.lastOutput = line
	s.mu.Unlock()
}

// exitError describes a finished process, quoting its exit status and last output.
func (s *Service) exitError(prefix string) error {
	msg := prefix
	if err := s.sup.ExitErr(); err != nil {
		msg += ": " + err.Error()
	}
	return errors.New(msg + s.outputSuffix())
}

func (s *Service) outputSuffix() string {
	s.mu.RLock()
	last := s.lastOutput
	s.mu.RUnlock()
	if last == "" {
		return ""
	}
	return " — " + last
}

// fail logs err and moves the tunnel to StatusError.
func (s *Service) fail(err error) {
	s.log(err.Error())
	s.setState(service.RuntimeState{
		Status: service.StatusError,
		Error:  err,
	})
}

func (s *Service) log(msg string) {
	s.sup.LogWriter().Write([]byte(msg + "\n")) //nolint:errcheck
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
	st.Reconnect = s.reconnect
	st.LocalPort = s.localPort
	s.mu.RUnlock()
	select {
	case s.updates <- service.StatusUpdate{Info: s.Info(), State: st}:
	default:
	}
}
