package runbook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
)

// trustFixture is a source with one runbook, ops/a, and a trust file.
func trustFixture(t *testing.T, manifest string) (*sourceFixture, *Trust) {
	t.Helper()
	f := newSourceFixture(t, config.RunbookSource{Name: "ops", URL: "acme/ops"})
	f.checkout(commitA, map[string]string{"a": manifest}, map[string]string{"a": "tree-1"})
	return f, NewTrust(filepath.Join(f.dir, "state", "runbook-trust.json"))
}

func (f *sourceFixture) trustedRegistry(trust *Trust) *Registry {
	f.t.Helper()
	reg, err := NewRegistry(f.cfg, f.cfgPath, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	reg.SetSources(f.store, trust)
	return reg
}

func lookupRunbook(t *testing.T, reg *Registry, name string) Runbook {
	t.Helper()
	r, ok := reg.Lookup(name)
	if !ok {
		t.Fatalf("%s is not listed: %s", name, warningsText(reg))
	}
	return r.Runbook()
}

func TestNewSourceRunbookAsksFirst(t *testing.T) {
	f, trust := trustFixture(t, "command: ./run.sh\n")
	rb := lookupRunbook(t, f.trustedRegistry(trust), "ops/a")
	if !rb.Confirm || rb.Remote.Trusted {
		t.Fatalf("confirm %v, trusted %v: a runbook never run asks first", rb.Confirm, rb.Remote.Trusted)
	}
	if !strings.Contains(rb.Remote.Note, "new from ops") {
		t.Errorf("note = %q", rb.Remote.Note)
	}
}

func TestRunningASourceRunbookTrustsIt(t *testing.T) {
	f, trust := trustFixture(t, "command: ./run.sh\n")
	reg := f.trustedRegistry(trust)
	r, _ := reg.Lookup("ops/a")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if run := r.Execute(ctx, reg.ExecOptionsFor(r.Runbook(), nil)); run.Status != StatusSucceeded {
		t.Fatalf("run = %+v", run)
	}
	rb := lookupRunbook(t, reg, "ops/a")
	if rb.Confirm || !rb.Remote.Trusted || rb.Remote.Note != "" {
		t.Fatalf("after a run: confirm %v, trusted %v, note %q: the same tree runs without asking", rb.Confirm, rb.Remote.Trusted, rb.Remote.Note)
	}
	if got := trust.All()["ops/a"]; got.Tree != "tree-1" || got.Commit != commitA {
		t.Errorf("trust = %+v", got)
	}
	// A new den reads the same trust.
	if rb := lookupRunbook(t, f.trustedRegistry(NewTrust(trust.path)), "ops/a"); rb.Confirm {
		t.Error("trust did not survive a restart")
	}
}

func TestChangedSourceRunbookAsksAgain(t *testing.T) {
	f, trust := trustFixture(t, "command: ./run.sh\n")
	if err := trust.Add("ops/a", "tree-0", "abc1234def"); err != nil {
		t.Fatal(err)
	}
	rb := lookupRunbook(t, f.trustedRegistry(trust), "ops/a")
	if !rb.Confirm || !strings.Contains(rb.Remote.Note, "changed in ops since you ran it at abc1234") {
		t.Fatalf("confirm %v, note %q", rb.Confirm, rb.Remote.Note)
	}
}

// A source runbook edited in its checkout asks again, however it was trusted
// before, until the user runs it.
func TestEditedSourceRunbookAsksUntilRun(t *testing.T) {
	f, trust := trustFixture(t, "command: ./run.sh\n")
	if err := trust.Add("ops/a", "tree-1", commitA); err != nil {
		t.Fatal(err)
	}
	reg := f.trustedRegistry(trust)
	if err := reg.MarkEdited("ops/a"); err != nil {
		t.Fatal(err)
	}
	reg.Rescan()
	rb := lookupRunbook(t, reg, "ops/a")
	if !rb.Confirm || rb.Remote.Trusted || !strings.Contains(rb.Remote.Note, "edited here") {
		t.Fatalf("confirm %v, trusted %v, note %q: an edited checkout asks first", rb.Confirm, rb.Remote.Trusted, rb.Remote.Note)
	}
	r, _ := reg.Lookup("ops/a")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if run := r.Execute(ctx, reg.ExecOptionsFor(r.Runbook(), nil)); run.Status != StatusSucceeded {
		t.Fatalf("run = %+v", run)
	}
	if rb := lookupRunbook(t, reg, "ops/a"); rb.Confirm || !rb.Remote.Trusted {
		t.Fatalf("after a run: confirm %v, trusted %v: the edit was said yes to", rb.Confirm, rb.Remote.Trusted)
	}
	if got := trust.All()["ops/a"]; got.Edited {
		t.Errorf("trust = %+v: running it clears the mark", got)
	}
}

func TestMarkEditedLeavesOwnRunbooksAlone(t *testing.T) {
	f, trust := trustFixture(t, "command: ./run.sh\n")
	f.cfg.Runbooks.Items = []config.RunbookSpec{{Name: "mine", Command: "true"}}
	reg := f.trustedRegistry(trust)
	lookupRunbook(t, reg, "mine")
	for _, name := range []string{"mine", "nope"} {
		if err := reg.MarkEdited(name); err != nil {
			t.Fatal(err)
		}
	}
	if len(trust.All()) != 0 {
		t.Errorf("trust = %v: only a source runbook is marked", trust.All())
	}
}

func TestTrustKeepsTheRunbooksOwnConfirm(t *testing.T) {
	f, trust := trustFixture(t, "command: ./run.sh\nconfirm: true\n")
	if err := trust.Add("ops/a", "tree-1", commitA); err != nil {
		t.Fatal(err)
	}
	if rb := lookupRunbook(t, f.trustedRegistry(trust), "ops/a"); !rb.Confirm || !rb.Remote.Trusted {
		t.Fatalf("confirm %v, trusted %v: confirm: true asks every time", rb.Confirm, rb.Remote.Trusted)
	}
}

func TestWithoutTrustASourceRunbookAlwaysAsks(t *testing.T) {
	f, _ := trustFixture(t, "command: ./run.sh\n")
	reg := f.trustedRegistry(nil)
	r, _ := reg.Lookup("ops/a")
	r.Execute(context.Background(), reg.ExecOptionsFor(r.Runbook(), nil))
	if rb := lookupRunbook(t, reg, "ops/a"); !rb.Confirm {
		t.Error("with nowhere to remember it, a source runbook asks every time")
	}
}

func TestSequenceWithASourceStepAsks(t *testing.T) {
	f, trust := trustFixture(t, "command: ./run.sh\n")
	f.cfg.Runbooks.Sequences = []config.SequenceSpec{{Name: "seq", Steps: []string{"ops/a"}}}
	seq, ok := f.trustedRegistry(trust).LookupSequence("seq")
	if !ok || !seq.NeedsConfirm() {
		t.Fatal("a sequence running a new source runbook asks first")
	}
}

func TestTrustFileIsPrivate(t *testing.T) {
	trust := NewTrust(filepath.Join(t.TempDir(), "state", "runbook-trust.json"))
	if err := trust.Add("ops/a", "t", "c"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(trust.path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("trust file = %v, %v: want 0600", info, err)
	}
	if NewTrust("") != nil || (*Trust)(nil).All() != nil {
		t.Error("an empty path keeps nothing")
	}
}

func TestRunbookStateDir(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Runbooks: &config.RunbooksConfig{Items: []config.RunbookSpec{{Name: "Hello", Command: "echo hi"}}}}
	reg, err := NewRegistry(cfg, filepath.Join(dir, "den.yaml"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rb := lookupRunbook(t, reg, "Hello"); rb.StateDir != "" || reg.ContextVars(rb)["DEN_RUNBOOK_STATE"] != "" {
		t.Fatal("without a state root there is no DEN_RUNBOOK_STATE")
	}

	root := filepath.Join(dir, "runbook-state")
	reg.SetStateRoot(root)
	rb := lookupRunbook(t, reg, "Hello")
	if !strings.HasPrefix(rb.StateDir, root+string(filepath.Separator)+"hello-") {
		t.Fatalf("state dir = %q, want a folder named after it in %s", rb.StateDir, root)
	}
	if got := reg.ContextVars(rb)["DEN_RUNBOOK_STATE"]; got != rb.StateDir {
		t.Errorf("DEN_RUNBOOK_STATE = %q", got)
	}
	if _, err := os.Stat(rb.StateDir); err == nil {
		t.Error("the folder is made for a run, not for a scan")
	}
	reg.ExecOptionsFor(rb, nil)
	if info, err := os.Stat(rb.StateDir); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("state dir after ExecOptionsFor = %v, %v: want a private folder", info, err)
	}
}
