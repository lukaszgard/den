// Package neptune reaches Amazon Neptune clusters (IAM database auth) through
// an SSM tunnel fronted by a local SigV4 signing proxy, so curl, the Gremlin
// console or any HTTP/WebSocket client can query them without signing requests.
package neptune

import (
	"fmt"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/sigv4proxy"
	"github.com/lukaszgard/den/internal/service/ssmfwd"
)

const (
	neptuneAWSPort = 8182
)

// New creates a Neptune service from its spec.
func New(name string, spec *config.NeptuneSpec) (*ssmfwd.Tunnel, error) {
	env, err := spec.ResolveEnv()
	if err != nil {
		return nil, err
	}
	if spec.LocalPort == 0 {
		spec.LocalPort = config.DefaultLocalPort(config.ServiceTypeNeptune)
	}
	remotePort := spec.Port
	if remotePort == 0 {
		remotePort = neptuneAWSPort
	}

	e := sigv4proxy.NewEngine(sigv4proxy.EngineOptions{
		Type:       service.TypeNeptune,
		Host:       spec.NeptuneHost,
		RemotePort: remotePort,
		LocalPort:  spec.LocalPort,
		Service:    "neptune-db",
		Env:        env,
		Command:    fmt.Sprintf("curl -s http://localhost:%d/status", spec.LocalPort),
	})
	return ssmfwd.New(ssmfwd.Options{
		Name:        name,
		Environment: spec.Environment,
		Env:         env,
		Reconnect:   spec.Reconnect,
	}, e), nil
}
