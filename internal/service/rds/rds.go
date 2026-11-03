// Package rds opens SSM tunnels to RDS/Aurora and authenticates over them with
// IAM database auth tokens. For PostgreSQL the token is kept in the pgpass file
// (~/.pgpass, %APPDATA%\postgresql\pgpass.conf on Windows) so psql picks it
// up; for MySQL/MariaDB it goes into a per-service option file.
//
// A service with auth: logs in with a username and password read from a
// configured secret instead (password.go).
package rds

import (
	"context"
	"fmt"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/secrets"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/pgpass"
	"github.com/lukaszgard/den/internal/service/ssmfwd"
)

// New creates an RDS service from its spec. loginSecret is the entry spec.Auth
// names, nil for a service that logs in with an IAM token.
func New(name string, spec *config.RDSSpec, loginSecret secrets.Item) (*ssmfwd.Tunnel, error) {
	env, err := spec.ResolveEnv()
	if err != nil {
		return nil, err
	}
	var e ssmfwd.Engine = &engine{spec: spec, env: env}
	if spec.Auth != nil {
		if loginSecret == nil {
			return nil, fmt.Errorf("rds.auth.secret %q: no such entry in secrets", spec.Auth.Secret)
		}
		e = newPasswordEngine(spec, loginSecret)
	}
	return ssmfwd.New(ssmfwd.Options{
		Name:        name,
		Environment: spec.Environment,
		Env:         env,
		Reconnect:   spec.Reconnect,
	}, e), nil
}

// engine is the RDS-specific half of the tunnel: where it goes, how the IAM
// token is minted, and the client command that uses it.
type engine struct {
	spec *config.RDSSpec
	env  config.EnvMapping
}

func (e *engine) Type() string  { return service.TypeRDS }
func (e *engine) UserPort() int { return e.spec.LocalPort }

func (e *engine) Forwards() ([]ssmfwd.Forward, error) {
	if e.spec.RDSHost == "" {
		return nil, fmt.Errorf("rds_host is required")
	}
	return []ssmfwd.Forward{{RemoteHost: e.spec.RDSHost, RemotePort: e.remotePort(), LocalPort: e.spec.LocalPort}}, nil
}

func (e *engine) ConnectCommand() string {
	if e.mysql() {
		return mysqlCommand(mysqlOptionFile(e.spec.LocalPort), e.spec.LocalPort, e.dbUser(), e.dbName(), "")
	}
	return `psql "` + e.conninfo() + `"`
}

// ConnectCommandWithSecret puts the token into the command itself, for
// machines where ~/.pgpass or the option file is not in play.
func (e *engine) ConnectCommandWithSecret(token string) string {
	if e.mysql() {
		return mysqlCommand("", e.spec.LocalPort, e.dbUser(), e.dbName(), token)
	}
	return `psql "` + e.conninfo() + " password=" + token + `"`
}

// TerminalCommand is the client den runs in a terminal tab. It is built from
// the same parts as ConnectCommand, so what den shows and what it runs cannot
// drift apart. psql takes the token from PGPASSWORD, which also works with
// update_pgpass off; mysql reads it from the option file Credential keeps
// current.
func (e *engine) TerminalCommand(token string) service.ClientCommand {
	if e.mysql() {
		return service.ClientCommand{
			Tool: "mysql",
			Args: mysqlArgs(mysqlOptionFile(e.spec.LocalPort), e.spec.LocalPort, e.dbUser(), e.dbName(), ""),
		}
	}
	return service.ClientCommand{
		Tool: "psql",
		Args: []string{e.conninfo()},
		Env:  []string{"PGPASSWORD=" + token},
	}
}

func (e *engine) conninfo() string {
	return conninfo(e.spec.LocalPort, e.dbName(), e.dbUser())
}

// conninfo is the libpq connection string for the local end of the tunnel. An
// empty user is left out: a password login does not know it until the secret
// has been read.
//
// hostaddr=127.0.0.1 because the forward listens on IPv4 only: with localhost
// alone psql tries ::1 first, and every failed login then also reports
// "Connection refused", which reads as a dead tunnel. host=localhost stays, as
// the name pgpass entries are matched on.
func conninfo(localPort int, dbName, user string) string {
	info := fmt.Sprintf("host=localhost hostaddr=127.0.0.1 port=%d", localPort)
	if dbName != "" {
		info += " dbname=" + dbName
	}
	if user != "" {
		info += " user=" + user
	}
	return info + " sslmode=require"
}

// Credential generates an IAM token and stores it where the client looks:
// the MySQL option file always (the connect command depends on it), the pgpass
// file when update_pgpass is enabled.
func (e *engine) Credential(ctx context.Context, log func(string)) (ssmfwd.Credential, error) {
	// The token signs host and port, so it must name the port RDS listens on,
	// not the local end of the tunnel.
	token, err := generateToken(ctx, e.spec.RDSHost, e.remotePort(), e.env.AWSRegionCode, e.env.CredentialProfile, e.dbUser())
	if err != nil {
		return ssmfwd.Credential{}, err
	}
	expires := tokenExpiresAt()
	log(fmt.Sprintf("IAM token generated (%s)", ssmfwd.RenewalNote(expires)))

	if e.mysql() {
		path := mysqlOptionFile(e.spec.LocalPort)
		if err := writeMySQLOptionFile(path, token); err != nil {
			return ssmfwd.Credential{}, err
		}
		log(path + " updated")
	} else if e.spec.UpdatePgpass {
		if err := pgpass.Update(e.spec.LocalPort, e.dbName(), e.dbUser(), token); err != nil {
			log(fmt.Sprintf("pgpass update failed: %v", err))
		} else {
			log(pgpass.Path() + " updated")
		}
	}
	return ssmfwd.Credential{Secret: token, Expires: expires}, nil
}

func (e *engine) mysql() bool { return e.spec.Engine == config.RDSEngineMySQL }

func (e *engine) remotePort() int {
	if e.mysql() {
		return mysqlAWSPort
	}
	return postgresAWSPort
}

// dbName is empty unless db_name is set: the client then uses its own default
// and the user picks the database.
func (e *engine) dbName() string { return e.spec.DBName }

func (e *engine) dbUser() string { return e.spec.DBUser }
