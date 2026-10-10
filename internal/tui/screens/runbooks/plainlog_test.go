package runbooks

import (
	"os"
	"path/filepath"
	"testing"
)

// The log on disk keeps the colours of the run, for History; the editor gets
// them as text.
func TestPlainLogOpensACopyWithoutColourCodes(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log-07.log")
	const coloured = "[10:51:11] \x1b[32m✔\x1b[0m eu-central-1  app-int\n[10:51:12] plain\n"
	if err := os.WriteFile(log, []byte(coloured), 0o600); err != nil {
		t.Fatal(err)
	}

	file, scratch, err := plainLog(log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(scratch) })
	if scratch == "" || file == log || filepath.Base(file) != "log-07.log" {
		t.Fatalf("file = %q, scratch = %q, want a copy named log-07.log in a directory of its own", file, scratch)
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != "[10:51:11] ✔ eu-central-1  app-int\n[10:51:12] plain\n" {
		t.Errorf("copy = %q, %v, want the log without its colour codes", got, err)
	}
	if kept, _ := os.ReadFile(log); string(kept) != coloured {
		t.Errorf("the log on disk changed: %q", kept)
	}
}

func TestPlainLogOpensALogWithoutColoursAsItIs(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log-01.log")
	if err := os.WriteFile(log, []byte("[10:00:00] plain\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, scratch, err := plainLog(log)
	if err != nil || file != log || scratch != "" {
		t.Errorf("plainLog = %q, %q, %v, want the log itself and no copy", file, scratch, err)
	}
	if _, _, err := plainLog(filepath.Join(t.TempDir(), "gone.log")); err == nil {
		t.Error("a missing log must be an error")
	}
}
