package sigv4proxy

import (
	"context"
	"fmt"
	"net"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/awsauth"
	"github.com/lukaszgard/den/internal/service/ssmfwd"
)

// EngineOptions configures a service reached through the signing proxy: the
// SSM forward lands on a private port, and the proxy serves the user on LocalPort.
type EngineOptions struct {
	Type       string            // service type shown in the TUI
	Host       string            // real endpoint hostname
	RemotePort int               // real endpoint port
	LocalPort  int               // where the proxy listens
	Service    string            // SigV4 signing name
	Env        config.EnvMapping // CredentialProfile signs, AWSRegionCode scopes the signature
	Command    string            // connect command shown and copied
}

// NewEngine returns an ssmfwd.Engine that fronts the tunnel with a signing proxy.
func NewEngine(o EngineOptions) ssmfwd.Engine {
	return &engine{opts: o}
}

type engine struct {
	opts EngineOptions
}

func (e *engine) Type() string           { return e.opts.Type }
func (e *engine) UserPort() int          { return e.opts.LocalPort }
func (e *engine) ConnectCommand() string { return e.opts.Command }

// TerminalCommand is what den runs in a terminal tab. Nothing logs in to an
// HTTP endpoint, so the tab is the user's shell with DEN_URL naming the proxy:
// curl $DEN_URL/... is signed like the shown command. The shell is handed no
// credential — the proxy holds them — so the secret is unused.
func (e *engine) TerminalCommand(string) service.ClientCommand {
	return service.ClientCommand{
		Tool: service.ShellTool,
		Env:  []string{fmt.Sprintf("DEN_URL=http://localhost:%d", e.opts.LocalPort)},
	}
}

// Forwards sends the SSM session to a fresh private port each attempt; only
// the proxy talks to it, and the user never needs to know it.
func (e *engine) Forwards() ([]ssmfwd.Forward, error) {
	if e.opts.Host == "" {
		return nil, fmt.Errorf("%s host is required", e.opts.Type)
	}
	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("pick tunnel port: %w", err)
	}
	return []ssmfwd.Forward{{RemoteHost: e.opts.Host, RemotePort: e.opts.RemotePort, LocalPort: port}}, nil
}

// Prepare checks the signing identity has live credentials — so an expired SSO
// session fails the connect instead of every request — then starts the proxy.
func (e *engine) Prepare(ctx context.Context, fwds []ssmfwd.Forward, log func(string)) (func(), error) {
	cfg, err := awsauth.LoadConfig(ctx, e.opts.Env.CredentialProfile, e.opts.Env.AWSRegionCode)
	if err != nil {
		return nil, err
	}
	if _, err := cfg.Credentials.Retrieve(ctx); err != nil {
		return nil, fmt.Errorf("retrieve credentials for %s: %w", e.opts.Env.CredentialProfile, err)
	}

	p, err := Start(Config{
		ListenPort:   e.opts.LocalPort,
		UpstreamHost: e.opts.Host,
		UpstreamPort: e.opts.RemotePort,
		DialAddr:     fmt.Sprintf("127.0.0.1:%d", fwds[0].LocalPort),
		Service:      e.opts.Service,
		Region:       e.opts.Env.AWSRegionCode,
		Credentials:  cfg.Credentials,
		Log:          log,
	})
	if err != nil {
		return nil, err
	}
	log(fmt.Sprintf("signing proxy on http://localhost:%d (signs %s requests as %s)",
		e.opts.LocalPort, e.opts.Service, e.opts.Env.CredentialProfile))
	return func() { p.Close() }, nil //nolint:errcheck
}

// freePort asks the OS for an unused loopback port. It is released before the
// SSM plugin binds it, so a race is possible; the tunnel's port check catches it.
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}
