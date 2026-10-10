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
