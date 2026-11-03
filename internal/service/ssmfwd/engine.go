// Package ssmfwd runs the lifecycle every SSM-tunnelled data service shares:
// open AWS-StartPortForwardingSessionToRemoteHost sessions through the
// environment's bastion, confirm the local ports really listen, mint a
// credential, keep it fresh, and reconnect with backoff when a session drops.
//
// What differs per service type — where the tunnel goes, which credential it
// needs, how the user connects — is supplied by an Engine, so adding a service
// type means writing an Engine rather than another copy of the lifecycle.
package ssmfwd

import (
	"context"
	"fmt"
	"time"

	"github.com/lukaszgard/den/internal/service"
)

// Forward is one SSM port-forward: RemoteHost:RemotePort as reachable from the
// bastion, exposed on localhost:LocalPort.
type Forward struct {
	RemoteHost string
	RemotePort int
	LocalPort  int
}

// parameters formats the forward for the AWS-StartPortForwardingSessionToRemoteHost document.
func (f Forward) parameters() string {
	return fmt.Sprintf("host=%s,portNumber=%d,localPortNumber=%d", f.RemoteHost, f.RemotePort, f.LocalPort)
}

// Credential is a short-lived secret minted once the tunnel is up. An empty
// Secret means the engine had nothing to mint; a zero Expires means the secret
// never needs refreshing.
type Credential struct {
	Secret  string
	Expires time.Time
}

// Engine is what a service type supplies to a Tunnel.
type Engine interface {
	// Type is the service type shown in the TUI, e.g. "rds".
	Type() string
	// Forwards lists the SSM forwards to open. It is called on every connection
	// attempt, so it may pick fresh local ports each time.
	Forwards() ([]Forward, error)
	// UserPort is the local port the user connects to.
	UserPort() int
	// ConnectCommand is the command the user runs against UserPort.
	ConnectCommand() string
}

// Credentialer is implemented by engines whose service needs a credential. The
// Tunnel calls it after the forwards are up, again before each expiry and on
// r, always over a connected tunnel; any side effects (rewriting ~/.pgpass)
// belong here so every refresh repeats them. The line logging the new
// credential ends with RenewalNote, so the log says when the next one comes.
type Credentialer interface {
	Credential(ctx context.Context, log func(string)) (Credential, error)
}

// Preflighter is implemented by engines that can tell, cheaply and before any
// forward opens, that connecting cannot work. An error fails the attempt with
// no session started, so the user does not wait for a bastion to learn that a
// login ran out.
type Preflighter interface {
	Preflight() error
}

// PasswordLoginer is implemented by engines that log in with a password read
// from a configured secret rather than a credential they mint.
type PasswordLoginer interface {
	PasswordLogin() service.PasswordLogin
}

// Preparer is implemented by engines that need work done between the tunnel
// coming up and the service being usable — fetching a CA bundle, starting an
// in-process proxy. An error fails the connection attempt; stop, when non-nil,
// runs once the connection ends.
type Preparer interface {
	Prepare(ctx context.Context, fwds []Forward, log func(string)) (stop func(), err error)
}

// SecretCommander is implemented by engines whose connect command can carry
// the credential inline, for users who would rather paste one command.
type SecretCommander interface {
	ConnectCommandWithSecret(secret string) string
}

// TerminalCommander is implemented by engines whose client den can run in a
// terminal tab. Unlike ConnectCommand this is never shown or copied, so it is
// an argument list rather than a shell line, and the secret goes wherever the
// client reads it without it appearing in the process list.
type TerminalCommander interface {
	TerminalCommand(secret string) service.ClientCommand
}

// LoginChecker is implemented by engines whose client can log in once and
// exit. The Tunnel runs the probe after each connect and each new credential:
// a tunnel that answers says nothing about whether the database takes the
// credential, and without the check a refused one stays hidden until the user
// opens a client.
type LoginChecker interface {
	LoginProbe(secret string) LoginProbe
}

// LoginProbe is a one-shot login. Build Command from the same parts as the
// engine's TerminalCommand, so the check logs in exactly as a tab would, with
// the secret in Env or a file, never in Args.
type LoginProbe struct {
	Command service.ClientCommand
	// Succeeded judges the client's output; nil means exit status 0 is
	// success. It is consulted only after exit status 0.
	Succeeded func(output string) bool
	// User is who the probe logs in as, for the log; "" leaves it out.
	User string
	// Hint is what to check when the database refuses, one line per cause.
	Hint string
}
