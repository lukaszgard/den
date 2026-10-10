// Package documentdb opens SSM tunnels to Amazon DocumentDB clusters and builds
// the mongosh command that authenticates over them with IAM (MONGODB-AWS).
//
// Unlike rds and redis, there is no token to generate. DocumentDB IAM auth has
// no token step at all: the driver signs the auth handshake with whatever the
// AWS credential provider chain resolves, and the cluster calls
// sts:GetCallerIdentity to resolve the caller's ARN and match it against its
// $external user list. So this service never sets TokenExpires and has no
// refresh loop — RefreshToken is a no-op.
package documentdb

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/ssmfwd"
)

const (
	documentDBAWSPort = 27017
)

// New creates a DocumentDB service from its spec, resolving the built-in env
// map and applying any overrides the spec sets.
func New(name string, spec *config.DocumentDBSpec) (*ssmfwd.Tunnel, error) {
	env, err := spec.ResolveEnv()
	if err != nil {
		return nil, err
	}
	if spec.LocalPort == 0 {
		spec.LocalPort = config.DefaultLocalPort(config.ServiceTypeDocumentDB)
	}
	e := &engine{spec: spec, env: env}
	return ssmfwd.New(ssmfwd.Options{
		Name:        name,
		Environment: spec.Environment,
		Env:         env,
		Reconnect:   spec.Reconnect,
	}, e), nil
}

// engine is the DocumentDB-specific half of the tunnel. It has no Credential
// method on purpose — see the package comment.
type engine struct {
	spec *config.DocumentDBSpec
	env  config.EnvMapping

	mu     sync.RWMutex
	caFile string // resolved at connect time; empty until then
}

func (e *engine) Type() string  { return service.TypeDocumentDB }
func (e *engine) UserPort() int { return e.spec.LocalPort }

func (e *engine) Forwards() ([]ssmfwd.Forward, error) {
	if e.spec.DocumentDBHost == "" {
		return nil, fmt.Errorf("documentdb_host is required")
	}
	return []ssmfwd.Forward{{RemoteHost: e.spec.DocumentDBHost, RemotePort: documentDBAWSPort, LocalPort: e.spec.LocalPort}}, nil
}

// ConnectCommand builds the mongosh invocation for the local tunnel.
//
// Two parameters are load-bearing and must not be "tidied":
//   - directConnection=true (and NO replicaSet=rs0): over a tunnel, replica-set
//     discovery makes the driver discard localhost in favour of the in-VPC
//     hostnames the cluster advertises, which fails as a server-selection
//     timeout that looks nothing like the tunnel problem it is.
//   - retryWrites=false: DocumentDB does not support retryable writes.
//
// tlsAllowInvalidHostnames is likewise tunnel-specific — the server certificate
// is issued for the cluster endpoint, not localhost.
func (e *engine) ConnectCommand() string {
	cmd := "mongosh"
	if profile := e.env.CredentialProfile; profile != "" {
		cmd = "AWS_PROFILE=" + profile + " mongosh"
	}
	return fmt.Sprintf(
		`%s "%s" --authenticationMechanism MONGODB-AWS --authenticationDatabase '$external' --tlsCAFile %s --tlsAllowInvalidHostnames`,
		cmd, e.uri(), e.caBundle())
}

// TerminalCommand is the client den runs in a terminal tab: ConnectCommand
// without its shell quoting, built from the same parts so what den shows and
// what it runs cannot drift apart. There is no credential to hand over — the
// driver signs with what the profile resolves — so the secret is unused.
func (e *engine) TerminalCommand(string) service.ClientCommand {
	cmd := service.ClientCommand{
		Tool: "mongosh",
		Args: []string{
			e.uri(),
			"--authenticationMechanism", "MONGODB-AWS",
			"--authenticationDatabase", "$external",
			"--tlsCAFile", e.caBundle(),
			"--tlsAllowInvalidHostnames",
		},
	}
	if profile := e.env.CredentialProfile; profile != "" {
		cmd.Env = []string{"AWS_PROFILE=" + profile}
	}
	return cmd
}

// uri is the connection string for the local end of the tunnel.
func (e *engine) uri() string {
	return fmt.Sprintf("mongodb://localhost:%d/%s?tls=true&directConnection=true&retryWrites=false",
		e.spec.LocalPort, e.spec.DBName)
}

// caBundle is the CA file mongosh is pointed at: the one Prepare resolved,
// before that the configured one or where the bundle will be downloaded to.
func (e *engine) caBundle() string {
	// The TUI renders the command from its own goroutine while Prepare may be
	// resolving the bundle, so the resolved path is read under the lock.
	e.mu.RLock()
	caFile := e.caFile
	e.mu.RUnlock()
	if caFile == "" {
		caFile = e.spec.CAFile
	}
	if caFile == "" {
		caFile = defaultCABundlePath()
	}
	return caFile
}

// Prepare resolves the CA bundle once the tunnel is up. mongosh cannot connect
// without it, so a failure here is fatal — handing over a command that is
// guaranteed to fail helps nobody.
func (e *engine) Prepare(ctx context.Context, _ []ssmfwd.Forward, log func(string)) (func(), error) {
	caFile, err := resolveCABundle(ctx, e.env.AWSRegionCode, e.spec.CAFile, log)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	e.caFile = caFile
	e.mu.Unlock()

	warnOnClientProblems(log)
	return nil, nil
}

// warnOnClientProblems logs the two conditions that make an otherwise-correct
// tunnel fail at the mongosh step. Neither is fatal — the tunnel is up and the
// user may be pasting the command into a different shell than den runs in.
func warnOnClientProblems(log func(string)) {
	if _, err := exec.LookPath("mongosh"); err != nil {
		log("warning: mongosh not found on PATH — DocumentDB IAM auth needs mongosh 2.5+ " +
			"(older versions bundle a Node driver that cannot do MONGODB-AWS at all)")
	}

	// Exported credentials outrank AWS_PROFILE in the provider chain, so a stale
	// session token silently wins over a freshly-logged-in SSO profile. This is
	// the most common cause of auth that "works sometimes".
	var stale []string
	for _, v := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"} {
		if os.Getenv(v) != "" {
			stale = append(stale, v)
		}
	}
	if len(stale) > 0 {
		log(fmt.Sprintf("warning: %s set in the environment — these outrank AWS_PROFILE "+
			"in the credential chain; run `unset %s` if auth fails",
			strings.Join(stale, ", "), strings.Join(stale, " ")))
	}
}
