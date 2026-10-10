package neptune

import (
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
)

func TestNewDefaults(t *testing.T) {
	svc, err := New("graph", &config.NeptuneSpec{
		EC2InstanceID: "i-0abc", AWSRegionCode: "eu-central-1",
		NeptuneHost: "db.cluster.neptune.amazonaws.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Info().Type != service.TypeNeptune || svc.State().LocalPort != config.DefaultLocalPort(config.ServiceTypeNeptune) {
		t.Fatalf("info = %+v, port = %d", svc.Info(), svc.State().LocalPort)
	}
	if got, want := svc.ConnectCommand(), "curl -s http://localhost:58182/status"; got != want {
		t.Fatalf("ConnectCommand = %q, want %q", got, want)
	}
	if _, ok := service.Terminal(svc); !ok {
		t.Fatal("no terminal tabs")
	}
}
