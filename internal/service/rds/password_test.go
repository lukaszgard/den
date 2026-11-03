package rds

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/secrets"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/ssmfwd"
)

const testPassword = `p@ss:w"ord $HOME\`

// fakeSecret is a configured secret, standing in for a provider's CLI or AWS.
type fakeSecret struct {
	fields  []secrets.Field
	readErr error
	status  secrets.Status
	reads   int
}

func (s *fakeSecret) Name() string              { return "orders-login" }
func (s *fakeSecret) Description() string       { return "" }
func (s *fakeSecret) Provider() string          { return "sops" }
func (s *fakeSecret) Location() string          { return "secrets/orders.yaml" }
func (s *fakeSecret) Details() []secrets.Detail { return nil }
func (s *fakeSecret) AutoReveal() bool          { return false }
func (s *fakeSecret) Probe() secrets.Status     { return s.status }
func (s *fakeSecret) Read(context.Context) ([]secrets.Field, error) {
	s.reads++
	return s.fields, s.readErr
}

func loginSecret() *fakeSecret {
	return &fakeSecret{
		status: secrets.Status{Ready: true},
		fields: []secrets.Field{{Key: "username", Value: "app"}, {Key: "password", Value: testPassword}},
	}
}

func passwordSpec(spec config.RDSSpec) *config.RDSSpec {
	spec.RDSHost = "db.cluster-x.eu-central-1.rds.amazonaws.com"
	spec.Auth = &config.SecretAuth{Secret: "orders-login"}
	return &spec
}

func discard(string) {}

// isolateFiles points everything a credential could be written to at an empty
// folder, and returns the pgpass file libpq would read.
func isolateFiles(t *testing.T) (pgpassFile string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	pgpassFile = filepath.Join(dir, ".pgpass")
	t.Setenv("PGPASSFILE", pgpassFile)
	return pgpassFile
}

// Before the first read only db_user can name the user, and a command without
// one still has to be a valid command.
func TestPostgresCommandNamesTheUserOnceKnown(t *testing.T) {
	isolateFiles(t)
	e := newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50110, DBName: "orders"}), loginSecret())

	if got, want := e.ConnectCommand(), `psql "host=localhost hostaddr=127.0.0.1 port=50110 dbname=orders sslmode=require"`; got != want {
		t.Errorf("before the read: ConnectCommand =\n%s\nwant\n%s", got, want)
	}
	if _, err := e.Credential(context.Background(), discard); err != nil {
		t.Fatal(err)
	}
	if got, want := e.ConnectCommand(), `psql "host=localhost hostaddr=127.0.0.1 port=50110 dbname=orders user=app sslmode=require"`; got != want {
		t.Errorf("after the read: ConnectCommand =\n%s\nwant\n%s", got, want)
	}
}

func TestDBUserWinsOverTheSecretsUsername(t *testing.T) {
	isolateFiles(t)
	e := newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50110, DBUser: "reporting"}), loginSecret())
	if _, err := e.Credential(context.Background(), discard); err != nil {
		t.Fatal(err)
	}
	if got := e.ConnectCommand(); !strings.Contains(got, "user=reporting") {
		t.Errorf("ConnectCommand = %s, want db_user", got)
	}
}

// A tab gets the password from the environment, so an awkward one needs no
// escaping and nothing shows in the process list. The shown command never
// carries it, and matches what runs.
func TestPostgresTerminalCommandTakesThePasswordFromTheEnvironment(t *testing.T) {
	isolateFiles(t)
	e := newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50110, DBName: "orders"}), loginSecret())
	if _, err := e.Credential(context.Background(), discard); err != nil {
		t.Fatal(err)
	}
	got := e.TerminalCommand(testPassword)
	want := service.ClientCommand{
		Tool: "psql",
		Args: []string{"host=localhost hostaddr=127.0.0.1 port=50110 dbname=orders user=app sslmode=require"},
		Env:  []string{"PGPASSWORD=" + testPassword},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TerminalCommand =\n%+v\nwant\n%+v", got, want)
	}
	if shown := e.ConnectCommand(); shown != `psql "`+got.Args[0]+`"` {
		t.Errorf("the shown command %s does not match the run one %q", shown, got.Args)
	}
}

// The cleartext plugin is for IAM tokens; a password must not switch it on. The
// shown command asks for the password with -p, which in a tab would make the
// client ask instead of reading MYSQL_PWD.
func TestMySQLCommandsWithAPassword(t *testing.T) {
	isolateFiles(t)
	e := newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50131, Engine: config.RDSEngineMySQL, DBName: "orders"}), loginSecret())
	if _, err := e.Credential(context.Background(), discard); err != nil {
		t.Fatal(err)
	}
	if got, want := e.ConnectCommand(), "mysql -h 127.0.0.1 -P 50131 -u app --ssl-mode=REQUIRED -p orders"; got != want {
		t.Errorf("ConnectCommand =\n%s\nwant\n%s", got, want)
	}
	got := e.TerminalCommand(testPassword)
	want := service.ClientCommand{
		Tool: "mysql",
		Args: []string{"-h", "127.0.0.1", "-P", "50131", "-u", "app", "--ssl-mode=REQUIRED", "orders"},
		Env:  []string{"MYSQL_PWD=" + testPassword},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TerminalCommand =\n%+v\nwant\n%+v", got, want)
	}
}

func TestForwardGoesToTheEnginesPort(t *testing.T) {
	cases := []struct {
		engine string
		want   int
	}{{config.RDSEnginePostgres, 5432}, {config.RDSEngineMySQL, 3306}}
	for _, c := range cases {
		spec := passwordSpec(config.RDSSpec{LocalPort: 50110, Engine: c.engine})
		fwds, err := newPasswordEngine(spec, loginSecret()).Forwards()
		if err != nil {
			t.Fatal(err)
		}
		want := []ssmfwd.Forward{{RemoteHost: spec.RDSHost, RemotePort: c.want, LocalPort: 50110}}
		if !reflect.DeepEqual(fwds, want) {
			t.Errorf("%s: Forwards = %+v, want %+v", c.engine, fwds, want)
		}
	}
}

// No expiry is what keeps the refresh loop and the TTL bar away.
func TestCredentialIsThePasswordWithNoExpiry(t *testing.T) {
	isolateFiles(t)
	e := newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50110}), loginSecret())

	cred, err := e.Credential(context.Background(), discard)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Secret != testPassword || !cred.Expires.IsZero() {
		t.Errorf("Credential = %+v, want the password and no expiry", cred)
	}
}

// r and a reconnect must pick up a rotated password.
func TestEveryCredentialReadsTheSecretAgain(t *testing.T) {
	isolateFiles(t)
	secret := loginSecret()
	e := newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50110}), secret)
	if _, err := e.Credential(context.Background(), discard); err != nil {
		t.Fatal(err)
	}

	secret.fields = []secrets.Field{{Key: "username", Value: "app2"}, {Key: "password", Value: "rotated"}}
	cred, err := e.Credential(context.Background(), discard)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Secret != "rotated" {
		t.Errorf("Secret = %q, want the rotated password", cred.Secret)
	}
	if got := e.ConnectCommand(); !strings.Contains(got, "user=app2") {
		t.Errorf("ConnectCommand = %s, want the new user", got)
	}
}

func TestCustomKeysAreUsed(t *testing.T) {
	isolateFiles(t)
	spec := passwordSpec(config.RDSSpec{LocalPort: 50110})
	spec.Auth.UsernameKey, spec.Auth.PasswordKey = "db.user", "db.pass"
	secret := &fakeSecret{fields: []secrets.Field{{Key: "db.user", Value: "nested"}, {Key: "db.pass", Value: "pw"}}}
	e := newPasswordEngine(spec, secret)

	cred, err := e.Credential(context.Background(), discard)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Secret != "pw" || !strings.Contains(e.ConnectCommand(), "user=nested") {
		t.Errorf("Secret = %q, command %s", cred.Secret, e.ConnectCommand())
	}
}

func TestCredentialFailures(t *testing.T) {
	cases := []struct {
		name    string
		spec    config.RDSSpec
		secret  *fakeSecret
		wantErr []string
	}{
		{
			name:    "the provider cannot read it",
			secret:  &fakeSecret{readErr: errors.New("permission denied")},
			wantErr: []string{"orders-login", "permission denied"},
		},
		{
			name:    "no password in it",
			secret:  &fakeSecret{fields: []secrets.Field{{Key: "username", Value: "app"}}},
			wantErr: []string{"orders-login", `"password"`},
		},
		{
			name:    "no user anywhere",
			secret:  &fakeSecret{fields: []secrets.Field{{Key: "password", Value: testPassword}}},
			wantErr: []string{"orders-login", `"username"`, "db_user"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolateFiles(t)
			c.spec.LocalPort = 50110
			_, err := newPasswordEngine(passwordSpec(c.spec), c.secret).Credential(context.Background(), discard)
			if err == nil {
				t.Fatal("want an error")
			}
			for _, want := range c.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %s", err, want)
				}
			}
			if strings.Contains(err.Error(), testPassword) {
				t.Errorf("error %q carries the password", err)
			}
		})
	}
}

// A secret with only a password works when db_user names the user.
func TestPasswordOnlySecretWithDBUser(t *testing.T) {
	isolateFiles(t)
	secret := &fakeSecret{fields: []secrets.Field{{Key: "password", Value: testPassword}}}
	e := newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50110, DBUser: "app"}), secret)
	if _, err := e.Credential(context.Background(), discard); err != nil {
		t.Fatal(err)
	}
}

// The promise of this login: the password reaches no file, whichever client,
// and the log says it was read without saying what it is.
func TestThePasswordReachesNoFileAndNoLog(t *testing.T) {
	for _, engine := range []string{config.RDSEnginePostgres, config.RDSEngineMySQL} {
		t.Run(engine, func(t *testing.T) {
			pgpassFile := isolateFiles(t)
			home, _ := os.UserHomeDir()
			e := newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50110, Engine: engine}), loginSecret())

			var logged []string
			if _, err := e.Credential(context.Background(), func(l string) { logged = append(logged, l) }); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(pgpassFile); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("Stat(pgpass) = %v, want no file", err)
			}
			if _, err := os.Stat(filepath.Join(home, ".config", "den", "mysql")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("Stat(option file folder) = %v, want none", err)
			}
			if len(logged) == 0 {
				t.Error("nothing logged: the log should say the password was read")
			}
			if joined := strings.Join(logged, "\n"); strings.Contains(joined, testPassword) {
				t.Errorf("the log carries the password: %s", joined)
			}
		})
	}
}

// A service moved from IAM to a password keeps its port, and psql would send
// the dead token from pgpass rather than ask.
func TestConnectingRemovesTheOldTokenFromPgpass(t *testing.T) {
	pgpassFile := isolateFiles(t)
	content := "localhost:50110:orders:app:dead-token\nlocalhost:50999:orders:app:other\n"
	if err := os.WriteFile(pgpassFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	e := newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50110}), loginSecret())
	if _, err := e.Credential(context.Background(), discard); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(pgpassFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := "localhost:50999:orders:app:other\n"; string(got) != want {
		t.Errorf("pgpass = %q, want %q", got, want)
	}
}

// MySQL never reads pgpass, so a MySQL service has no business in that file.
func TestMySQLLeavesPgpassAlone(t *testing.T) {
	pgpassFile := isolateFiles(t)
	const content = "localhost:50110:orders:app:someone-elses\n"
	if err := os.WriteFile(pgpassFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	e := newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50110, Engine: config.RDSEngineMySQL}), loginSecret())
	if _, err := e.Credential(context.Background(), discard); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(pgpassFile); string(got) != content {
		t.Errorf("pgpass = %q, want it unchanged", got)
	}
}

func TestPreflightReportsASecretThatIsNotReady(t *testing.T) {
	secret := loginSecret()
	e := newPasswordEngine(passwordSpec(config.RDSSpec{LocalPort: 50110}), secret)
	if err := e.Preflight(); err != nil {
		t.Errorf("ready secret: Preflight = %v", err)
	}

	secret.status = secrets.Status{Reason: "SSO login expired — log in from the AWS pane"}
	err := e.Preflight()
	if err == nil {
		t.Fatal("want an error for a secret that is not ready")
	}
	for _, want := range []string{"orders-login", "SSO login expired"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
	if secret.reads != 0 {
		t.Errorf("Preflight read the secret %d times; it must never decrypt", secret.reads)
	}
}

func TestNewPicksTheLoginFromTheSpec(t *testing.T) {
	isolateFiles(t)
	env := config.SSMTarget{EC2InstanceID: "i-1", AWSRegionCode: "eu-central-1"}

	withPassword := passwordSpec(config.RDSSpec{SSMTarget: env})
	pw, err := New("orders", withPassword, loginSecret())
	if err != nil {
		t.Fatal(err)
	}
	login, ok := service.PasswordLoginOf(pw)
	if !ok || login.Secret != "orders-login" || login.Provider != "sops" {
		t.Errorf("PasswordLoginOf = %+v, %v", login, ok)
	}
	if !login.ReadAt.IsZero() {
		t.Errorf("ReadAt = %v before any read", login.ReadAt)
	}
	// r and p stay; Y goes, since it would put a lasting password in a shell's history.
	if !service.HoldsToken(pw) {
		t.Error("a password service must offer r and p")
	}
	if _, ok := service.SecretCommand(pw); ok {
		t.Error("a password service must not offer Y")
	}

	withToken := &config.RDSSpec{SSMTarget: env, RDSHost: "db.example", DBUser: "app"}
	iam, err := New("orders", withToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := service.PasswordLoginOf(iam); ok {
		t.Error("an IAM service has no password login")
	}
	if _, ok := service.SecretCommand(iam); !ok {
		t.Error("an IAM service must keep Y")
	}
}

// Validation normally catches this, but a service must not connect with no way
// to log in.
func TestNewWithAuthButNoSecretFails(t *testing.T) {
	spec := passwordSpec(config.RDSSpec{SSMTarget: config.SSMTarget{EC2InstanceID: "i-1", AWSRegionCode: "eu-central-1"}})
	if _, err := New("orders", spec, nil); err == nil || !strings.Contains(err.Error(), "orders-login") {
		t.Errorf("New = %v, want it to name the missing secret", err)
	}
}
