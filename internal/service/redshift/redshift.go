// Package redshift opens SSM tunnels to Amazon Redshift (provisioned clusters
// and Serverless workgroups) and logs in with temporary database credentials
// minted from the caller's IAM identity, kept in ~/.pgpass for psql.
package redshift

import (
	"context"
	"fmt"
	"sync"
	"time"

	rs "github.com/aws/aws-sdk-go-v2/service/redshift"
	rss "github.com/aws/aws-sdk-go-v2/service/redshiftserverless"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/awsauth"
	"github.com/lukaszgard/den/internal/service/pgpass"
	"github.com/lukaszgard/den/internal/service/ssmfwd"
)

const (
	redshiftAWSPort = 5439
	defaultTTL      = 15 * time.Minute
)

// New creates a Redshift service from its spec, resolving the built-in env map
// and applying any overrides the spec sets.
func New(name string, spec *config.RedshiftSpec) (*ssmfwd.Tunnel, error) {
	env, err := spec.ResolveEnv()
	if err != nil {
		return nil, err
	}
	if spec.LocalPort == 0 {
		spec.LocalPort = config.DefaultLocalPort(config.ServiceTypeRedshift)
	}
	ttl := defaultTTL
	if spec.CredentialTTL != "" {
		if ttl, err = time.ParseDuration(spec.CredentialTTL); err != nil {
			return nil, fmt.Errorf("redshift.credential_ttl: %w", err)
		}
	}
	e := &engine{spec: spec, env: env, ttl: ttl}
	return ssmfwd.New(ssmfwd.Options{
		Name:        name,
		Environment: spec.Environment,
		Env:         env,
		Reconnect:   spec.Reconnect,
	}, e), nil
}

// engine is the Redshift-specific half of the tunnel.
type engine struct {
	spec *config.RedshiftSpec
	env  config.EnvMapping
	ttl  time.Duration

	mu   sync.RWMutex
	user string // database user Redshift returned with the last credentials
}

func (e *engine) Type() string  { return service.TypeRedshift }
func (e *engine) UserPort() int { return e.spec.LocalPort }

func (e *engine) Forwards() ([]ssmfwd.Forward, error) {
	if e.spec.RedshiftHost == "" {
		return nil, fmt.Errorf("redshift_host is required")
	}
	return []ssmfwd.Forward{{RemoteHost: e.spec.RedshiftHost, RemotePort: redshiftAWSPort, LocalPort: e.spec.LocalPort}}, nil
}

func (e *engine) ConnectCommand() string {
	return `psql "` + e.conninfo() + `"`
}

// ConnectCommandWithSecret puts the password into the conninfo string, for
// machines where ~/.pgpass is not in play.
func (e *engine) ConnectCommandWithSecret(password string) string {
	return fmt.Sprintf(`psql "host=localhost port=%d dbname=%s user=%s password=%s sslmode=require"`,
		e.spec.LocalPort, e.spec.DBName, e.dbUser(), password)
}

// TerminalCommand is the client den runs in a terminal tab. It is built from
// the same parts as ConnectCommand, so what den shows and what it runs cannot
// drift apart. psql takes the password from PGPASSWORD, which also works with
// update_pgpass off.
func (e *engine) TerminalCommand(password string) service.ClientCommand {
	return service.ClientCommand{
		Tool: "psql",
		Args: []string{e.conninfo()},
		Env:  []string{"PGPASSWORD=" + password},
	}
}

// conninfo is the libpq connection string for the local end of the tunnel.
func (e *engine) conninfo() string {
	return fmt.Sprintf("host=localhost port=%d dbname=%s user=%s sslmode=require",
		e.spec.LocalPort, e.spec.DBName, e.dbUser())
}

// dbUser is the user to log in as. Until credentials have been minted the real
// name is unknown — Redshift decides it — so the command shows what will be
// asked for.
func (e *engine) dbUser() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.user != "" {
		return e.user
	}
	if e.spec.DBUser != "" {
		return "IAM:" + e.spec.DBUser
	}
	return "<iam-user>"
}

// Credential mints temporary credentials and, when enabled, writes them to ~/.pgpass.
func (e *engine) Credential(ctx context.Context, log func(string)) (ssmfwd.Credential, error) {
	cfg, err := awsauth.LoadConfig(ctx, e.env.CredentialProfile, e.env.AWSRegionCode)
	if err != nil {
		return ssmfwd.Credential{}, err
	}
	creds, err := fetchCredentials(ctx, e.spec, e.ttl, rs.NewFromConfig(cfg), rss.NewFromConfig(cfg))
	if err != nil {
		return ssmfwd.Credential{}, err
	}
	e.mu.Lock()
	e.user = creds.User
	e.mu.Unlock()
	log(fmt.Sprintf("temporary credentials generated for %s (%s)", creds.User, ssmfwd.RenewalNote(creds.Expires)))

	if e.spec.UpdatePgpass {
		if err := pgpass.Update(e.spec.LocalPort, e.spec.DBName, creds.User, creds.Password); err != nil {
			log(fmt.Sprintf("pgpass update failed: %v", err))
		} else {
			log("~/.pgpass updated")
		}
	}
	return ssmfwd.Credential{Secret: creds.Password, Expires: creds.Expires}, nil
}
