package vpn

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/process"
	"github.com/lukaszgard/den/internal/service"
)

const (
	samlLoginTimeout   = 2 * time.Minute
	processPollEvery   = 3 * time.Second
	healthCheckTimeout = 2 * time.Second
	healthCheckEvery   = 15 * time.Second
)

// reconnectDelays is the full retry schedule after a drop: three attempts, then
// den gives up until the user presses c. A VPN is not a tunnel — every attempt
// re-runs a SAML login that opens a browser tab, and each half-finished login
// holds a FortiGate session slot, so an unbounded 5s→60s loop like the tunnel's
// would spray browser tabs and can end with the gateway 403ing every new login.
var reconnectDelays = []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second}

// exitReason says why a connection ended, and crucially whether openfortivpn may
// still be running. A process that really exited needs no teardown; a tunnel that
// died underneath a live process does — otherwise the session stays registered
// with the gateway and leaks one of the account's concurrent-session slots.
type exitReason struct {
	msg            string
	processMayLive bool
}

var (
	reasonProcessExited = exitReason{msg: "openfortivpn exited"}
	reasonInterfaceGone = exitReason{msg: "VPN interface ppp0 disappeared", processMayLive: true}
	reasonDataPathDead  = exitReason{msg: "VPN data path went dead", processMayLive: true}
)

// attemptOutcome records how one connect attempt ended, which decides whether a
// retry is safe. outcomeLoginTimeout is disqualifying: it means the login
// never finished, and repeating that is what exhausts the gateway's session
// slots until it 403s every new login. outcomeFatal is too: the machine itself
// cannot run the tunnel, so every retry would only cost another SAML login.
type attemptOutcome int

const (
	outcomeFailed       attemptOutcome = iota // never came up, but the login did not hang
	outcomeConnected                          // reached connected, then dropped
	outcomeLoginTimeout                       // SAML login never completed — never retry this
	outcomeFatal                              // no PPP in the kernel, no SAML in openfortivpn — never retry this
)

// The TUI discovers the r/H key bindings by type-asserting to these, so losing
// one would silently drop a toggle from the panes rather than fail to build.
// Monitorable is deliberately not implemented: for a VPN the health check is the
// monitor, so a second toggle would gate the same loop twice.
var (
	_ service.Reconnectable   = (*Service)(nil)
	_ service.HealthCheckable = (*Service)(nil)
)

// Service implements service.Service for openfortivpn-based VPN connections.
type Service struct {
	mu          sync.RWMutex
	spec        *config.VPNSpec
	name        string
	sup         *process.Supervisor
	state       service.RuntimeState
	updates     chan service.StatusUpdate
	cancel      context.CancelFunc
	monitoring  bool   // true while den has an active management goroutine
	logFilePath string // temp file holding openfortivpn output; empty in helper mode, where the helper owns the log
	slug        string // profile name the privilege helper knows this VPN by

	// The two runtime toggles, guarded by mu and seeded from the spec. Flipping
	// one never writes back to den.yaml, so a restart restores the file's values.
	// Note monitoring above is unrelated: it is the "one management goroutine at a
	// time" latch, not a user-facing setting.
	reconnect   bool
	healthCheck bool

	outcome attemptOutcome // how the current connect attempt ended; guarded by mu

	// stopTunnel is killTunnel; tests replace it, since the real one stops
	// whatever openfortivpn runs on this machine.
	stopTunnel func(context.Context)
}

// New constructs a VPN service. Returns an error on unsupported platforms or
// when required config is missing.
func New(name string, spec *config.VPNSpec) (*Service, error) {
	if spec == nil || (spec.Gateway == "" && spec.ConfigFile == "") {
		return nil, fmt.Errorf("vpn: either gateway or config_file is required")
	}
	if err := supportedOS(); err != nil {
		return nil, err
	}
	svc := &Service{
		spec:        spec,
		name:        name,
		slug:        Slug(name),
		reconnect:   spec.Reconnect,
		healthCheck: spec.HealthCheck != "",
		sup:         process.NewSupervisor(200),
		updates:     make(chan service.StatusUpdate, 32),
		state:       service.RuntimeState{Status: service.StatusIdle},
	}
	svc.stopTunnel = svc.killTunnel
	if _, err := exec.LookPath("openfortivpn"); err != nil {
		svc.state = service.RuntimeState{
			Status: service.StatusError,
			Error:  fmt.Errorf("openfortivpn not found — install via `brew install openfortivpn` or `apt install openfortivpn`"),
		}
	} else if vpnIsRunning() {
		// A tunnel from a previous den session (or a manual launch) is still up.
		// Process + ppp0 alone don't prove the data path carries traffic — a dead
		// tunnel can leave both lingering as zombies. When a health_check is set,
		// confirm an internal host is actually reachable before adopting.
		if spec.HealthCheck == "" || svc.dataPathAlive() {
			// Seed Connected so the pane snapshot reflects it immediately; the TUI
			// kicks StartOne on startup, which adopts and monitors it (see Start).
			svc.state = service.RuntimeState{
				Status:     service.StatusConnected,
				ConnectCmd: svc.ConnectCommand(),
			}
		} else {
			svc.state = service.RuntimeState{
				Status: service.StatusError,
				Error: fmt.Errorf("openfortivpn and ppp0 are present but %s is unreachable — "+
					"the tunnel data path appears dead; press d to disconnect, then c to reconnect", spec.HealthCheck),
			}
		}
	}
	return svc, nil
}

// dataPathAlive probes the configured health_check host:port to confirm the VPN
// data path actually carries traffic. It retries briefly so a single transient
// miss doesn't read as a dead tunnel. Returns true when no health_check is set
// (nothing to verify — caller falls back to the interface check).
func (s *Service) dataPathAlive() bool {
	target := s.activeHealthCheck()
	if target == "" {
		return true
	}
	for range 3 {
		if service.Reachable(target, healthCheckTimeout) {
			return true
		}
	}
	return false
}

// vpnIsRunning reports whether an openfortivpn tunnel is already established
// (process present AND ppp0 up) from a previous den session or manual launch.
// Both conditions are required so we don't adopt a half-open process still
// completing SAML login.
func vpnIsRunning() bool {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return false
	}
	if len(openfortivpnPIDs()) == 0 {
		return false
	}
	return interfaceExists("ppp0")
}

// openfortivpnPIDs lists the openfortivpn processes on this machine, whoever
// started them, by process name (watchProcess says why not -f).
func openfortivpnPIDs() []string {
	out, _ := exec.Command("pgrep", "-x", "openfortivpn").Output()
	return strings.Fields(string(out))
}

func (s *Service) Info() service.ServiceInfo {
	return service.ServiceInfo{Name: s.name, Type: service.TypeVPN, Environment: s.spec.Description}
}

func (s *Service) State() service.RuntimeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.state
	return st
}

func (s *Service) LogWriter() io.Writer { return s.sup.LogWriter() }

func (s *Service) Logs() []string { return s.sup.Logs() }

func (s *Service) LogSeq() uint64 { return s.sup.LogSeq() }

func (s *Service) ConnectCommand() string {
	return "ifconfig ppp0"
}

// Description returns the VPN's description from the config, or "".
func (s *Service) Description() string { return s.spec.Description }

// HealthCheckTarget returns the configured internal host:port probed to verify
// the VPN data path, or "" when none is configured. It reports the target even
// while the probe is switched off, so the TUI can show what would be checked.
func (s *Service) HealthCheckTarget() string {
	return s.spec.HealthCheck
}

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

// ReconnectEnabled reports whether a dropped tunnel is re-established.
func (s *Service) ReconnectEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reconnect
}

// ToggleReconnect flips the runtime reconnect setting and returns the new value.
// It applies at the next drop; it cannot cancel a retry already waiting.
func (s *Service) ToggleReconnect() bool {
	s.mu.Lock()
	s.reconnect = !s.reconnect
	enabled := s.reconnect
	s.mu.Unlock()
	s.sendUpdate()
	return enabled
}

// ToggleHealthCheck flips the runtime health-check setting and returns the new
// value. It is inert when no target is configured — there is nothing to probe.
// The data-path loop re-reads it every tick, so it applies within healthCheckEvery.
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

func (s *Service) RefreshToken(ctx context.Context) error { return nil }

func (s *Service) Start(ctx context.Context) (<-chan service.StatusUpdate, error) {
	s.mu.Lock()
	// monitoring means a management goroutine (run or monitorAdopted) is already
	// active for this service — refuse a second one.
	if s.monitoring || s.state.Status == service.StatusConnecting {
		s.mu.Unlock()
		return s.updates, service.ErrAlreadyRunning
	}
	// New() seeds StatusConnected when it detects a tunnel already up; adopt it
	// (monitor for drops) rather than launching a second openfortivpn.
	adopt := s.state.Status == service.StatusConnected
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.monitoring = true
	s.mu.Unlock()

	if adopt {
		go s.monitorAdopted(runCtx)
	} else {
		go s.run(runCtx)
	}
	return s.updates, nil
}

// monitorAdopted watches an already-running openfortivpn tunnel that den did
// not launch itself (adopted at startup). It runs the same drop-detection as a
// den-initiated connection but never spawns a process — a later drop transitions
// to StatusError via waitAfterConnected/failFromLastLine.
func (s *Service) monitorAdopted(ctx context.Context) {
	defer s.clearMonitoring()
	s.log("adopted an already-running openfortivpn tunnel from a previous session")
	// The tunnel is up, so a drop from here is retry-eligible just like one from a
	// session den started itself. Nothing called markConnected on this path.
	s.setOutcome(outcomeConnected)
	exitCh := make(chan exitReason, 1)
	var once sync.Once
	signalExit := func(r exitReason) { once.Do(func() { exitCh <- r }) }
	switch runtime.GOOS {
	case "darwin":
		go s.watchProcess(ctx, signalExit)
	case "linux":
		go s.watchInterface(ctx, signalExit)
	}
	go s.watchDataPath(ctx, signalExit)
	s.waitAfterConnected(ctx, exitCh)
	if ctx.Err() != nil {
		return
	}
	s.reconnectAfterDrop(ctx)
}

func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	cancel := s.cancel
	logPath := s.logFilePath
	s.monitoring = false
	s.mu.Unlock()

	s.setState(service.RuntimeState{Status: service.StatusStopping})
	s.killTunnel(ctx)

	if cancel != nil {
		cancel()
	}
	if err := s.sup.Stop(); err != nil {
		s.log(fmt.Sprintf("supervisor stop: %v", err))
	}
	if logPath != "" {
		_ = os.Remove(logPath)
	}
	s.setState(service.RuntimeState{Status: service.StatusIdle})
	return nil
}

// killTunnel terminates openfortivpn, preferring the privilege helper so the
// user is not prompted. It is safe to call when nothing is running.
//
// This is deliberately reachable from the drop path as well as from Stop. A
// tunnel that dies underneath a live openfortivpn leaves the session registered
// with the gateway, and FortiGate counts every one against the account's
// concurrent-session limit — past a few, /remote/saml/start starts answering
// 403 and no new login can be started at all.
func (s *Service) killTunnel(ctx context.Context) {
	var (
		name string
		args []string
		err  error
	)
	if helper, _ := Available(ctx); helper {
		name, args = helperCommand("stop", s.slug)
		s.log("tearing down VPN via the privilege helper — no admin prompt")
		s.logOutdatedHelper()
	} else {
		name, args, err = stopCommand(s.spec.Gateway, s.spec.ConfigFilePath())
		if err == nil {
			s.log("tearing down VPN — your OS will prompt for your admin password again")
		}
	}
	if err != nil {
		s.log(fmt.Sprintf("cannot terminate openfortivpn: %v", err))
		return
	}
	s.log(fmt.Sprintf("terminating openfortivpn via %s", name))
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = s.sup.LogWriter()
	cmd.Stderr = s.sup.LogWriter()
	if runErr := cmd.Run(); runErr != nil {
		s.log(fmt.Sprintf("stop command: %v", runErr))
	}
}

// ---

// run drives the connect lifecycle, retrying a dropped tunnel when reconnect is
// on. Unlike the tunnel service's open-ended 5s→60s loop, this one is capped at
// len(reconnectDelays) attempts and refuses to retry a login that timed out:
// every attempt re-runs an interactive SAML login that opens a browser tab, and
// half-finished logins hold gateway session slots.
func (s *Service) run(ctx context.Context) {
	defer s.clearMonitoring()

	s.setOutcome(outcomeFailed)
	s.runOnce(ctx)
	if ctx.Err() != nil {
		return
	}
	s.reconnectAfterDrop(ctx)
}

// reconnectAfterDrop retries a connection that has gone away, until it comes back,
// the attempt cap is reached, or a gate in nextRetry says stop. Shared by run and
// monitorAdopted: a tunnel den adopted from a previous session drops exactly the
// same way as one it started itself.
func (s *Service) reconnectAfterDrop(ctx context.Context) {
	for attempt := 0; ; attempt++ {
		delay, ok := s.nextRetry(ctx, attempt)
		if !ok {
			return
		}

		// Free the gateway session slot before asking for another one. killTunnel
		// is safe when nothing is running, and skipping it is how repeated retries
		// exhaust the concurrent-session limit.
		s.killTunnel(ctx)

		retryAt := time.Now().Add(delay)
		s.setState(service.RuntimeState{Status: service.StatusReconnecting, ReconnectAt: &retryAt})
		s.log(fmt.Sprintf("reconnecting in %v (attempt %d of %d)...",
			delay.Round(time.Second), attempt+2, len(reconnectDelays)+1))

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		s.setOutcome(outcomeFailed)
		s.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
	}
}

// nextRetry reports how long to wait before the next connect attempt, or false
// when den should stop and leave the VPN in its error state.
func (s *Service) nextRetry(ctx context.Context, attempt int) (time.Duration, bool) {
	s.mu.RLock()
	enabled, outcome := s.reconnect, s.outcome
	s.mu.RUnlock()

	if !enabled {
		return 0, false
	}
	if outcome == outcomeLoginTimeout {
		s.log("not reconnecting: the SAML login never completed — retrying would hold more gateway sessions")
		return 0, false
	}
	if outcome == outcomeFatal {
		s.log("not reconnecting: this machine cannot run the tunnel until the error above is fixed")
		return 0, false
	}
	if attempt >= len(reconnectDelays) {
		s.log(fmt.Sprintf("giving up after %d reconnect attempts — press c to try again", len(reconnectDelays)))
		return 0, false
	}
	// Without the privilege helper every attempt raises an OS password dialog,
	// which must never fire unattended from a background retry.
	if helper, _ := Available(ctx); !helper {
		s.log("not reconnecting: needs the passwordless VPN helper — run `den vpn install-helper`")
		return 0, false
	}
	return reconnectDelays[attempt], true
}

func (s *Service) runOnce(ctx context.Context) {
	// A second openfortivpn over one still running costs a SAML login and a
	// gateway session slot, and cannot come up: the first one holds ppp0 and the
	// DNS. Start adopts a tunnel it found, so one here is a process den could not
	// take over or stop, such as one hung in its logout after a stop. That is
	// fatal, so a reconnect stops here too rather than retrying.
	if pids := openfortivpnPIDs(); len(pids) > 0 {
		pid := strings.Join(pids, ", ")
		s.log(fmt.Sprintf("not starting openfortivpn: one is already running (pid %s)", pid))
		s.markFatal(fmt.Sprintf("openfortivpn is still running (pid %s) — press d to stop it, then c", pid))
		return
	}
	s.setState(service.RuntimeState{Status: service.StatusConnecting})

	helper, why := Available(ctx)
	s.logConnectionMode(helper, why)
	if helper && s.runHelper(ctx) {
		return
	}

	s.log("openfortivpn requires root privileges — your OS will now prompt for your admin password")
	switch runtime.GOOS {
	case "darwin":
		s.log("approve the macOS dialog (\"osascript wants to make changes\") to continue")
		s.runDarwin(ctx)
	case "linux":
		s.log("approve the PolicyKit dialog to continue")
		s.runLinux(ctx)
	default:
		s.setState(service.RuntimeState{Status: service.StatusError, Error: fmt.Errorf("VPN not supported on %s", runtime.GOOS)})
	}
}

// logConnectionMode emits log lines describing exactly how openfortivpn will
// be invoked — privilege mode, connection mode, source of each parameter, and
// the full command.
func (s *Service) logConnectionMode(helper bool, why string) {
	if helper {
		s.log(fmt.Sprintf("privilege mode: passwordless helper (%s) — no admin prompt", HelperPath))
		s.log(fmt.Sprintf("profile: %s (%s)", s.slug, HelperConfPath(s.slug)))
		s.log(fmt.Sprintf("command: sudo -n %s start %s", HelperPath, s.slug))
		s.logOutdatedHelper()
		return
	}
	s.log("privilege mode: admin prompt — " + why)
	switch {
	case s.spec.Gateway != "" && s.spec.ConfigFile != "":
		s.log("connection mode: gateway + config file")
		s.log(fmt.Sprintf("  gateway:     %s", s.spec.Gateway))
		s.log(fmt.Sprintf("  config file: %s", s.spec.ConfigFilePath()))
	case s.spec.ConfigFile != "":
		s.log("connection mode: config file only (host and credentials read from file)")
		s.log(fmt.Sprintf("  config file: %s", s.spec.ConfigFilePath()))
	default:
		s.log("connection mode: gateway only")
		s.log(fmt.Sprintf("  gateway: %s", s.spec.Gateway))
	}
	if len(s.spec.ExtraArgs) > 0 {
		s.log(fmt.Sprintf("  extra args:  %s", strings.Join(s.spec.ExtraArgs, " ")))
	}
	cmdParts := []string{"openfortivpn"}
	if s.spec.Gateway != "" {
		cmdParts = append(cmdParts, s.spec.Gateway)
	}
	cmdParts = append(cmdParts, "--saml-login")
	if s.spec.ConfigFile != "" {
		cmdParts = append(cmdParts, "-c", s.spec.ConfigFilePath())
	}
	cmdParts = append(cmdParts, s.spec.ExtraArgs...)
	s.log(fmt.Sprintf("command: %s", strings.Join(cmdParts, " ")))
}

// logOutdatedHelper says so when the installed wrapper is older than this den's,
// so the log explains a stop that left openfortivpn running.
func (s *Service) logOutdatedHelper() {
	if outdated, _ := InstalledScriptOutdated(); outdated {
		s.log("the installed helper script is outdated — " + OutdatedScriptFix)
	}
}

// runHelper starts the tunnel through the privilege helper. The helper returns
// as soon as openfortivpn is detached, so from here on this is the same flow as
// runDarwin: tail the log the helper handed us for the SAML URL and the
// tunnel-up line, then watch for drops.
//
// It returns false when the helper could not be used at all, so the caller can
// still fall back to the admin prompt — `sudo -n` can succeed for the check and
// then fail for the real command if it was only riding on a cached credential.
func (s *Service) runHelper(ctx context.Context) bool {
	name, args := helperCommand("start", s.slug)
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if text := strings.TrimSpace(string(out)); text != "" {
		s.log(text)
	}
	if err != nil {
		if ctx.Err() != nil {
			return true // cancelled, not a helper problem
		}
		s.log(fmt.Sprintf("privilege helper could not start the tunnel (%v) — falling back to the admin prompt", err))
		return false
	}

	// The log lives under the helper's root-owned directory and is reused on
	// every connect, so — unlike the osascript path — den must not delete it.
	logPath := HelperLogPath(s.slug)
	s.mu.Lock()
	s.logFilePath = ""
	s.mu.Unlock()

	readyCh := make(chan struct{})
	fatalCh, fatal := fatalSignal()
	go s.tailLogFile(ctx, logPath, readyCh, fatal)

	if !s.waitForReady(ctx, readyCh, fatalCh) {
		return true
	}

	exitCh := make(chan exitReason, 1)
	var once sync.Once
	signalExit := func(r exitReason) { once.Do(func() { exitCh <- r }) }
	go s.watchProcess(ctx, signalExit)
	go s.watchDataPath(ctx, signalExit)
	s.waitAfterConnected(ctx, exitCh)
	return true
}

// runDarwin uses osascript to trigger the macOS admin password prompt and
// background openfortivpn to a temp log file. We then tail the log file to
// detect the SAML URL and tunnel-up signal — osascript itself buffers stdout
// and cannot stream it, so a side-channel log file is required.
func (s *Service) runDarwin(ctx context.Context) {
	logFile, err := os.CreateTemp("", "den-vpn-*.log")
	if err != nil {
		s.setState(service.RuntimeState{Status: service.StatusError, Error: fmt.Errorf("create temp log: %w", err)})
		return
	}
	logPath := logFile.Name()
	logFile.Close()

	s.mu.Lock()
	s.logFilePath = logPath
	s.mu.Unlock()

	script := darwinStartScript(s.spec.Gateway, s.spec.ConfigFilePath(), s.spec.ExtraArgs, logPath)
	s.log("requesting admin authentication via osascript…")

	osa := exec.CommandContext(ctx, "osascript", "-e", script)
	osaOut, osaErr := osa.CombinedOutput()
	if osaErr != nil {
		out := strings.TrimSpace(string(osaOut))
		s.log(fmt.Sprintf("osascript failed: %v %s", osaErr, out))
		s.setState(service.RuntimeState{
			Status: service.StatusError,
			Error:  fmt.Errorf("admin authentication cancelled or failed: %v", osaErr),
		})
		_ = os.Remove(logPath)
		return
	}
	s.log("admin authentication approved — openfortivpn starting in background")

	readyCh := make(chan struct{})
	fatalCh, fatal := fatalSignal()
	go s.tailLogFile(ctx, logPath, readyCh, fatal)

	// Pre-tunnel: rely on the log tail + SAML timeout only. pgrep is unreliable
	// here — the backgrounded process gets reparented to launchd and a single
	// transient miss would falsely trip "exited before tunnel came up" while the
	// user is still completing SAML in the browser.
	if !s.waitForReady(ctx, readyCh, fatalCh) {
		return
	}

	// Connected — now start polling pgrep to detect an unexpected exit, and
	// (when configured) probe the internal host to detect a dead data path.
	exitCh := make(chan exitReason, 1)
	var once sync.Once
	signalExit := func(r exitReason) { once.Do(func() { exitCh <- r }) }
	go s.watchProcess(ctx, signalExit)
	go s.watchDataPath(ctx, signalExit)
	s.waitAfterConnected(ctx, exitCh)
}

// runLinux uses pkexec which streams stdout/stderr in real time, so the
// supervisor-based path can detect both the ready signal and the process exit
// directly — no pgrep dance required.
func (s *Service) runLinux(ctx context.Context) {
	name, args := linuxStartCommand(s.spec.Gateway, s.spec.ConfigFilePath(), s.spec.ExtraArgs)
	s.log(fmt.Sprintf("launching %s %s", name, strings.Join(args, " ")))

	s.sup.Drain()
	if err := s.sup.Start(ctx, name, args...); err != nil {
		s.setState(service.RuntimeState{Status: service.StatusError, Error: err})
		return
	}

	readyCh := make(chan struct{})
	exitCh := make(chan exitReason, 1)
	var closeOnce sync.Once
	signalExit := func(r exitReason) { closeOnce.Do(func() { exitCh <- r }) }
	fatalCh, fatal := fatalSignal()

	go s.watchSupervisorEvents(readyCh, signalExit, fatal)

	if !s.waitForReadyOrExit(ctx, readyCh, exitCh, fatalCh) {
		return
	}

	go s.watchInterface(ctx, signalExit)
	go s.watchDataPath(ctx, signalExit)
	s.waitAfterConnected(ctx, exitCh)
}

// waitForReady blocks until the tunnel comes up, a line says it never will
// (fatalLine), the SAML timeout fires, or the context is cancelled. Returns true on success. Used on darwin where we
// have no reliable pre-tunnel exit signal.
func (s *Service) waitForReady(ctx context.Context, readyCh <-chan struct{}, fatalCh <-chan string) bool {
	select {
	case <-readyCh:
		s.markConnected()
		return true
	case msg := <-fatalCh:
		s.failFatal(ctx, msg)
		return false
	case <-time.After(samlLoginTimeout):
		s.log("timed out waiting for SAML login + tunnel")
		s.setOutcome(outcomeLoginTimeout)
		s.setState(service.RuntimeState{
			Status: service.StatusError,
			Error:  fmt.Errorf("timed out after %s waiting for tunnel", samlLoginTimeout),
		})
		return false
	case <-ctx.Done():
		return false
	}
}

// waitForReadyOrExit is the linux variant: the supervisor gives us an
// authoritative process exit signal, so we can fail fast on early exit.
func (s *Service) waitForReadyOrExit(ctx context.Context, readyCh <-chan struct{}, exitCh <-chan exitReason, fatalCh <-chan string) bool {
	select {
	case <-readyCh:
		s.markConnected()
		return true
	case msg := <-fatalCh:
		s.failFatal(ctx, msg)
		return false
	case reason := <-exitCh:
		if reason.processMayLive {
			s.killTunnel(ctx)
		}
		// Lines are read before the exit, so a fatal one is already here, and
		// it says more than "exited".
		select {
		case msg := <-fatalCh:
			s.markFatal(msg)
		default:
			s.failFromLastLine(reason.msg + " before tunnel came up")
		}
		return false
	case <-time.After(samlLoginTimeout):
		// The process is still sitting in an unfinished SAML login. Leaving it
		// there holds a session slot on the gateway, and enough of those make
		// /remote/saml/start answer 403 for every later attempt.
		s.log("timed out waiting for SAML login + tunnel")
		s.setOutcome(outcomeLoginTimeout)
		s.killTunnel(ctx)
		s.setState(service.RuntimeState{
			Status: service.StatusError,
			Error:  fmt.Errorf("timed out after %s waiting for tunnel", samlLoginTimeout),
		})
		return false
	case <-ctx.Done():
		return false
	}
}

// waitAfterConnected blocks until openfortivpn exits or the context is
// cancelled, transitioning to error state on unexpected exit.
func (s *Service) waitAfterConnected(ctx context.Context, exitCh <-chan exitReason) {
	select {
	case reason := <-exitCh:
		if ctx.Err() != nil {
			return
		}
		// Only a process that really exited can be left alone. Anything else is
		// still holding a session with the gateway, so tear it down before
		// reporting the failure — otherwise every drop leaks a session slot and
		// eventually the gateway refuses to start a new SAML login at all.
		if reason.processMayLive {
			s.killTunnel(ctx)
		}
		s.failFromLastLine(reason.msg)
	case <-ctx.Done():
		return
	}
}

func (s *Service) markConnected() {
	now := time.Now()
	s.mu.Lock()
	s.state = service.RuntimeState{
		Status:     service.StatusConnected,
		StartedAt:  &now,
		ConnectCmd: s.ConnectCommand(),
	}
	// The login completed, so a later drop is safe to retry.
	s.outcome = outcomeConnected
	s.mu.Unlock()
	s.sendUpdate()
}

// failFatal ends an attempt that cannot succeed on this machine. It stops
// openfortivpn, which a persistent setting would otherwise keep re-running
// against a closed SAML session, and keeps auto-reconnect away from it.
func (s *Service) failFatal(ctx context.Context, msg string) {
	s.log("stopping openfortivpn: " + msg)
	s.stopTunnel(ctx)
	s.markFatal(msg)
}

// markFatal puts the VPN in its error state with msg, never to be retried.
func (s *Service) markFatal(msg string) {
	s.setOutcome(outcomeFatal)
	s.setState(service.RuntimeState{Status: service.StatusError, Error: fmt.Errorf("%s", msg)})
}

// fatalSignal returns the channel that receives the first fatal line's
// message, and the func the log readers report it through.
func fatalSignal() (<-chan string, func(string)) {
	ch := make(chan string, 1)
	return ch, func(msg string) {
		select {
		case ch <- msg:
		default: // the first one already ends the attempt
		}
	}
}

// setOutcome records how the current attempt ended, for the reconnect decision.
func (s *Service) setOutcome(o attemptOutcome) {
	s.mu.Lock()
	s.outcome = o
	s.mu.Unlock()
}

func (s *Service) failFromLastLine(prefix string) {
	msg := prefix
	if last := s.sup.LastLines(1); len(last) > 0 && last[0] != "" {
		msg += " — " + last[0]
	}
	s.setState(service.RuntimeState{Status: service.StatusError, Error: fmt.Errorf("%s", msg)})
}

// tailLogFile streams new lines from path into the supervisor log buffer,
// auto-opens the SAML URL on first match, closes readyCh when the tunnel is
// up, and reports a line no retry gets past (fatalLine) before then. It exits
// when ctx is cancelled.
func (s *Service) tailLogFile(ctx context.Context, path string, readyCh chan<- struct{}, fatal func(string)) {
	f, err := os.Open(path)
	if err != nil {
		s.log(fmt.Sprintf("tail: open %s: %v", path, err))
		return
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	readySent := false
	samlOpened := false
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\n")
			s.sup.LogWriter().Write([]byte(line + "\n")) //nolint:errcheck

			if !samlOpened {
				if url, ok := extractSAMLURL(line); ok {
					samlOpened = true
					s.log("opening SAML login URL in browser")
					if openErr := platform.OpenURL(url); openErr != nil {
						s.log(fmt.Sprintf("browser open failed: %v — open manually: %s", openErr, url))
					}
				}
			}
			if msg, ok := fatalLine(line); ok && !readySent {
				fatal(msg)
			}
			if !readySent && strings.Contains(line, "Tunnel is up and running") {
				readySent = true
				close(readyCh)
			}
		}
		if readErr == io.EOF {
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
			continue
		}
		if readErr != nil {
			return
		}
	}
}

// watchProcess polls pgrep and the ppp0 interface to detect when the
// backgrounded openfortivpn exits or the tunnel drops. Used on darwin only.
//
// We match by process name (-x), not full command line (-f). On macOS the
// argv of a process owned by another user (openfortivpn runs as root) is
// hidden from non-root callers via KERN_PROCARGS2 restrictions, so `pgrep -f`
// would always come back empty and falsely report an exit. The process name
// (p_comm) is visible cross-user.
func (s *Service) watchProcess(ctx context.Context, signalExit func(exitReason)) {
	// Give the backgrounded process a moment to actually start before polling.
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Second):
	}
	ticker := time.NewTicker(processPollEvery)
	defer ticker.Stop()
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			processOk := len(openfortivpnPIDs()) > 0
			interfaceOk := interfaceExists("ppp0")
			if !processOk || !interfaceOk {
				misses++
				if misses >= 2 {
					// A missing process is already gone; a missing interface
					// under a live process has to be killed, or it keeps the
					// gateway session open.
					if !processOk {
						signalExit(reasonProcessExited)
					} else {
						s.log("VPN interface ppp0 is gone — tunnel dropped")
						signalExit(reasonInterfaceGone)
					}
					return
				}
				continue
			}
			misses = 0
		}
	}
}

// watchInterface polls the ppp0 network interface to detect tunnel drops
// independently of the process state. Signals via signalExit when gone.
func (s *Service) watchInterface(ctx context.Context, signalExit func(exitReason)) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(3 * time.Second):
	}
	ticker := time.NewTicker(processPollEvery)
	defer ticker.Stop()
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !interfaceExists("ppp0") {
				misses++
				if misses >= 2 {
					s.log("VPN interface ppp0 is gone — tunnel dropped")
					signalExit(reasonInterfaceGone)
					return
				}
			} else {
				misses = 0
			}
		}
	}
}

// watchDataPath periodically probes the configured health_check host:port to
// detect a dead data path — a tunnel that silently stops carrying traffic while
// the openfortivpn process and ppp0 interface both linger. It is a no-op when no
// health_check is configured. Signals via signalExit on repeated failure.
// The loop runs whenever a target is configured, not only while the check is on,
// and re-reads the toggle every tick — so switching the probe off and back on
// mid-connection works without reconnecting.
func (s *Service) watchDataPath(ctx context.Context, signalExit func(exitReason)) {
	if s.spec.HealthCheck == "" {
		return
	}
	ticker := time.NewTicker(healthCheckEvery)
	defer ticker.Stop()
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		target := s.activeHealthCheck()
		if target == "" {
			misses = 0 // re-enabling starts from a clean slate
			continue
		}
		if service.Reachable(target, healthCheckTimeout) {
			misses = 0
			continue
		}
		misses++
		if misses >= 2 {
			s.log(fmt.Sprintf("VPN data path check failed — %s unreachable; tunnel dropped", target))
			signalExit(reasonDataPathDead)
			return
		}
	}
}

// interfaceExists reports whether the named network interface is present.
func interfaceExists(name string) bool {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("ifconfig", name).Output()
		return err == nil && len(out) > 0
	case "linux":
		_, err := exec.Command("ip", "link", "show", name).Output()
		return err == nil
	}
	return false
}

// watchSupervisorEvents bridges process.Supervisor events to the readyCh /
// signalExit used by waitForTunnel. Linux only.
func (s *Service) watchSupervisorEvents(readyCh chan<- struct{}, signalExit func(exitReason), fatal func(string)) {
	readySent := false
	samlOpened := false
	for ev := range s.sup.Events() {
		switch ev.Kind {
		case process.EventLine:
			if !samlOpened {
				if url, ok := extractSAMLURL(ev.Line); ok {
					samlOpened = true
					s.log("opening SAML login URL in browser")
					if err := platform.OpenURL(url); err != nil {
						s.log(fmt.Sprintf("browser open failed: %v — open manually: %s", err, url))
					}
				}
			}
			if msg, ok := fatalLine(ev.Line); ok && !readySent {
				fatal(msg)
			}
			if !readySent && strings.Contains(ev.Line, "Tunnel is up and running") {
				readySent = true
				close(readyCh)
			}
		case process.EventExited:
			signalExit(reasonProcessExited)
			return
		}
	}
}

// clearMonitoring marks the service as no longer actively managed, so a later
// Start (e.g. a manual reconnect after an error or drop) is allowed through.
func (s *Service) clearMonitoring() {
	s.mu.Lock()
	s.monitoring = false
	s.mu.Unlock()
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
	s.mu.RUnlock()
	select {
	case s.updates <- service.StatusUpdate{Info: s.Info(), State: st}:
	default:
	}
}
