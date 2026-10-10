package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLockRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), LockName)
	at := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	want := Lock{Sources: map[string]Locked{
		"platform-ops": {URL: "git@ghe.acme.com:ops/den-runbooks.git", Ref: "v1.4.0", Commit: strings.Repeat("a", 40), Updated: at},
		"team":         {URL: "acme/team", Commit: strings.Repeat("b", 40), Updated: at},
	}}
	if err := WriteLock(path, want); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "# ") {
		t.Errorf("den.lock should say what it is and that den writes it:\n%s", data)
	}
	got, err := ReadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, w := range want.Sources {
		g := got.Sources[name]
		if g.URL != w.URL || g.Ref != w.Ref || g.Commit != w.Commit || !g.Updated.Equal(w.Updated) {
			t.Errorf("%s = %+v, want %+v", name, g, w)
		}
	}
}

func TestReadLockMissing(t *testing.T) {
	lock, err := ReadLock(filepath.Join(t.TempDir(), LockName))
	if err != nil || len(lock.Sources) != 0 {
		t.Fatalf("a missing den.lock = %+v, %v: want an empty lock", lock, err)
	}
	if lock.Sources == nil {
		t.Error("an empty lock should still take entries")
	}
}

func TestLockPathSitsBesideDenYaml(t *testing.T) {
	if LockPath("") != "" {
		t.Error("no den.yaml, no den.lock")
	}
	dir := t.TempDir()
	cfg := filepath.Join(dir, "den.yaml")
	if err := os.WriteFile(cfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "den.yaml")
	if err := os.Symlink(cfg, link); err != nil {
		t.Skip(err)
	}
	// Beside the file the link points at: the lock is committed next to it.
	if got := LockPath(link); got != filepath.Join(evalDir(t, dir), LockName) {
		t.Errorf("LockPath(link) = %s", got)
	}
}

func evalDir(t *testing.T, dir string) string {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// den.lock is committed and shared, so its commit ids are input like any
// other: one that is not a commit id would reach git as an option, or a
// checkout path outside the store.
func TestReadLockRefusesAnythingButCommitIDs(t *testing.T) {
	for _, commit := range []string{"--output=/tmp/pwned", "../../../tmp/x", "c0ffee", "main"} {
		path := filepath.Join(t.TempDir(), LockName)
		body := "sources:\n  ops:\n    url: acme/ops\n    commit: " + commit + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadLock(path); err == nil || !strings.Contains(err.Error(), "not a commit id") {
			t.Errorf("commit %q: err = %v", commit, err)
		}
	}
}
