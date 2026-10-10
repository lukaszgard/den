package runbook

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
)

// deleteDen is an opsDen that keeps runs and state folders, as the TUI's
// registry does, all inside the test's own directory.
type deleteDen struct {
	*opsDen
	history *History
	state   string
}

func newDeleteDen(t *testing.T, extra string) *deleteDen {
	t.Helper()
	d := &deleteDen{opsDen: newOpsDen(t, extra)}
	root := filepath.Dir(d.cfgPath)
	d.history = NewHistory(filepath.Join(root, "state"))
	d.state = filepath.Join(root, "runbook-state")
	d.reg.SetHistory(d.history, func(err error) { t.Error(err) })
	d.reg.SetStateRoot(d.state)
	return d
}

// run runs name to its end, keeping the run, and gives it a state folder
// with something in it, as a script that keeps a cache would.
func (d *deleteDen) run(name string) {
	d.t.Helper()
	r, ok := d.reg.Lookup(name)
	if !ok {
		d.t.Fatalf("%s is not listed: %s", name, warningsText(d.reg))
	}
	opts := d.reg.ExecOptionsFor(r.Runbook(), nil)
	if got := r.Execute(context.Background(), opts); got.Status != StatusSucceeded {
		d.t.Fatalf("%s: %v %v", name, got.Status, got.Err)
	}
	if err := os.WriteFile(filepath.Join(r.Runbook().StateDir, "cache"), []byte("cached"), 0o600); err != nil {
		d.t.Fatal(err)
	}
}

func gone(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s is still there (%v)", p, err)
		}
	}
}

const deleteSequences = `  sequences:
    - name: both
      steps: [deploy, Hello]
  items:
    # Hello stays.
    - name: Hello
      command: echo hello
`

func TestDeleteAFolderRunbook(t *testing.T) {
	d := newDeleteDen(t, deleteSequences)
	folder := writeFolder(t, d.mine, "deploy", "command: ./run.sh\n",
		map[string]string{"run.sh": "#!/bin/sh\necho deployed\n", "README.md": "# deploy\n"})
	d.reg.Rescan()
	d.run("deploy")
	stateDir := lookupRunbook(t, d.reg, "deploy").StateDir

	plan, err := d.reg.PlanDelete("deploy")
	if err != nil {
		t.Fatal(err)
	}
	// runbook.yaml, run.sh, README.md, logs/.gitignore, log-01.log, log-01.json
	if plan.Kind != DeleteFolder || plan.Files != 6 || plan.Runs != 1 || plan.State == "" || plan.StateFiles != 1 {
		t.Errorf("plan = %+v", plan)
	}
	if !slices.Equal(plan.UsedBy, []string{"both"}) {
		t.Errorf("used by %v, want the sequence both", plan.UsedBy)
	}
	said := strings.Join(plan.Gone(), "\n")
	for _, want := range []string{"deploy/ — 6 files", "1 run, with its log", "its state folder"} {
		if !strings.Contains(said, want) {
			t.Errorf("Gone() says\n%s\nwant %q", said, want)
		}
	}
	if notes := strings.Join(plan.Notes(), "\n"); !strings.Contains(notes, "sequence both has it as a step") || !strings.Contains(notes, "nothing can bring it back") {
		t.Errorf("Notes() = %s", notes)
	}

	if _, err := d.reg.Delete("deploy"); err != nil {
		t.Fatal(err)
	}
	gone(t, folder, stateDir)
	if left, _ := os.ReadDir(d.mine); len(left) != 0 {
		t.Errorf("%s still holds %v", d.mine, left)
	}
	if _, ok := d.reg.Lookup("deploy"); ok {
		t.Error("deploy is still listed")
	}
	if _, ok := d.reg.LookupSequence("both"); ok || !strings.Contains(warningsText(d.reg), `unknown step "deploy"`) {
		t.Errorf("the sequence should stop loading, saying why:\n%s", warningsText(d.reg))
	}
	if !strings.Contains(d.denYAML(), "- name: both") {
		t.Error("deleting a step must not edit the sequence")
	}

	// A runbook given the name again starts afresh.
	if _, err := d.reg.NewRunbook("deploy", ""); err != nil {
		t.Fatal(err)
	}
	r, _ := d.reg.Lookup("deploy")
	if runs, _ := d.reg.Runs("deploy"); len(runs) != 0 || r.Tally().Runs != 0 || r.State().Status != StatusNeverRun {
		t.Errorf("the new deploy inherited %d runs, tally %+v, status %v", len(runs), r.Tally(), r.State().Status)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Error("the new deploy inherited the old one's state folder")
	}
}

func TestDeleteADenYamlRunbookAndSequence(t *testing.T) {
	d := newDeleteDen(t, deleteSequences)
	writeFolder(t, d.mine, "deploy", "command: echo deployed\n", nil)
	d.reg.Rescan()
	d.run("Hello")
	historyDir := d.history.dir("Hello", "")
	stateDir := lookupRunbook(t, d.reg, "Hello").StateDir

	if _, err := d.reg.Delete("both"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.denYAML(), "sequences:") {
		t.Errorf("den.yaml still has the sequence:\n%s", d.denYAML())
	}
	plan, err := d.reg.Delete("Hello")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != DeleteItem || plan.Runs != 1 || plan.History != historyDir {
		t.Errorf("plan = %+v", plan)
	}
	yaml := d.denYAML()
	if strings.Contains(yaml, "Hello") || strings.Contains(yaml, "items:") || !strings.Contains(yaml, "# my den") {
		t.Errorf("den.yaml after deleting Hello:\n%s", yaml)
	}
	gone(t, historyDir, stateDir)
	if _, ok := d.reg.Lookup("Hello"); ok {
		t.Error("Hello is still listed")
	}
	if _, ok := d.reg.Lookup("deploy"); !ok {
		t.Error("deploy should stay")
	}
}

// A sequence that stopped loading, its step deleted, is still den.yaml's to
// delete.
func TestDeleteASequenceDenSkipped(t *testing.T) {
	d := newDeleteDen(t, "  sequences:\n    - name: both\n      steps: [gone]\n")
	if _, ok := d.reg.LookupSequence("both"); ok {
		t.Fatal("both should be skipped")
	}
	if _, err := d.reg.Delete("both"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.denYAML(), "both") {
		t.Errorf("den.yaml:\n%s", d.denYAML())
	}
	if _, err := d.reg.Delete("both"); err == nil || !strings.Contains(err.Error(), `no runbook or sequence named "both"`) {
		t.Errorf("deleting it twice: %v", err)
	}
}

func TestDeleteAHeaderScriptAndRevealWhatItHid(t *testing.T) {
	d := newDeleteDen(t, "")
	if err := os.MkdirAll(d.mine, 0o750); err != nil {
		t.Fatal(err)
	}
	script := writeScript(t, d.mine, "old.sh", "#!/bin/sh\n# den:name report\necho old\n", true)
	writeFolder(t, d.mine, "report", "command: echo new\n", nil)
	writeFolder(t, d.mine, "other", "name: report\ncommand: echo other\n", nil)
	d.reg.Rescan()
	if rb := lookupRunbook(t, d.reg, "report"); rb.Dir == "" {
		t.Fatalf("a folder wins over a script: %+v", rb)
	}
	plan, err := d.reg.PlanDelete("report")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Shadowed) != 2 || !strings.Contains(strings.Join(plan.Notes(), "\n"), "it takes its place") {
		t.Errorf("shadowed = %v, notes = %v", plan.Shadowed, plan.Notes())
	}
	if _, err := d.reg.Delete("report"); err != nil {
		t.Fatal(err)
	}
	// The one den skipped first now has the name, and runs: its runner is not
	// the retired one.
	r, ok := d.reg.Lookup("report")
	if !ok || r.Runbook().Dir == "" {
		t.Fatalf("report after the delete: %v %+v", ok, r)
	}
	if got := r.Execute(context.Background(), d.reg.ExecOptionsFor(r.Runbook(), nil)); got.Status != StatusSucceeded {
		t.Fatalf("the runbook that took the name does not run: %v %v", got.Status, got.Err)
	}
	if _, err := d.reg.Delete("report"); err != nil {
		t.Fatal(err)
	}
	if rb := lookupRunbook(t, d.reg, "report"); rb.Source != script || rb.Dir != "" {
		t.Fatalf("the script should be report now: %+v", rb)
	}
	if plan, _ := d.reg.PlanDelete("report"); plan.Kind != DeleteScript {
		t.Errorf("kind = %v", plan.Kind)
	}
	if _, err := d.reg.Delete("report"); err != nil {
		t.Fatal(err)
	}
	gone(t, script)
	if left, _ := os.ReadDir(d.mine); len(left) != 0 {
		t.Errorf("%s still holds %v", d.mine, left)
	}
}

func TestDeleteRefusesWhatIsRunning(t *testing.T) {
	d := newDeleteDen(t, "  sequences:\n    - name: slow-then-deploy\n      steps: [slow, deploy]\n")
	writeFolder(t, d.mine, "slow", "command: sleep 5\n", nil)
	writeFolder(t, d.mine, "deploy", "command: echo deployed\n", nil)
	d.reg.Rescan()
	slow, _ := d.reg.Lookup("slow")
	if err := slow.Start(context.Background(), d.reg.ExecOptionsFor(slow.Runbook(), nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.reg.Delete("slow"); err == nil || !strings.Contains(err.Error(), "slow is running") {
		t.Errorf("deleting a running runbook: %v", err)
	}
	slow.Cancel()
	waitFor(t, "slow to stop", func() bool { return !slow.IsRunning() })

	seq, _ := d.reg.LookupSequence("slow-then-deploy")
	if err := seq.Start(context.Background(), SequenceOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.reg.Delete("deploy"); err == nil || !strings.Contains(err.Error(), "sequence slow-then-deploy is running") {
		t.Errorf("deleting a step of a running sequence: %v", err)
	}
	if _, err := d.reg.Delete("slow-then-deploy"); err == nil || !strings.Contains(err.Error(), "is running") {
		t.Errorf("deleting a running sequence: %v", err)
	}
	seq.Cancel()
	waitFor(t, "the sequence to stop", func() bool { return !seq.IsRunning() })

	// A run another den is writing.
	deploy := lookupRunbook(t, d.reg, "deploy")
	rec, err := d.history.Begin("deploy", deploy.LogsDir(), 20, Entry{StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.reg.Delete("deploy"); err == nil || !strings.Contains(err.Error(), "running in another den") {
		t.Errorf("deleting what another den runs: %v", err)
	}
	if err := rec.Finish(Entry{Status: StatusSucceeded.String(), FinishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.reg.Delete("deploy"); err != nil {
		t.Errorf("once it is over: %v", err)
	}
}

// Nothing starts a runbook while it is being deleted, and a sequence going
// through it fails there rather than reading its last run as this one.
func TestARetiredRunbookDoesNotStart(t *testing.T) {
	d := newDeleteDen(t, "")
	writeFolder(t, d.mine, "deploy", "command: echo deployed\n", nil)
	d.reg.Rescan()
	d.run("deploy")
	r, _ := d.reg.Lookup("deploy")
	if err := r.retire(); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(context.Background(), d.reg.ExecOptionsFor(r.Runbook(), nil)); err == nil || !strings.Contains(err.Error(), "being deleted") {
		t.Errorf("Start: %v", err)
	}
	if got := r.Execute(context.Background(), d.reg.ExecOptionsFor(r.Runbook(), nil)); got.Status != StatusFailed || got.Err == nil {
		t.Errorf("Execute = %v %v, want a failure", got.Status, got.Err)
	}
	r.unretire()
	if err := r.Start(context.Background(), d.reg.ExecOptionsFor(r.Runbook(), nil)); err != nil {
		t.Errorf("after unretire: %v", err)
	}
	waitFor(t, "the run to end", func() bool { return !r.IsRunning() })
}

// A folder that holds den.yaml, or a runbooks directory, is not den's to
// delete, whatever runbook.yaml it has.
func TestDeleteRefusesAFolderHoldingDenYamlOrARunbooksDir(t *testing.T) {
	for _, tc := range []struct {
		name, dirs, refusal string
	}{
		{"home", "[..]", "holds den.yaml"},
		{"team", "[., ./team]", "holds the runbooks directory"},
	} {
		root := t.TempDir()
		folder := writeFolder(t, root, tc.name, "command: echo\n", nil)
		cfgPath := filepath.Join(root, "den.yaml")
		if tc.name == "home" {
			cfgPath = filepath.Join(folder, "den.yaml")
		}
		if err := os.WriteFile(cfgPath, []byte("runbooks:\n  dirs: "+tc.dirs+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		reg, err := NewRegistry(cfg, cfgPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reg.Delete(tc.name); err == nil || !strings.Contains(err.Error(), tc.refusal) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.refusal)
		}
		if _, err := os.Stat(filepath.Join(folder, ManifestName)); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

func TestDeleteAFolderThatStopsHalfway(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory its owner cannot delete from")
	}
	d := newDeleteDen(t, "")
	folder := writeFolder(t, d.mine, "stuck", "command: echo\n", nil)
	locked := filepath.Join(folder, "locked")
	if err := os.MkdirAll(locked, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		matches, _ := filepath.Glob(filepath.Join(d.mine, ".stuck.deleting*", "locked"))
		for _, m := range matches {
			_ = os.Chmod(m, 0o750)
		}
	})
	d.reg.Rescan()
	_, err := d.reg.Delete("stuck")
	if err == nil || !strings.Contains(err.Error(), ".stuck.deleting") {
		t.Fatalf("err = %v, want it to name what is left", err)
	}
	if _, ok := d.reg.Lookup("stuck"); ok {
		t.Error("stuck is still listed")
	}
}

func TestDeleteASourceRunbook(t *testing.T) {
	repo := newGitRepo(t)
	repo.runbook("a", "command: ./run.sh\n", "a")
	repo.runbook("b", "command: ./run.sh\n", "b")
	repo.commit()
	d := newDeleteDen(t, "")
	if _, err := d.reg.AddSource(opsCtx(t), config.RunbookSource{Name: "ops", URL: repo.dir}, true); err != nil {
		t.Fatal(err)
	}
	d.run("ops/a")
	if !d.reg.trust.Has("ops/a") {
		t.Fatal("running it should remember the review")
	}
	checkout := lookupRunbook(t, d.reg, "ops/a").Dir

	plan, err := d.reg.Delete("ops/a")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != DeleteSource || !plan.Trusted || plan.Runs != 1 {
		t.Errorf("plan = %+v", plan)
	}
	if !strings.Contains(d.denYAML(), "      exclude:\n        - a\n") {
		t.Errorf("den.yaml:\n%s", d.denYAML())
	}
	if d.reg.trust.Has("ops/a") {
		t.Error("the review is still remembered")
	}
	if _, err := os.Stat(checkout); err != nil {
		t.Errorf("the shared checkout went: %v", err)
	}
	if got := names(d.reg); !slices.Equal(got, []string{"ops/b"}) {
		t.Errorf("runbooks = %v", got)
	}
}

// Removing a source forgets its runbooks as deleting each would.
func TestRemoveSourceForgetsItsRunbooks(t *testing.T) {
	repo := newGitRepo(t)
	repo.runbook("a", "command: ./run.sh\n", "a")
	repo.commit()
	d := newDeleteDen(t, "")
	if _, err := d.reg.AddSource(opsCtx(t), config.RunbookSource{Name: "ops", URL: repo.dir}, true); err != nil {
		t.Fatal(err)
	}
	d.run("ops/a")
	stateDir := lookupRunbook(t, d.reg, "ops/a").StateDir
	if err := d.reg.RemoveSource("ops"); err != nil {
		t.Fatal(err)
	}
	gone(t, d.history.dir("ops/a", ""), stateDir)
	if d.reg.trust.Has("ops/a") {
		t.Error("the review is still remembered")
	}
}
