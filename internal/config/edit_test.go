package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// editFixture is a den.yaml written the way people write theirs: aligned
// trailing comments, a list indented by one space, commented-out keys and a
// comment heading the next block.
const editFixture = `version: "1"                         # config format

menu_order:                         # optional, customize left-menu order
 - connect
 - runbooks

environments:
  eu-prod:                        # the production account
    region: eu
    environment: prod
    ec2_instance_id: i-0123
    aws_profile: prod
    aws_region_code: eu-central-1

runbooks:                           # user-defined scripts
  dirs:                             # every folder in them is a runbook
    - ~/.config/den/runbooks        # relative paths: to this file
  # rescan: true                    # pick up folders while den runs

  items:
    - name: "Hello"
      command: echo hi

# Secrets come last.
secrets:
  view: tree
`

func writeEditFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "den.yaml")
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

var opsSource = RunbookSource{
	Name:   "platform-ops",
	URL:    "git@ghe.acme.com:ops/den-runbooks.git",
	Ref:    "v1.4.0",
	EnvMap: map[string]string{"prod": "eu-prod"},
}

func mustEdit(t *testing.T, path string) *ConfigEdit {
	t.Helper()
	e, err := EditConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestAddSourceToExistingRunbooks(t *testing.T) {
	path := writeEditFixture(t, editFixture)
	e := mustEdit(t, path)
	if err := e.AddSource(opsSource); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(editFixture, `      command: echo hi
`, `      command: echo hi
  sources:
    - name: platform-ops
      url: git@ghe.acme.com:ops/den-runbooks.git
      ref: v1.4.0
      env_map:
        prod: eu-prod
`, 1)
	if got := string(e.Bytes()); got != want {
		t.Fatalf("den.yaml after AddSource:\n%s\nwant:\n%s", got, want)
	}
	cfg, err := e.Save()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.RunbookSources(); len(got) != 1 || got[0].Name != "platform-ops" || got[0].EnvMap["prod"] != "eu-prod" {
		t.Fatalf("saved sources = %+v", got)
	}
	data, _ := os.ReadFile(path)
	if string(data) != want {
		t.Fatalf("file on disk differs from the edit:\n%s", data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want the file's own 0640", info.Mode().Perm())
	}
}

func TestAddSourceAppendsToSources(t *testing.T) {
	body := strings.Replace(editFixture, "\n# Secrets come last.", `  sources:
    - name: first
      url: acme/first

# Secrets come last.`, 1)
	e := mustEdit(t, writeEditFixture(t, body))
	if err := e.AddSource(RunbookSource{Name: "second", URL: "acme/second"}); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(body, "      url: acme/first\n", "      url: acme/first\n    - name: second\n      url: acme/second\n", 1)
	if got := string(e.Bytes()); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if err := e.AddSource(RunbookSource{Name: "first", URL: "acme/other"}); err == nil || !strings.Contains(err.Error(), `"first" exists already`) {
		t.Errorf("adding a name twice: err = %v", err)
	}
}

func TestAddSourceWithoutRunbooksBlock(t *testing.T) {
	body := "version: \"1\"\nsecrets:\n  view: tree" // no final newline
	e := mustEdit(t, writeEditFixture(t, body))
	if err := e.AddSource(RunbookSource{Name: "ops", URL: "acme/ops"}); err != nil {
		t.Fatal(err)
	}
	want := body + "\n\nrunbooks:\n  sources:\n    - name: ops\n      url: acme/ops\n"
	if got := string(e.Bytes()); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestAddSourceUnderAnEmptyRunbooksKey(t *testing.T) {
	body := "runbooks:\nsecrets:\n  view: tree\n"
	e := mustEdit(t, writeEditFixture(t, body))
	if err := e.AddSource(RunbookSource{Name: "ops", URL: "acme/ops"}); err != nil {
		t.Fatal(err)
	}
	want := "runbooks:\n  sources:\n    - name: ops\n      url: acme/ops\nsecrets:\n  view: tree\n"
	if got := string(e.Bytes()); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRemoveSource(t *testing.T) {
	body := strings.Replace(editFixture, "\n# Secrets come last.", `  sources:
    - name: first
      url: acme/first
      include: [a]
    - name: second
      url: acme/second

# Secrets come last.`, 1)
	e := mustEdit(t, writeEditFixture(t, body))
	if err := e.RemoveSource("first"); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(body, "    - name: first\n      url: acme/first\n      include: [a]\n", "", 1)
	if got := string(e.Bytes()); got != want {
		t.Fatalf("after removing first:\n%s\nwant:\n%s", got, want)
	}
	if err := e.RemoveSource("second"); err != nil {
		t.Fatal(err)
	}
	if got := string(e.Bytes()); got != editFixture {
		t.Fatalf("removing the last source should drop sources: too:\n%s", got)
	}
	if err := e.RemoveSource("third"); err == nil || !strings.Contains(err.Error(), `no source "third"`) {
		t.Errorf("removing an unknown source: err = %v", err)
	}
}

func TestRemoveRunbook(t *testing.T) {
	body := strings.Replace(editFixture, `      command: echo hi
`, `      command: echo hi
    # The deploy, reviewed by ops.
    - name: deploy
      command: |
        # a comment of the script, not of den.yaml
        ./deploy.sh
      confirm: true
    - name: last
      command: echo bye
`, 1)
	e := mustEdit(t, writeEditFixture(t, body))
	if err := e.RemoveRunbook("deploy"); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(body, `    - name: deploy
      command: |
        # a comment of the script, not of den.yaml
        ./deploy.sh
      confirm: true
`, "", 1)
	if got := string(e.Bytes()); got != want {
		t.Fatalf("after removing deploy:\n%s\nwant:\n%s", got, want)
	}
	for _, name := range []string{"Hello", "last"} {
		if err := e.RemoveRunbook(name); err != nil {
			t.Fatal(err)
		}
	}
	if got := string(e.Bytes()); strings.Contains(got, "items:") || !strings.Contains(got, "  # rescan: true") {
		t.Fatalf("removing the last runbook should drop items: and keep the rest:\n%s", got)
	}
	if err := e.RemoveRunbook("gone"); err == nil || !strings.Contains(err.Error(), `no runbook "gone"`) {
		t.Errorf("removing an unknown runbook: err = %v", err)
	}
}

func TestRemoveSequence(t *testing.T) {
	body := strings.Replace(editFixture, "\n# Secrets come last.", `  sequences:
    - name: both
      steps: [Hello, Hello]
    - name: one
      steps:
        - Hello

# Secrets come last.`, 1)
	e := mustEdit(t, writeEditFixture(t, body))
	if err := e.RemoveSequence("both"); err != nil {
		t.Fatal(err)
	}
	if err := e.RemoveSequence("Hello"); err == nil || !strings.Contains(err.Error(), `no sequence "Hello"`) {
		t.Errorf("a runbook's name is not a sequence's: err = %v", err)
	}
	if err := e.RemoveSequence("one"); err != nil {
		t.Fatal(err)
	}
	if got := string(e.Bytes()); got != editFixture {
		t.Fatalf("removing both sequences should leave the file as it was:\n%s", got)
	}
}

func TestSetSourceList(t *testing.T) {
	body := strings.Replace(editFixture, "\n# Secrets come last.", `  sources:
    - name: ops
      url: acme/ops       # the ops team's
      include: [a, b]
      env_map:
        prod: eu-prod

# Secrets come last.`, 1)
	e := mustEdit(t, writeEditFixture(t, body))

	// Replacing a flow list writes a block one in its place.
	if err := e.SetSourceList("ops", "include", []string{"a", "rds-*"}); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(body, "      include: [a, b]\n", "      include:\n        - a\n        - rds-*\n", 1)
	if got := string(e.Bytes()); got != want {
		t.Fatalf("after replacing include:\n%s\nwant:\n%s", got, want)
	}

	// A list the item does not have yet goes at its end.
	if err := e.SetSourceList("ops", "exclude", []string{"old"}); err != nil {
		t.Fatal(err)
	}
	want = strings.Replace(want, "        prod: eu-prod\n", "        prod: eu-prod\n      exclude:\n        - old\n", 1)
	if got := string(e.Bytes()); got != want {
		t.Fatalf("after adding exclude:\n%s\nwant:\n%s", got, want)
	}

	// An empty list removes the key.
	if err := e.SetSourceList("ops", "include", nil); err != nil {
		t.Fatal(err)
	}
	want = strings.Replace(want, "      include:\n        - a\n        - rds-*\n", "", 1)
	if got := string(e.Bytes()); got != want {
		t.Fatalf("after clearing include:\n%s\nwant:\n%s", got, want)
	}
	if err := e.SetSourceList("ops", "url", []string{"x"}); err == nil {
		t.Error("only include and exclude are lists den edits")
	}
}

func TestEditKeepsCRLF(t *testing.T) {
	body := "version: \"1\"\r\nrunbooks:\r\n  dirs:\r\n    - ./runbooks\r\n"
	e := mustEdit(t, writeEditFixture(t, body))
	if err := e.AddSource(RunbookSource{Name: "ops", URL: "acme/ops"}); err != nil {
		t.Fatal(err)
	}
	want := body + "  sources:\r\n    - name: ops\r\n      url: acme/ops\r\n"
	if got := string(e.Bytes()); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestEditRefusesFlowStyle(t *testing.T) {
	for _, body := range []string{
		"runbooks: {dirs: [./runbooks]}\n",
		"runbooks:\n  sources: [{name: a, url: acme/a}]\n",
	} {
		e := mustEdit(t, writeEditFixture(t, body))
		err := e.AddSource(RunbookSource{Name: "ops", URL: "acme/ops"})
		if err == nil || !strings.Contains(err.Error(), "by hand") {
			t.Errorf("%q: err = %v, want a refusal to edit flow style", body, err)
		}
		if got := string(e.Bytes()); got != body {
			t.Errorf("%q: a refused edit changed the text to %q", body, got)
		}
	}
	for _, body := range []string{
		"runbooks:\n  items: [{name: a, command: echo}]\n",
		"runbooks:\n  items:\n    - {name: a, command: echo}\n",
	} {
		e := mustEdit(t, writeEditFixture(t, body))
		if err := e.RemoveRunbook("a"); err == nil || !strings.Contains(err.Error(), "by hand") {
			t.Errorf("%q: err = %v, want a refusal to edit flow style", body, err)
		}
	}
}

func TestSaveRefusesAnInvalidResult(t *testing.T) {
	path := writeEditFixture(t, editFixture)
	e := mustEdit(t, path)
	if err := e.AddSource(RunbookSource{Name: "ops", URL: "acme/ops", EnvMap: map[string]string{"prod": "nowhere"}}); err != nil {
		t.Fatal(err)
	}
	_, err := e.Save()
	if err == nil || !strings.Contains(err.Error(), `unknown env "nowhere"`) {
		t.Fatalf("Save err = %v, want the validation error", err)
	}
	if strings.Contains(err.Error(), ".den.yaml") {
		t.Errorf("the error names the temporary file: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != editFixture {
		t.Error("a refused save changed den.yaml")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("a refused save left files behind: %v", entries)
	}
}

func TestSaveFollowsASymlink(t *testing.T) {
	real := writeEditFixture(t, editFixture)
	link := filepath.Join(t.TempDir(), "den.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	e := mustEdit(t, link)
	if err := e.AddSource(RunbookSource{Name: "ops", URL: "acme/ops"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Save(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("den.yaml is no longer a link (%v): dotfiles setups would lose it", err)
	}
	data, _ := os.ReadFile(real)
	if !strings.Contains(string(data), "name: ops") {
		t.Error("the link's target was not edited")
	}
}
