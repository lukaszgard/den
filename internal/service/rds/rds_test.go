package rds

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
)

func newEngine(spec config.RDSSpec) *engine {
	spec.RDSHost = "db.cluster-x.eu-central-1.rds.amazonaws.com"
	return &engine{spec: &spec}
}

func TestPostgresCommandsUnchanged(t *testing.T) {
	e := newEngine(config.RDSSpec{Environment: "INT", LocalPort: 50111, DBName: "orders", DBUser: "app"})

	if got, want := e.ConnectCommand(),
		`psql "host=localhost port=50111 dbname=orders user=app sslmode=require"`; got != want {
		t.Errorf("ConnectCommand =\n%s\nwant\n%s", got, want)
	}
	if got, want := e.ConnectCommandWithSecret("tok"),
		`psql "host=localhost port=50111 dbname=orders user=app sslmode=require password=tok"`; got != want {
		t.Errorf("ConnectCommandWithSecret =\n%s\nwant\n%s", got, want)
	}
	if e.remotePort() != 5432 {
		t.Errorf("remotePort = %d, want 5432", e.remotePort())
	}
}

func TestMySQLCommands(t *testing.T) {
	t.Setenv("HOME", "/home/dev")
	e := newEngine(config.RDSSpec{
		Environment: "INT", LocalPort: 50131,
		Engine: config.RDSEngineMySQL,
		DBUser: "app_iam",
		DBName: "orders",
	})

	if e.remotePort() != 3306 {
		t.Errorf("remotePort = %d, want 3306 — the IAM token is signed for the RDS port", e.remotePort())
	}
	if got, want := e.ConnectCommand(),
		"mysql --defaults-extra-file=/home/dev/.config/den/mysql/50131.cnf -h 127.0.0.1 -P 50131 -u app_iam --enable-cleartext-plugin --ssl-mode=REQUIRED orders"; got != want {
		t.Errorf("ConnectCommand =\n%s\nwant\n%s", got, want)
	}
	if got, want := e.ConnectCommandWithSecret("host:3306/?Action=connect&X-Amz-Signature=ab"),
		"mysql -h 127.0.0.1 -P 50131 -u app_iam --enable-cleartext-plugin --ssl-mode=REQUIRED --password='host:3306/?Action=connect&X-Amz-Signature=ab' orders"; got != want {
		t.Errorf("ConnectCommandWithSecret =\n%s\nwant\n%s", got, want)
	}
}

func TestNoDatabaseNameIsLeftToTheClient(t *testing.T) {
	pg := newEngine(config.RDSSpec{LocalPort: 50111, DBUser: "app"})
	if got, want := pg.ConnectCommand(), `psql "host=localhost port=50111 user=app sslmode=require"`; got != want {
		t.Errorf("ConnectCommand =\n%s\nwant\n%s", got, want)
	}
	my := newEngine(config.RDSSpec{LocalPort: 50131, Engine: config.RDSEngineMySQL, DBUser: "app_iam"})
	if my.dbName() != "" || strings.Contains(my.ConnectCommand(), " orders") {
		t.Errorf("mysql without db_name must name no database: %s", my.ConnectCommand())
	}
}

func TestMySQLOptionFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mysql", "50131.cnf")
	token := "db.example:3306/?Action=connect&DBUser=app&X-Amz-Signature=abc"

	if err := writeMySQLOptionFile(path, "stale"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeMySQLOptionFile(path, token); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "[client]\npassword=\"" + token + "\"\n"; string(got) != want {
		t.Errorf("option file =\n%s\nwant\n%s", got, want)
	}
	info, _ := os.Stat(path)
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

// The terminal command is run by den, so the token must not sit where other
// users of the machine can read it: the argument list.
func TestPostgresTerminalCommand(t *testing.T) {
	e := newEngine(config.RDSSpec{Environment: "INT", LocalPort: 50111, DBName: "orders", DBUser: "app"})

	got := e.TerminalCommand("tok-1")
	want := service.ClientCommand{
		Tool: "psql",
		Args: []string{"host=localhost port=50111 dbname=orders user=app sslmode=require"},
		Env:  []string{"PGPASSWORD=tok-1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TerminalCommand =\n%+v\nwant\n%+v", got, want)
	}
	// The command den shows and the one it runs must reach the same database.
	if shown := e.ConnectCommand(); shown != `psql "`+got.Args[0]+`"` {
		t.Fatalf("the shown command %s does not match the run one %q", shown, got.Args)
	}
}

// MySQL reads the token from den's option file, which every refresh rewrites,
// so the command carries it nowhere.
func TestMySQLTerminalCommand(t *testing.T) {
	t.Setenv("HOME", "/home/dev")
	e := newEngine(config.RDSSpec{
		Environment: "INT", LocalPort: 50131,
		Engine: config.RDSEngineMySQL,
		DBUser: "app_iam",
		DBName: "orders",
	})

	got := e.TerminalCommand("tok-1")
	want := service.ClientCommand{
		Tool: "mysql",
		Args: []string{"--defaults-extra-file=/home/dev/.config/den/mysql/50131.cnf", "-h", "127.0.0.1", "-P", "50131",
			"-u", "app_iam", "--enable-cleartext-plugin", "--ssl-mode=REQUIRED", "orders"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TerminalCommand =\n%+v\nwant\n%+v", got, want)
	}
	if shown := e.ConnectCommand(); shown != "mysql "+strings.Join(got.Args, " ") {
		t.Fatalf("the shown command %s does not match the run one %q", shown, got.Args)
	}
}

func TestTokenArgsLeaveOutEmptyFlags(t *testing.T) {
	full := tokenArgs("db.example.com", 5432, "eu-central-1", "acme-dev", "jane")
	want := []string{"rds", "generate-db-auth-token", "--hostname", "db.example.com", "--port", "5432",
		"--region", "eu-central-1", "--profile", "acme-dev", "--username", "jane"}
	if !slices.Equal(full, want) {
		t.Errorf("tokenArgs = %q, want %q", full, want)
	}
	bare := tokenArgs("db.example.com", 5432, "", "", "jane")
	want = []string{"rds", "generate-db-auth-token", "--hostname", "db.example.com", "--port", "5432", "--username", "jane"}
	if !slices.Equal(bare, want) {
		t.Errorf("tokenArgs without profile and region = %q, want %q", bare, want)
	}
}
