package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sourceCLI is a den.yaml with an environment and a runbooks directory, a
// git repository holding runbook a, and the user's data and state
// directories in temp dirs, so nothing reaches the developer's own.
type sourceCLI struct {
	t       *testing.T
	cfg     string
	repo    string
	runbook string // runbooks.dirs[0]
}

func newSourceCLI(t *testing.T) *sourceCLI {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	dir := t.TempDir()
	aws := filepath.Join(dir, "aws-config")
	c := &sourceCLI{t: t, cfg: filepath.Join(dir, "den.yaml"), repo: filepath.Join(dir, "upstream"), runbook: filepath.Join(dir, "runbooks")}
	write(t, aws, "", 0o600)
	write(t, c.cfg, `# my den
aws:
  config_path: `+aws+`
  credentials_path: `+aws+`
environments:
  eu-prod:
    region: eu
    environment: prod
    ec2_instance_id: i-0123
    aws_profile: prod
    aws_region_code: eu-central-1
runbooks:
  dirs:
    - ./runbooks
`, 0o600)
	if err := os.MkdirAll(c.repo, 0o750); err != nil {
		t.Fatal(err)
	}
	c.git("init", "-q", "-b", "main")
	c.runbookIn("a", "description: Alpha\ncommand: ./run.sh\nenv: prod\n", "echo alpha")
	c.commit()
	return c
}

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func (c *sourceCLI) git(args ...string) {
	c.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = c.repo
	if out, err := cmd.CombinedOutput(); err != nil {
		c.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func (c *sourceCLI) runbookIn(folder, manifest, script string) {
	write(c.t, filepath.Join(c.repo, folder, "runbook.yaml"), manifest, 0o600)
	write(c.t, filepath.Join(c.repo, folder, "run.sh"), "#!/bin/sh\n"+script+"\n", 0o700)
}

func (c *sourceCLI) commit() {
	c.git("add", "-A")
	c.git("commit", "-q", "-m", "change")
}

func (c *sourceCLI) den(args ...string) (string, error) {
	c.t.Helper()
	return runDen(c.t, append(args, "-c", c.cfg)...)
}

func (c *sourceCLI) mustDen(args ...string) string {
	c.t.Helper()
	out, err := c.den(args...)
	if err != nil {
		c.t.Fatalf("den %v: %v\n%s", args, err, out)
	}
	return out
}

func (c *sourceCLI) denYAML() string {
	data, _ := os.ReadFile(c.cfg)
	return string(data)
}

// add adds the repository as source ops, with prod mapped.
func (c *sourceCLI) add() string {
	return c.mustDen("runbook", "source", "add", c.repo, "--name", "ops", "--env-map", "prod=eu-prod")
}

func TestRunbookSourceAddThenRun(t *testing.T) {
	c := newSourceCLI(t)
	out := c.add()
	for _, want := range []string{"added ops to den.yaml", "ops  checked out", "+ a", "ask before"} {
		if !strings.Contains(out, want) {
			t.Errorf("add output lacks %q:\n%s", want, out)
		}
	}
	if y := c.denYAML(); !strings.HasPrefix(y, "# my den\n") || !strings.Contains(y, "      env_map:\n        prod: eu-prod\n") {
		t.Fatalf("den.yaml:\n%s", y)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(c.cfg), "den.lock")); err != nil {
		t.Fatalf("den.lock: %v", err)
	}

	var entries []runbookEntry
	if err := json.Unmarshal([]byte(c.mustDen("run", "--list", "--json")), &entries); err != nil {
		t.Fatal(err)
	}
	var found *runbookEntry
	for i := range entries {
		if entries[i].Name == "ops/a" {
			found = &entries[i]
		}
	}
	if found == nil || found.From != "ops" || len(found.Commit) != 40 || !found.Confirm || !strings.Contains(found.Note, "new from ops") {
		t.Fatalf("ops/a in den run --list --json = %+v", found)
	}

	// Not a terminal: a runbook never run asks for --yes...
	if out, err := c.den("run", "ops/a"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("den run ops/a without --yes: %v\n%s", err, out)
	}
	// ...runs with it, and is trusted from then on.
	c.mustDen("run", "ops/a", "--yes")
	c.mustDen("run", "ops/a")
}

func TestRunbookSourceUpdatePrintsTheChanges(t *testing.T) {
	c := newSourceCLI(t)
	c.add()
	c.runbookIn("a", "description: Alpha\ncommand: ./run.sh\nenv: prod\n", "echo alpha, changed")
	c.runbookIn("b", "command: ./run.sh\n", "echo bravo")
	c.commit()

	out := c.mustDen("runbook", "source", "update")
	for _, want := range []string{"ops  ", " → ", "~ a", "+ b", "1 file changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("update output lacks %q:\n%s", want, out)
		}
	}
	if out := c.mustDen("runbook", "source", "update", "ops"); !strings.Contains(out, "up to date") {
		t.Errorf("a second update:\n%s", out)
	}
	if _, err := c.den("runbook", "source", "update", "nope"); err == nil {
		t.Error("updating an unknown source should fail")
	}
}

func TestRunbookSourceListAndBrowse(t *testing.T) {
	c := newSourceCLI(t)
	c.runbookIn("b", "name: Bravo\ncommand: ./run.sh\n", "echo b")
	c.commit()
	c.add()
	c.mustDen("runbook", "uninstall", "ops/Bravo")

	out := c.mustDen("runbook", "source", "list")
	if !strings.Contains(out, "ops") || !strings.Contains(out, "1 of 2 runbooks installed") {
		t.Errorf("source list:\n%s", out)
	}
	var sources []sourceEntry
	if err := json.Unmarshal([]byte(c.mustDen("runbook", "source", "list", "--json")), &sources); err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || len(sources[0].Commit) != 40 || len(sources[0].Runbooks) != 2 || !sources[0].Runbooks[0].Installed || sources[0].Runbooks[1].Installed {
		t.Fatalf("source list --json = %+v", sources)
	}

	browse := c.mustDen("runbook", "browse", "ops")
	if !strings.Contains(browse, "✓ a") || !strings.Contains(browse, "  b") || !strings.Contains(browse, "ops/Bravo") {
		t.Errorf("browse:\n%s", browse)
	}
}

func TestRunbookInstallAndUninstall(t *testing.T) {
	c := newSourceCLI(t)
	c.add()
	if out := c.mustDen("runbook", "uninstall", "ops/a"); !strings.Contains(out, "uninstalled ops/a") {
		t.Errorf("uninstall:\n%s", out)
	}
	if !strings.Contains(c.denYAML(), "      exclude:\n        - a\n") {
		t.Fatalf("den.yaml:\n%s", c.denYAML())
	}
	if out := c.mustDen("run", "--list"); strings.Contains(out, "ops/a") {
		t.Errorf("ops/a is still listed:\n%s", out)
	}
	c.mustDen("runbook", "install", "ops/a")
	if out := c.mustDen("run", "--list"); !strings.Contains(out, "ops/a") {
		t.Errorf("ops/a is not listed again:\n%s", out)
	}
	if _, err := c.den("runbook", "install", "a"); err == nil || !strings.Contains(err.Error(), "<source>/<runbook>") {
		t.Errorf("install without a source: %v", err)
	}
}

// Deleting a runbook from a source uninstalls it: its checkout is the
// source's, shared with the others.
func TestRunbookDeleteASourceRunbook(t *testing.T) {
	c := newSourceCLI(t)
	c.add()
	_, err := c.den("runbook", "delete", "ops/a")
	if err == nil || !strings.Contains(err.Error(), "uninstalled in den.yaml") || !strings.Contains(err.Error(), "den runbook source remove ops") {
		t.Fatalf("delete without --yes: %v", err)
	}
	c.mustDen("runbook", "delete", "ops/a", "--yes")
	if !strings.Contains(c.denYAML(), "      exclude:\n        - a\n") {
		t.Fatalf("den.yaml:\n%s", c.denYAML())
	}
	if out := c.mustDen("run", "--list"); strings.Contains(out, "ops/a") {
		t.Errorf("ops/a is still listed:\n%s", out)
	}
	if out := c.mustDen("runbook", "browse", "ops"); !strings.Contains(out, "a") {
		t.Errorf("the source lost its runbook:\n%s", out)
	}
}

func TestRunbookFork(t *testing.T) {
	c := newSourceCLI(t)
	c.add()
	out := c.mustDen("runbook", "fork", "ops/a")
	if !strings.Contains(out, "copied ops/a") || !strings.Contains(out, filepath.Join(c.runbook, "a")) {
		t.Errorf("fork:\n%s", out)
	}
	manifest, _ := os.ReadFile(filepath.Join(c.runbook, "a", "runbook.yaml"))
	if !strings.Contains(string(manifest), "env: eu-prod") {
		t.Errorf("the fork's runbook.yaml:\n%s", manifest)
	}
	if list := c.mustDen("run", "--list"); !strings.Contains(list, "  a ") && !strings.Contains(list, " a\n") {
		t.Errorf("the fork is not listed:\n%s", list)
	}
}

func TestRunbookSourceRemove(t *testing.T) {
	c := newSourceCLI(t)
	c.add()
	if _, err := c.den("runbook", "source", "remove", "ops"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("remove without --yes outside a terminal: %v", err)
	}
	c.mustDen("runbook", "source", "remove", "ops", "--yes")
	if strings.Contains(c.denYAML(), "sources:") {
		t.Errorf("den.yaml:\n%s", c.denYAML())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(c.cfg), "den.lock")); err == nil {
		t.Error("den.lock outlived its last source")
	}
}

func TestRunbookSourceSync(t *testing.T) {
	c := newSourceCLI(t)
	c.add()
	// Another machine: the same den.yaml and den.lock, nothing checked out.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if out := c.mustDen("run", "--list"); strings.Contains(out, "ops/a") {
		t.Fatalf("nothing should be checked out yet:\n%s", out)
	}
	out := c.mustDen("runbook", "source", "sync")
	if !strings.Contains(out, "ops  at ") {
		t.Errorf("sync:\n%s", out)
	}
	if list := c.mustDen("run", "--list"); !strings.Contains(list, "ops/a") {
		t.Errorf("after sync:\n%s", list)
	}
}

func TestDoctorChecksRunbookSources(t *testing.T) {
	c := newSourceCLI(t)
	c.add()
	out, _ := c.den("doctor") // other checks may fail on this machine; this one must be there
	if !strings.Contains(out, "runbook source ops") || !strings.Contains(out, "checked out at") {
		t.Errorf("den doctor:\n%s", out)
	}
}
