package rds

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/secrets"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/pgpass"
	"github.com/lukaszgard/den/internal/service/ssmfwd"
)

// passwordEngine is the half of the tunnel for a service that logs in with a
// username and password read from a configured secret. The password lives in
// the tunnel's state while connected and reaches no file: it stays valid after
// a leak, where an IAM token left behind is useless in 15 minutes. A client den
// runs reads it from its environment; one the user runs asks for it.
//
// It has no ConnectCommandWithSecret on purpose, which is what takes Y away:
// that command ends up in a shell's history.
type passwordEngine struct {
	base   engine
	secret secrets.Item

	// The user may come from the secret, so a read can change it; the TUI
	// renders the command from its own goroutine. Guarded by mu.
	mu     sync.RWMutex
	user   string
	readAt time.Time
}

func newPasswordEngine(spec *config.RDSSpec, secret secrets.Item) *passwordEngine {
	return &passwordEngine{base: engine{spec: spec}, secret: secret, user: spec.DBUser}
}

func (e *passwordEngine) Type() string                        { return e.base.Type() }
func (e *passwordEngine) UserPort() int                       { return e.base.UserPort() }
func (e *passwordEngine) Forwards() ([]ssmfwd.Forward, error) { return e.base.Forwards() }

func (e *passwordEngine) ConnectCommand() string {
	if e.base.mysql() {
		return "mysql " + strings.Join(mysqlPasswordArgs(e.base.spec.LocalPort, e.username(), e.base.dbName(), true), " ")
	}
	return `psql "` + conninfo(e.base.spec.LocalPort, e.base.dbName(), e.username()) + `"`
}

// TerminalCommand hands the password over in the environment: nothing has to
// be escaped however awkward it is, and it stays out of the process list.
func (e *passwordEngine) TerminalCommand(password string) service.ClientCommand {
	if e.base.mysql() {
		return service.ClientCommand{
			Tool: "mysql",
			Args: mysqlPasswordArgs(e.base.spec.LocalPort, e.username(), e.base.dbName(), false),
			Env:  []string{"MYSQL_PWD=" + password},
		}
	}
	return service.ClientCommand{
		Tool: "psql",
		Args: []string{conninfo(e.base.spec.LocalPort, e.base.dbName(), e.username())},
		Env:  []string{"PGPASSWORD=" + password},
	}
}

// Preflight checks the secret's status, which never decrypts anything, so an
// expired login shows before a session is opened. The status may name a key of
// the Secrets pane, so the error says where that is.
func (e *passwordEngine) Preflight() error {
	st := e.secret.Probe()
	if st.Ready {
		return nil
	}
	return fmt.Errorf("secret %q (Secrets pane): %s", e.secret.Name(), st.Reason)
}

// Credential reads the secret, on every connect, reconnect and r, so a rotated
// password arrives without a restart. No expiry: there is no TTL to show and
// nothing to renew by itself.
func (e *passwordEngine) Credential(ctx context.Context, log func(string)) (ssmfwd.Credential, error) {
	name, auth := e.secret.Name(), e.base.spec.Auth
	fields, err := e.secret.Read(ctx)
	if err != nil {
		return ssmfwd.Credential{}, fmt.Errorf("secret %q: %w", name, err)
	}
	username, password, err := secrets.Login(fields, auth.UsernameKey, auth.PasswordKey)
	if err != nil {
		return ssmfwd.Credential{}, fmt.Errorf("secret %q: %w", name, err)
	}
	user := cmp.Or(e.base.spec.DBUser, username)
	if user == "" {
		return ssmfwd.Credential{}, fmt.Errorf("secret %q: no key %q, and db_user is not set", name, cmp.Or(auth.UsernameKey, "username"))
	}
	e.mu.Lock()
	e.user, e.readAt = user, time.Now()
	e.mu.Unlock()
	log(fmt.Sprintf("password read from secret %q", name))

	if !e.base.mysql() {
		e.dropOldToken(log)
	}
	return ssmfwd.Credential{Secret: password}, nil
}

// dropOldToken removes what an IAM login on this port may have left in pgpass:
// psql would send that dead token instead of asking for the password. A failure
// is worth a line and no more, since a tab does not read the file anyway.
func (e *passwordEngine) dropOldToken(log func(string)) {
	removed, err := pgpass.Remove(e.base.spec.LocalPort)
	switch {
	case err != nil:
		log(fmt.Sprintf("could not remove the old token from pgpass: %v", err))
	case removed:
		log(fmt.Sprintf("removed the old token for port %d from %s", e.base.spec.LocalPort, pgpass.Path()))
	}
}

// PasswordLogin says where the password comes from, for the views.
func (e *passwordEngine) PasswordLogin() service.PasswordLogin {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return service.PasswordLogin{Secret: e.secret.Name(), Provider: e.secret.Provider(), ReadAt: e.readAt}
}

func (e *passwordEngine) username() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.user
}
