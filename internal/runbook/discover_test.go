package runbook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
)

func TestParseParam(t *testing.T) {
	cases := []struct {
		in       string
		wantName string
		wantDesc string
		wantOpts []string
		wantDef  string
		wantReq  bool
		wantSec  bool
	}{
		{in: "cluster", wantName: "cluster"},
		{in: "cluster  Cluster identifier", wantName: "cluster", wantDesc: "Cluster identifier"},
		{in: "tier options=test,prod Which tier", wantName: "tier", wantOpts: []string{"test", "prod"}, wantDesc: "Which tier"},
		{in: "tier options=test, prod", wantName: "tier", wantOpts: []string{"test"}, wantDesc: "prod"},
		{in: "token secret required An approval token", wantName: "token", wantSec: true, wantReq: true, wantDesc: "An approval token"},
		{in: "tier default=test Which tier", wantName: "tier", wantDef: "test", wantDesc: "Which tier"},
		// A description that happens to start with a modifier-looking word is
		// still consumed as a modifier — documented behaviour, not a bug.
		{in: "x required", wantName: "x", wantReq: true},
	}
	for _, c := range cases {
		got, err := parseParam(c.in)
		if err != nil {
			t.Fatalf("parseParam(%q): unexpected error %v", c.in, err)
		}
		if got.Name != c.wantName {
			t.Errorf("parseParam(%q).Name = %q, want %q", c.in, got.Name, c.wantName)
		}
		if got.Description != c.wantDesc {
			t.Errorf("parseParam(%q).Description = %q, want %q", c.in, got.Description, c.wantDesc)
		}
		if got.Default != c.wantDef {
			t.Errorf("parseParam(%q).Default = %q, want %q", c.in, got.Default, c.wantDef)
		}
		if got.Required != c.wantReq {
			t.Errorf("parseParam(%q).Required = %v, want %v", c.in, got.Required, c.wantReq)
		}
		if got.Secret != c.wantSec {
			t.Errorf("parseParam(%q).Secret = %v, want %v", c.in, got.Secret, c.wantSec)
		}
		if len(got.Options) != len(c.wantOpts) {
			t.Errorf("parseParam(%q).Options = %v, want %v", c.in, got.Options, c.wantOpts)
		}
	}

	if _, err := parseParam("   "); err == nil {
		t.Error("parseParam(blank): want error, got nil")
	}
}

func writeScript(t *testing.T, dir, name, body string, exec bool) string {
	t.Helper()
	path := filepath.Join(dir, name)
	mode := os.FileMode(0o644)
	if exec {
		mode = 0o755
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiscover(t *testing.T) {
	dir := t.TempDir()

	writeScript(t, dir, "good.sh", `#!/bin/sh
# den:name        Good One
# den:description does a thing
# den:env         eu-prod
# den:confirm     true
# den:timeout     5m
# den:param       cluster required Cluster id
echo hi
`, true)

	// No den:name at all: a plain helper, skipped without complaint.
	writeScript(t, dir, "helper.sh", "#!/bin/sh\necho helper\n", true)

	// Has directives but no name: must warn rather than vanish.
	writeScript(t, dir, "typo.sh", "#!/bin/sh\n# den:describtion oops\n", true) //nolint:misspell // the typo is the point

	// Correct header but not executable: not a runbook.
	writeScript(t, dir, "notexec.sh", "#!/bin/sh\n# den:name Not Executable\n", false)

	// Go-style comments must work too.
	writeScript(t, dir, "gostyle", "//go:build ignore\n// den:name Go Style\n", true)

	specs, warnings := Discover([]string{dir}, "")

	byName := map[string]bool{}
	for _, s := range specs {
		byName[s.Name] = true
	}
	if !byName["Good One"] {
		t.Error("expected to discover \"Good One\"")
	}
	if !byName["Go Style"] {
		t.Error("expected // comment style to be recognised")
	}
	if byName["Not Executable"] {
		t.Error("non-executable file must not become a runbook")
	}
	if len(specs) != 2 {
		t.Errorf("discovered %d runbooks, want 2: %+v", len(specs), specs)
	}

	// helper.sh must not warn; typo.sh must.
	var typoWarned, helperWarned bool
	for _, w := range warnings {
		if filepath.Base(w.Path) == "typo.sh" {
			typoWarned = true
		}
		if filepath.Base(w.Path) == "helper.sh" {
			helperWarned = true
		}
	}
	if !typoWarned {
		t.Error("a file with den: directives but no den:name must produce a warning")
	}
	if helperWarned {
		t.Error("a plain helper with no den: directives must be skipped silently")
	}

	// Discovered commands must be absolute so cwd cannot break them.
	for _, s := range specs {
		if !filepath.IsAbs(s.Source) {
			t.Errorf("source %q is not absolute", s.Source)
		}
	}
}

func TestDiscoverMissingDirIsQuiet(t *testing.T) {
	// Configuring a scripts directory before creating it is normal and must
	// not nag; an unreadable one still should.
	specs, warnings := Discover([]string{filepath.Join(t.TempDir(), "nope")}, "")
	if len(specs) != 0 {
		t.Errorf("want no runbooks from a missing dir, got %d", len(specs))
	}
	if len(warnings) != 0 {
		t.Errorf("a missing dir must not warn, got %v", warnings)
	}
}

func TestDiscoverUnreadableDirWarns(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o000); err != nil {
		t.Skip("cannot create an unreadable dir here")
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, warnings := Discover([]string{dir}, ""); len(warnings) != 1 {
		t.Errorf("want 1 warning for an unreadable dir, got %d", len(warnings))
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"/tmp/a b.sh":    `'/tmp/a b.sh'`,
		"/tmp/it's.sh":   `'/tmp/it'\''s.sh'`,
		"/tmp/plain.sh":  `'/tmp/plain.sh'`,
		"/tmp/;rm -rf /": `'/tmp/;rm -rf /'`,
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// Paths in den.yaml are read against den.yaml's directory, and a discovered
// script runs in its own: launching den from somewhere else used to change
// which scripts were found and what ./helper.sh inside one meant.
func TestRelativePathsFollowTheConfigNotTheWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "den.yaml")
	scripts := filepath.Join(root, "ops")
	if err := os.MkdirAll(filepath.Join(scripts, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, scripts, "helper.sh", "#!/bin/sh\necho helped\n", true)
	writeScript(t, scripts, "main.sh", "#!/bin/sh\n# den:name Main\n./helper.sh\n", true)
	writeScript(t, scripts, "elsewhere.sh", "#!/bin/sh\n# den:name Elsewhere\n# den:cwd sub\npwd\n", true)

	cfg := &config.Config{Runbooks: &config.RunbooksConfig{
		Dirs: []string{"ops"},
		Items: []config.RunbookSpec{
			{Name: "Inline", Command: "pwd"},
			{Name: "Inline cwd", Command: "pwd", Cwd: "ops/sub"},
		},
	}}
	// Somewhere unrelated, as when den is started from another directory.
	t.Chdir(t.TempDir())
	reg, err := NewRegistry(cfg, cfgPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Inline":     root,
		"Inline cwd": filepath.Join(root, "ops", "sub"),
		"Main":       scripts,
		"Elsewhere":  filepath.Join(scripts, "sub"),
	}
	for name, dir := range want {
		r, ok := reg.Lookup(name)
		if !ok {
			t.Fatalf("runbook %q not found; warnings %v", name, reg.Warnings())
		}
		if got := r.Runbook().Cwd; got != dir {
			t.Errorf("%s: cwd = %q, want %q", name, got, dir)
		}
	}

	r, _ := reg.Lookup("Main")
	got := r.Execute(context.Background(), reg.ExecOptionsFor(r.Runbook(), nil))
	if got.Status != StatusSucceeded || !strings.Contains(strings.Join(got.Logs, "\n"), "helped") {
		t.Fatalf("Main = %v %q, want ./helper.sh found next to the script", got.Status, got.Logs)
	}
	if env := envMap(reg.ExecOptionsFor(r.Runbook(), nil).Env); env["DEN_CONFIG"] != cfgPath {
		t.Fatalf("DEN_CONFIG = %q, want %q", env["DEN_CONFIG"], cfgPath)
	}
}
