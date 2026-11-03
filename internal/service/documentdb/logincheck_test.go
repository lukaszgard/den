package documentdb

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
)

// The check is a tab's mongosh, plus a script that fails unless the
// connection is authenticated: mongosh can connect without logging in and
// say nothing about it.
func TestLoginProbe(t *testing.T) {
	spec := &config.DocumentDBSpec{DocumentDBHost: "cluster.docdb.amazonaws.com", DBName: "app", CAFile: "/etc/rds-ca.pem"}
	spec.LocalPort = 50141
	e := &engine{spec: spec, env: config.EnvMapping{CredentialProfile: "acme-data"}}

	p := e.LoginProbe("")
	tab := e.TerminalCommand("")
	want := service.ClientCommand{
		Tool: "mongosh",
		Args: append(slices.Clone(tab.Args), "--quiet", "--eval", authCheck),
		Env:  []string{"AWS_PROFILE=acme-data"},
	}
	if !reflect.DeepEqual(p.Command, want) {
		t.Fatalf("probe =\n%+v\nwant\n%+v", p.Command, want)
	}
	if !strings.Contains(authCheck, "authenticatedUsers") || !strings.Contains(authCheck, "throw") {
		t.Errorf("the script does not fail on a connection without a user: %s", authCheck)
	}
	for _, want := range []string{`AWS profile "acme-data"`, "$external", "MONGODB-AWS", "AWS_ACCESS_KEY_ID", "aws sso login --profile acme-data"} {
		if !strings.Contains(p.Hint, want) {
			t.Errorf("hint has no %q:\n%s", want, p.Hint)
		}
	}
}

// The hint is read in the detail view's log, which shows 104 runes of a line
// at 120 columns and cuts the rest.
func TestLoginHintLinesFitTheLog(t *testing.T) {
	e := &engine{spec: &config.DocumentDBSpec{}, env: config.EnvMapping{CredentialProfile: "acme-cn-e2e-data"}}
	for line := range strings.Lines(e.LoginProbe("").Hint) {
		if n := len([]rune(strings.TrimRight(line, "\n"))); n > 104 {
			t.Errorf("a line of %d runes:\n%s", n, line)
		}
	}
}
