package rds

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/ssmfwd"
)

// psqlOnce is what makes psql answer once and exit: no psqlrc, never a
// password prompt, one bare value. Options go first: BSD getopt on macOS stops
// at the first argument that is not one.
var psqlOnce = []string{"-X", "-w", "-A", "-t", "-c", "select 1"}

// The check logs in as a tab would: the same connection string and the same
// credential, handed over the same way.
func TestPostgresLoginProbe(t *testing.T) {
	e := newEngine(config.RDSSpec{LocalPort: 50111, DBName: "orders", DBUser: "app"})

	p := e.LoginProbe("tok-1")
	tab := e.TerminalCommand("tok-1")
	want := service.ClientCommand{Tool: "psql", Args: append(slices.Clone(psqlOnce), tab.Args...), Env: tab.Env}
	if !reflect.DeepEqual(p.Command, want) {
		t.Fatalf("probe =\n%+v\nwant\n%+v", p.Command, want)
	}
	if p.Succeeded != nil || p.User != "app" {
		t.Errorf("Succeeded set: %v, User = %q; want exit status and app", p.Succeeded != nil, p.User)
	}
}

func TestMySQLLoginProbe(t *testing.T) {
	t.Setenv("HOME", "/home/dev")
	e := newEngine(config.RDSSpec{LocalPort: 50131, Engine: config.RDSEngineMySQL, DBUser: "app_iam", DBName: "orders"})

	p := e.LoginProbe("host:3306/?Action=connect&X-Amz-Signature=ab")
	want := service.ClientCommand{
		Tool: "mysql",
		Args: []string{"--defaults-extra-file=/home/dev/.config/den/mysql/50131.cnf", "-h", "127.0.0.1", "-P", "50131",
			"-u", "app_iam", "--enable-cleartext-plugin", "--ssl-mode=REQUIRED", "--batch", "-e", "select 1", "orders"},
	}
	if !reflect.DeepEqual(p.Command, want) {
		t.Fatalf("probe =\n%+v\nwant\n%+v", p.Command, want)
	}
}

func TestPasswordLoginProbes(t *testing.T) {
	isolateFiles(t)
	pg := newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50110, DBName: "orders"}), loginSecret())
	if _, err := pg.Credential(context.Background(), discard); err != nil {
		t.Fatal(err)
	}
	p := pg.LoginProbe(testPassword)
	tab := pg.TerminalCommand(testPassword)
	want := service.ClientCommand{Tool: "psql", Args: append(slices.Clone(psqlOnce), tab.Args...), Env: tab.Env}
	if !reflect.DeepEqual(p.Command, want) {
		t.Fatalf("postgres probe =\n%+v\nwant\n%+v", p.Command, want)
	}
	if p.User != "app" {
		t.Errorf("User = %q, want the secret's username", p.User)
	}

	my := newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50131, Engine: config.RDSEngineMySQL, DBName: "orders"}), loginSecret())
	if _, err := my.Credential(context.Background(), discard); err != nil {
		t.Fatal(err)
	}
	want = service.ClientCommand{
		Tool: "mysql",
		Args: []string{"-h", "127.0.0.1", "-P", "50131", "-u", "app", "--ssl-mode=REQUIRED", "--batch", "-e", "select 1", "orders"},
		Env:  []string{"MYSQL_PWD=" + testPassword},
	}
	if got := my.LoginProbe(testPassword).Command; !reflect.DeepEqual(got, want) {
		t.Fatalf("mysql probe =\n%+v\nwant\n%+v", got, want)
	}
}

// The process list is readable by every user of the machine.
func TestLoginProbesKeepTheSecretOutOfTheArguments(t *testing.T) {
	isolateFiles(t)
	probes := map[string]ssmfwd.LoginProbe{
		"postgres": newEngine(config.RDSSpec{LocalPort: 50111, DBUser: "app"}).LoginProbe("tok-1"),
		"mysql":    newEngine(config.RDSSpec{LocalPort: 50131, Engine: config.RDSEngineMySQL, DBUser: "app"}).LoginProbe("tok-1"),
		"postgres password": newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50110, DBUser: "app"}), loginSecret()).
			LoginProbe(testPassword),
		"mysql password": newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50131, Engine: config.RDSEngineMySQL, DBUser: "app"}), loginSecret()).
			LoginProbe(testPassword),
	}
	for name, p := range probes {
		for _, a := range p.Command.Args {
			if strings.Contains(a, "tok-1") || strings.Contains(a, testPassword) {
				t.Errorf("%s: the secret is in argument %q", name, a)
			}
		}
	}
}

// The token signs a profile, a user and a host, and RDS says only "PAM
// authentication failed": the hint names all three and the usual causes.
func TestIAMLoginHint(t *testing.T) {
	e := newEngine(config.RDSSpec{LocalPort: 50111, DBUser: "base-app-rw"})
	e.spec.AWSProfile = "acme-int"
	e.env = config.EnvMapping{AWSProfile: "acme-int", CredentialProfile: "acme-int", AWSRegionCode: "eu-central-1"}

	hint := e.LoginProbe("tok").Hint
	for _, want := range []string{
		`AWS profile "acme-int"`,
		"credential_profile is not set",
		`"base-app-rw"`,
		"db.cluster-x.eu-central-1.rds.amazonaws.com:5432",
		`GRANT rds_iam TO "base-app-rw"`,
		"rds-db:connect on\n    arn:aws:rds-db:eu-central-1:<account>:dbuser:<resource-id>/base-app-rw",
		"aws sso login --profile acme-int",
	} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint has no %q:\n%s", want, hint)
		}
	}

	e.spec.CredentialProfile = "acme-int-db"
	e.env.CredentialProfile = "acme-int-db"
	if hint := e.LoginProbe("tok").Hint; strings.Contains(hint, "credential_profile is not set") || !strings.Contains(hint, `"acme-int-db"`) {
		t.Errorf("with credential_profile set:\n%s", hint)
	}
}

func TestMySQLIAMLoginHint(t *testing.T) {
	e := newEngine(config.RDSSpec{LocalPort: 50131, Engine: config.RDSEngineMySQL, DBUser: "app_iam"})
	hint := e.LoginProbe("tok").Hint
	for _, want := range []string{"AWSAuthenticationPlugin", "the default AWS credentials", ":3306", "dbuser:<resource-id>/app_iam", "arn:aws:rds-db:<region>:"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint has no %q:\n%s", want, hint)
		}
	}
	if strings.Contains(hint, "rds_iam") {
		t.Errorf("rds_iam is PostgreSQL's:\n%s", hint)
	}
}

// A password login fails for its own reasons: a stale secret, or a user RDS
// holds to IAM.
func TestPasswordLoginHint(t *testing.T) {
	isolateFiles(t)
	pg := newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50110, DBUser: "app"}), loginSecret())
	hint := pg.LoginProbe(testPassword).Hint
	for _, want := range []string{`secret "orders-login"`, "rds_iam", `"app"`} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint has no %q:\n%s", want, hint)
		}
	}
	my := newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50131, Engine: config.RDSEngineMySQL, DBUser: "app"}), loginSecret())
	if hint := my.LoginProbe(testPassword).Hint; !strings.Contains(hint, "AWSAuthenticationPlugin") {
		t.Errorf("mysql hint has no AWSAuthenticationPlugin:\n%s", hint)
	}
}

// maxHintLine is what the detail view's log shows of a line at 120 columns,
// after the frame and the timestamp; anything past it is cut off.
const maxHintLine = 104

// The hint is read in the log, which cuts long lines: none may lose its end.
func TestLoginHintLinesFitTheLog(t *testing.T) {
	isolateFiles(t)
	host := "cn-e2e.cluster-c9akciq32x4f.eu-central-1.rds.amazonaws.com"
	iam := newEngine(config.RDSSpec{LocalPort: 50111, DBUser: "base-app-rw"})
	iam.spec.RDSHost = host
	iam.env = config.EnvMapping{CredentialProfile: "acme-cn-e2e-admin", AWSRegionCode: "eu-central-1"}
	my := newEngine(config.RDSSpec{LocalPort: 50131, Engine: config.RDSEngineMySQL, DBUser: "base-app-rw"})
	my.spec.RDSHost = host
	pw := newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50110, DBUser: "base-app-rw"}), loginSecret())
	for name, hint := range map[string]string{
		"postgres": iam.LoginProbe("tok").Hint,
		"mysql":    my.LoginProbe("tok").Hint,
		"password": pw.LoginProbe(testPassword).Hint,
	} {
		for line := range strings.Lines(hint) {
			if n := len([]rune(strings.TrimRight(line, "\n"))); n > maxHintLine {
				t.Errorf("%s: a line of %d runes, the log shows %d:\n%s", name, n, maxHintLine, line)
			}
		}
	}
}
