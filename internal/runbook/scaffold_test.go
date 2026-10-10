package runbook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
)

func TestNewRunbookScaffoldsARunnableFolder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runbooks") // created on demand
	reg, _ := NewRegistry(&config.Config{Runbooks: &config.RunbooksConfig{Dirs: []string{root}}}, "", nil)

	if got := reg.NewRunbookDir("  "); got != root {
		t.Errorf("NewRunbookDir without a name = %q, want the directory %q", got, root)
	}
	where := reg.NewRunbookDir(" Rotate DB password ")
	dir, err := reg.NewRunbook("Rotate DB password", "")
	if err != nil {
		t.Fatal(err)
	}
	if where != dir {
		t.Errorf("NewRunbookDir said %q before it was made, want %q, where it went", where, dir)
	}
	if filepath.Base(dir) != "rotate-db-password" {
		t.Errorf("folder %q, want it named after the runbook", filepath.Base(dir))
	}
	r, ok := reg.Lookup("Rotate DB password")
	if !ok {
		t.Fatalf("the new runbook is not listed: %v", reg.Warnings())
	}
	if got := reg.EditTarget(r.Runbook()); got != filepath.Join(dir, "run.sh") {
		t.Errorf("EditTarget = %q, want its run.sh", got)
	}
	st := r.Execute(context.Background(), reg.ExecOptionsFor(r.Runbook(), nil))
	if st.Status != StatusSucceeded || !strings.Contains(strings.Join(st.Logs, "\n"), "runbook: Rotate DB password") {
		t.Fatalf("the scaffolded runbook = %v %q, want it to run as is", st.Status, st.Logs)
	}
	if _, err := reg.NewRunbook("Rotate DB password", ""); err == nil {
		t.Fatal("a second runbook with the same name was scaffolded")
	}

	none, _ := NewRegistry(&config.Config{}, "", nil)
	if _, err := none.NewRunbook("x", ""); err == nil || !strings.Contains(err.Error(), "runbooks.dirs") {
		t.Fatalf("without a runbooks directory: %v, want a pointer to runbooks.dirs", err)
	}
	if got := none.NewRunbookDir("x"); got != "" {
		t.Errorf("NewRunbookDir without a runbooks directory = %q, want none", got)
	}
}

// A new runbook's name must be free: of runbooks and sequences, whatever the
// case, and of folders, which "Greet!" shares with "Greet".
func TestNewRunbookRefusesATakenName(t *testing.T) {
	root := t.TempDir()
	writeFolder(t, root, "greet", "name: Greet\ncommand: echo hi\n", nil)
	if err := os.Mkdir(filepath.Join(root, "broken"), 0o750); err != nil {
		t.Fatal(err)
	}
	reg, _ := NewRegistry(&config.Config{Runbooks: &config.RunbooksConfig{
		Dirs:      []string{root},
		Sequences: []config.SequenceSpec{{Name: "Both", Steps: []string{"Greet"}}},
	}}, "", nil)

	for name, want := range map[string]string{
		"  ":      "needs a name",
		"greet":   `a runbook named "Greet" exists already`,
		"BOTH":    `a sequence named "Both" exists already`,
		"Broken!": "broken" + string(filepath.Separator) + " exists already",
	} {
		if err := reg.NewRunbookConflict(name, ""); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("NewRunbookConflict(%q) = %v, want %q", name, err, want)
		}
		if _, err := reg.NewRunbook(name, ""); err == nil {
			t.Errorf("NewRunbook(%q) made a runbook", name)
		}
	}
	if err := reg.NewRunbookConflict("Greet", t.TempDir()); err == nil {
		t.Error("a runbook's name is taken whichever folder the new one goes into")
	}
	if err := reg.NewRunbookConflict("Report", ""); err != nil {
		t.Errorf("a free name: %v", err)
	}
}

func TestNewRunbookCopiesTheTemplate(t *testing.T) {
	base := t.TempDir()
	tpl := writeFolder(t, base, "tpl", "name: placeholder\ncommand: python3 main.py\n",
		map[string]string{"main.py": "print('hi')\n"})
	if err := os.Symlink("/etc/hosts", filepath.Join(tpl, "escape")); err != nil {
		t.Fatal(err)
	}
	// A template that is a runbook too has runs of its own.
	writeFolder(t, tpl, LogsFolder, "", map[string]string{"log-01.log": "the template's run\n"})
	cfg := &config.Config{Runbooks: &config.RunbooksConfig{Dirs: []string{"rb"}, Template: "tpl"}}
	reg, _ := NewRegistry(cfg, filepath.Join(base, "den.yaml"), nil)

	dir, err := reg.NewRunbook("Data fix", "")
	if err != nil {
		t.Fatal(err)
	}
	r, ok := reg.Lookup("Data fix")
	if !ok {
		t.Fatalf("not listed under its new name: %v", reg.Warnings())
	}
	if r.Runbook().Command != "python3 main.py" {
		t.Errorf("command = %q, want the template's", r.Runbook().Command)
	}
	if got := reg.EditTarget(r.Runbook()); got != filepath.Join(dir, "main.py") {
		t.Errorf("EditTarget = %q, want the script the command runs", got)
	}
	if info, err := os.Stat(filepath.Join(dir, "main.py")); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("main.py = %v, %v; want copied with its mode", info, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "escape")); err == nil {
		t.Error("a symlink in the template was copied")
	}
	if _, err := os.Stat(filepath.Join(dir, LogsFolder)); err == nil {
		t.Error("the template's runs were copied into the new runbook")
	}
}

func TestEditTargetAndContextVars(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "den.yaml")
	writeFolder(t, root, "inline-cmd", "command: aws sts get-caller-identity\n", nil)
	cfg := &config.Config{
		Environments: map[string]config.EnvSpec{"dev": {AWSProfile: "dev-profile", AWSRegionCode: "eu-central-1"}},
		Runbooks: &config.RunbooksConfig{
			Dirs: []string{"."},
			Items: []config.RunbookSpec{{
				Name: "Inline", Command: "true", Env: "dev",
				EnvVars: map[string]string{"FOO": "bar"},
				Params:  []config.ParamSpec{{Name: "tier", Default: "prod"}, {Name: "token", Default: "x", Secret: true}},
			}},
		},
	}
	ports := func() map[string]int { return map[string]int{"RDS_EU_TEST": 50110} }
	reg, _ := NewRegistry(cfg, cfgPath, ports)

	folder, _ := reg.Lookup("inline-cmd")
	if got := reg.EditTarget(folder.Runbook()); got != filepath.Join(root, "inline-cmd", ManifestName) {
		t.Errorf("a command without a script edits %q, want its runbook.yaml", got)
	}
	inline, _ := reg.Lookup("Inline")
	if got := reg.EditTarget(inline.Runbook()); got != cfgPath {
		t.Errorf("an inline runbook edits %q, want den.yaml", got)
	}

	vars := reg.ContextVars(inline.Runbook())
	want := map[string]string{
		"AWS_PROFILE": "dev-profile", "AWS_REGION": "eu-central-1", "DEN_RUNBOOK": "Inline",
		"DEN_PORT_RDS_EU_TEST": "50110", "DEN_PARAM_TIER": "prod", "FOO": "bar", "DEN_CONFIG": cfgPath,
	}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("%s = %q, want %q", k, vars[k], v)
		}
	}
	if _, ok := vars["DEN_PARAM_TOKEN"]; ok {
		t.Error("a secret parameter's default reached a shell's environment")
	}
	if _, ok := vars["HOME"]; ok {
		t.Error("ContextVars holds inherited variables, want only den's own")
	}
}
