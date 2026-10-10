package runbook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
)

// The shape of the user's own old-format runbook: a header, a separator, a
// comment block with an indented list, then the code.
const headerScript = `#!/usr/bin/env bash
# den:name        Assume role (non-prod)
# den:description Assume the developer role
# den:env         dev
# den:timeout     2m
# den:cwd         data
# den:param       tier options=test,prod default=test Which tier
#
# Assumes the developer role and stores the credentials
# as a named AWS profile.
#
# Overrides, all optional:
#   DEN_SOURCE_PROFILE   profile used to call AssumeRole
#   DEN_DURATION         session length in seconds

set -euo pipefail
echo "ran in $(basename "$PWD") as $AWS_PROFILE tier=$DEN_PARAM_TIER"
`

func TestMigrateScriptMakesAFolderThatRunsTheSame(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "data"), 0o750); err != nil {
		t.Fatal(err)
	}
	script := writeScript(t, root, "assume-role.sh", headerScript, true)
	cfg := &config.Config{
		Environments: map[string]config.EnvSpec{"dev": {AWSProfile: "dev-profile"}},
		Runbooks:     &config.RunbooksConfig{Dirs: []string{root}},
	}
	reg, _ := NewRegistry(cfg, "", nil)
	h := NewHistory(t.TempDir())
	reg.SetHistory(h, func(err error) { t.Error(err) })
	before, _ := reg.Lookup("Assume role (non-prod)")
	before.Execute(context.Background(), reg.ExecOptionsFor(before.Runbook(), nil))

	m, err := reg.Migrate("Assume role (non-prod)")
	if err != nil {
		t.Fatal(err)
	}
	if m.Dir != filepath.Join(root, "assume-role") || m.Run != "run.sh" {
		t.Fatalf("migration = %+v, want folder assume-role with run.sh", m)
	}
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Fatal("the old script is still there")
	}

	run, _ := os.ReadFile(filepath.Join(m.Dir, "run.sh"))
	if strings.Contains(string(run), "den:") || !strings.HasPrefix(string(run), "#!/usr/bin/env bash\n") ||
		!strings.Contains(string(run), "# Assumes the developer role") {
		t.Fatalf("run.sh should be the script without its directives:\n%s", run)
	}
	if info, _ := os.Stat(filepath.Join(m.Dir, "run.sh")); info.Mode().Perm()&0o100 == 0 {
		t.Fatal("run.sh lost its executable bit")
	}
	readme, _ := os.ReadFile(filepath.Join(m.Dir, "README.md"))
	for _, want := range []string{"# Assume role (non-prod)", "Assumes the developer role and stores the credentials",
		"\n\n     DEN_SOURCE_PROFILE   profile used"} {
		if !strings.Contains(string(readme), want) {
			t.Errorf("README.md lacks %q:\n%s", want, readme)
		}
	}

	r, ok := reg.Lookup("Assume role (non-prod)")
	if !ok || r != before {
		t.Fatalf("the runbook did not keep its runner across the move: %v", reg.Warnings())
	}
	rb := r.Runbook()
	if rb.Dir != m.Dir || rb.Env != "dev" || rb.Timeout.String() != "2m0s" ||
		rb.Cwd != filepath.Join(root, "data") || rb.Command != "'../assume-role/run.sh'" || len(rb.Params) != 1 || rb.Params[0].Default != "test" {
		t.Fatalf("the folder runbook = %+v, want what the header said", rb)
	}
	st := r.Execute(context.Background(), reg.ExecOptionsFor(rb, nil))
	if st.Status != StatusSucceeded || !strings.Contains(strings.Join(st.Logs, "\n"), "ran in data as dev-profile tier=test") {
		t.Fatalf("the migrated runbook = %v %q", st.Status, st.Logs)
	}
	runs, _ := reg.Runs("Assume role (non-prod)")
	if len(runs) != 2 {
		t.Fatalf("history has %d runs, want the one from before the move and the one after", len(runs))
	}
	// The run from before moved into the folder's logs/ with the new one,
	// numbered in the order they ran.
	logs := filepath.Join(m.Dir, LogsFolder)
	if runs[0].LogPath() != filepath.Join(logs, "log-02.log") || runs[1].LogPath() != filepath.Join(logs, "log-01.log") {
		t.Errorf("runs kept in %q and %q, want log-02 and log-01 in %s", runs[0].LogPath(), runs[1].LogPath(), logs)
	}
	if old, _ := h.List("Assume role (non-prod)", ""); len(old) != 0 {
		t.Errorf("%d runs stayed in den's state directory", len(old))
	}
}

func TestMigrateRefusesWhatIsNotAHeaderScript(t *testing.T) {
	root := t.TempDir()
	writeFolder(t, root, "folder", "command: true\n", nil)
	writeScript(t, root, "busy.sh", "#!/bin/sh\n# den:name Busy\nsleep 2\n", true)
	writeScript(t, root, "taken.sh", "#!/bin/sh\n# den:name Taken\n", true)
	if err := os.Mkdir(filepath.Join(root, "taken"), 0o750); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Runbooks: &config.RunbooksConfig{
		Dirs:  []string{root},
		Items: []config.RunbookSpec{{Name: "Inline", Command: "true"}},
	}}
	reg, _ := NewRegistry(cfg, "", nil)
	busy, _ := reg.Lookup("Busy")
	if err := busy.Start(context.Background(), ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	defer func() { busy.Cancel(); waitIdle(t, busy) }()

	for name, want := range map[string]string{
		"folder": "already has its folder",
		"Inline": "defined in den.yaml",
		"Busy":   "is running",
		"Taken":  "already exists",
		"Ghost":  "no runbook",
	} {
		if _, err := reg.Migrate(name); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Migrate(%s) = %v, want an error saying %q", name, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "taken.sh")); err != nil {
		t.Fatal("a refused migration touched the script")
	}
	if got := len(reg.HeaderScripts()); got != 2 {
		t.Fatalf("HeaderScripts = %d, want busy and taken", got)
	}
}

// Without a den:cwd the runbook now runs inside its folder, by ./run<ext>.
func TestMigrateWithoutCwdRunsInTheFolder(t *testing.T) {
	root := t.TempDir()
	writeScript(t, root, "hello.py", "#!/usr/bin/env python3\n# den:name Hello\nprint('hi')\n", true)
	m, err := MigrateScript(filepath.Join(root, "hello.py"))
	if err != nil {
		t.Fatal(err)
	}
	specs, warns := Discover([]string{root}, "")
	if len(specs) != 1 || len(warns) != 0 {
		t.Fatalf("after the move: %+v, warnings %v", specs, warns)
	}
	if s := specs[0]; s.Name != "Hello" || s.Command != "./run.py" || s.Cwd != m.Dir || s.Dir != m.Dir {
		t.Fatalf("migrated spec = %+v, want ./run.py run in %s", s, m.Dir)
	}
	readme, _ := os.ReadFile(filepath.Join(m.Dir, "README.md"))
	if string(readme) != "# Hello\n" {
		t.Fatalf("README.md = %q", readme)
	}
}

// The last run a den restored at start is read from where the move took it,
// not from the file that is gone.
func TestMigrateTakesTheRestoredRunAlong(t *testing.T) {
	root := t.TempDir()
	writeScript(t, root, "job.sh", "#!/bin/sh\n# den:name Job\necho done\n", true)
	cfg := &config.Config{Runbooks: &config.RunbooksConfig{Dirs: []string{root}}}
	state := t.TempDir()
	reg, _ := NewRegistry(cfg, "", nil)
	reg.SetHistory(NewHistory(state), func(err error) { t.Error(err) })
	r, _ := reg.Lookup("Job")
	r.Execute(context.Background(), reg.ExecOptionsFor(r.Runbook(), nil))

	// As a den started afterwards sees it.
	again, _ := NewRegistry(cfg, "", nil)
	again.SetHistory(NewHistory(state), nil)
	m, err := again.Migrate("Job")
	if err != nil {
		t.Fatal(err)
	}
	job, _ := again.Lookup("Job")
	st := job.State()
	if st.Saved == nil || st.LogPath != filepath.Join(m.Dir, LogsFolder, "log-01.log") {
		t.Fatalf("restored run kept in %q (saved %v), want log-01.log in the folder's logs/", st.LogPath, st.Saved != nil)
	}
	if lines, err := again.History().Load(*st.Saved); err != nil || len(lines) != 1 || !strings.HasSuffix(lines[0], "done") {
		t.Fatalf("restored output = %q, %v", lines, err)
	}
}
