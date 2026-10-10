package opensearch

import (
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
)

// Serverless collections have no _cluster API, so the suggested command must
// differ from a managed domain's.
func TestConnectCommandPerFlavour(t *testing.T) {
	for _, tc := range []struct {
		serverless bool
		want       string
	}{
		{false, "curl -s 'http://localhost:59200/_cluster/health?pretty'"},
		{true, "curl -s 'http://localhost:59200/_cat/indices?v'"},
	} {
		svc, err := New("search", &config.OpenSearchSpec{
			EC2InstanceID: "i-0abc", AWSRegionCode: "eu-central-1",
			OpenSearchHost: "vpc-x.es.amazonaws.com",
			Serverless:     tc.serverless,
		})
		if err != nil {
			t.Fatal(err)
		}
		if svc.Info().Type != service.TypeOpenSearch || svc.ConnectCommand() != tc.want {
			t.Errorf("serverless=%v: type %q, command %q, want %q", tc.serverless, svc.Info().Type, svc.ConnectCommand(), tc.want)
		}
		if _, ok := service.Terminal(svc); !ok {
			t.Errorf("serverless=%v: no terminal tabs", tc.serverless)
		}
	}
}
