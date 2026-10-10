// Package redis opens SSM tunnels to ElastiCache or MemoryDB (Redis OSS/Valkey)
// and, when a cache user is configured, authenticates with IAM tokens.
//
// MemoryDB always runs in cluster mode, so through a single tunnel only
// single-shard clusters work: on more shards, MOVED redirects point at node
// addresses that only resolve inside the VPC.
package redis

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/ssmfwd"
)

// New creates a Redis service from its spec, resolving the built-in env map and
// applying any overrides the spec sets.
func New(name string, spec *config.RedisSpec) (*ssmfwd.Tunnel, error) {
	env, err := spec.ResolveEnv()
	if err != nil {
		return nil, err
	}
	if spec.LocalPort == 0 {
		spec.LocalPort = config.DefaultLocalPort(config.ServiceTypeRedis)
	}
	e := &engine{spec: spec, env: env}
	return ssmfwd.New(ssmfwd.Options{
		Name:        name,
		Environment: spec.Environment,
		Env:         env,
		Reconnect:   spec.Reconnect,
	}, e), nil
}

// engine is the ElastiCache/MemoryDB-specific half of the tunnel.
type engine struct {
	spec *config.RedisSpec
	env  config.EnvMapping
}

func (e *engine) Type() string  { return service.TypeRedis }
func (e *engine) UserPort() int { return e.spec.LocalPort }

func (e *engine) Forwards() ([]ssmfwd.Forward, error) {
	if e.spec.RedisHost == "" {
		return nil, fmt.Errorf("redis_host is required")
	}
	return []ssmfwd.Forward{{RemoteHost: e.spec.RedisHost, RemotePort: redisAWSPort, LocalPort: e.spec.LocalPort}}, nil
}

func (e *engine) ConnectCommand() string {
	return "redis-cli " + strings.Join(e.args(), " ")
}

// args are redis-cli's arguments for the local end of the tunnel.
func (e *engine) args() []string {
	args := []string{"-h", "localhost", "-p", strconv.Itoa(e.spec.LocalPort), "--tls"}
	if e.spec.UserID != "" {
		args = append(args, "--user", e.spec.UserID)
	}
	return args
}

// ConnectCommandWithSecret passes the IAM token with -a, for pasting a single
// command. The plain connect command (y) never carries it.
func (e *engine) ConnectCommandWithSecret(token string) string {
	return fmt.Sprintf(`%s -a "%s"`, e.ConnectCommand(), token)
}

// TerminalCommand is the client den runs in a terminal tab, built from the
// same arguments as ConnectCommand so what den shows and what it runs cannot
// drift apart. The token goes into REDISCLI_AUTH rather than -a, which would
// put it into the process list. A cache reached without IAM has no token, and
// an empty REDISCLI_AUTH would be sent as an empty password.
func (e *engine) TerminalCommand(token string) service.ClientCommand {
	cmd := service.ClientCommand{Tool: "redis-cli", Args: e.args()}
	if token != "" {
		cmd.Env = []string{"REDISCLI_AUTH=" + token}
	}
	return cmd
}

// Credential generates an IAM token when cache_name and user_id are configured.
// Without them the cache is reached without IAM auth, so there is nothing to
// mint and the empty credential tells the tunnel not to schedule refreshes.
func (e *engine) Credential(ctx context.Context, log func(string)) (ssmfwd.Credential, error) {
	if e.spec.CacheName == "" || e.spec.UserID == "" {
		return ssmfwd.Credential{}, nil
	}
	token, err := generateToken(ctx, e.tokenTarget(), e.env.CredentialProfile)
	if err != nil {
		return ssmfwd.Credential{}, err
	}
	expires := tokenExpiresAt()
	log(fmt.Sprintf("IAM token generated (%s)", ssmfwd.RenewalNote(expires)))
	return ssmfwd.Credential{Secret: token, Expires: expires}, nil
}

func (e *engine) tokenTarget() tokenTarget {
	target := tokenTarget{
		name:       e.spec.CacheName,
		userID:     e.spec.UserID,
		region:     e.env.AWSRegionCode,
		service:    signingElastiCache,
		serverless: e.spec.Serverless,
	}
	if e.spec.MemoryDB {
		target.service = signingMemoryDB
	}
	return target
}
