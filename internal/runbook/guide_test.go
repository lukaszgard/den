package runbook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
)

func authoringRegistry(t *testing.T) (*Registry, string) {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{
		Environments: map[string]config.EnvSpec{
			"eu-staging": {AWSProfile: "app-e2e", AWSRegionCode: "eu-central-1", Environment: "e2e", EC2InstanceID: "i-0123456789abcdef0"},
			"eu-prod":    {AWSProfile: "app-prod", AWSRegionCode: "eu-central-1", Environment: "prod"},
		},
		Runbooks: &config.RunbooksConfig{
			Dirs:  []string{root},
			Items: []config.RunbookSpec{{Name: "Existing", Command: "true"}},
		},
	}
	reg, _ := NewRegistry(cfg, "", nil)
	return reg, root
}

// The guide is den's runbook guide plus what only this den knows; it holds
// facts an agent needs, and none it should not have.
func TestGuideNamesTheDirsEnvironmentsAndTakenNames(t *testing.T) {
	reg, root := authoringRegistry(t)
	guide := reg.Guide(filepath.Join(root, "rds-status"))
	for _, want := range []string{
		"# Writing a den runbook", // the static guide
		"Write the runbook in `" + filepath.Join(root, "rds-status") + "`",
		"- `" + root + "` (new runbooks go here)",
		"| `eu-staging` | app-e2e | eu-central-1 | e2e | i-0123456789abcdef0 | no |",
		"| `eu-prod` | app-prod | eu-central-1 | prod | – | **yes** |",
		"- Existing",
	} {
		if !strings.Contains(guide, want) {
			t.Errorf("the guide lacks %q", want)
		}
	}
	empty, _ := NewRegistry(&config.Config{}, "", nil)
	if g := empty.Guide(""); !strings.Contains(g, "ask the user to add `runbooks.dirs`") || !strings.Contains(g, "None are defined") {
		t.Errorf("a den without dirs or environments should say so:\n%s", g[len(g)-400:])
	}
}

func TestResolveFolderStaysInsideTheRunbooksDirectories(t *testing.T) {
	reg, root := authoringRegistry(t)
	real, _ := filepath.EvalSymlinks(root)
	if got, err := reg.ResolveFolder("rds-status"); err != nil || got != filepath.Join(real, "rds-status") {
		t.Fatalf("a bare name = %q, %v; want it in the first dir", got, err)
	}
	if got, err := reg.ResolveFolder(filepath.Join(root, "rds-status")); err != nil || got != filepath.Join(real, "rds-status") {
		t.Fatalf("a path inside = %q, %v", got, err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"/etc", "../outside", root, filepath.Join(root, "a", "b"), "escape"} {
		if got, err := reg.ResolveFolder(bad); err == nil {
			t.Errorf("ResolveFolder(%q) = %q, want a refusal", bad, got)
		}
	}
}

func TestValidateFolder(t *testing.T) {
	reg, root := authoringRegistry(t)
	cases := []struct {
		name, manifest string
		files          map[string]string
		ok             bool
		want           string
	}{
		{"good", "name: Good\ndescription: d\ncommand: ./run.sh\nenv: eu-staging\ntimeout: 1m\n",
			map[string]string{"run.sh": "#!/bin/sh\n", "README.md": "# Good\n"}, true, ""},
		{"rust", "name: Rusty\ndescription: d\ncommand: cargo run --quiet --release\nenv: eu-staging\ntimeout: 5m\n",
			map[string]string{"Cargo.toml": "[package]\n", "README.md": "#\n"}, true, ""},
		{"typo", "command: ./run.sh\nconfrim: true\n", nil, false, "confrim"},
		{"badenv", "command: true\nenv: nowhere\n", nil, false, `unknown env "nowhere"`},
		{"missing", "command: ./run.sh\n", nil, false, "./run.sh, which is not in"},
		{"taken", "name: Existing\ncommand: true\n", nil, false, `the name "Existing" is taken`},
		{"none", "", nil, false, "no runbook.yaml"},
	}
	for _, c := range cases {
		dir := writeFolder(t, root, c.name, c.manifest, c.files)
		v := reg.ValidateFolder(dir)
		if v.OK != c.ok || (c.want != "" && !strings.Contains(strings.Join(v.Errors, "\n"), c.want)) {
			t.Errorf("%s: ok=%v errors=%v, want ok=%v with %q", c.name, v.OK, v.Errors, c.ok, c.want)
		}
		if c.name == "rust" && v.Kind != "rust" {
			t.Errorf("rust: kind %q", v.Kind)
		}
		if c.name == "good" && len(v.Warnings) != 0 {
			t.Errorf("good: warnings %v, want none", v.Warnings)
		}
	}
	// writeFolder makes files executable; a ./script that is not must fail.
	dir := writeFolder(t, root, "noexec", "command: ./run.sh\n", nil)
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v := reg.ValidateFolder(dir); v.OK || !strings.Contains(strings.Join(v.Errors, " "), "not executable") {
		t.Fatalf("a non-executable ./run.sh: %+v", v)
	}
}
