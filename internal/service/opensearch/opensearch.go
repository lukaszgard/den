// Package opensearch reaches Amazon OpenSearch Service VPC domains and
// OpenSearch Serverless collections through an SSM tunnel fronted by a local
// SigV4 signing proxy, so curl, a browser (Dashboards) or any HTTP client can
// use them without signing requests itself.
package opensearch

import (
	"fmt"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/sigv4proxy"
	"github.com/lukaszgard/den/internal/service/ssmfwd"
)

const (
	openSearchAWSPort = 443
)

// New creates an OpenSearch service from its spec.
func New(name string, spec *config.OpenSearchSpec) (*ssmfwd.Tunnel, error) {
	env, err := spec.ResolveEnv()
	if err != nil {
		return nil, err
	}
	if spec.LocalPort == 0 {
		spec.LocalPort = config.DefaultLocalPort(config.ServiceTypeOpenSearch)
	}

	// Managed domains sign for "es"; Serverless collections for "aoss", which
	// also has no _cluster API.
	signing := "es"
	command := fmt.Sprintf("curl -s 'http://localhost:%d/_cluster/health?pretty'", spec.LocalPort)
	if spec.Serverless {
		signing = "aoss"
		command = fmt.Sprintf("curl -s 'http://localhost:%d/_cat/indices?v'", spec.LocalPort)
	}

	e := sigv4proxy.NewEngine(sigv4proxy.EngineOptions{
		Type:       service.TypeOpenSearch,
		Host:       spec.OpenSearchHost,
		RemotePort: openSearchAWSPort,
		LocalPort:  spec.LocalPort,
		Service:    signing,
		Env:        env,
		Command:    command,
	})
	return ssmfwd.New(ssmfwd.Options{
		Name:        name,
		Environment: spec.Environment,
		Env:         env,
		Reconnect:   spec.Reconnect,
	}, e), nil
}
