package keepassxc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/secrets"
)

// fakeCLI writes a keepassxc-cli stand-in and returns its path and the
// directory where it logs each call's args and stdin. It behaves like the real
// CLI: the prompt on stderr, "Invalid credentials" for a wrong password,
// values one per line on stdout. The password it accepts is "pw".
func fakeCLI(t *testing.T) (cli, logDir string) {
	t.Helper()
	dir := t.TempDir()
	logDir = t.TempDir()
	script := `#!/bin/sh
n=$(ls "` + logDir + `" | grep -c '^args\.')
echo "$@" > "` + logDir + `/args.$n"
IFS= read -r pw
printf '%s' "$pw" > "` + logDir + `/stdin.$n"
db=""; entry=""; for a in "$@"; do db="$entry"; entry="$a"; done
printf 'Enter password to unlock %s: \n' "$db" >&2
case "$*" in *--no-password*) ;; *)
  if [ "$pw" != "pw" ]; then
    echo "Error while reading the database: Invalid credentials were provided, please try again." >&2
    exit 1
  fi;;
esac
if [ "$1" = "ls" ]; then
  # Real ls -R -f output: groups end in /, empty groups show [empty], and
  # paths under a group argument are relative to it.
  if [ "$entry" = "AMS" ]; then printf 'Postgres INT\nKafka/\nKafka/E2E\n'; exit 0; fi
  printf 'CI/\nCI/Jenkins\nEmpty/\nEmpty/[empty]\nRecycle Bin/\nRecycle Bin/Old\nAMS/\nAMS/Postgres INT\n'
  exit 0
fi
if [ "$entry" = "Missing" ]; then echo "Could not find entry with path Missing." >&2; exit 1; fi
# Emit one line per requested attribute, in order; Notes is multi-line.
prev=""
for a in "$@"; do
  if [ "$prev" = "-a" ]; then
    case "$a" in
      UserName) echo "ci-bot";;
      Password) echo "s3cr3t";;
      URL) echo "https://ci";;
      Notes) if [ "$entry" = "Multi" ]; then printf 'line one\nline two\n'; else echo "short"; fi;;
      *) echo "custom-$a";;
    esac
  fi
  [ "$a" = "-t" ] && echo "123456"
  prev="$a"
done
`
	cli = filepath.Join(dir, "keepassxc-cli")
	if err := os.WriteFile(cli, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lockAll)
	return cli, logDir
}

func database(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "team.kdbx")
	if err := os.WriteFile(p, []byte("kdbx"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func calls(t *testing.T, logDir string) (args, stdin []string) {
	t.Helper()
	for i := 0; ; i++ {
		a, err := os.ReadFile(filepath.Join(logDir, "args."+strconv.Itoa(i)))
		if err != nil {
			break
		}
		s, _ := os.ReadFile(filepath.Join(logDir, "stdin."+strconv.Itoa(i)))
		args = append(args, strings.TrimSpace(string(a)))
		stdin = append(stdin, string(s))
	}
	return args, stdin
}

func field(k, v string) secrets.Field { return secrets.Field{Key: k, Value: v} }

func TestReadWithPasswordOneCall(t *testing.T) {
	cli, logDir := fakeCLI(t)
	it := New(config.KeePassXCSecretSpec{Name: "j", Database: database(t), Entry: "CI/Jenkins", CLIPath: cli, TOTP: true})

	fields, err := it.ReadWithPassword(context.Background(), []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	want := []secrets.Field{
		field("UserName", "ci-bot"), field("Password", "s3cr3t"), field("URL", "https://ci"),
		field("Notes", "short"), field("TOTP", "123456"),
	}
	if !reflect.DeepEqual(fields, want) {
		t.Errorf("fields = %v\nwant %v", fields, want)
	}
	args, stdin := calls(t, logDir)
	if len(args) != 1 {
		t.Fatalf("calls = %d, want one when every value is one line", len(args))
	}
	for a := range strings.FieldsSeq(args[0]) {
		if a == "pw" {
			t.Errorf("password leaked into argv: %q", args[0])
		}
	}
	if strings.Contains(args[0], " -q") {
		t.Errorf("-q silences errors and must not be used: %q", args[0])
	}
	if stdin[0] != "pw" {
		t.Errorf("stdin = %q, want the password", stdin[0])
	}
}

func TestMultiLineValueFallsBackToOneCallPerAttribute(t *testing.T) {
	cli, logDir := fakeCLI(t)
	it := New(config.KeePassXCSecretSpec{Name: "m", Database: database(t), Entry: "Multi", CLIPath: cli})

	fields, err := it.ReadWithPassword(context.Background(), []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fields[3]; !reflect.DeepEqual(got, field("Notes", "line one\nline two")) {
		t.Errorf("Notes = %q, want both lines", got.Value)
	}
	if args, _ := calls(t, logDir); len(args) != 5 {
		t.Errorf("calls = %d, want 1 + one per attribute", len(args))
	}
}

func TestCustomAttributes(t *testing.T) {
	cli, _ := fakeCLI(t)
	it := New(config.KeePassXCSecretSpec{Name: "c", Database: database(t), Entry: "E", CLIPath: cli,
		Attributes: []string{"Password", "api-token"}})
	fields, err := it.ReadWithPassword(context.Background(), []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []secrets.Field{field("Password", "s3cr3t"), field("api-token", "custom-api-token")}; !reflect.DeepEqual(fields, want) {
		t.Errorf("fields = %v", fields)
	}
}

func TestWrongPassword(t *testing.T) {
	cli, _ := fakeCLI(t)
	it := New(config.KeePassXCSecretSpec{Name: "j", Database: database(t), Entry: "E", CLIPath: cli, RememberPassword: "10m"})
	_, err := it.ReadWithPassword(context.Background(), []byte("nope"))
	if !errors.Is(err, secrets.ErrBadPassword) {
		t.Fatalf("err = %v, want ErrBadPassword", err)
	}
	if !it.NeedsPassword() {
		t.Error("a wrong password must not be remembered")
	}
}

func TestOtherErrorsStripThePrompt(t *testing.T) {
	cli, _ := fakeCLI(t)
	it := New(config.KeePassXCSecretSpec{Name: "j", Database: database(t), Entry: "Missing", CLIPath: cli})
	_, err := it.ReadWithPassword(context.Background(), []byte("pw"))
	if err == nil || err.Error() != "keepassxc: Could not find entry with path Missing." {
		t.Errorf("err = %v", err)
	}
}

func TestRememberPassword(t *testing.T) {
	cli, logDir := fakeCLI(t)
	db := database(t)
	clock := time.Now()
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = time.Now })

	a := New(config.KeePassXCSecretSpec{Name: "a", Database: db, Entry: "A", CLIPath: cli, RememberPassword: "10m"})
	b := New(config.KeePassXCSecretSpec{Name: "b", Database: db, Entry: "B", CLIPath: cli})
	if !a.NeedsPassword() || !b.NeedsPassword() {
		t.Fatal("nothing is remembered yet")
	}
	pw := []byte("pw")
	if _, err := a.ReadWithPassword(context.Background(), pw); err != nil {
		t.Fatal(err)
	}
	clear(pw) // the caller clears its copy; the cache must hold its own
	if a.NeedsPassword() || b.NeedsPassword() {
		t.Fatal("the password must be remembered for every entry of the same database")
	}
	if _, err := b.Read(context.Background()); err != nil {
		t.Fatalf("Read with the remembered password: %v", err)
	}
	if _, stdin := calls(t, logDir); stdin[len(stdin)-1] != "pw" {
		t.Errorf("remembered password not used: %q", stdin[len(stdin)-1])
	}

	clock = clock.Add(10 * time.Minute)
	if !a.NeedsPassword() {
		t.Fatal("the password must be forgotten after remember_password")
	}
}

func TestNotRememberedByDefault(t *testing.T) {
	cli, _ := fakeCLI(t)
	it := New(config.KeePassXCSecretSpec{Name: "a", Database: database(t), Entry: "A", CLIPath: cli})
	if _, err := it.ReadWithPassword(context.Background(), []byte("pw")); err != nil {
		t.Fatal(err)
	}
	if !it.NeedsPassword() {
		t.Fatal("without remember_password the password must be asked every time")
	}
	if _, err := it.Read(context.Background()); !errors.Is(err, secrets.ErrBadPassword) {
		t.Errorf("Read while locked: err = %v", err)
	}
}

func TestLockForgetsEverything(t *testing.T) {
	cli, _ := fakeCLI(t)
	it := New(config.KeePassXCSecretSpec{Name: "a", Database: database(t), Entry: "A", CLIPath: cli, RememberPassword: "1h"})
	if _, err := it.ReadWithPassword(context.Background(), []byte("pw")); err != nil {
		t.Fatal(err)
	}
	it.Lock()
	if !it.NeedsPassword() {
		t.Fatal("Lock must forget the remembered password")
	}
}

func TestNoPasswordNeverPrompts(t *testing.T) {
	cli, logDir := fakeCLI(t)
	key := database(t) // any existing file works as a key file here
	it := New(config.KeePassXCSecretSpec{Name: "k", Database: database(t), Entry: "A", CLIPath: cli,
		KeyFile: key, NoPassword: true})
	if it.NeedsPassword() {
		t.Fatal("a key-file-only database never needs a password")
	}
	if _, err := it.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	args, stdin := calls(t, logDir)
	if !strings.Contains(args[0], "--no-password") || !strings.Contains(args[0], "-k "+key) {
		t.Errorf("args = %q", args[0])
	}
	if stdin[0] != "" {
		t.Errorf("stdin = %q, want nothing", stdin[0])
	}
}

func TestProbe(t *testing.T) {
	cli, _ := fakeCLI(t)
	db := database(t)
	cases := []struct {
		name string
		spec config.KeePassXCSecretSpec
		want string
	}{
		{"ready", config.KeePassXCSecretSpec{Database: db, CLIPath: cli}, ""},
		{"no cli", config.KeePassXCSecretSpec{Database: db, CLIPath: "/nope/keepassxc-cli"}, "cli_path not found"},
		{"no database", config.KeePassXCSecretSpec{Database: db + ".missing", CLIPath: cli}, "database not found"},
		{"no key file", config.KeePassXCSecretSpec{Database: db, CLIPath: cli, KeyFile: db + ".key"}, "key file not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := New(tc.spec).Probe()
			if st.Reason != tc.want || st.Ready != (tc.want == "") {
				t.Errorf("Probe = %+v, want %q", st, tc.want)
			}
		})
	}
}

func TestListEntries(t *testing.T) {
	cli, logDir := fakeCLI(t)
	db := database(t)
	it := New(config.KeePassXCSecretSpec{Name: "all", Database: db, CLIPath: cli})
	if !it.Browsing() {
		t.Fatal("no entry configured means browsing")
	}
	entries, err := it.ListEntries(context.Background(), []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"CI/Jenkins", "AMS/Postgres INT"}; !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %q, want groups, [empty] and the Recycle Bin left out", entries)
	}
	args, stdin := calls(t, logDir)
	if args[0] != "ls -R -f "+db || stdin[0] != "pw" {
		t.Errorf("args = %q stdin = %q", args[0], stdin[0])
	}
}

func TestListEntriesUnderGroup(t *testing.T) {
	cli, _ := fakeCLI(t)
	it := New(config.KeePassXCSecretSpec{Name: "ams", Database: database(t), Group: "AMS/", CLIPath: cli})
	entries, err := it.ListEntries(context.Background(), []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"AMS/Postgres INT", "AMS/Kafka/E2E"}; !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %q, want full paths show accepts", entries)
	}
}

func TestReadEntryUsesGivenPath(t *testing.T) {
	cli, logDir := fakeCLI(t)
	db := database(t)
	it := New(config.KeePassXCSecretSpec{Name: "all", Database: db, CLIPath: cli, Attributes: []string{"Password"}})
	fields, err := it.ReadEntry(context.Background(), "AMS/Postgres INT", []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0].Value != "s3cr3t" {
		t.Errorf("fields = %v", fields)
	}
	if args, _ := calls(t, logDir); !strings.HasSuffix(args[0], db+" AMS/Postgres INT") {
		t.Errorf("args = %q", args[0])
	}
}

func TestBrowsingWithRememberedPassword(t *testing.T) {
	cli, _ := fakeCLI(t)
	it := New(config.KeePassXCSecretSpec{Name: "all", Database: database(t), CLIPath: cli, RememberPassword: "5m"})
	if _, err := it.ListEntries(context.Background(), []byte("pw")); err != nil {
		t.Fatal(err)
	}
	if _, err := it.ReadEntry(context.Background(), "CI/Jenkins", nil); err != nil {
		t.Fatalf("ReadEntry with the remembered password: %v", err)
	}
	if _, err := it.ListEntries(context.Background(), []byte("nope")); !errors.Is(err, secrets.ErrBadPassword) {
		t.Errorf("wrong password: err = %v", err)
	}
}
