package service

import (
	"context"
	"errors"
	"time"
)

// ErrAlreadyRunning is returned by Start when the service is already up, so the
// TUI can show a passing warning rather than an error.
var ErrAlreadyRunning = errors.New("already running")

// Status represents the runtime state of a service.
type Status int

const (
	StatusIdle Status = iota
	StatusConnecting
	StatusConnected
	StatusError
	StatusStopping
	StatusReconnecting
)

func (s Status) String() string {
	switch s {
	case StatusIdle:
		return "idle"
	case StatusConnecting:
		return "connecting"
	case StatusConnected:
		return "connected"
	case StatusError:
		return "error"
	case StatusStopping:
		return "stopping"
	case StatusReconnecting:
		return "reconnecting"
	default:
		return "unknown"
	}
}

// Service types, as reported in ServiceInfo.Type.
const (
	TypeRDS        = "rds"
	TypeKafka      = "kafka"
	TypeRedis      = "redis"
	TypeDocumentDB = "documentdb"
	TypeRedshift   = "redshift"
	TypeOpenSearch = "opensearch"
	TypeNeptune    = "neptune"
	TypeDocker     = "docker"
	TypeTunnel     = "tunnel"
	TypeVPN        = "vpn"
	TypeAWS        = "aws"
)

var typeLabels = map[string]string{
	TypeTunnel:     "SSH tunnel",
	TypeRDS:        "RDS connection",
	TypeKafka:      "Kafka connection",
	TypeRedis:      "Redis connection",
	TypeDocumentDB: "DocumentDB connection",
	TypeRedshift:   "Redshift connection",
	TypeOpenSearch: "OpenSearch connection",
	TypeNeptune:    "Neptune connection",
	TypeDocker:     "Docker compose",
	TypeVPN:        "VPN connection",
	TypeAWS:        "AWS SSO profile",
}

// TypeLabel describes a service type for people, e.g. "RDS connection". An
// unknown type is returned as-is.
func TypeLabel(typ string) string {
	if l, ok := typeLabels[typ]; ok {
		return l
	}
	return typ
}

// ServiceInfo holds static metadata about a service.
type ServiceInfo struct {
	Name        string
	Type        string // one of the Type* constants
	Environment string // e.g. "prod", "staging", "dev"
	Production  bool   // den.yaml marks it production: an agent needs the user's OK to connect it
}

// RuntimeState holds the live state of a service.
type RuntimeState struct {
	Status       Status
	Error        error
	StartedAt    *time.Time
	TokenExpires *time.Time    // set by services holding an expiring credential
	TokenTTL     time.Duration // full lifetime of that credential, for the TTL bar; zero means 15 min
	Token        string        // last minted credential (IAM auth token, temporary password)
	LocalPort    int
	ConnectCmd   string
	ReconnectAt  *time.Time // set when StatusReconnecting; TUI renders countdown from this
	Reconnect    bool       // current runtime reconnect setting
}

// StatusUpdate is sent by a service when its state changes.
type StatusUpdate struct {
	Info  ServiceInfo
	State RuntimeState
}

// Service is the common interface all service types implement.
type Service interface {
	Info() ServiceInfo
	Start(ctx context.Context) (<-chan StatusUpdate, error)
	Stop(ctx context.Context) error
	RefreshToken(ctx context.Context) error // no-op for services without a credential
	ConnectCommand() string
	State() RuntimeState
	// Logs returns the service's recent log lines. It copies them, so callers
	// that poll should compare LogSeq first and only read Logs when it moved.
	Logs() []string
	LogSeq() uint64
}

// Describer is implemented by services whose config carries a free-text
// description, which the TUI shows alongside the service's name.
type Describer interface {
	Description() string
}

// Reconnectable is implemented by services that can auto-reopen their tunnel
// when it drops (SSM tunnels and SSH tunnels). The TUI type-asserts to this to
// wire the reconnect toggle without knowing the concrete service type.
type Reconnectable interface {
	// ToggleReconnect flips the runtime auto-reconnect setting and returns the
	// new value.
	ToggleReconnect() bool
	// ReconnectEnabled reports the current setting, so the TUI can render it
	// live instead of from a cached status snapshot.
	ReconnectEnabled() bool
}

// Monitorable is implemented by services that re-run their connection checks
// while connected. The TUI type-asserts to this to wire the monitor toggle.
type Monitorable interface {
	// ToggleMonitor flips the runtime monitor setting and returns the new value.
	ToggleMonitor() bool
	// Monitoring reports the current setting.
	Monitoring() bool
}

// HealthCheckable is implemented by services with a health-check target that can
// be switched on and off at runtime. The target itself stays configuration: the
// toggle only decides whether it is probed.
type HealthCheckable interface {
	// ToggleHealthCheck flips the runtime health-check setting and returns the
	// new value. It is a no-op when no target is configured.
	ToggleHealthCheck() bool
	// HealthCheckEnabled reports the current setting.
	HealthCheckEnabled() bool
	// HealthCheckTarget returns the configured host:port, or "" when none is
	// configured. It reports the target even while the check is switched off.
	HealthCheckTarget() string
}

// VPNBound is implemented by services that run over a VPN. While AutoConnect
// is on, the TUI connects them when that VPN comes up and stops them when it
// goes down; either way it warns about a manual connect without the VPN.
type VPNBound interface {
	// BoundVPN is the name of the VPN, as in den.yaml's vpn: section.
	BoundVPN() string
	// AutoConnect reports the current setting.
	AutoConnect() bool
	// ToggleAutoConnect flips the runtime setting and returns the new value.
	ToggleAutoConnect() bool
}

// Leftover is one process of Leftovers.
type Leftover struct {
	Name string
	PID  string
	Age  time.Duration // how long it has run; zero when ps did not say
}

// Leftovers are processes from an earlier session that keep a service from
// connecting, and the commands that end them by hand.
type Leftovers struct {
	Processes   []Leftover
	KillCommand string // ends them by pid: sudo kill, or taskkill on Windows
	StopCommand string // den's own stop as a person runs it; "" without the VPN helper
	AdminPrompt bool   // den's stop raises the OS password dialog
}

// LeftoverAware is implemented by services that cannot connect while such
// processes run (a VPN: a hung openfortivpn, or the pppd it leaves behind).
type LeftoverAware interface {
	// Leftovers reports what runs now, nil when nothing does. It runs ps and
	// sudo -n, so callers keep it off the UI goroutine.
	Leftovers(ctx context.Context) *Leftovers
}

// TokenHolder is implemented by services that hold an expiring credential the
// user can refresh (r) and copy (p).
type TokenHolder interface {
	HoldsToken() bool
}

// SecretCommander is implemented by services whose connect command can carry
// the current credential inline (Y in the detail view).
type SecretCommander interface {
	HasSecretCommand() bool
	// ConnectCommandWithSecret returns the connect command with the current
	// credential embedded, or the plain command while there is none.
	ConnectCommandWithSecret() string
}

// ShellTool is the Tool of a ClientCommand that runs the user's own shell
// rather than a client from the prereq catalog: what a terminal tab is for a
// store that is used with curl and has no client to log in.
const ShellTool = "shell"

// ClientCommand is a client invocation den runs itself instead of printing it
// for the user to paste.
type ClientCommand struct {
	// Tool is the client's id in the prereq catalog, e.g. "psql", or
	// ShellTool; whoever runs the command finds the binary through it.
	Tool string
	Args []string // without the program name
	// Env is added to the client's environment. The credential travels here
	// when the client can read it from there: arguments are visible in the
	// process list, the environment is not.
	Env []string
}

// TerminalOpener is implemented by services whose client den can run in a
// terminal tab, already logged in (t in the detail view).
type TerminalOpener interface {
	HasTerminal() bool
	// TerminalCommand returns the client invocation carrying the current
	// credential. It fails while the service is not connected: there is
	// neither a port to reach nor a credential to send.
	TerminalCommand() (ClientCommand, error)
}

// HoldsToken reports whether svc holds a refreshable credential.
func HoldsToken(svc Service) bool {
	h, ok := svc.(TokenHolder)
	return ok && h.HoldsToken()
}

// SecretCommand returns svc as a SecretCommander when it can embed its credential.
func SecretCommand(svc Service) (SecretCommander, bool) {
	sc, ok := svc.(SecretCommander)
	if !ok || !sc.HasSecretCommand() {
		return nil, false
	}
	return sc, true
}

// Terminal returns svc as a TerminalOpener when den can run its client.
func Terminal(svc Service) (TerminalOpener, bool) {
	to, ok := svc.(TerminalOpener)
	if !ok || !to.HasTerminal() {
		return nil, false
	}
	return to, true
}
