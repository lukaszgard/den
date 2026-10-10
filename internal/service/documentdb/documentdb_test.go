package documentdb

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
)

func target() config.SSMTarget {
	return config.SSMTarget{Region: "x", Environment: "y", EC2InstanceID: "i-0abc", AWSProfile: "ssm", CredentialProfile: "acme-data", AWSRegionCode: "eu-central-1"}
}

// The URI flags are load-bearing (see ConnectCommand): losing directConnection
// or adding replicaSet makes mongosh abandon the tunnel for in-VPC hostnames.
func TestConnectCommandKeepsTheTunnelFlags(t *testing.T) {
	svc, err := New("docdb", &config.DocumentDBSpec{SSMTarget: target(), DocumentDBHost: "cluster.docdb.amazonaws.com", DBName: "app"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := svc.ConnectCommand()
	for _, want := range []string{
		"AWS_PROFILE=acme-data mongosh",
		`"mongodb://localhost:27017/app?tls=true&directConnection=true&retryWrites=false"`,
		"--authenticationMechanism MONGODB-AWS",
		"--tlsAllowInvalidHostnames",
		"--tlsCAFile ",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("connect command lacks %q:\n%s", want, cmd)
		}
	}
	if strings.Contains(cmd, "replicaSet") {
		t.Errorf("connect command must not name a replica set:\n%s", cmd)
	}
	if st := svc.State(); st.LocalPort != config.DefaultLocalPort(config.ServiceTypeDocumentDB) {
		t.Errorf("default local port = %d, want %d", st.LocalPort, config.DefaultLocalPort(config.ServiceTypeDocumentDB))
	}
}

func TestConfiguredCAFileAndForwards(t *testing.T) {
	spec := &config.DocumentDBSpec{SSMTarget: target(), DocumentDBHost: "cluster.docdb.amazonaws.com", CAFile: "/etc/rds-ca.pem"}
	spec.LocalPort = 50141
	e := &engine{spec: spec}
	if !strings.Contains(e.ConnectCommand(), "--tlsCAFile /etc/rds-ca.pem") {
		t.Fatalf("configured ca_file not used: %s", e.ConnectCommand())
	}
	fwds, err := e.Forwards()
	if err != nil || len(fwds) != 1 || fwds[0].RemotePort != 27017 || fwds[0].LocalPort != 50141 || fwds[0].RemoteHost != spec.DocumentDBHost {
		t.Fatalf("Forwards = %+v, %v", fwds, err)
	}
	if _, err := (&engine{spec: &config.DocumentDBSpec{}}).Forwards(); err == nil {
		t.Fatal("a missing documentdb_host must be an error")
	}
}

// den runs the terminal command itself, without a shell: the arguments are the
// shown command's, unquoted, and the profile travels in the environment.
func TestTerminalCommandIsTheShownCommand(t *testing.T) {
	spec := &config.DocumentDBSpec{SSMTarget: target(), DocumentDBHost: "cluster.docdb.amazonaws.com", DBName: "app", CAFile: "/etc/rds-ca.pem"}
	spec.LocalPort = 50141
	e := &engine{spec: spec, env: config.EnvMapping{CredentialProfile: "acme-data"}}

	got := e.TerminalCommand("")
	want := service.ClientCommand{
		Tool: "mongosh",
		Args: []string{
			"mongodb://localhost:50141/app?tls=true&directConnection=true&retryWrites=false",
			"--authenticationMechanism", "MONGODB-AWS",
			"--authenticationDatabase", "$external",
			"--tlsCAFile", "/etc/rds-ca.pem",
			"--tlsAllowInvalidHostnames",
		},
		Env: []string{"AWS_PROFILE=acme-data"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TerminalCommand =\n%+v\nwant\n%+v", got, want)
	}
	// The command den shows and the one it runs must reach the same cluster.
	shown := e.ConnectCommand()
	for _, arg := range got.Args {
		if !strings.Contains(shown, arg) {
			t.Errorf("the shown command lacks the run argument %q:\n%s", arg, shown)
		}
	}
	if !strings.HasPrefix(shown, got.Env[0]+" mongosh ") {
		t.Errorf("the shown command does not start with the run environment %q:\n%s", got.Env[0], shown)
	}
}

// Without a configured profile mongosh resolves credentials as den's own
// environment does; an empty AWS_PROFILE would name a profile that is not there.
func TestTerminalCommandWithoutAProfileSetsNone(t *testing.T) {
	spec := &config.DocumentDBSpec{DocumentDBHost: "cluster.docdb.amazonaws.com", CAFile: "/etc/rds-ca.pem"}
	spec.LocalPort = 50141
	if got := (&engine{spec: spec}).TerminalCommand(""); got.Env != nil {
		t.Fatalf("Env = %q, want none", got.Env)
	}
}
