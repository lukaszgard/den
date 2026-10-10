package vault

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/secrets"
)

// fakeVault puts a `vault` on PATH that answers like the real CLI (output
// formats captured from Vault 1.14): KV v2 nests data.data next to
// data.metadata, KV v1 has data only, lists are JSON arrays with folders
// ending in "/", a missing folder prints {} and exits 2, and a bad token gets
// a 403 on stderr. It logs its args and VAULT_ADDR to logFile.
func fakeVault(t *testing.T) (logFile string) {
	t.Helper()
	bin := t.TempDir()
	logFile = filepath.Join(t.TempDir(), "calls")
	script := `#!/bin/sh
echo "$* ADDR=$VAULT_ADDR NS=$VAULT_NAMESPACE" >> "` + logFile + `"
if [ "$VAULT_TOKEN" = "bogus" ]; then
  printf 'Error making API request.\n\nURL: GET http://v/v1/sys/internal/ui/mounts/secret\nCode: 403. Errors:\n\n* permission denied\n' >&2
  exit 2
fi
for a in "$@"; do last="$a"; done
case "$1 $2" in
"kv get")
  case "$last" in
  grafana/admin) echo '{"request_id":"x","data":{"data":{"db":{"host":"h","port":5432},"password":"gr4f","user":"admin"},"metadata":{"version":2,"destroyed":false}},"warnings":null}';;
  legacy/thing) echo '{"request_id":"x","lease_duration":2764800,"data":{"token":"abc"},"warnings":null}';;
  apps/*) echo '{"data":{"data":{"password":"p-'"$last"'"},"metadata":{"version":1}}}';;
  *) echo "No value found at secret/data/$last" >&2; exit 2;;
  esac;;
"kv list")
  case "$last" in
  apps/) echo '["ams/","top"]';;
  apps/ams/) echo '["kafka/","postgres"]';;
  apps/ams/kafka/) echo '["e2e"]';;
  big/) i=0; printf '['; while [ $i -lt 600 ]; do [ $i -gt 0 ] && printf ','; printf '"s%03d"' $i; i=$((i+1)); done; echo ']';;
  *) echo '{}'; exit 2;;
  esac;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "vault"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("VAULT_TOKEN", "root")
	return logFile
}

func calls(t *testing.T, logFile string) []string {
	t.Helper()
	b, _ := os.ReadFile(logFile)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func field(k, v string) secrets.Field { return secrets.Field{Key: k, Value: v} }

// keyValues drops each field's Path: what is read is checked here, the path
// once, in package secrets.
func keyValues(fields []secrets.Field) []secrets.Field {
	out := make([]secrets.Field, len(fields))
	for i, f := range fields {
		out[i] = field(f.Key, f.Value)
	}
	return out
}

func TestReadKVv2(t *testing.T) {
	log := fakeVault(t)
	it := New(config.VaultSecretSpec{Name: "g", Path: "grafana/admin", Address: "https://vault.example", Namespace: "team-a"})
	fields, err := it.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []secrets.Field{field("db.host", "h"), field("db.port", "5432"), field("password", "gr4f"), field("user", "admin")}
	if !reflect.DeepEqual(keyValues(fields), want) {
		t.Errorf("fields = %v\nwant %v (metadata must not show up)", fields, want)
	}
	got := calls(t, log)[0]
	if got != "kv get -format=json -mount=secret grafana/admin ADDR=https://vault.example NS=team-a" {
		t.Errorf("call = %q", got)
	}
}

func TestReadKVv1(t *testing.T) {
	fakeVault(t)
	fields, err := New(config.VaultSecretSpec{Name: "l", Mount: "/kv1/", Path: "legacy/thing"}).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []secrets.Field{field("token", "abc")}; !reflect.DeepEqual(keyValues(fields), want) {
		t.Errorf("fields = %v", fields)
	}
}

func TestFieldAndVersion(t *testing.T) {
	log := fakeVault(t)
	it := New(config.VaultSecretSpec{Name: "g", Path: "grafana/admin", Field: "db", Version: 1})
	fields, err := it.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []secrets.Field{field("db.host", "h"), field("db.port", "5432")}; !reflect.DeepEqual(keyValues(fields), want) {
		t.Errorf("fields = %v, want only db and its children", fields)
	}
	if !strings.Contains(calls(t, log)[0], "-version=1") {
		t.Errorf("version not passed: %q", calls(t, log)[0])
	}
	_, err = New(config.VaultSecretSpec{Name: "g", Path: "grafana/admin", Field: "nope"}).Read(context.Background())
	if err == nil || !strings.Contains(err.Error(), `no field "nope"`) {
		t.Errorf("missing field: err = %v", err)
	}
}

func TestListFolderRecursively(t *testing.T) {
	fakeVault(t)
	it := New(config.VaultSecretSpec{Name: "apps", Path: "apps/"})
	if !it.Browsing() {
		t.Fatal("a path ending in / is a folder")
	}
	entries, err := it.ListEntries(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"apps/ams/kafka/e2e", "apps/ams/postgres", "apps/top"}; !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %q", entries)
	}
	fields, err := it.ReadEntry(context.Background(), "apps/ams/postgres", nil)
	if err != nil || len(fields) != 1 || fields[0].Value != "p-apps/ams/postgres" {
		t.Errorf("ReadEntry = %v, %v", fields, err)
	}
}

func TestEmptyFolderIsNotAnError(t *testing.T) {
	fakeVault(t)
	entries, err := New(config.VaultSecretSpec{Name: "e", Path: "nothing/"}).ListEntries(context.Background(), nil)
	if err != nil || len(entries) != 0 {
		t.Errorf("entries = %v, err = %v", entries, err)
	}
}

func TestListStopsAtCap(t *testing.T) {
	fakeVault(t)
	entries, err := New(config.VaultSecretSpec{Name: "b", Path: "big/"}).ListEntries(context.Background(), nil)
	if len(entries) != maxEntries {
		t.Errorf("entries = %d, want the first %d", len(entries), maxEntries)
	}
	if err == nil || !strings.Contains(err.Error(), "stopped listing") {
		t.Errorf("err = %v, want a note that the list is incomplete", err)
	}
}

func TestPermissionDeniedSaysLogin(t *testing.T) {
	fakeVault(t)
	t.Setenv("VAULT_TOKEN", "bogus")
	_, err := New(config.VaultSecretSpec{Name: "g", Path: "grafana/admin"}).Read(context.Background())
	if err == nil || err.Error() != "vault: permission denied — token expired or no access (L to log in)" {
		t.Errorf("err = %v", err)
	}
}

func TestOtherErrorsOnOneLine(t *testing.T) {
	fakeVault(t)
	_, err := New(config.VaultSecretSpec{Name: "m", Path: "missing"}).Read(context.Background())
	if err == nil || err.Error() != "vault: No value found at secret/data/missing" {
		t.Errorf("err = %v", err)
	}
}

func TestProbe(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("VAULT_CONFIG_PATH", "")

	t.Setenv("PATH", t.TempDir())
	if st := New(config.VaultSecretSpec{Name: "x", Path: "p"}).Probe(); st.Missing != "vault" || !strings.HasPrefix(st.Reason, "vault not installed — ") {
		t.Errorf("no cli: %+v", st)
	}

	fakeVault(t)
	t.Setenv("VAULT_TOKEN", "")
	it := New(config.VaultSecretSpec{Name: "x", Path: "p"})
	if st := it.Probe(); st.Ready || st.Reason != "vault login required" {
		t.Errorf("no token: %+v", st)
	}
	withLogin := New(config.VaultSecretSpec{Name: "x", Path: "p", Login: "vault login -method=oidc"})
	if st := withLogin.Probe(); st.Reason != "vault login required — L to log in" {
		t.Errorf("no token, login set: %+v", st)
	}

	if err := os.WriteFile(filepath.Join(home, ".vault-token"), []byte("hvs.x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := it.Probe(); !st.Ready {
		t.Errorf("token file: %+v", st)
	}
	os.Remove(filepath.Join(home, ".vault-token"))

	t.Setenv("VAULT_TOKEN", "hvs.env")
	if st := it.Probe(); !st.Ready {
		t.Errorf("VAULT_TOKEN: %+v", st)
	}
	t.Setenv("VAULT_TOKEN", "")

	cfg := filepath.Join(home, "vault.hcl")
	if err := os.WriteFile(cfg, []byte(`token_helper = "/usr/local/bin/helper"`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VAULT_CONFIG_PATH", cfg)
	if st := it.Probe(); !st.Ready {
		t.Errorf("token helper: %+v", st)
	}
}

func TestLoginCmd(t *testing.T) {
	if New(config.VaultSecretSpec{Name: "x", Path: "p"}).LoginCmd() != nil {
		t.Error("no login configured must give no command")
	}
	cmd := New(config.VaultSecretSpec{Name: "x", Path: "p", Address: "https://v", Login: "vault login -method=oidc"}).LoginCmd()
	if got := strings.Join(cmd.Args, " "); got != "sh -c vault login -method=oidc" {
		t.Errorf("args = %q", got)
	}
	if !contains(cmd.Env, "VAULT_ADDR=https://v") {
		t.Error("login must run against the item's VAULT_ADDR")
	}
}

func contains(ss []string, s string) bool {
	return slices.Contains(ss, s)
}
