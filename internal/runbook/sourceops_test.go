package runbook

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook/source"
)

// gitRepo is a real repository of runbook folders, as a source's upstream.
type gitRepo struct {
	t   *testing.T
	dir string
}

func newGitRepo(t *testing.T) *gitRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	r := &gitRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	return r
}

func (r *gitRepo) git(args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = r.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// runbook writes a runbook folder whose script echoes say.
func (r *gitRepo) runbook(folder, manifest, say string) {
	r.t.Helper()
	dir := filepath.Join(r.dir, folder)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(manifest), 0o600); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\necho "+say+"\n"), 0o700); err != nil {
		r.t.Fatal(err)
	}
}

func (r *gitRepo) commit() {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "change")
}

// opsDen is a den.yaml on disk with environments, a runbooks directory and,
// when extra is set, more of the runbooks: block, and a registry over it that
// keeps sources in a temp store.
type opsDen struct {
	t       *testing.T
	cfgPath string
	mine    string // runbooks.dirs[0]
	reg     *Registry
	store   *source.Store
}

func newOpsDen(t *testing.T, extra string) *opsDen {
	t.Helper()
	dir := t.TempDir()
	d := &opsDen{t: t, cfgPath: filepath.Join(dir, "den.yaml"), mine: filepath.Join(dir, "mine"), store: source.NewStore(filepath.Join(dir, "data"))}
	body := `# my den
environments:
  eu-prod:
    region: eu
    environment: prod
    ec2_instance_id: i-0123
    aws_profile: prod
    aws_region_code: eu-central-1

runbooks:
  dirs:
    - ./mine
` + extra
	if err := os.WriteFile(d.cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(d.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if d.reg, err = NewRegistry(cfg, d.cfgPath, nil); err != nil {
		t.Fatal(err)
	}
	d.reg.SetSources(d.store, NewTrust(filepath.Join(dir, "trust.json")))
	return d
}

func (d *opsDen) denYAML() string {
	d.t.Helper()
	data, err := os.ReadFile(d.cfgPath)
	if err != nil {
		d.t.Fatal(err)
	}
	return string(data)
}

func (d *opsDen) lock() source.Lock {
	d.t.Helper()
	lock, err := source.ReadLock(source.LockPath(d.cfgPath))
	if err != nil {
		d.t.Fatal(err)
	}
	return lock
}

func opsCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestAddSourceFetchesAndLists(t *testing.T) {
	repo := newGitRepo(t)
	repo.runbook("a", "command: ./run.sh\nenv: prod\n", "a")
	repo.commit()
	d := newOpsDen(t, "")

	u, err := d.reg.AddSource(opsCtx(t), config.RunbookSource{Name: "ops", URL: repo.dir, EnvMap: map[string]string{"prod": "eu-prod"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if u.Commit == "" || len(u.Changes) != 1 || u.Changes[0].Kind != source.Added {
		t.Fatalf("update = %+v", u)
	}
	if y := d.denYAML(); !strings.HasPrefix(y, "# my den\n") || !strings.Contains(y, "  sources:\n    - name: ops\n") {
		t.Fatalf("den.yaml:\n%s", y)
	}
	if got := d.lock().Sources["ops"]; got.Commit != u.Commit || got.URL != repo.dir {
		t.Errorf("den.lock = %+v", got)
	}
	rb := lookupRunbook(t, d.reg, "ops/a")
	if rb.Env != "eu-prod" || !rb.Confirm {
		t.Errorf("ops/a env %q confirm %v", rb.Env, rb.Confirm)
	}
	if _, err := d.reg.AddSource(opsCtx(t), config.RunbookSource{Name: "ops", URL: repo.dir}, false); err == nil || !strings.Contains(err.Error(), `"ops" exists already`) {
		t.Errorf("adding a name twice: %v", err)
	}
}

func TestAddSourceOutsideTheAllowList(t *testing.T) {
	d := newOpsDen(t, "  allow_sources: [\"ghe.acme.com/ops/*\"]\n")
	before := d.denYAML()
	_, err := d.reg.AddSource(opsCtx(t), config.RunbookSource{Name: "x", URL: "github.com/someone/runbooks"}, false)
	if err == nil || !strings.Contains(err.Error(), "not in runbooks.allow_sources") {
		t.Fatalf("err = %v", err)
	}
	if d.denYAML() != before {
		t.Error("a refused source was written to den.yaml")
	}
}

func TestAddSourceThatCannotBeFetchedStaysInDenYaml(t *testing.T) {
	newGitRepo(t) // git config isolation
	d := newOpsDen(t, "")
	_, err := d.reg.AddSource(opsCtx(t), config.RunbookSource{Name: "gone", URL: filepath.Join(t.TempDir(), "missing")}, true)
	if err == nil || !strings.Contains(err.Error(), "den runbook source update gone") {
		t.Fatalf("err = %v, want the fetch error and how to retry", err)
	}
	if !strings.Contains(d.denYAML(), "name: gone") {
		t.Error("the source should stay in den.yaml for a later update")
	}
}

func TestUpdateSourceMovesTheLock(t *testing.T) {
	repo := newGitRepo(t)
	repo.runbook("a", "command: ./run.sh\n", "one")
	repo.commit()
	d := newOpsDen(t, "")
	first, err := d.reg.AddSource(opsCtx(t), config.RunbookSource{Name: "ops", URL: repo.dir}, true)
	if err != nil {
		t.Fatal(err)
	}
	repo.runbook("a", "command: ./run.sh\n", "two")
	repo.commit()

	u, err := d.reg.UpdateSource(opsCtx(t), "ops")
	if err != nil {
		t.Fatal(err)
	}
	if u.Previous != first.Commit || u.Commit == first.Commit || len(u.Changes) != 1 || u.Changes[0].Kind != source.Changed {
		t.Fatalf("update = %+v", u)
	}
	if d.lock().Sources["ops"].Commit != u.Commit {
		t.Error("den.lock still pins the old commit")
	}
	data, _ := os.ReadFile(filepath.Join(lookupRunbook(t, d.reg, "ops/a").Dir, "run.sh"))
	if !strings.Contains(string(data), "two") {
		t.Errorf("ops/a runs %q, want the new commit's script", data)
	}
	if _, err := d.reg.UpdateSource(opsCtx(t), "nope"); err == nil {
		t.Error("updating an unknown source should fail")
	}
}

func TestSyncSourcesChecksOutWhatTheLockPins(t *testing.T) {
	repo := newGitRepo(t)
	repo.runbook("a", "command: ./run.sh\n", "pinned")
	repo.commit()
	d := newOpsDen(t, "")
	if _, err := d.reg.AddSource(opsCtx(t), config.RunbookSource{Name: "ops", URL: repo.dir}, true); err != nil {
		t.Fatal(err)
	}
	repo.runbook("a", "command: ./run.sh\n", "newer")
	repo.commit()

	// Another machine: same den.yaml and den.lock, an empty store.
	cfg, _ := config.Load(d.cfgPath)
	other, _ := NewRegistry(cfg, d.cfgPath, nil)
	other.SetSources(source.NewStore(t.TempDir()), nil)
	if _, ok := other.Lookup("ops/a"); ok {
		t.Fatal("nothing is checked out on the other machine yet")
	}
	if err := other.SyncSources(opsCtx(t)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(lookupRunbook(t, other, "ops/a").Dir, "run.sh"))
	if !strings.Contains(string(data), "pinned") {
		t.Errorf("sync checked out %q, want the commit den.lock pins", data)
	}
}

func TestRemoveSourceForgetsIt(t *testing.T) {
	repo := newGitRepo(t)
	repo.runbook("a", "command: ./run.sh\n", "a")
	repo.commit()
	d := newOpsDen(t, "")
	u, err := d.reg.AddSource(opsCtx(t), config.RunbookSource{Name: "ops", URL: repo.dir}, true)
	if err != nil {
		t.Fatal(err)
	}
	src := config.RunbookSource{Name: "ops", URL: repo.dir}
	if err := d.reg.RemoveSource("ops"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.denYAML(), "sources:") {
		t.Errorf("den.yaml still has the source:\n%s", d.denYAML())
	}
	if _, ok := d.lock().Sources["ops"]; ok {
		t.Error("den.lock still has the source")
	}
	if d.store.Tree(src, u.Commit) != "" {
		t.Error("the checkout is still there")
	}
	if _, ok := d.reg.Lookup("ops/a"); ok {
		t.Error("ops/a is still listed")
	}
}

func TestSelectSourceRunbookEditsDenYaml(t *testing.T) {
	repo := newGitRepo(t)
	repo.runbook("a", "command: ./run.sh\n", "a")
	repo.runbook("b", "name: Bravo\ncommand: ./run.sh\n", "b")
	repo.commit()
	d := newOpsDen(t, "")
	if _, err := d.reg.AddSource(opsCtx(t), config.RunbookSource{Name: "ops", URL: repo.dir}, true); err != nil {
		t.Fatal(err)
	}

	if err := d.reg.SelectSourceRunbook("ops", "ops/Bravo", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.denYAML(), "      exclude:\n        - b\n") {
		t.Fatalf("den.yaml:\n%s", d.denYAML())
	}
	if got := names(d.reg); !slices.Equal(got, []string{"ops/a"}) {
		t.Fatalf("runbooks = %v", got)
	}
	if err := d.reg.SelectSourceRunbook("ops", "b", true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.denYAML(), "exclude") || !slices.Contains(names(d.reg), "ops/Bravo") {
		t.Fatalf("after installing b again: %v\n%s", names(d.reg), d.denYAML())
	}
	if err := d.reg.SelectSourceRunbook("ops", "zzz", true); err == nil || !strings.Contains(err.Error(), "den runbook browse ops") {
		t.Errorf("an unknown runbook: %v", err)
	}
}

func TestForkCopiesASourceRunbookIntoYourFolder(t *testing.T) {
	repo := newGitRepo(t)
	repo.runbook("rds-status", "description: RDS status\ncommand: ./run.sh\nenv: prod\n", "status")
	repo.commit()
	d := newOpsDen(t, "")
	if _, err := d.reg.AddSource(opsCtx(t), config.RunbookSource{Name: "ops", URL: repo.dir, EnvMap: map[string]string{"prod": "eu-prod"}}, true); err != nil {
		t.Fatal(err)
	}

	where := d.reg.ForkDir("ops/rds-status")
	name, err := d.reg.Fork("ops/rds-status", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(d.mine, "rds-status"); where != want {
		t.Errorf("ForkDir said %q before the copy, want %q, where it went", where, want)
	}
	if name != "rds-status" {
		t.Fatalf("fork is called %q", name)
	}
	rb := lookupRunbook(t, d.reg, "rds-status")
	if rb.Remote != nil || rb.Dir != filepath.Join(d.mine, "rds-status") {
		t.Fatalf("fork = %+v", rb)
	}
	if rb.Env != "eu-prod" {
		t.Errorf("the fork's env is %q: it should name your environment, not the source's", rb.Env)
	}
	if info, err := os.Stat(filepath.Join(rb.Dir, "run.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("run.sh = %v, %v: want it executable", info, err)
	}
	if _, err := d.reg.Fork("ops/rds-status", ""); err == nil || !strings.Contains(err.Error(), "exists already") {
		t.Errorf("forking twice: %v", err)
	}
	if _, err := d.reg.Fork("rds-status", ""); err == nil || !strings.Contains(err.Error(), "yours already") {
		t.Errorf("forking your own: %v", err)
	}
}

func TestSourcesState(t *testing.T) {
	repo := newGitRepo(t)
	repo.runbook("a", "command: ./run.sh\ndescription: Alpha\n", "a")
	repo.runbook("b", "command: ./run.sh\n", "b")
	repo.commit()
	d := newOpsDen(t, "")
	if _, err := d.reg.AddSource(opsCtx(t), config.RunbookSource{Name: "ops", URL: repo.dir, Exclude: []string{"b"}}, true); err != nil {
		t.Fatal(err)
	}
	states := d.reg.Sources()
	if len(states) != 1 {
		t.Fatalf("states = %+v", states)
	}
	st := states[0]
	if st.Locked == nil || !st.CheckedOut || st.Problem != "" || st.Repo == "" {
		t.Fatalf("state = %+v", st)
	}
	want := []SourceFolder{
		{Folder: "a", Name: "ops/a", Description: "Alpha", Selected: true, Listed: true},
		{Folder: "b", Name: "ops/b", Selected: false, Listed: false},
	}
	if !slices.Equal(st.Folders, want) {
		t.Errorf("folders = %+v\nwant %+v", st.Folders, want)
	}
}
