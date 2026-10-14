package pgpass

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPathIsWhereLibpqLooks(t *testing.T) {
	home := func() (string, error) { return "/home/ana", nil }
	noHome := func() (string, error) { return "", errors.New("no home") }
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	cases := []struct {
		name string
		goos string
		env  map[string]string
		home func() (string, error)
		want string
	}{
		{"PGPASSFILE on linux", "linux", map[string]string{"PGPASSFILE": "/etc/pg/pass"}, home, "/etc/pg/pass"},
		{"PGPASSFILE on windows", "windows", map[string]string{"PGPASSFILE": `D:\pg\pass`, "APPDATA": `C:\Users\ana\AppData\Roaming`}, home, `D:\pg\pass`},
		{"windows with APPDATA", "windows", map[string]string{"APPDATA": `C:\Users\ana\AppData\Roaming`}, home, filepath.Join(`C:\Users\ana\AppData\Roaming`, "postgresql", "pgpass.conf")},
		{"windows without APPDATA", "windows", nil, home, filepath.Join("/home/ana", "AppData", "Roaming", "postgresql", "pgpass.conf")},
		{"darwin", "darwin", map[string]string{"APPDATA": "/ignored"}, home, filepath.Join("/home/ana", ".pgpass")},
		{"linux", "linux", nil, home, filepath.Join("/home/ana", ".pgpass")},
		{"no home", "linux", nil, noHome, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := path(c.goos, env(c.env), c.home); got != c.want {
				t.Errorf("path = %q, want %q", got, c.want)
			}
		})
	}
}

func TestLineEscapesEveryField(t *testing.T) {
	got := Line(55439, "dev", "IAMR:admin", `to:k\en`)
	want := `localhost:55439:dev:IAMR\:admin:to\:k\\en`
	if got != want {
		t.Fatalf("Line() = %q, want %q", got, want)
	}
}

func TestUpdateReplacesEntryForSamePort(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".pgpass")
	seed := "otherhost:5432:db:user:secret\nlocalhost:50110:old_db:old_user:stale\nlocalhost:50111:db:user:keep\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := update(path, 50110, "orders", "app", "fresh"); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "otherhost:5432:db:user:secret\nlocalhost:50111:db:user:keep\nlocalhost:50110:orders:app:fresh\n"
	if string(got) != want {
		t.Fatalf("pgpass =\n%s\nwant\n%s", got, want)
	}

	if runtime.GOOS == "windows" {
		return // Perm reports 0666 there, and libpq checks no mode on Windows
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want 600 (libpq ignores a group/world-readable pgpass)", perm)
	}
}

func TestUpdateCreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	// The Windows location sits in a folder that is often not there yet.
	for _, path := range []string{filepath.Join(dir, ".pgpass"), filepath.Join(dir, "postgresql", "pgpass.conf")} {
		if err := update(path, 50110, "db", "user", "pw"); err != nil {
			t.Fatalf("update %s: %v", path, err)
		}
		got, _ := os.ReadFile(path)
		if string(got) != "localhost:50110:db:user:pw\n" {
			t.Fatalf("%s = %q", path, got)
		}
	}
}

func TestLineWithoutDatabaseUsesTheWildcard(t *testing.T) {
	if got, want := Line(50111, "", "app", "tok"), "localhost:50111:*:app:tok"; got != want {
		t.Errorf("Line = %q, want %q", got, want)
	}
}
