package pgpass

import (
	"os"
	"path/filepath"
	"testing"
)

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

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want 600 (libpq ignores a group/world-readable pgpass)", perm)
	}
}

func TestUpdateCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".pgpass")
	if err := update(path, 50110, "db", "user", "pw"); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "localhost:50110:db:user:pw\n" {
		t.Fatalf("pgpass = %q", got)
	}
}

func TestLineWithoutDatabaseUsesTheWildcard(t *testing.T) {
	if got, want := Line(50111, "", "app", "tok"), "localhost:50111:*:app:tok"; got != want {
		t.Errorf("Line = %q, want %q", got, want)
	}
}
