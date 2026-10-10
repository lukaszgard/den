package runbook

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
)

// writeFolder makes dir/name a runbook folder: its runbook.yaml and any other
// files, by name.
func writeFolder(t *testing.T, dir, name, manifest string, files map[string]string) string {
	t.Helper()
	folder := filepath.Join(dir, name)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(folder, ManifestName), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for f, body := range files {
		if err := os.WriteFile(filepath.Join(folder, f), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return folder
}

func TestDiscoverFolders(t *testing.T) {
	root := t.TempDir()
	named := writeFolder(t, root, "assume-role", "name: Assume role\ncommand: ./run.sh\ntimeout: 2m\n",
		map[string]string{"run.sh": "#!/bin/sh\necho hi\n"})
	plain := writeFolder(t, root, "snapshot", "command: python3 snapshot.py\ncwd: data\n", nil)
	writeFolder(t, root, "lib", "", map[string]string{"common.sh": "#!/bin/sh\n"})
	writeFolder(t, root, ".hidden", "command: echo no\n", nil)
	writeFolder(t, root, "typo", "command: ./run.sh\nconfrim: true\n", nil)
	writeScript(t, root, "loose.sh", "#!/bin/sh\n# den:name Loose\n", true)

	specs, warnings := Discover([]string{root}, "")
	var names []string
	for _, s := range specs {
		names = append(names, s.Name)
	}
	// Folders first, so one wins a name collision with a loose script.
	if got := strings.Join(names, ","); got != "Assume role,snapshot,Loose" {
		t.Fatalf("runbooks = %s, want Assume role,snapshot,Loose", got)
	}
	if s := specs[0]; s.Dir != named || s.Cwd != named || s.Source != filepath.Join(named, ManifestName) || s.Timeout != "2m" {
		t.Errorf("assume-role = %+v, want its folder as dir and cwd", s)
	}
	if s := specs[1]; s.Cwd != filepath.Join(plain, "data") {
		t.Errorf("snapshot cwd = %q, want data/ inside its folder", s.Cwd)
	}
	// A misspelt key must not silently drop what it was meant to set.
	if len(warnings) != 1 || !strings.Contains(warnings[0].String(), "confrim") || !strings.Contains(warnings[0].String(), "skipped") {
		t.Fatalf("warnings = %v, want one naming the unknown key", warnings)
	}
}

func TestFolderRunbookIsValidatedLikeAnItem(t *testing.T) {
	root := t.TempDir()
	writeFolder(t, root, "no-command", "description: forgot it\n", nil)
	writeFolder(t, root, "bad-env", "command: true\nenv: nowhere\n", nil)
	writeFolder(t, root, "ok", "command: true\nenv: dev\n", nil)

	cfg := &config.Config{
		Environments: map[string]config.EnvSpec{"dev": {AWSProfile: "dev"}},
		Runbooks:     &config.RunbooksConfig{Dirs: []string{root}},
	}
	reg, _ := NewRegistry(cfg, "", nil)
	if _, ok := reg.Lookup("ok"); !ok {
		t.Fatal("the valid folder runbook is missing")
	}
	for _, name := range []string{"no-command", "bad-env"} {
		if _, ok := reg.Lookup(name); ok {
			t.Errorf("%s was loaded, want it skipped", name)
		}
	}
	warns := fmt.Sprint(reg.Warnings())
	if !strings.Contains(warns, "command is required") || !strings.Contains(warns, `unknown env "nowhere"`) {
		t.Fatalf("warnings = %s, want both problems", warns)
	}
}

func TestSettingsLayerRunbookOverDefaultsOverDen(t *testing.T) {
	root := t.TempDir()
	writeFolder(t, root, "own", "command: true\nhistory: 5\neditor: nvim\nabout: false\nagent: claude --model opus\n", nil)
	writeFolder(t, root, "inherits", "command: true\n", nil)
	ten := 10
	cfg := &config.Config{Runbooks: &config.RunbooksConfig{
		Dirs:     []string{root},
		Defaults: config.RunbookSettings{History: &ten, Shell: "bash", Agent: "codex"},
		Items:    []config.RunbookSpec{{Name: "bare", Command: "true"}},
	}}
	reg, _ := NewRegistry(cfg, "", nil)
	want := map[string]Settings{
		"own":      {History: 5, About: false, Shell: "bash", Editor: "nvim", Agent: "claude --model opus"},
		"inherits": {History: 10, About: true, Shell: "bash", Agent: "codex"},
	}
	for name, w := range want {
		r, ok := reg.Lookup(name)
		if !ok {
			t.Fatalf("%s missing: %v", name, reg.Warnings())
		}
		if got := r.Runbook().Settings; got != w {
			t.Errorf("%s settings = %+v, want %+v", name, got, w)
		}
	}
	reg2, _ := NewRegistry(&config.Config{Runbooks: &config.RunbooksConfig{
		Items: []config.RunbookSpec{{Name: "bare", Command: "true"}},
	}}, "", nil)
	r, _ := reg2.Lookup("bare")
	if got := r.Runbook().Settings; got != (Settings{History: DefaultHistory, About: true}) {
		t.Errorf("without defaults = %+v, want den's own", got)
	}
}

// A rescan reconciles like the service manager: what is still there keeps its
// runner and its run, what changed is redefined, what is gone goes — except a
// run in flight, which finishes first.
func TestRescanKeepsRunningWorkAndPicksUpChanges(t *testing.T) {
	root := t.TempDir()
	writeFolder(t, root, "stay", "command: echo stay\n", nil)
	writeFolder(t, root, "edit", "command: echo old\n", nil)
	busy := writeFolder(t, root, "busy", "command: sleep 0.5\n", nil)
	writeFolder(t, root, "drop", "command: true\n", nil)
	cfg := &config.Config{Runbooks: &config.RunbooksConfig{
		Dirs:      []string{root},
		Sequences: []config.SequenceSpec{{Name: "Seq", Steps: []string{"drop"}}},
	}}
	reg, _ := NewRegistry(cfg, "", nil)

	stay, _ := reg.Lookup("stay")
	stay.Execute(context.Background(), reg.ExecOptionsFor(stay.Runbook(), nil))
	running, _ := reg.Lookup("busy")
	if err := running.Start(context.Background(), reg.ExecOptionsFor(running.Runbook(), nil)); err != nil {
		t.Fatal(err)
	}
	gen := reg.Generation()

	writeFolder(t, root, "edit", "command: echo new\n", nil)
	writeFolder(t, root, "added", "command: true\n", nil)
	if err := os.RemoveAll(filepath.Join(root, "drop")); err != nil {
		t.Fatal(err)
	}
	// Its runbook.yaml goes, not the folder: that is the run's working
	// directory, and removing it before the process has started would end the
	// run on the spot.
	if err := os.Remove(filepath.Join(busy, ManifestName)); err != nil {
		t.Fatal(err)
	}
	reg.Rescan()

	if reg.Generation() == gen {
		t.Fatal("generation unchanged after a rescan that changed things")
	}
	if r, _ := reg.Lookup("stay"); r != stay || r.State().Status != StatusSucceeded {
		t.Fatal("an unchanged runbook lost its runner or its last run")
	}
	if r, _ := reg.Lookup("edit"); r.Runbook().Command != "echo new" {
		t.Fatalf("edit command = %q, want the new one", r.Runbook().Command)
	}
	if _, ok := reg.Lookup("added"); !ok {
		t.Fatal("a new folder was not picked up")
	}
	if _, ok := reg.Lookup("drop"); ok {
		t.Fatal("a removed folder is still there")
	}
	if _, ok := reg.LookupSequence("Seq"); ok {
		t.Fatal("a sequence whose step went away is still there")
	}
	if r, ok := reg.Lookup("busy"); !ok || r != running {
		t.Fatal("a runbook removed mid-run was dropped before it finished")
	}

	waitIdle(t, running)
	gen = reg.Generation()
	reg.Rescan()
	if _, ok := reg.Lookup("busy"); ok {
		t.Fatal("a removed runbook is still there after its run finished")
	}
	if reg.Generation() == gen {
		t.Fatal("dropping the finished runbook did not change the generation")
	}
	gen = reg.Generation()
	reg.Rescan()
	if reg.Generation() != gen {
		t.Fatal("a rescan that changed nothing moved the generation")
	}
}

// A definition that changes mid-run applies to the next run, not the one in
// flight.
func TestRedefineWaitsForTheRunInFlight(t *testing.T) {
	root := t.TempDir()
	writeFolder(t, root, "x", "command: sleep 0.3; echo first\n", nil)
	reg, _ := NewRegistry(&config.Config{Runbooks: &config.RunbooksConfig{Dirs: []string{root}}}, "", nil)
	r, _ := reg.Lookup("x")
	if err := r.Start(context.Background(), reg.ExecOptionsFor(r.Runbook(), nil)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	writeFolder(t, root, "x", "command: echo second\n", nil)
	reg.Rescan()
	if got := r.Runbook().Command; got != "sleep 0.3; echo first" {
		t.Fatalf("command mid-run = %q, want the one it started with", got)
	}
	st := waitIdle(t, r)
	if !strings.Contains(strings.Join(st.Logs, "\n"), "first") {
		t.Fatalf("logs = %q, want the first command's output", st.Logs)
	}
	if got := r.Runbook().Command; got != "echo second" {
		t.Fatalf("command after the run = %q, want the new one", got)
	}
}

// A runbook.yaml whose agent den cannot start still runs: only E cannot
// start it, and the panel says so.
func TestARunbooksUnknownAgentIsAWarning(t *testing.T) {
	root := t.TempDir()
	writeFolder(t, root, "odd", "command: true\nagent: ./my-agent.sh\n", nil)
	reg, _ := NewRegistry(&config.Config{Runbooks: &config.RunbooksConfig{Dirs: []string{root}}}, "", nil)
	if _, ok := reg.Lookup("odd"); !ok {
		t.Fatalf("odd was skipped: %v", reg.Warnings())
	}
	var found bool
	for _, w := range reg.Warnings() {
		found = found || strings.Contains(w.String(), `agent "./my-agent.sh"`) && strings.Contains(w.String(), "E cannot start it")
	}
	if !found {
		t.Errorf("warnings = %v, want one naming the agent", reg.Warnings())
	}
	if v := reg.ValidateFolder(filepath.Join(root, "odd")); len(v.Errors) != 0 || !strings.Contains(strings.Join(v.Warnings, "\n"), "E cannot start it") {
		t.Errorf("validate_runbook = %+v, want a warning, no error", v)
	}
}
