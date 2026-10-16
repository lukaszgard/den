package ssmfwd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/process"
	"github.com/lukaszgard/den/internal/service"
)

const (
	// maxAttempts bounds the SSM start retries within one connection attempt.
	maxAttempts = 3
	// refreshLead is how long before expiry a credential is re-minted. With the
	// 15-minute IAM tokens RDS and ElastiCache issue, this refreshes at 13 min.
	refreshLead = 2 * time.Minute
	// minRefreshDelay stops a failing or very short-lived credential from
	// turning the refresh loop into a busy loop.
	minRefreshDelay = 30 * time.Second
	// portCheckTimeout bounds one probe. The forward is on loopback, so a
	// healthy one answers immediately and anything slower is already broken.
	portCheckTimeout = 500 * time.Millisecond
)

const (
	// defaultMonitorEvery is how often a live connection re-checks that its
	// forwards still accept connections. session-manager-plugin can stop serving
	// the local port while the process stays alive — its websocket to AWS drops
	// but nothing exits — so waiting for process exit alone leaves a dead tunnel
	// reported as connected indefinitely.
	defaultMonitorEvery = 15 * time.Second
	// defaultMonitorMaxMisses tolerates a single missed probe, so one slow check
	// does not tear down a working tunnel.
	defaultMonitorMaxMisses = 2
	// stopWait bounds how long Stop waits for the connection loop to exit after
	// its sessions are killed.
	stopWait = 5 * time.Second
	// retryPause spaces out SSM start attempts within one connection attempt.
	retryPause = 2 * time.Second
)

// Options is the per-service configuration of a Tunnel.
type Options struct {
	Name        string
	Environment string            // shown in the TUI, e.g. "prod"
	Env         config.EnvMapping // bastion, SSM profile and region
	Reconnect   bool              // initial auto-reconnect setting

	// MonitorEvery and MonitorMaxMisses tune the liveness probe of a connected
	// tunnel; zero means the defaults (15 s, two misses in a row).
	MonitorEvery     time.Duration
	MonitorMaxMisses int

	// Transport opens the forwards; nil means the one Env names (SSM through
	// its bastion unless Env.Transport says otherwise).
	Transport Transport
}

// The TUI wires the reconnect toggle and the terminal tabs by type-asserting to
// these, so losing one would silently drop the binding rather than fail to build.
var (
	_ service.Reconnectable  = (*Tunnel)(nil)
	_ service.TerminalOpener = (*Tunnel)(nil)
)

// Tunnel implements service.Service for any Engine reached through SSM
// port-forwards. It also implements service.Reconnectable,
// service.TokenHolder, service.SecretCommander and service.TerminalOpener.
type Tunnel struct {
	mu     sync.RWMutex
	opts   Options
	engine Engine
	// transport opens each forward; transportErr is why none could be built
	// (an invalid transport), reported by every connection attempt.
	transport    Transport
	transportErr error
	sup          *process.Supervisor   // first forward; its log is the service log
	extra        []*process.Supervisor // forwards after the first, grown on demand
	state        service.RuntimeState
	updates      chan service.StatusUpdate
	cancel       context.CancelFunc
	// done is closed when the connection loop started by Start has exited, so
	// Stop can wait for it; nil before the first Start. Guarded by mu.
	done chan struct{}

	reconnect bool // guarded by mu; initialized from opts.Reconnect

	// refreshAt is when the refresh loop renews the credential next; zero
	// while no loop runs. Guarded by mu.
	refreshAt time.Time
	// renewed wakes the refresh loop after RefreshToken minted a credential,
	// so it renews that one rather than the one it was armed for.
	renewed chan struct{}
}

// New creates a Tunnel driving engine.
func New(opts Options, engine Engine) *Tunnel {
	if opts.MonitorEvery <= 0 {
		opts.MonitorEvery = defaultMonitorEvery
	}
	if opts.MonitorMaxMisses <= 0 {
		opts.MonitorMaxMisses = defaultMonitorMaxMisses
	}
	tr, trErr := opts.Transport, error(nil)
	if tr == nil {
		tr, trErr = NewTransport(opts.Env)
	}
	return &Tunnel{
		opts:         opts,
		engine:       engine,
		transport:    tr,
		transportErr: trErr,
		reconnect:    opts.Reconnect,
		sup:          process.NewSupervisor(200),
		updates:      make(chan service.StatusUpdate, 32),
		renewed:      make(chan struct{}, 1),
		state:        service.RuntimeState{Status: service.StatusIdle, LocalPort: engine.UserPort()},
	}
}

func (t *Tunnel) Info() service.ServiceInfo {
	return service.ServiceInfo{Name: t.opts.Name, Type: t.engine.Type(), Environment: t.opts.Environment, Production: t.opts.Env.Production}
}

func (t *Tunnel) State() service.RuntimeState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	st := t.state
	st.Reconnect = t.reconnect
	return st
}

func (t *Tunnel) LogWriter() io.Writer { return t.sup.LogWriter() }

func (t *Tunnel) Logs() []string { return t.sup.Logs() }

func (t *Tunnel) LogSeq() uint64 { return t.sup.LogSeq() }

func (t *Tunnel) ConnectCommand() string { return t.engine.ConnectCommand() }

// ToggleReconnect flips the runtime auto-reconnect setting and returns the new value.
func (t *Tunnel) ToggleReconnect() bool {
	t.mu.Lock()
	t.reconnect = !t.reconnect
	enabled := t.reconnect
	t.mu.Unlock()
	t.sendUpdate()
	return enabled
}

// ReconnectEnabled reports whether the tunnel reopens itself after a drop.
func (t *Tunnel) ReconnectEnabled() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.reconnect
}

// HoldsToken reports whether the engine mints a credential the user can refresh and copy.
func (t *Tunnel) HoldsToken() bool {
	_, ok := t.engine.(Credentialer)
	return ok
}

// HasSecretCommand reports whether the connect command can carry the credential inline.
func (t *Tunnel) HasSecretCommand() bool {
	_, ok := t.engine.(SecretCommander)
	return ok
}

// ConnectCommandWithSecret returns the connect command with the current
// credential embedded, or the plain command while there is none.
func (t *Tunnel) ConnectCommandWithSecret() string {
	token := t.State().Token
	sc, ok := t.engine.(SecretCommander)
	if !ok || token == "" {
		return t.engine.ConnectCommand()
	}
	return sc.ConnectCommandWithSecret(token)
}

// HasTerminal reports whether den can run the engine's client in a terminal tab.
func (t *Tunnel) HasTerminal() bool {
	_, ok := t.engine.(TerminalCommander)
	return ok
}

// TerminalCommand returns the client invocation for a terminal tab, carrying
// the current credential. It fails unless the tunnel is connected: the status
// turns connected only after the credential is minted, so a command handed out
// then always has a port to reach and a credential to send.
func (t *Tunnel) TerminalCommand() (service.ClientCommand, error) {
	tc, ok := t.engine.(TerminalCommander)
	if !ok {
		return service.ClientCommand{}, fmt.Errorf("%s has no client den can run", t.engine.Type())
	}
	st := t.State()
	if st.Status != service.StatusConnected {
		return service.ClientCommand{}, fmt.Errorf("%s is not connected", t.opts.Name)
	}
	return tc.TerminalCommand(st.Token), nil
}

// Start opens the tunnel in the background and returns the update channel.
func (t *Tunnel) Start(ctx context.Context) (<-chan service.StatusUpdate, error) {
	t.mu.Lock()
	switch t.state.Status {
	case service.StatusConnecting, service.StatusConnected, service.StatusReconnecting:
		// Reconnecting counts as running: its loop is alive, and a second one
		// would fight it over the same supervisors.
		t.mu.Unlock()
		return t.updates, service.ErrAlreadyRunning
	}
	tunnelCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	t.cancel = cancel
	t.done = done
	t.mu.Unlock()

	go func() {
		defer close(done)
		t.run(tunnelCtx)
	}()
	return t.updates, nil
}

// Stop tears the connection down and waits, bounded by ctx and stopWait, for
// the connection loop to exit, so a Start right after it cannot run a second
// loop over the same supervisors.
func (t *Tunnel) Stop(ctx context.Context) error {
	t.mu.Lock()
	cancel, done := t.cancel, t.done
	t.mu.Unlock()

	t.setState(service.RuntimeState{Status: service.StatusStopping})
	if cancel != nil {
		cancel()
	}
	if err := t.stopForwards(); err != nil {
		return err
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
		case <-time.After(stopWait):
		}
	}
	t.setState(service.RuntimeState{Status: service.StatusIdle, LocalPort: t.engine.UserPort()})
	return nil
}

// RefreshToken mints a fresh credential on demand; a no-op for engines without one.
func (t *Tunnel) RefreshToken(ctx context.Context) error {
	c, ok := t.engine.(Credentialer)
	if !ok {
		return nil
	}
	// The rule of connecting holds here too: no credential without a tunnel
	// to carry it. The engine's log line also names the auto-refresh, which
	// only a connection's refresh loop does.
	if t.State().Status != service.StatusConnected {
		return fmt.Errorf("%s is not connected", t.opts.Name)
	}
	cred, err := c.Credential(ctx, t.log)
	if err != nil {
		return err
	}
	t.applyCredential(cred)
	select {
	case t.renewed <- struct{}{}:
	default: // one wake-up already pending reads the same state
	}
	return nil
}

// ---

// run drives the connect lifecycle. With reconnect enabled it re-runs runOnce
// after the session drops, backing off exponentially (5s → 60s) and resetting the
// backoff once a session connects. With reconnect off it runs exactly once.
func (t *Tunnel) run(ctx context.Context) {
	backoff := 5 * time.Second
	for {
		wasConnected := t.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}

		t.mu.RLock()
		shouldReconnect := t.reconnect
		t.mu.RUnlock()
		if !shouldReconnect {
			return
		}

		if wasConnected {
			backoff = 5 * time.Second
		}

		reconnectAt := time.Now().Add(backoff)
		t.setState(service.RuntimeState{
			Status:      service.StatusReconnecting,
			ReconnectAt: &reconnectAt,
			LocalPort:   t.engine.UserPort(),
		})
		t.log(fmt.Sprintf("reconnecting in %v...", backoff.Round(time.Second)))

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff = min(backoff*2, 60*time.Second)
	}
}

// runOnce performs a single connect-and-serve cycle, blocking until an SSM
// session exits. It returns true if the connection reached StatusConnected
// (used to reset the reconnect backoff).
func (t *Tunnel) runOnce(ctx context.Context) bool {
	port := t.engine.UserPort()
	t.setState(service.RuntimeState{Status: service.StatusConnecting, LocalPort: port})

	fail := func(err error) bool {
		t.stopForwards() //nolint:errcheck
		// A cancelled context means Stop is tearing us down and has already
		// set the state; an error here is the teardown, not a failure.
		if ctx.Err() == nil {
			t.setState(service.RuntimeState{Status: service.StatusError, Error: err, LocalPort: port})
		}
		return false
	}

	if t.transportErr != nil {
		return fail(t.transportErr)
	}
	fwds, err := t.engine.Forwards()
	if err != nil {
		return fail(err)
	}
	if len(fwds) == 0 {
		return fail(fmt.Errorf("no forwards to open"))
	}

	// A port something else already listens on would pass the readiness check
	// for the wrong process: den would report connected and mint a credential
	// for a session it does not own.
	for _, f := range fwds {
		if service.Reachable(fmt.Sprintf("127.0.0.1:%d", f.LocalPort), portCheckTimeout) {
			return fail(fmt.Errorf("port %d is already in use — is another %s or den still running?", f.LocalPort, t.transport.Name()))
		}
	}

	// Every forward must be confirmed open before anything credential-related
	// happens. Minting an IAM token (and rewriting ~/.pgpass) against a dead
	// tunnel is worse than useless.
	sups := t.supervisors(len(fwds))
	for i, f := range fwds {
		if err := t.open(ctx, sups[i], f, len(fwds) > 1); err != nil {
			return fail(err)
		}
	}

	if p, ok := t.engine.(Preparer); ok {
		stop, err := p.Prepare(ctx, fwds, t.log)
		if err != nil {
			return fail(err)
		}
		if stop != nil {
			defer stop()
		}
	}

	var cred Credential
	c, hasCred := t.engine.(Credentialer)
	if hasCred {
		cred, err = c.Credential(ctx, t.log)
		if err != nil {
			t.log(fmt.Sprintf("token generation failed: %v", err))
			return fail(err)
		}
	}

	// Built before the lock is taken — engines guard their own state and may
	// call back into State().
	connectCmd := t.engine.ConnectCommand()

	now := time.Now()
	t.mu.Lock()
	t.state = service.RuntimeState{
		Status:     service.StatusConnected,
		StartedAt:  &now,
		LocalPort:  port,
		ConnectCmd: connectCmd,
		Token:      cred.Secret,
	}
	if expiring(cred) {
		expires := cred.Expires
		t.state.TokenExpires = &expires
		t.state.TokenTTL = time.Until(expires).Round(time.Minute)
	}
	t.mu.Unlock()
	t.sendUpdate()

	if hasCred && expiring(cred) {
		// Auto-refresh goroutine, scoped to this connection: on reconnect runOnce
		// starts a fresh one, and refreshCancel stops this one so they do not
		// accumulate across reconnects.
		refreshCtx, refreshCancel := context.WithCancel(ctx)
		defer refreshCancel()
		go t.refreshLoop(refreshCtx, c, cred.Expires, fwds)
	}

	// Watch the ports as well as the processes: a forward can stop serving
	// without its process exiting, and then waitExit never fires.
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	defer stopMonitor()
	dropped := make(chan error, 1)
	go t.monitor(monitorCtx, fwds, dropped)

	// One dead forward leaves the service unusable, so the first exit — or the
	// first port that stops answering — ends the connection and takes the
	// others down with it.
	exit := make(chan *process.Supervisor, 1)
	go func() { exit <- waitExit(sups) }()

	var msg string
	select {
	case exited := <-exit:
		msg = t.transport.Name() + " exited"
		if exitErr := exited.ExitErr(); exitErr != nil {
			msg = fmt.Sprintf("%s exited: %v", t.transport.Name(), exitErr)
		}
	case err := <-dropped:
		msg = err.Error()
	}
	stopMonitor()
	t.stopForwards() //nolint:errcheck

	if ctx.Err() == nil {
		if last := t.sup.LastLines(1); len(last) > 0 && last[0] != "" {
			msg += " — " + last[0]
		}
		t.setState(service.RuntimeState{
			Status:    service.StatusError,
			Error:     errors.New(msg),
			LocalPort: port,
		})
	}
	return true
}

// open starts one SSM session and confirms its local port accepts connections,
// retrying a few times to ride out transient SSM failures.
func (t *Tunnel) open(ctx context.Context, sup *process.Supervisor, f Forward, labelled bool) error {
	// A group of its own, so a stop takes down what the command starts as
	// well: aws runs session-manager-plugin as its child. No WaitDelay: it
	// counts from the start, not the exit, and would cut off the last lines of
	// a long session that den's error messages quote.
	startOpts := process.StartOptions{ProcessGroup: true}
	tr := t.transport
	starting := tr.Name() + " starting..."
	if labelled {
		target := fmt.Sprintf("%s:%d", f.RemoteHost, f.RemotePort)
		starting = fmt.Sprintf("%s starting (%s → :%d)...", tr.Name(), target, f.LocalPort)
		if sup != t.sup {
			// Mirror the extra session's output into the service log, which is
			// the only log the TUI shows.
			primary := t.sup.LogWriter()
			startOpts.OnLine = func(line string) {
				fmt.Fprintf(primary, "[%s] %s\n", target, line) //nolint:errcheck
			}
		}
	}

	sup.Drain() // clear stale events from a previous connection
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			t.log(fmt.Sprintf("%s retry %d/%d...", tr.Name(), attempt, maxAttempts))
			sup.Stop() //nolint:errcheck
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryPause):
			}
			sup.Drain() // clear stale EventExited from the killed process
		}

		name, args, err := tr.Command(ctx, f)
		if err != nil {
			return err // a bad address does not get better by retrying
		}
		t.log(starting)
		if err := sup.StartWith(ctx, startOpts, name, args...); err != nil {
			lastErr = err
			t.log(fmt.Sprintf("%s start failed: %v", tr.Name(), err))
			continue
		}

		if waitReady(ctx, sup, tr, f.LocalPort) {
			t.log(fmt.Sprintf("port forwarding established on :%d", f.LocalPort))
			return nil
		}
		lastErr = fmt.Errorf("port %d not open after %s start", f.LocalPort, tr.Name())
		if last := sup.LastLines(1); len(last) > 0 && last[0] != "" {
			lastErr = fmt.Errorf("%w — %s", lastErr, last[0])
		}
		t.log(fmt.Sprintf("port %d not open (attempt %d/%d)", f.LocalPort, attempt, maxAttempts))
	}
	return lastErr
}

// waitReady polls until the local port accepts connections, the process
// exits, or the transport's start timeout passes. The port is the signal that
// counts: SSM logs success before its socket listens and exits silently when
// AssumeRole fails, and ssh -N prints nothing at all.
func waitReady(ctx context.Context, sup *process.Supervisor, tr Transport, port int) bool {
	deadline := time.Now().Add(tr.ReadyTimeout())
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for {
		if service.Reachable(addr, portCheckTimeout) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-sup.Done():
			return service.Reachable(addr, portCheckTimeout)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (t *Tunnel) refreshLoop(ctx context.Context, c Credentialer, expires time.Time, fwds []Forward) {
	defer t.setRefreshAt(time.Time{})
	for {
		now := time.Now()
		delay := refreshDelay(expires, now)
		t.setRefreshAt(now.Add(delay))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-t.renewed:
			// r minted a credential and logged when it is auto-refreshed:
			// re-arm for that one. A wake-up left over from an earlier
			// connection reads this connection's credential, which is right too.
			timer.Stop()
			if exp := t.State().TokenExpires; exp != nil {
				expires = *exp
			}
			continue
		case <-timer.C:
		}

		// Same rule as at connect time: never mint against a dead tunnel. For
		// RDS this rewrites ~/.pgpass, so refreshing over a forward that stopped
		// answering would leave working-looking credentials pointing at a port
		// that refuses connections. The monitor ends the connection shortly;
		// until it does, skip rather than publish.
		if !t.forwardsAlive(fwds) {
			t.log("skipping token refresh: tunnel is not accepting connections")
			continue
		}

		t.log("auto-refresh: renewing the token...")
		cred, err := c.Credential(ctx, t.log)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// expires is unchanged, so the next attempt comes minRefreshDelay
			// from now rather than a whole token lifetime later.
			t.log(fmt.Sprintf("token auto-refresh failed: %v", err))
			continue
		}
		t.applyCredential(cred)
		if !expiring(cred) {
			return
		}
		expires = cred.Expires
	}
}

func (t *Tunnel) setRefreshAt(at time.Time) {
	t.mu.Lock()
	t.refreshAt = at
	t.mu.Unlock()
}

// nextRefresh reports when the refresh loop renews the credential next, or the
// zero time while none runs.
func (t *Tunnel) nextRefresh() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.refreshAt
}

// refreshDelay is how long to wait before re-minting a credential that expires
// at exp: refreshLead ahead of expiry, but never sooner than minRefreshDelay.
func refreshDelay(exp, now time.Time) time.Duration {
	return max(exp.Sub(now)-refreshLead, minRefreshDelay)
}

// RenewalNote is what an engine logs with a credential it has just minted:
// when it expires and when the tunnel will mint the next one, e.g.
// "expires 14:43:13, auto-refresh at 14:41:13". A Tunnel mints only over a
// connected tunnel, whose refresh loop renews the credential at that time.
func RenewalNote(expires time.Time) string { return renewalNote(expires, time.Now()) }

func renewalNote(exp, now time.Time) string {
	return fmt.Sprintf("expires %s, auto-refresh at %s",
		exp.Local().Format("15:04:05"), now.Add(refreshDelay(exp, now)).Local().Format("15:04:05"))
}

func expiring(c Credential) bool {
	return c.Secret != "" && !c.Expires.IsZero()
}

func (t *Tunnel) applyCredential(cred Credential) {
	if cred.Secret == "" {
		return
	}
	t.mu.Lock()
	t.state.Token = cred.Secret
	if !cred.Expires.IsZero() {
		expires := cred.Expires
		t.state.TokenExpires = &expires
		t.state.TokenTTL = time.Until(expires).Round(time.Minute)
	}
	t.mu.Unlock()
	t.sendUpdate()
}

// supervisors returns one supervisor per forward, the primary first.
func (t *Tunnel) supervisors(n int) []*process.Supervisor {
	t.mu.Lock()
	defer t.mu.Unlock()
	for len(t.extra) < n-1 {
		t.extra = append(t.extra, process.NewSupervisor(50))
	}
	return append([]*process.Supervisor{t.sup}, t.extra[:n-1]...)
}

// stopForwards kills every SSM session. A session that already exited is not an error.
func (t *Tunnel) stopForwards() error {
	t.mu.RLock()
	sups := append([]*process.Supervisor{t.sup}, t.extra...)
	t.mu.RUnlock()

	var firstErr error
	for _, s := range sups {
		if err := s.Stop(); err != nil && !errors.Is(err, os.ErrProcessDone) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// forwardsAlive reports whether every forward still accepts a connection.
func (t *Tunnel) forwardsAlive(fwds []Forward) bool {
	for _, f := range fwds {
		if !service.Reachable(fmt.Sprintf("127.0.0.1:%d", f.LocalPort), portCheckTimeout) {
			return false
		}
	}
	return true
}

// monitor re-probes every forward while the connection is up, reporting on
// dropped once the ports have failed MonitorMaxMisses times in a row.
//
// This exists because a live process is not a live tunnel: session-manager-plugin
// keeps running after its websocket dies, so process exit — the only other
// liveness signal here — never arrives. The ssh tunnel service learned the same
// lesson; see the monitor in internal/service/tunnel.
func (t *Tunnel) monitor(ctx context.Context, fwds []Forward, dropped chan<- error) {
	ticker := time.NewTicker(t.opts.MonitorEvery)
	defer ticker.Stop()
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		bad := 0
		var failed Forward
		for _, f := range fwds {
			if !service.Reachable(fmt.Sprintf("127.0.0.1:%d", f.LocalPort), portCheckTimeout) {
				bad++
				failed = f
			}
		}
		if ctx.Err() != nil {
			return
		}
		if bad == 0 {
			misses = 0
			continue
		}
		misses++
		t.log(fmt.Sprintf("port :%d not accepting connections (%d/%d)", failed.LocalPort, misses, t.opts.MonitorMaxMisses))
		if misses < t.opts.MonitorMaxMisses {
			continue
		}
		select {
		case dropped <- fmt.Errorf("tunnel stopped accepting connections on :%d", failed.LocalPort):
		default:
		}
		return
	}
}

// waitExit blocks until any of sups exits and returns it. It waits on Done, not
// the events channel, because events drop on overflow and a chatty session can
// lose its own EventExited.
func waitExit(sups []*process.Supervisor) *process.Supervisor {
	if len(sups) == 1 {
		<-sups[0].Done()
		return sups[0]
	}
	exited := make(chan *process.Supervisor, len(sups))
	for _, s := range sups {
		go func(s *process.Supervisor) {
			<-s.Done()
			exited <- s
		}(s)
	}
	return <-exited
}

func (t *Tunnel) log(msg string) {
	t.sup.LogWriter().Write([]byte(msg + "\n")) //nolint:errcheck
}

func (t *Tunnel) setState(state service.RuntimeState) {
	t.mu.Lock()
	t.state = state
	t.mu.Unlock()
	t.sendUpdate()
}

func (t *Tunnel) sendUpdate() {
	t.mu.RLock()
	st := t.state
	st.Reconnect = t.reconnect
	t.mu.RUnlock()
	select {
	case t.updates <- service.StatusUpdate{Info: t.Info(), State: st}:
	default:
	}
}
