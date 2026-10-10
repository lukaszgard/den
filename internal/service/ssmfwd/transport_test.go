package ssmfwd

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/lukaszgard/den/internal/config"
)

var fwd = Forward{RemoteHost: "db.cluster.rds.amazonaws.com", RemotePort: 5432, LocalPort: 50111}

func TestTransportCommands(t *testing.T) {
	env := config.EnvMapping{EC2InstanceID: "i-0abc", AWSProfile: "dev", AWSRegionCode: "eu-central-1"}
	resolve := func(_ context.Context, host string) ([]string, error) {
		if host != fwd.RemoteHost {
			return nil, errors.New("unexpected host " + host)
		}
		return []string{"fd00::1", "10.0.3.7"}, nil
	}
	cases := []struct {
		name     string
		tr       Transport
		wantName string
		wantArgs []string
	}{
		{"ssm", ssmTransport{env: env}, "aws", []string{
			"ssm", "start-session", "--region", "eu-central-1", "--target", "i-0abc",
			"--document-name", "AWS-StartPortForwardingSessionToRemoteHost",
			"--parameters", "host=db.cluster.rds.amazonaws.com,portNumber=5432,localPortNumber=50111", "--profile", "dev",
		}},
		{"ssh", sshTransport{host: "ops@bastion.example.com", args: []string{"-i", "/k", "-J", "jump"}}, "ssh", []string{
			"-N", "-o", "ExitOnForwardFailure=yes", "-o", "BatchMode=yes", "-o", "ServerAliveInterval=30",
			"-L", "127.0.0.1:50111:db.cluster.rds.amazonaws.com:5432", "-i", "/k", "-J", "jump", "ops@bastion.example.com",
		}},
		{"eice prefers IPv4", eiceTransport{env: env, endpointID: "eice-123", resolve: resolve}, "aws", []string{
			"ec2-instance-connect", "open-tunnel", "--instance-connect-endpoint-id", "eice-123",
			"--private-ip-address", "10.0.3.7", "--remote-port", "5432", "--local-port", "50111",
			"--region", "eu-central-1", "--profile", "dev",
		}},
		{"kubectl", kubectlTransport{resource: "svc/pg-proxy", context: "stage", namespace: "data"}, "kubectl", []string{
			"port-forward", "--address", "127.0.0.1", "--context", "stage", "--namespace", "data", "svc/pg-proxy", "50111:5432",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, args, err := c.tr.Command(context.Background(), fwd)
			if err != nil || name != c.wantName || !reflect.DeepEqual(args, c.wantArgs) {
				t.Fatalf("got %s %q, %v\nwant %s %q", name, args, err, c.wantName, c.wantArgs)
			}
		})
	}
}

func TestEICETakesAnIPAsIsAndReportsResolutionFailure(t *testing.T) {
	failing := func(context.Context, string) ([]string, error) { return nil, errors.New("no such host") }
	tr := eiceTransport{endpointID: "e", resolve: failing}
	if _, args, err := tr.Command(context.Background(), Forward{RemoteHost: "10.1.2.3", RemotePort: 1, LocalPort: 2}); err != nil || args[5] != "10.1.2.3" {
		t.Fatalf("an IP must be used without a lookup: %q %v", args, err)
	}
	if _, _, err := tr.Command(context.Background(), fwd); err == nil {
		t.Fatal("a host that does not resolve must fail the start")
	}
}

func TestNewTransport(t *testing.T) {
	for typ, want := range map[string]string{
		"": "SSM session", config.TransportSSM: "SSM session", config.TransportSSH: "ssh",
		config.TransportEICE: "EC2 Instance Connect tunnel", config.TransportKubectl: "kubectl port-forward",
	} {
		var spec *config.TransportSpec
		if typ != "" {
			spec = &config.TransportSpec{Type: typ}
		}
		tr, err := NewTransport(config.EnvMapping{Transport: spec})
		if err != nil || tr.Name() != want {
			t.Errorf("type %q: %v %v, want %s", typ, tr, err, want)
		}
	}
	if _, err := NewTransport(config.EnvMapping{Transport: &config.TransportSpec{Type: "carrier-pigeon"}}); err == nil {
		t.Error("an unknown transport must be an error")
	}
}

func TestSSMArgsLeaveOutEmptyProfileAndRegion(t *testing.T) {
	args := ssmArgs(config.EnvMapping{EC2InstanceID: "i-0abc"}, fwd)
	for _, a := range args {
		if a == "--profile" || a == "--region" || a == "" {
			t.Fatalf("empty profile and region must be left out: %q", args)
		}
	}
	if !slices.Contains(args, "i-0abc") {
		t.Fatalf("the target is missing: %q", args)
	}
}
