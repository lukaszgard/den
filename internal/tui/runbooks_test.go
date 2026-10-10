package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/runbook/source"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/termsession"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/tui/testkeys"
)

// programStarts records the programs the root model started for runbooks.
type programStarts struct {
	cmds  []termsession.Command
	terms []*fakeTerminal
}

func (ps *programStarts) start(cmd termsession.Command, cols, rows int) (liveTerminal, error) {
	term := &fakeTerminal{changed: make(chan struct{}, 1)}
	ps.cmds = append(ps.cmds, cmd)
	ps.terms = append(ps.terms, term)
	return term, nil
}

// runbookFixture is a den whose runbooks directory holds one folder runbook,
// beside an inline runbook asking for a parameter and a sequence, with the
// Runbooks pane focused.
type runbookFixture struct {
	*harness
	starts *programStarts
	root   string // the runbooks directory
	greet  string // the Greet runbook's folder
}

func runbookHarness(t *testing.T) runbookFixture { return runbookHarnessWith(t, nil) }

// runbookHarnessWith is runbookHarness with runbooks.agents set to agents.
func runbookHarnessWith(t *testing.T, agents []config.AgentSpec) runbookFixture {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "runbooks")
	greet := filepath.Join(root, "greet")
	if err := os.MkdirAll(greet, 0o750); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string, mode os.FileMode) {
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(greet, runbook.ManifestName), "name: Greet\ndescription: Says hello\ncommand: ./run.sh\nenv: dev\n", 0o600)
	write(filepath.Join(greet, "run.sh"), "#!/bin/sh\necho \"hello from $(basename \"$PWD\") as $AWS_PROFILE\"\n", 0o700)
	write(filepath.Join(greet, "README.md"), "# Greet\n\nThe friendliest runbook.\n", 0o600)
	awsCfg := filepath.Join(dir, "aws-config")
	write(awsCfg, "", 0o600)

	cfg := &config.Config{
		AWS:          &config.AWSSpec{ConfigPath: awsCfg, CredentialsPath: awsCfg},
		Environments: map[string]config.EnvSpec{"dev": {AWSProfile: "dev-profile", AWSRegionCode: "eu-central-1"}},
		Runbooks: &config.RunbooksConfig{
			Dirs: []string{root},
			Items: []config.RunbookSpec{{
				Name: "Ask", Command: "echo $DEN_PARAM_WHO",
				Params: []config.ParamSpec{{Name: "who", Required: true}},
			}},
			Sequences: []config.SequenceSpec{{Name: "Both", Steps: []string{"Greet"}}},
			Agents:    agents,
		},
	}
	h := &harness{t: t, m: New(managerWith(), Options{
		ConfigPath:     filepath.Join(dir, "den.yaml"),
		Config:         cfg,
		Version:        "test",
		RunbookHistory: runbook.NewHistory(filepath.Join(dir, "state")),
	})}
	t.Cleanup(func() { h.m.shutdown() })
	h.send(tea.WindowSizeMsg{Width: 140, Height: 40})
	starts := &programStarts{}
	h.m.startProgram = starts.start
	for h.m.currentPane() != paneRunbooks {
		h.press("down")
	}
	h.press("right")
	return runbookFixture{harness: h, starts: starts, root: root, greet: greet}
}

// flatView is view without its styling, borders and line breaks, its runs of
// spaces made one: text that wraps in a pop-up reads as one line.
func flatView(view string) string {
	view = strings.NewReplacer("│", " ", "┃", " ", "\n", " ").Replace(ansi.Strip(view))
	return strings.Join(strings.Fields(view), " ")
}

// selectRow moves the list's cursor to the runbook called name.
func (f runbookFixture) selectRow(name string) {
	f.t.Helper()
	f.press("g")
	for range 10 {
		if e, ok := f.m.runbooksPane.Selected(); ok && e == name {
			return
		}
		f.press("down")
	}
	f.t.Fatalf("no row %q in the list", name)
}

// waitRun waits for the runbook called name to finish, then lets a tick show it.
func (f runbookFixture) waitRun(name string) {
	f.t.Helper()
	r, _ := f.m.runbookReg.Lookup(name)
	deadline := time.Now().Add(5 * time.Second)
	for time.Sleep(20 * time.Millisecond); r.IsRunning(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			f.t.Fatalf("%s is still running", name)
		}
	}
	f.send(msgs.TickMsg{Time: time.Now()})
}

func TestEnterOpensTheRunbookViewAndEscLeavesIt(t *testing.T) {
	f := runbookHarness(t)
	f.selectRow("Greet")
	f.press("enter")
	if !f.m.showRunbook || f.m.runbookView.Name() != "Greet" {
		t.Fatal("enter on a runbook did not open its view")
	}
	view := plain(f.m)
	for _, want := range []string{"runbook", "Greet", "Logs", "History", "About", "./run.sh", "env dev"} {
		if !strings.Contains(view, want) {
			t.Errorf("the view lacks %q:\n%s", want, view)
		}
	}
	f.press("esc")
	if f.m.showRunbook || f.m.leftFocused {
		t.Fatal("esc should go back to the list, focused")
	}
}

func TestRunFromTheViewShowsItsOutputInLogs(t *testing.T) {
	f := runbookHarness(t)
	f.selectRow("Greet")
	f.press("enter", "r")
	f.waitRun("Greet")
	if view := plain(f.m); !strings.Contains(view, "hello from greet as dev-profile") || !strings.Contains(view, "EXIT 0") {
		t.Fatalf("the run's output is not in Logs, run in its folder with its env:\n%s", view)
	}
}

// Runs started in the TUI also write to the Logs panel, tagged with the
// runbook's or the sequence's name; o there hides them and shows them again.
func TestRunsShowInTheLogsPanel(t *testing.T) {
	f := runbookHarness(t)
	f.selectRow("Greet")
	f.press("r")
	f.waitRun("Greet")
	f.selectRow("Both")
	f.press("r")
	seq, _ := f.m.runbookReg.LookupSequence("Both")
	deadline := time.Now().Add(5 * time.Second)
	for time.Sleep(20 * time.Millisecond); seq.IsRunning(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("Both is still running")
		}
	}

	f.press("left", "L")
	f.send(msgs.TickMsg{Time: time.Now()})
	view := plain(f.m)
	for _, want := range []string{"[Greet] hello from greet as dev-profile", "[Both] ── step 1/1: Greet ──", "[Both] hello from greet"} {
		if !strings.Contains(view, want) {
			t.Errorf("the Logs panel lacks %q:\n%s", want, view)
		}
	}
	f.press("o")
	if view := plain(f.m); strings.Contains(view, "hello from greet") || !strings.Contains(view, "run output hidden") {
		t.Errorf("o should hide the runs' output:\n%s", view)
	}
	f.press("o")
	if view := plain(f.m); !strings.Contains(view, "[Greet] hello from greet") {
		t.Errorf("o again should show the runs' output:\n%s", view)
	}
}

// t opens the user's shell where the runbook lives, with what den sets for it;
// the tab outlives the view, like a service's.
func TestTOpensAShellInTheRunbooksFolder(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	f := runbookHarness(t)
	f.selectRow("Greet")
	f.press("enter", "t")

	if len(f.starts.cmds) != 1 {
		t.Fatalf("t started %d programs, want 1", len(f.starts.cmds))
	}
	cmd := f.starts.cmds[0]
	if cmd.Path != "/bin/zsh" || cmd.Dir != f.greet {
		t.Fatalf("started %s in %q, want the shell in %s", cmd.Path, cmd.Dir, f.greet)
	}
	for _, want := range []string{"AWS_PROFILE=dev-profile", "DEN_RUNBOOK=Greet", "AWS_REGION=eu-central-1"} {
		if !slices.Contains(cmd.Env, want) {
			t.Errorf("the shell's env lacks %s: %v", want, cmd.Env)
		}
	}
	if !f.m.runbookView.CapturingKeys() {
		t.Fatal("a fresh terminal should have the keyboard")
	}
	f.press("l", "s")
	if got := f.starts.terms[0].typed(); got != "l s" {
		t.Fatalf("the shell got %q, want the keys typed", got)
	}
	if view := plain(f.m); !strings.Contains(view, "1 zsh") || !strings.Contains(view, "fake client screen") {
		t.Fatalf("the terminal tab is not on screen:\n%s", view)
	}

	f.press("ctrl+t", "esc")
	if f.m.showRunbook {
		t.Fatal("ctrl+t then esc should leave the terminal, then the view")
	}
	if f.starts.terms[0].closed() != 0 {
		t.Fatal("leaving the view stopped the shell")
	}
	if !strings.Contains(plain(f.m), "⌨ 1") {
		t.Fatalf("the list does not show the runbook's running terminal:\n%s", plain(f.m))
	}
	f.press("enter")
	if !strings.Contains(plain(f.m), "1 zsh") {
		t.Fatal("the terminal tab is gone after reopening the view")
	}
}

func TestEOpensTheScriptInTheEditor(t *testing.T) {
	t.Setenv("VISUAL", "nvim -p")
	f := runbookHarness(t)
	f.selectRow("Greet")
	f.press("enter", "e")
	if len(f.starts.cmds) != 1 {
		t.Fatalf("e started %d programs, want 1", len(f.starts.cmds))
	}
	cmd := f.starts.cmds[0]
	script := filepath.Join(f.greet, "run.sh")
	if cmd.Path != "/bin/sh" || cmd.Args[len(cmd.Args)-1] != script || !strings.Contains(cmd.Args[1], "nvim -p") {
		t.Fatalf("started %s %q, want the editor on %s", cmd.Path, cmd.Args, script)
	}
	if !strings.Contains(plain(f.m), "1 nvim") {
		t.Fatalf("the editor's tab is not named after it:\n%s", plain(f.m))
	}
}

// o opens the log of the run on screen in the editor, in a tab named after
// the run: on History the run picked, on Logs the last one.
func TestOOpensTheRunsLogInTheEditor(t *testing.T) {
	t.Setenv("VISUAL", "nvim -R")
	f := runbookHarness(t)
	f.selectRow("Greet")
	f.press("enter", "o")
	if len(f.starts.cmds) != 0 || !strings.Contains(f.m.flashMsg, "nothing run yet") {
		t.Fatalf("o before any run started %d programs (flash %q), want none and why", len(f.starts.cmds), f.m.flashMsg)
	}
	for range 2 {
		f.press("r")
		f.waitRun("Greet")
	}
	logs := filepath.Join(f.greet, "logs")

	f.press("right", "down", "o") // History, the older run
	if len(f.starts.cmds) != 1 {
		t.Fatalf("o on History started %d programs, want 1", len(f.starts.cmds))
	}
	cmd := f.starts.cmds[0]
	if want := filepath.Join(logs, "log-01.log"); cmd.Args[len(cmd.Args)-1] != want || !strings.Contains(cmd.Args[1], "nvim -R") {
		t.Fatalf("started %s %q, want the editor on %s", cmd.Path, cmd.Args, want)
	}
	if cmd.Dir != f.greet {
		t.Errorf("the editor runs in %q, want the runbook's folder", cmd.Dir)
	}
	if !strings.Contains(plain(f.m), "1 log-01") {
		t.Fatalf("the editor's tab is not named after the run:\n%s", plain(f.m))
	}

	f.press("ctrl+t", "left", "left", "left", "left", "o") // back to Logs
	if len(f.starts.cmds) != 2 || f.starts.cmds[1].Args[len(f.starts.cmds[1].Args)-1] != filepath.Join(logs, "log-02.log") {
		t.Fatalf("o on Logs did not open the last run's log: %d programs", len(f.starts.cmds))
	}
}

// A sequence has no terminals: o names its log's file instead.
func TestOOnASequenceNamesItsLog(t *testing.T) {
	f := runbookHarness(t)
	seq, _ := f.m.runbookReg.LookupSequence("Both")
	seq.Execute(context.Background(), runbook.SequenceOptions{})
	f.selectRow("Both")
	f.press("enter", "o")
	if len(f.starts.cmds) != 0 || !strings.Contains(f.m.flashMsg, "log-01.log") {
		t.Fatalf("o in a sequence's view started %d programs (flash %q), want its log named", len(f.starts.cmds), f.m.flashMsg)
	}
}

// Each run has a log of its own: Logs shows the last run alone, and History
// lists the runs with the selected one's log under them, enter showing that
// log full height and esc going back to the list before leaving the view.
func TestHistoryShowsEachRunsOwnLog(t *testing.T) {
	f := runbookHarness(t)
	f.selectRow("Greet")
	f.press("enter", "r")
	f.waitRun("Greet")
	if err := os.WriteFile(filepath.Join(f.greet, "run.sh"), []byte("#!/bin/sh\necho second run\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.press("r")
	f.waitRun("Greet")
	if view := plain(f.m); strings.Contains(view, "hello from") || !strings.Contains(view, "second run") {
		t.Fatalf("Logs shows more than the last run:\n%s", view)
	}

	f.press("right") // History
	view := plain(f.m)
	if runRows(view) != 2 || !strings.Contains(view, "tui") || !strings.Contains(view, filepath.Join("logs", "log-02.log")) {
		t.Fatalf("History does not list both runs with the newest one's file in the runbook's logs/:\n%s", view)
	}
	if !strings.Contains(view, "second run") || strings.Contains(view, "hello from") {
		t.Fatalf("the newest run's log is not the only one under the list:\n%s", view)
	}
	f.press("down")
	view = plain(f.m)
	if !strings.Contains(view, "hello from greet") || strings.Contains(view, "second run") {
		t.Fatalf("selecting the first run did not show its log alone:\n%s", view)
	}

	f.press("enter")
	view = plain(f.m)
	if runRows(view) != 0 || !strings.Contains(view, "hello from greet") {
		t.Fatalf("enter did not show the run's log full height:\n%s", view)
	}
	f.press("esc")
	if !f.m.showRunbook || runRows(plain(f.m)) != 2 {
		t.Fatalf("esc did not go back to the runs:\n%s", plain(f.m))
	}
	f.press("esc")
	if f.m.showRunbook {
		t.Fatal("a second esc did not leave the view")
	}
}

// A run in flight is in History from its start, its log following it there.
func TestHistoryFollowsTheRunInFlight(t *testing.T) {
	f := runbookHarness(t)
	gate := filepath.Join(t.TempDir(), "gate")
	script := "#!/bin/sh\necho started\nwhile [ ! -f '" + gate + "' ]; do sleep 0.02; done\necho finished\n"
	if err := os.WriteFile(filepath.Join(f.greet, "run.sh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	f.selectRow("Greet")
	f.press("enter", "right", "r") // History, then run it there
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.send(msgs.TickMsg{Time: time.Now()})
		view := plain(f.m)
		if strings.Contains(view, "running") && strings.Contains(view, "started") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("History does not show the run in flight with its output:\n%s", view)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.waitRun("Greet")
	if view := plain(f.m); runRows(view) != 1 || !strings.Contains(view, "finished") {
		t.Fatalf("the finished run is not shown whole:\n%s", view)
	}
}

// A run whose den died never got its end: History says so rather than
// showing it running for good.
func TestHistoryShowsARunCutShortAsInterrupted(t *testing.T) {
	f := runbookHarness(t)
	greet, _ := f.m.runbookReg.Lookup("Greet")
	rec, err := f.m.runbookReg.History().Begin("Greet", greet.Runbook().LogsDir(), 5, runbook.Entry{StartedAt: time.Now(), Origin: runbook.OriginCLI})
	if err != nil {
		t.Fatal(err)
	}
	rec.Line("[09:00:00] half way")
	meta := strings.TrimSuffix(rec.Path(), ".log") + ".json"
	data, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	gone := exec.Command("sh", "-c", "exit 0")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), fmt.Sprintf(`"pid": %d`, os.Getpid()), fmt.Sprintf(`"pid": %d`, gone.Process.Pid), 1))
	if err := os.WriteFile(meta, data, 0o600); err != nil {
		t.Fatal(err)
	}

	f.selectRow("Greet")
	f.press("enter", "right")
	view := plain(f.m)
	if !strings.Contains(view, "interrupted") || !strings.Contains(view, "half way") || strings.Contains(view, "running") {
		t.Fatalf("the run cut short does not read as interrupted:\n%s", view)
	}
}

// History lists every run a runbook keeps while they fit in two thirds of the
// tab, and says how many more there are, and how many are kept, when they do
// not: it used to show a third with nothing to say the rest was there.
func TestHistoryListsEveryRunKept(t *testing.T) {
	f := runbookHarness(t)
	if err := os.WriteFile(filepath.Join(f.greet, runbook.ManifestName), []byte("name: Greet\ncommand: ./run.sh\nhistory: 50\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.m.runbookReg.Rescan()
	greet, _ := f.m.runbookReg.Lookup("Greet")
	base := time.Now().Add(-time.Hour)
	seed := func(from, to int) {
		for i := from; i < to; i++ {
			at := base.Add(time.Duration(i) * time.Minute)
			e := runbook.Entry{Status: runbook.StatusSucceeded.String(), StartedAt: at, FinishedAt: at.Add(time.Second), Origin: runbook.OriginCLI}
			if err := f.m.runbookReg.History().Save("Greet", greet.Runbook().LogsDir(), 50, e, []string{fmt.Sprint("run ", i)}); err != nil {
				t.Fatal(err)
			}
		}
	}

	seed(0, 15)
	f.selectRow("Greet")
	f.press("enter", "right") // History
	view := plain(f.m)
	if runRows(view) != 15 || strings.Contains(view, "more") || !strings.Contains(view, "keeps 50 runs") {
		t.Fatalf("History does not list all 15 runs kept, with nothing more to scroll to:\n%s", view)
	}

	seed(15, 40)
	f.press("left", "right") // back on History reads the runs again
	view = plain(f.m)
	if n := runRows(view); n >= 40 || n < 15 || !strings.Contains(view, "more · 40 of 50 runs kept") || !strings.Contains(view, "↓ ") {
		t.Fatalf("40 runs do not fill the list with how many more there are (%d listed):\n%s", n, view)
	}
	if !strings.Contains(view, filepath.Join("logs", "log-40.log")) || !strings.Contains(view, "run 39") {
		t.Fatalf("the newest run's log is not under the list:\n%s", view)
	}
	for range 30 {
		f.press("down")
	}
	view = plain(f.m)
	if !strings.Contains(view, "↑ ") || !strings.Contains(view, "▸") || !strings.Contains(view, "run 9") {
		t.Fatalf("scrolling down the runs does not say how many are above, or lost the cursor:\n%s", view)
	}
}

// runRows counts the runs History lists on screen: the divider over the log
// names its run too.
func runRows(view string) int {
	n := 0
	for line := range strings.SplitSeq(view, "\n") {
		if strings.Contains(line, "exit 0") && !strings.Contains(line, "── ") {
			n++
		}
	}
	return n
}

func TestASequenceHasNoTerminals(t *testing.T) {
	f := runbookHarness(t)
	f.selectRow("Both")
	f.press("enter", "t", "e")
	if !f.m.showRunbook || len(f.starts.cmds) != 0 {
		t.Fatalf("t and e in a sequence's view started %d programs, want none", len(f.starts.cmds))
	}
	if !strings.Contains(f.m.flashMsg, "no folder of its own") {
		t.Fatalf("flash = %q, want the reason t and e do nothing", f.m.flashMsg)
	}
	f.press("right", "right") // About
	if !strings.Contains(plain(f.m), "Greet") || !strings.Contains(plain(f.m), "Steps") {
		t.Fatalf("the sequence's About does not list its steps:\n%s", plain(f.m))
	}
}

// Files lists the runbook's folder as tree does, hidden files left out; a
// runbook without a folder of its own has no Files tab.
func TestFilesListsTheRunbooksFolderAsATree(t *testing.T) {
	f := runbookHarness(t)
	if err := os.MkdirAll(filepath.Join(f.greet, "sql"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{filepath.Join("sql", "01-grant.sql"), ".env"} {
		if err := os.WriteFile(filepath.Join(f.greet, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.selectRow("Greet")
	f.press("enter", "right", "right", "right")
	view := plain(f.m)
	for _, want := range []string{"About │ Files", "├── README.md", "├── run.sh", "├── runbook.yaml",
		"└── sql/", "    └── 01-grant.sql", "1 directory, 4 files", "↑↓: select", "enter: view", "e: edit in editor"} {
		if !strings.Contains(view, want) {
			t.Errorf("Files lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, ".env") || strings.Contains(view, "scroll   0%") {
		t.Errorf("Files lists a hidden file, or scrolls a tree that fits:\n%s", view)
	}
	// The tab strip, not the word: the header shows the runbook's folder, a
	// temp dir named after this test, in full where TMPDIR is short (/tmp).
	for _, name := range []string{"Both", "Ask"} {
		f.press("esc")
		f.selectRow(name)
		f.press("enter")
		if view := plain(f.m); strings.Contains(view, "│ Files") {
			t.Errorf("%s has no folder of its own, but has a Files tab:\n%s", name, view)
		}
	}
}

// enter on Files shows the file picked in the tab, rendered as About is; e
// opens it in the editor, in a tab named after it, from there or from the
// tree; a folder has nothing to show.
func TestFilesShowsAFileAndEOpensItInTheEditor(t *testing.T) {
	t.Setenv("VISUAL", "nvim -p")
	f := runbookHarness(t)
	if err := os.MkdirAll(filepath.Join(f.greet, "sql"), 0o750); err != nil {
		t.Fatal(err)
	}
	f.selectRow("Greet")
	f.press("enter", "right", "right", "right", "down", "enter") // run.sh, under README.md
	view := plain(f.m)
	for _, want := range []string{"── run.sh · ", `echo "hello from $(basename "$PWD") as $AWS_PROFILE"`,
		filepath.Join(f.greet, "run.sh"), "↑↓: scroll", "e: edit in editor", "esc: files"} {
		if !strings.Contains(view, want) {
			t.Errorf("the file shown lacks %q:\n%s", want, view)
		}
	}
	if len(f.starts.cmds) != 0 || strings.Contains(view, "runbook.yaml") {
		t.Fatalf("enter should show the file in the tab, instead of the tree, and start nothing:\n%s", view)
	}

	f.press("e")
	if len(f.starts.cmds) != 1 {
		t.Fatalf("e on a file shown started %d programs, want 1", len(f.starts.cmds))
	}
	cmd := f.starts.cmds[0]
	if want := filepath.Join(f.greet, "run.sh"); cmd.Args[len(cmd.Args)-1] != want || !strings.Contains(cmd.Args[1], "nvim -p") || cmd.Dir != f.greet {
		t.Fatalf("started %q in %q, want the editor on %s in the runbook's folder", cmd.Args, cmd.Dir, want)
	}
	if !strings.Contains(plain(f.m), "1 run.sh") {
		t.Fatalf("the editor's tab is not named after the file:\n%s", plain(f.m))
	}

	// Back in Files the file is still shown; esc goes back to the tree, and e
	// there edits the file picked, not the runbook's script.
	f.press("ctrl+t", "left")
	if !strings.Contains(plain(f.m), "esc: files") {
		t.Fatalf("Files should still show the file:\n%s", plain(f.m))
	}
	f.press("esc")
	if !f.m.showRunbook || !strings.Contains(plain(f.m), "▸ ├── run.sh") {
		t.Fatalf("esc should go back to the tree, on the file:\n%s", plain(f.m))
	}
	f.press("up", "e")
	if len(f.starts.cmds) != 2 || f.starts.cmds[1].Args[len(f.starts.cmds[1].Args)-1] != filepath.Join(f.greet, "README.md") {
		t.Fatalf("e on README.md did not open it: %d programs", len(f.starts.cmds))
	}
	f.press("ctrl+t", "left", "left", "end", "enter")
	if len(f.starts.cmds) != 2 || !strings.Contains(f.m.flashMsg, "sql/ is a folder") {
		t.Fatalf("enter on a folder started %d programs (flash %q), want none and why", len(f.starts.cmds), f.m.flashMsg)
	}
}

// An editor that exits takes its tab with it and hands the screen back to the
// tab it was opened from: Files, still showing the file, or Logs for the
// script. A shell keeps its tab, to be restarted; an editor that exits behind
// another tab leaves the selection where it is.
func TestAnEditorClosesItsTabAndGoesBackWhenItExits(t *testing.T) {
	t.Setenv("VISUAL", "vi")
	t.Setenv("SHELL", "/bin/zsh")
	f := runbookHarness(t)
	f.selectRow("Greet")
	f.press("enter", "right", "right", "right", "down", "enter", "e") // run.sh, shown on Files
	tabs := f.m.runbookTabs["Greet"]
	if tabs.Len() != 1 || !strings.Contains(plain(f.m), "1 run.sh") {
		t.Fatalf("e should open run.sh in a tab:\n%s", plain(f.m))
	}
	exited := func(n int) {
		t.Helper()
		f.starts.terms[n].Close()
		f.send(terminalChangedMsg{term: f.starts.terms[n]})
	}
	exited(0)
	view := plain(f.m)
	if tabs.Len() != 0 || strings.Contains(view, "1 run.sh") {
		t.Fatalf("the editor's tab should close with it:\n%s", view)
	}
	if tabs.FixedName(tabs.Selected()) != "Files" || f.m.runbookView.CapturingKeys() ||
		!strings.Contains(view, "── run.sh · ") || !strings.Contains(view, "esc: files") || strings.Contains(view, "TERMINAL") {
		t.Fatalf("Files should be back, showing run.sh, with den's keys:\n%s", view)
	}

	f.press("esc", "left", "left", "left", "e") // the script, from Logs
	if len(f.starts.cmds) != 2 || !strings.Contains(plain(f.m), "1 vi") {
		t.Fatalf("e on Logs should open the script in the editor:\n%s", plain(f.m))
	}
	exited(1)
	if tabs.Len() != 0 || tabs.FixedName(tabs.Selected()) != "Logs" {
		t.Fatalf("the editor should hand the screen back to Logs, not %q", tabs.FixedName(tabs.Selected()))
	}

	f.press("t")
	exited(2)
	if tabs.Len() != 1 || !strings.Contains(plain(f.m), "exited") {
		t.Fatalf("a shell that exits keeps its tab:\n%s", plain(f.m))
	}

	f.press("ctrl+t", "right", "e", "ctrl+t", "left") // from Logs, then back on the shell
	exited(3)
	if tabs.Len() != 1 || tabs.Current() != components.Terminal(f.starts.terms[2]) {
		t.Fatalf("an editor exiting behind the shell should close and leave the shell on screen, %d tabs", tabs.Len())
	}
}

// An editor that fails keeps its tab, so what it said can be read, and enter
// there opens the same file again, in the same tab, rather than a shell.
func TestAFailedEditorKeepsItsTabAndEnterReopensIt(t *testing.T) {
	t.Setenv("VISUAL", "vi")
	f := runbookHarness(t)
	f.selectRow("Greet")
	f.press("enter", "right", "right", "right", "down", "e") // run.sh, from the tree
	f.starts.terms[0].exit(errors.New("exit status 127"))
	f.send(terminalChangedMsg{term: f.starts.terms[0]})
	tabs := f.m.runbookTabs["Greet"]
	if view := plain(f.m); tabs.Len() != 1 || !strings.Contains(view, "exited: exit status 127") {
		t.Fatalf("a failed editor should keep its tab and say how it ended:\n%s", view)
	}

	f.press("enter")
	if len(f.starts.cmds) != 2 {
		t.Fatalf("enter on the failed editor started %d programs, want 2", len(f.starts.cmds))
	}
	cmd := f.starts.cmds[1]
	if want := filepath.Join(f.greet, "run.sh"); len(cmd.Args) < 2 || cmd.Args[len(cmd.Args)-1] != want || !strings.Contains(cmd.Args[1], "vi") {
		t.Fatalf("restarted %s %q, want the editor on %s again", cmd.Path, cmd.Args, want)
	}
	if tabs.Len() != 1 || !strings.Contains(plain(f.m), "1 run.sh") {
		t.Fatalf("the editor should restart in its own tab:\n%s", plain(f.m))
	}

	f.starts.terms[1].Close()
	f.send(terminalChangedMsg{term: f.starts.terms[1]})
	if tabs.Len() != 0 || tabs.FixedName(tabs.Selected()) != "Files" {
		t.Fatalf("the restarted editor should still go back to Files, not %q", tabs.FixedName(tabs.Selected()))
	}
}

// A markdown file is rendered; a file taller than the tab scrolls, saying how
// far down it is; a binary file is only described.
func TestFilesShowsMarkdownLongAndBinaryFiles(t *testing.T) {
	f := runbookHarness(t)
	var long strings.Builder
	for i := range 100 {
		fmt.Fprintf(&long, "line %03d\n", i)
	}
	for name, body := range map[string]string{"long.txt": long.String(), "blob.bin": "\x00\x01\x02"} {
		if err := os.WriteFile(filepath.Join(f.greet, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// blob.bin, long.txt, README.md, run.sh, runbook.yaml
	f.selectRow("Greet")
	f.press("enter", "right", "right", "right", "enter")
	if view := plain(f.m); !strings.Contains(view, "A binary file of 3 B") {
		t.Fatalf("a binary file should only be described:\n%s", view)
	}
	f.press("esc", "down", "enter")
	view := plain(f.m)
	if !strings.Contains(view, "line 000") || strings.Contains(view, "line 099") || !strings.Contains(view, "scroll   0%") {
		t.Fatalf("the top of a long file, with how far down it is:\n%s", view)
	}
	f.press("end")
	if view := plain(f.m); !strings.Contains(view, "line 099") || !strings.Contains(view, "scroll 100%") {
		t.Fatalf("end should show the end of the file:\n%s", view)
	}
	f.press("esc", "down", "enter")
	if view := plain(f.m); !strings.Contains(view, "The friendliest runbook.") || strings.Contains(view, "scroll   0%") {
		t.Fatalf("README.md should be rendered whole:\n%s", view)
	}
	f.press("esc", "esc")
	if f.m.showRunbook {
		t.Fatal("esc on the tree should leave the view")
	}
}

// A tree taller than the tab scrolls with the cursor, and its footer says how
// far down it is.
func TestFilesScrollsAndSaysHowFar(t *testing.T) {
	f := runbookHarness(t)
	for i := range 60 {
		if err := os.WriteFile(filepath.Join(f.greet, fmt.Sprintf("file-%02d.txt", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.selectRow("Greet")
	f.press("enter", "right", "right", "right")
	view := plain(f.m)
	if !strings.Contains(view, "scroll   0%") || !strings.Contains(view, "0 directories, 63 files") || strings.Contains(view, "runbook.yaml") {
		t.Fatalf("the top of a long tree, with its footer:\n%s", view)
	}
	f.press("end")
	view = plain(f.m)
	if !strings.Contains(view, "scroll 100%") || !strings.Contains(view, "▸ └── runbook.yaml") {
		t.Fatalf("end should show the last file, under the cursor:\n%s", view)
	}
	f.press("pgup")
	if view := plain(f.m); strings.Contains(view, "scroll 100%") || strings.Contains(view, "scroll   0%") {
		t.Fatalf("pgup should scroll a page back up, not to either end:\n%s", view)
	}
	f.press("home")
	if view := plain(f.m); !strings.Contains(view, "scroll   0%") || !strings.Contains(view, "▸ .") {
		t.Fatalf("home should go back to the top:\n%s", view)
	}
}

// den's runs fold into the logs folder's row, which leads to History; a file
// a script keeps there is listed.
func TestFilesFoldsTheRunsIntoTheLogsRow(t *testing.T) {
	f := runbookHarness(t)
	f.selectRow("Greet")
	f.press("enter")
	for range 2 {
		f.press("r")
		f.waitRun("Greet")
	}
	if err := os.WriteFile(filepath.Join(f.greet, "logs", "mine.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.press("right", "right", "right")
	view := plain(f.m)
	if !strings.Contains(view, "▸ ├── logs/  2 runs · History") || !strings.Contains(view, "│   └── mine.txt") || strings.Contains(view, "log-01") {
		t.Fatalf("den's runs should fold into the logs row, the script's file listed:\n%s", view)
	}
	f.press("enter")
	if view := plain(f.m); !strings.Contains(view, "enter: whole log") || len(f.starts.cmds) != 0 {
		t.Fatalf("enter on the logs row should go to History:\n%s", view)
	}
}

// A source runbook's file can be edited in its checkout; when the editor
// closes with the file changed, the runbook asks before its next run.
func TestEditingASourceRunbooksFileMakesItAskAgain(t *testing.T) {
	t.Setenv("VISUAL", "vi")
	f := sourceHarness(t)
	commit := strings.Repeat("c", 40)
	if err := runbook.NewTrust(filepath.Join(f.dir, "trust.json")).Add("ops/a", "tree-a", commit); err != nil {
		t.Fatal(err)
	}
	f.m.runbookReg.Rescan()
	f.press("g")
	for range 5 {
		if name, _ := f.m.runbooksPane.Selected(); name == "ops/a" {
			break
		}
		f.press("down")
	}
	f.press("enter", "right", "right", "right")
	if !strings.Contains(f.screen(), "from ops: an edit here is lost when it updates") {
		t.Fatalf("Files should say a source's checkout is replaced by updates:\n%s", f.screen())
	}
	asks := func() bool {
		r, _ := f.m.runbookReg.Lookup("ops/a")
		return r.Runbook().Confirm
	}

	f.press("e") // run.sh
	if len(f.starts.cmds) != 1 {
		t.Fatalf("enter on a source runbook's file started %d programs, want the editor", len(f.starts.cmds))
	}
	script := f.starts.cmds[0].Args[len(f.starts.cmds[0].Args)-1]
	f.starts.terms[0].Close()
	f.send(terminalChangedMsg{term: f.starts.terms[0]})
	if asks() || strings.Contains(f.m.flashMsg, "was edited") {
		t.Fatalf("an editor closed without a change should leave the runbook trusted (flash %q)", f.m.flashMsg)
	}

	// The editor's tab closed with it, back on Files, where run.sh is still
	// picked.
	f.press("e")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho changed here\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.starts.terms[1].Close()
	f.send(terminalChangedMsg{term: f.starts.terms[1]})
	if !strings.Contains(f.m.flashMsg, "ops/a was edited") || !asks() {
		t.Fatalf("an edited checkout should ask before its next run (flash %q)", f.m.flashMsg)
	}
	f.press("r")
	if !strings.Contains(f.screen(), "edited here") {
		t.Fatalf("the confirmation should say why it asks:\n%s", f.screen())
	}
	f.press("n")
}

// A folder deleted while its view is open closes the view at the next rescan,
// and its terminals are stopped.
func TestARunbookRemovedFromDiskLeavesItsView(t *testing.T) {
	f := runbookHarness(t)
	f.selectRow("Greet")
	f.press("enter", "t", "ctrl+t")
	if err := os.RemoveAll(f.greet); err != nil {
		t.Fatal(err)
	}
	f.m.runbookReg.Rescan()
	f.send(runbooksRescannedMsg{})
	if f.m.showRunbook {
		t.Fatal("the view of a runbook that is gone is still open")
	}
	if f.starts.terms[0].closed() == 0 {
		t.Fatal("the gone runbook's shell is still running")
	}
}

// D asks before deleting a runbook, saying all that goes, and only y deletes
// it: its folder, its view, its terminals, which stop first.
func TestDDeletesARunbookAfterAskingOnlyOnY(t *testing.T) {
	f := runbookHarness(t)
	f.selectRow("Greet")
	if !strings.Contains(plain(f.m), "D: delete") {
		t.Errorf("the list's hint line should offer D:\n%s", plain(f.m))
	}
	f.press("r")
	f.waitRun("Greet")
	f.press("enter")
	if !strings.Contains(plain(f.m), "D: delete") {
		t.Errorf("the view's help line should offer D:\n%s", plain(f.m))
	}
	f.press("t", "ctrl+t")

	f.press("D")
	view := flatView(plain(f.m))
	for _, want := range []string{"Delete Greet", "greet/ — ", "1 run, with its log",
		"a program still running in its terminals is stopped first", "sequence Both has it as a step", "Delete it for good?"} {
		if !strings.Contains(view, want) {
			t.Errorf("the question does not say %q:\n%s", want, plain(f.m))
		}
	}
	f.press("enter")
	if _, err := os.Stat(f.greet); err != nil || !strings.Contains(f.m.flashMsg, "kept Greet") {
		t.Fatalf("enter must keep it: %v, flash %q", err, f.m.flashMsg)
	}
	if !f.m.showRunbook {
		t.Fatal("keeping it closed the view")
	}

	f.press("D", "y")
	if _, err := os.Stat(f.greet); !os.IsNotExist(err) {
		t.Fatalf("y did not delete the folder: %v", err)
	}
	if f.m.showRunbook || f.m.flashMsg != "deleted Greet" {
		t.Fatalf("after the delete: view open %v, flash %q", f.m.showRunbook, f.m.flashMsg)
	}
	if f.starts.terms[0].closed() == 0 {
		t.Fatal("its shell is still running")
	}
	if _, ok := f.m.runbookTabs["Greet"]; ok {
		t.Error("its terminals are still kept")
	}
	if _, ok := f.m.runbookReg.Lookup("Greet"); ok {
		t.Error("Greet is still listed")
	}
}

// tab pressed by mistake in a pop-up does nothing: it neither leaves for the
// menu, with the pop-up still drawn, nor reaches the form, where on the last
// field it would submit — a runbook created, or run, by a slip.
func TestTabStaysInsideARunbooksPopUp(t *testing.T) {
	t.Setenv("VISUAL", "vi")
	// No agent on PATH: the name is the form's only field, and its last.
	t.Setenv("PATH", t.TempDir())
	f := runbookHarness(t)
	f.press("n", "tab", "tab")
	for _, r := range "Abc" {
		f.press(string(r))
	}
	if f.m.leftFocused || !strings.Contains(flatView(plain(f.m)), "> Abc") {
		t.Fatalf("after tab, typing should still show in the pop-up (menu focused %v):\n%s", f.m.leftFocused, plain(f.m))
	}
	if _, err := os.Stat(filepath.Join(f.root, "abc")); !os.IsNotExist(err) {
		t.Fatalf("tab created the runbook: %v", err)
	}
	f.press("enter")
	if _, err := os.Stat(filepath.Join(f.root, "abc", runbook.ManifestName)); err != nil {
		t.Fatalf("enter did not create it: %v", err)
	}
	f.press("esc", "esc")

	f.selectRow("Greet")
	f.press("D", "tab")
	if f.m.leftFocused || !strings.Contains(flatView(plain(f.m)), "Delete it for good?") {
		t.Fatalf("tab closed or left the delete question (menu focused %v):\n%s", f.m.leftFocused, plain(f.m))
	}
	f.press("n")
	if _, err := os.Stat(f.greet); err != nil {
		t.Fatal(err)
	}

	// The parameter form, in the runbook view: tab must not run it.
	f.selectRow("Ask")
	f.press("enter", "r", "tab")
	if !f.m.runbookView.CapturingKeys() {
		t.Fatalf("tab closed the parameter form:\n%s", plain(f.m))
	}
	if r, _ := f.m.runbookReg.Lookup("Ask"); r.State().Status != runbook.StatusNeverRun {
		t.Fatalf("tab ran Ask: %v", r.State().Status)
	}
	f.press("esc", "esc")

	// With no pop-up open, tab switches to the menu as before.
	f.press("tab")
	if !f.m.leftFocused {
		t.Fatal("tab no longer leaves the panel for the menu")
	}
}

// n checks the name as it is typed, against the runbooks, the sequences and
// the folders there are, and from the request ↑ goes back to the name to
// change it: the runbook made is the renamed one, with the request kept.
func TestNChecksTheNameAndGoesBackToItFromTheRequest(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f := runbookHarness(t)
	typeText := func(s string) {
		for _, r := range s {
			f.press(string(r))
		}
	}
	clear := func(n int) {
		for range n {
			f.press("backspace")
		}
	}

	f.press("n")
	if view := flatView(plain(f.m)); !strings.Contains(view, "New runbook name") || strings.Contains(view, "↑/shift+tab") {
		t.Fatalf("the pop-up should ask for the new runbook's name:\n%s", plain(f.m))
	}
	for _, c := range []struct{ name, says string }{
		{"greet", `a runbook named "Greet" exists already`},
		{"both", `a sequence named "Both" exists already`},
	} {
		typeText(c.name)
		f.press("enter")
		if view := flatView(plain(f.m)); !strings.Contains(view, c.says) || strings.Contains(view, "↑/shift+tab") {
			t.Fatalf("%q: want %q, and enter not going on:\n%s", c.name, c.says, plain(f.m))
		}
		clear(len(c.name))
	}

	typeText("Report")
	if strings.Contains(flatView(plain(f.m)), "✗") {
		t.Fatalf("a free name is flagged:\n%s", plain(f.m))
	}
	f.press("enter")
	typeText("list")
	f.press("ctrl+j")
	typeText("sort")
	f.press("up", "up")
	typeText("s")
	if view := flatView(plain(f.m)); !strings.Contains(view, "> Reports") || !strings.Contains(view, "sort") {
		t.Fatalf("↑ from the request should go back to the name, keeping the request:\n%s", plain(f.m))
	}
	f.press("enter", "enter")

	if !f.m.showRunbook || f.m.runbookView.Name() != "Reports" {
		t.Fatal("the renamed runbook's view did not open")
	}
	if len(f.starts.cmds) != 1 || f.starts.cmds[0].Args[0] != "list\nsort" {
		t.Fatalf("the agent was started with %q", f.starts.cmds)
	}
	if _, err := os.Stat(filepath.Join(f.root, "report")); !os.IsNotExist(err) {
		t.Fatalf("the first name was made too: %v", err)
	}
}

// However long the request, the pop-up stays inside the pane, which is
// shorter than the terminal: the request scrolls, and the pop-up's last line
// stays in sight.
func TestNALongRequestScrollsInsideThePane(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	f := runbookHarness(t)
	f.press("n", "R", "enter")
	f.send(testkeys.Text(strings.Repeat("and the size of its writer instance, ", 100)))
	view := plain(f.m)
	if lines := strings.Count(view, "\n") + 1; lines != 40 || !strings.Contains(view, "╰") ||
		!strings.Contains(view, "esc: cancel") || !strings.Contains(view, "↑↓ scroll") {
		t.Fatalf("a long request should scroll inside the pane (%d lines):\n%s", lines, view)
	}
}

// D in the list deletes too, and refuses what is running.
func TestDInTheListRefusesARunningRunbook(t *testing.T) {
	f := runbookHarness(t)
	slow := filepath.Join(f.root, "slow")
	if err := os.MkdirAll(slow, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slow, runbook.ManifestName), []byte("command: sleep 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.m.runbookReg.Rescan()
	f.send(msgs.TickMsg{Time: time.Now()})
	f.selectRow("slow")
	if strings.Contains(plain(f.m), "x: cancel") {
		t.Errorf("x is offered with nothing running:\n%s", plain(f.m))
	}
	f.press("r")
	if !strings.Contains(plain(f.m), "x: cancel") {
		t.Errorf("x is not offered for the running runbook:\n%s", plain(f.m))
	}
	f.press("D")
	if !strings.Contains(f.m.flashMsg, "slow is running") || f.m.runbooksPane.Busy() {
		t.Fatalf("D on a running runbook: flash %q, question open %v", f.m.flashMsg, f.m.runbooksPane.Busy())
	}
	f.press("x")
	r, _ := f.m.runbookReg.Lookup("slow")
	deadline := time.Now().Add(5 * time.Second)
	for r.IsRunning() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	f.press("D", "y")
	if _, err := os.Stat(slow); !os.IsNotExist(err) {
		t.Fatalf("the folder is still there: %v", err)
	}
	if name, _ := f.m.runbooksPane.Selected(); name == "slow" {
		t.Error("the list still shows slow")
	}
}

// A running runbook shows in the terminal's tab as an indeterminate progress
// bar, and the bar is gone once it ends.
func TestProgressBarShowsWhileARunbookRuns(t *testing.T) {
	f := runbookHarness(t)
	slow := filepath.Join(f.root, "slow")
	if err := os.MkdirAll(slow, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slow, runbook.ManifestName), []byte("command: sleep 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.m.runbookReg.Rescan()
	f.send(msgs.TickMsg{Time: time.Now()})
	f.selectRow("slow")
	if bar := f.m.View().ProgressBar; bar != nil {
		t.Fatalf("nothing runs but the bar is %+v", bar)
	}
	f.press("r")
	if bar := f.m.View().ProgressBar; bar == nil || bar.State != tea.ProgressBarIndeterminate {
		t.Fatalf("a running runbook should show an indeterminate bar, got %+v", bar)
	}
	f.press("x")
	r, _ := f.m.runbookReg.Lookup("slow")
	deadline := time.Now().Add(5 * time.Second)
	for r.IsRunning() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if bar := f.m.View().ProgressBar; bar != nil {
		t.Fatalf("the run ended but the bar is %+v", bar)
	}
}

// Deleting a sequence takes it out of den.yaml.
func TestDDeletesASequenceFromDenYaml(t *testing.T) {
	f := runbookHarness(t)
	cfgPath := f.m.configPath
	if err := os.WriteFile(cfgPath, []byte("runbooks:\n  dirs: ["+f.root+"]\n  items:\n    - name: Ask\n      command: echo $DEN_PARAM_WHO\n      params:\n        - name: who\n          required: true\n  sequences:\n    - name: Both\n      steps: [Greet]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.selectRow("Both")
	f.press("D")
	if view := flatView(plain(f.m)); !strings.Contains(view, "its entry in den.yaml (runbooks.sequences)") || !strings.Contains(view, "its steps: Greet") {
		t.Fatalf("the question:\n%s", plain(f.m))
	}
	f.press("y")
	data, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(data), "Both") || !strings.Contains(string(data), "name: Ask") {
		t.Fatalf("den.yaml:\n%s", data)
	}
	if _, ok := f.m.runbookReg.LookupSequence("Both"); ok {
		t.Error("Both is still listed")
	}
}

// The parameter prompt is text: a q typed into it must not quit den.
func TestParameterPromptTakesQAsText(t *testing.T) {
	f := runbookHarness(t)
	f.selectRow("Ask")
	f.press("r", "q")
	if f.quit {
		t.Fatal("q typed into a parameter quit den")
	}
	f.press("esc")
	if f.m.runbooksPane.Busy() {
		t.Fatal("esc should abandon the prompt")
	}
}

func TestNNewRunbookScaffoldsItAndOpensTheEditor(t *testing.T) {
	t.Setenv("VISUAL", "vi")
	// No agent on PATH: the form then asks only for the name.
	t.Setenv("PATH", t.TempDir())
	f := runbookHarness(t)
	f.press("n")
	// The form names the folder the runbook goes into, as the name is typed.
	// A temporary directory is long enough to wrap: its end is what counts.
	// The wrap can fall after the hyphen in rotate-keys, depending on how
	// long the directory's random name is, so a hyphen at a break is rejoined.
	sep := string(filepath.Separator)
	says := func(folder string) bool {
		view := strings.ReplaceAll(flatView(plain(f.m)), "- ", "-")
		return strings.Contains(view, "Goes into") &&
			strings.Contains(view, "runbooks"+sep+folder+sep+" — change where with runbooks.dirs in den.yaml")
	}
	if !says("<name>") {
		t.Errorf("before a name, the form should name the runbooks directory:\n%s", plain(f.m))
	}
	for _, r := range "Rotate keys" {
		f.press(string(r))
	}
	if !says("rotate-keys") {
		t.Errorf("with a name, the form should name the folder it makes:\n%s", plain(f.m))
	}
	f.press("enter")
	dir := filepath.Join(f.root, "rotate-keys")
	if _, err := os.Stat(filepath.Join(dir, runbook.ManifestName)); err != nil {
		t.Fatalf("no runbook folder was made: %v", err)
	}
	if !f.m.showRunbook || f.m.runbookView.Name() != "Rotate keys" {
		t.Fatal("the new runbook's view did not open")
	}
	if len(f.starts.cmds) != 1 || f.starts.cmds[0].Args[len(f.starts.cmds[0].Args)-1] != filepath.Join(dir, "run.sh") {
		t.Fatalf("the editor was not opened on the new run.sh: %+v", f.starts.cmds)
	}
}

// Without runbooks.dirs there is nowhere to make a folder: the form says how to
// get one before a name is typed for nothing.
func TestNWithoutRunbooksDirsSaysWhatToAdd(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	h := &harness{t: t, m: New(managerWith(), Options{
		ConfigPath: filepath.Join(dir, "den.yaml"),
		Config: &config.Config{Runbooks: &config.RunbooksConfig{
			Items: []config.RunbookSpec{{Name: "Hello", Command: "echo hi"}},
		}},
		Version:        "test",
		RunbookHistory: runbook.NewHistory(filepath.Join(dir, "state")),
	})}
	t.Cleanup(func() { h.m.shutdown() })
	h.send(tea.WindowSizeMsg{Width: 140, Height: 40})
	for h.m.currentPane() != paneRunbooks {
		h.press("down")
	}
	h.press("right", "n")
	if view := flatView(plain(h.m)); !strings.Contains(view, "No runbooks directory: add one to runbooks.dirs in den.yaml") {
		t.Fatalf("the form should say runbooks.dirs is missing:\n%s", plain(h.m))
	}
}

// m moves a runbook defined by a header script into a folder of its own, after
// a question; it keeps its name, its last run and its terminals.
func TestMMovesAnOldFormatRunbookIntoAFolder(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	f := runbookHarness(t)
	script := filepath.Join(f.root, "legacy.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n# den:name Legacy\n#\n# Does legacy things.\necho legacy\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.m.runbookReg.Rescan()
	f.send(msgs.TickMsg{Time: time.Now()})
	f.selectRow("Legacy")
	f.press("r")
	f.waitRun("Legacy")
	f.press("enter", "t", "ctrl+t")
	if !strings.Contains(plain(f.m), "single script in") {
		t.Fatalf("the view does not say the runbook is one script:\n%s", plain(f.m))
	}

	f.press("m")
	if view := plain(f.m); !strings.Contains(view, "Move into its own folder") || !strings.Contains(view, "run.sh") {
		t.Fatalf("m did not ask first:\n%s", view)
	}
	f.press("n")
	if _, err := os.Stat(script); err != nil {
		t.Fatal("n moved the script anyway")
	}
	f.press("m", "y")
	if _, err := os.Stat(filepath.Join(f.root, "legacy", "run.sh")); err != nil {
		t.Fatalf("y did not move it: %v", err)
	}
	if !f.m.showRunbook || f.m.runbookView.Name() != "Legacy" {
		t.Fatal("the view of the moved runbook closed")
	}
	view := plain(f.m)
	if strings.Contains(view, "single script in") || !strings.Contains(view, "legacy") || !strings.Contains(view, "1 sh") {
		t.Fatalf("after the move the view should show its folder and keep its terminal:\n%s", view)
	}
	if r, _ := f.m.runbookReg.Lookup("Legacy"); r.State().Status != runbook.StatusSucceeded {
		t.Fatal("the moved runbook lost its last run")
	}

	// Anything that is not a header script says why it does not move.
	f.press("esc")
	f.selectRow("Ask")
	f.press("m")
	if !strings.Contains(f.m.flashMsg, "den.yaml") {
		t.Fatalf("m on a den.yaml runbook flashed %q", f.m.flashMsg)
	}
}

// The list is a table with a header, whose columns a narrow terminal gives up
// in a fixed order — runs, last run, description, type — and whose frame keeps
// its height at any width.
func TestRunbooksTableColumnsFollowTheWidth(t *testing.T) {
	f := runbookHarness(t)
	cases := []struct {
		width    int
		has, not []string
	}{
		{80, []string{"STATUS", "NAME", "ENV · PROFILE", "⌨"}, []string{"TYPE", "DESCRIPTION", "LAST RUN", "RUNS"}},
		{120, []string{"STATUS", "NAME", "TYPE", "DESCRIPTION", "ENV · PROFILE"}, []string{"LAST RUN", "RUNS"}},
		{170, []string{"STATUS", "NAME", "TYPE", "DESCRIPTION", "ENV · PROFILE", "LAST RUN", "RUNS", "⌨"}, nil},
	}
	for _, c := range cases {
		f.send(tea.WindowSizeMsg{Width: c.width, Height: 30})
		view := plain(f.m)
		if got := strings.Count(view, "\n") + 1; got != 30 {
			t.Errorf("%d columns: the frame is %d lines, want 30", c.width, got)
		}
		for _, want := range c.has {
			if !strings.Contains(view, want) {
				t.Errorf("%d columns: no %q column:\n%s", c.width, want, view)
			}
		}
		for _, gone := range c.not {
			if strings.Contains(view, gone) {
				t.Errorf("%d columns: %q should have given way:\n%s", c.width, gone, view)
			}
		}
	}
	view := plain(f.m)
	for _, want := range []string{"Says hello", "dev · dev-profile", "seq · 1", "command", "○ never"} {
		if !strings.Contains(view, want) {
			t.Errorf("the wide table lacks %q:\n%s", want, view)
		}
	}
}

// RUNS counts every run of a runbook, and how many of the runs it keeps failed.
func TestRunbooksTableCountsRunsAndFailures(t *testing.T) {
	f := runbookHarness(t)
	f.send(tea.WindowSizeMsg{Width: 200, Height: 30})
	row := func() string {
		for _, line := range strings.Split(plain(f.m), "\n") {
			if strings.Contains(line, "Says hello") {
				return strings.Join(strings.Fields(line), " ")
			}
		}
		t.Fatal("no Greet row")
		return ""
	}
	run := func() {
		f.selectRow("Greet")
		f.press("r")
		f.waitRun("Greet")
	}
	run()
	run()
	// RUNS is the row's last cell: Greet has no terminal open.
	if got := row(); !strings.HasSuffix(got, "s 2") {
		t.Fatalf("after two runs the row reads %q, want RUNS 2", got)
	}
	if err := os.WriteFile(filepath.Join(f.greet, "run.sh"), []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	run()
	if got := row(); !strings.HasSuffix(got, "s 3 · 1✗") {
		t.Fatalf("after a failed run the row reads %q, want RUNS 3 · 1✗", got)
	}
}

// n with a description has an agent write the runbook: it starts in a
// terminal tab of the new runbook's view, in its folder, with den's MCP server
// and guide, and keeps esc — Claude Code interrupts with it — while ctrl+t
// gives den the keys back.
func TestNWithADescriptionStartsAnAgentInTheFolder(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f := runbookHarness(t)

	f.press("n")
	if !strings.Contains(plain(f.m), "Ask an agent to write it (optional)") {
		t.Fatalf("with claude installed the form should offer an agent to write the runbook:\n%s", plain(f.m))
	}
	if strings.Contains(plain(f.m), "Agent  ‹") {
		t.Fatalf("with one agent there is nothing to pick:\n%s", plain(f.m))
	}
	for _, r := range "RDS status" {
		f.press(string(r))
	}
	f.press("enter")
	for _, r := range "check RDS in Rust" {
		f.press(string(r))
	}
	f.press("enter")

	if !f.m.showRunbook || f.m.runbookView.Name() != "RDS status" {
		t.Fatal("the new runbook's view did not open")
	}
	if len(f.starts.cmds) != 1 {
		t.Fatalf("started %d programs, want the agent", len(f.starts.cmds))
	}
	cmd := f.starts.cmds[0]
	args := strings.Join(cmd.Args, "\n")
	if cmd.Path != filepath.Join(bin, "claude") || cmd.Dir != filepath.Join(f.root, "rds-status") ||
		cmd.Args[0] != "check RDS in Rust" || !strings.Contains(args, "--mcp-config") || !strings.Contains(args, "Writing a den runbook") {
		t.Fatalf("the agent was started as %s %.300q in %s", cmd.Path, cmd.Args, cmd.Dir)
	}
	if !strings.Contains(plain(f.m), "1 claude") {
		t.Fatalf("no claude tab:\n%s", plain(f.m))
	}

	if view := plain(f.m); !strings.Contains(view, "esc esc/ctrl+t: den keys") {
		t.Fatalf("the help line should offer esc twice in the agent's tab:\n%s", view)
	}
	f.press("esc")
	if got := f.starts.terms[0].typed(); got != "esc" || !f.m.showRunbook || !f.m.runbookView.CapturingKeys() {
		t.Fatalf("one esc should reach the agent (got %q) and keep its keys", got)
	}
	f.press("esc")
	if got := f.starts.terms[0].typed(); got != "esc" || !f.m.showRunbook || f.m.runbookView.CapturingKeys() {
		t.Fatalf("a second esc should give den the keys, not reach the agent (got %q)", got)
	}
	f.press("enter", "ctrl+t")
	if f.m.runbookView.CapturingKeys() {
		t.Fatal("ctrl+t should give den the keys back")
	}
}

// With several runbooks.agents the pop-up picks one: tab reaches the row, →
// picks the next, enter starts that command, its own words before the request.
// A restarted tab starts the same agent again.
func TestNPicksTheAgentThatWritesTheRunbook(t *testing.T) {
	bin := t.TempDir()
	for _, n := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(bin, n), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f := runbookHarnessWith(t, []config.AgentSpec{
		{Name: "Claude Opus", Command: "claude --model opus"},
		{Name: "Codex Mini", Command: "codex --model mini"},
		{Name: "Claude Haiku", Command: "claude --model haiku"},
	})

	f.press("n")
	if view := plain(f.m); !strings.Contains(view, "Agent  ‹ Claude Opus ›  (1/3)") {
		t.Fatalf("the pop-up should offer the three agents, the first picked:\n%s", view)
	}
	for _, r := range "RDS status" {
		f.press(string(r))
	}
	f.press("enter")
	for _, r := range "check RDS in Rust" {
		f.press(string(r))
	}
	f.press("tab", "right")
	if view := plain(f.m); !strings.Contains(view, "Agent  ‹ Codex Mini ›  (2/3)") ||
		!strings.Contains(flatView(view), "Codex Mini writes it in a terminal tab") {
		t.Fatalf("→ should pick the second agent:\n%s", view)
	}
	f.press("enter")

	if len(f.starts.cmds) != 1 {
		t.Fatalf("started %d programs, want the agent", len(f.starts.cmds))
	}
	cmd := f.starts.cmds[0]
	if cmd.Path != filepath.Join(bin, "codex") || strings.Join(cmd.Args[:2], " ") != "--model mini" {
		t.Fatalf("the agent was started as %s %.200q, want codex with its own words first", cmd.Path, cmd.Args)
	}
	if !strings.Contains(strings.Join(cmd.Args, "\n"), "\ncheck RDS in Rust") {
		t.Fatalf("the request is missing: %.300q", cmd.Args)
	}
	if view := plain(f.m); !strings.Contains(view, "1 Codex Mini") {
		t.Fatalf("the tab should carry the agent's name:\n%s", view)
	}
	if got := f.m.agentTabs["RDS status"]; len(got) != 1 {
		t.Fatalf("the agent tab was not noted: %v", got)
	}
	for _, name := range f.m.agentTabs["RDS status"] {
		if name != "Codex Mini" {
			t.Fatalf("the tab remembers %q, want Codex Mini for a restart", name)
		}
	}
}

// fakeAgents puts fake claude and codex programs first on PATH.
func fakeAgents(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	for _, n := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(bin, n), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin
}

// setAgent gives the Greet runbook an agent: line of its runbook.yaml.
func (f runbookFixture) setAgent(value string) {
	f.t.Helper()
	manifest := filepath.Join(f.greet, runbook.ManifestName)
	if err := os.WriteFile(manifest, []byte("name: Greet\ndescription: Says hello\ncommand: ./run.sh\nenv: dev\nagent: "+value+"\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
	f.m.runbookReg.Rescan()
	f.send(msgs.TickMsg{Time: time.Now()})
}

// E asks what to change, then starts the runbook's own agent, the model it
// names among its words, in the runbook's folder, with the request and the
// instructions for changing a runbook that exists.
func TestEEditsARunbookWithItsOwnAgent(t *testing.T) {
	bin := fakeAgents(t)
	f := runbookHarness(t)
	f.setAgent("claude --model opus")
	f.selectRow("Greet")
	f.press("enter")
	if view := plain(f.m); !strings.Contains(view, "E: edit with claude opus") {
		t.Fatalf("the key line should name the agent:\n%s", view)
	}

	f.press("E")
	view := flatView(plain(f.m))
	if !strings.Contains(view, "What should change in Greet?") || !strings.Contains(view, "claude opus changes it in a terminal tab") {
		t.Fatalf("E should ask what to change, naming the agent:\n%s", view)
	}
	if len(f.starts.cmds) != 0 {
		t.Fatal("nothing starts before the request is answered")
	}
	for _, r := range "add a retry" {
		f.press(string(r))
	}
	f.press("enter")

	if len(f.starts.cmds) != 1 {
		t.Fatalf("started %d programs, want the agent", len(f.starts.cmds))
	}
	cmd := f.starts.cmds[0]
	if cmd.Path != filepath.Join(bin, "claude") || cmd.Dir != f.greet {
		t.Fatalf("started %s in %s, want claude in the runbook's folder", cmd.Path, cmd.Dir)
	}
	if got := strings.Join(cmd.Args[:3], " "); got != "--model opus add a retry" {
		t.Fatalf("arguments start %q, want the agent's own words, then the request", got)
	}
	if all := strings.Join(cmd.Args, "\n"); !strings.Contains(all, "changing the den runbook in "+f.greet) || strings.Contains(all, "den created it") {
		t.Fatalf("the agent should be told to change the runbook, not write one: %.400q", all)
	}
	if view := plain(f.m); !strings.Contains(view, "1 claude opus") {
		t.Fatalf("the tab should be named after the agent:\n%s", view)
	}
	for id, name := range f.m.agentTabs["Greet"] {
		if name != "claude --model opus" || !f.m.agentEdits["Greet"][id] {
			t.Fatalf("the tab remembers %q (edit %v), want the command and that it edits", name, f.m.agentEdits["Greet"][id])
		}
		// Restarting the tab starts the same command, still changing the runbook.
		f.send(msgs.OpenRunbookTerminalMsg{Name: "Greet", TabID: id})
		if len(f.starts.cmds) != 2 {
			t.Fatalf("a restart started %d programs in all, want 2", len(f.starts.cmds))
		}
		again := f.starts.cmds[1]
		if got := strings.Join(again.Args[:2], " "); got != "--model opus" ||
			!strings.Contains(strings.Join(again.Args, "\n"), "changing the den runbook") {
			t.Fatalf("the restart was %.300q, want claude --model opus changing the runbook", again.Args)
		}
	}
}

// From the list E opens the runbook's view with the question; esc drops it,
// and without an agent setting runbooks.agent's choice starts.
func TestEFromTheListAsksAndEscCancels(t *testing.T) {
	bin := fakeAgents(t)
	f := runbookHarness(t)
	f.selectRow("Greet")
	f.press("E")
	if !f.m.showRunbook || f.m.runbookView.Name() != "Greet" || !strings.Contains(plain(f.m), "What should change in Greet?") {
		t.Fatalf("E in the list should open the view with the question:\n%s", plain(f.m))
	}
	f.press("esc")
	if strings.Contains(plain(f.m), "What should change") || len(f.starts.cmds) != 0 {
		t.Fatal("esc should drop the question and start nothing")
	}
	f.press("E", "enter")
	if len(f.starts.cmds) != 1 || f.starts.cmds[0].Path != filepath.Join(bin, "claude") {
		t.Fatalf("an empty request should start the default agent: %+v", f.starts.cmds)
	}
	if args := f.starts.cmds[0].Args; len(args) == 0 || args[0] != "--mcp-config" {
		t.Fatalf("an empty request starts the agent without a prompt: %.200q", args)
	}
}

// A runbook that is no folder of its own, a sequence, and an agent den cannot
// start say why instead of asking.
func TestERefusesWhatAnAgentCannotEdit(t *testing.T) {
	fakeAgents(t)
	f := runbookHarness(t)
	for name, want := range map[string]string{
		"Both": "a sequence has no folder of its own",
		"Ask":  "defined in den.yaml",
	} {
		f.selectRow(name)
		f.press("E")
		if view := flatView(plain(f.m)); !strings.Contains(view, want) || strings.Contains(view, "What should change") {
			t.Errorf("E on %s should say %q:\n%s", name, want, view)
		}
		f.press("esc")
	}

	f.setAgent("./my-agent.sh")
	f.selectRow("Greet")
	f.press("E")
	if view := flatView(plain(f.m)); !strings.Contains(view, "set kind") || strings.Contains(view, "What should change") {
		t.Errorf("an agent of no known kind should be refused, naming the fix:\n%s", view)
	}
}

// sourceFixture is a den whose den.yaml, on disk, names the runbook source
// ops, checked out without git with the runbooks a and b, and whose Runbooks
// pane is focused.
type sourceFixture struct {
	*harness
	starts  *programStarts
	dir     string
	cfgPath string
}

func sourceHarness(t *testing.T) sourceFixture {
	t.Helper()
	dir := t.TempDir()
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	aws := filepath.Join(dir, "aws-config")
	write(aws, "", 0o600)
	cfgPath := filepath.Join(dir, "den.yaml")
	write(cfgPath, `# mine
aws:
  config_path: `+aws+`
  credentials_path: `+aws+`
environments:
  dev:
    aws_profile: dev-profile
    aws_region_code: eu-central-1
runbooks:
  dirs:
    - ./mine
  sources:
    - name: ops
      url: acme/ops
      env_map:
        prod: dev
`, 0o600)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	store := source.NewStore(filepath.Join(dir, "data"))
	src := cfg.Runbooks.Sources[0]
	commit := strings.Repeat("c", 40)
	tree := store.TreeDir(src, commit)
	write(filepath.Join(tree, "a", runbook.ManifestName), "description: Alpha\ncommand: ./run.sh\nenv: prod\n", 0o600)
	write(filepath.Join(tree, "a", "run.sh"), "#!/bin/sh\necho alpha\n", 0o700)
	write(filepath.Join(tree, "b", runbook.ManifestName), "description: Bravo\ncommand: ./run.sh\n", 0o600)
	write(filepath.Join(tree, "b", "run.sh"), "#!/bin/sh\necho bravo\n", 0o700)
	if err := store.SaveIndex(src, commit, source.Index{Root: "r", Runbooks: map[string]string{"a": "tree-a", "b": "tree-b"}}); err != nil {
		t.Fatal(err)
	}
	if err := source.WriteLock(source.LockPath(cfgPath), source.Lock{Sources: map[string]source.Locked{"ops": {URL: src.URL, Commit: commit}}}); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, m: New(managerWith(), Options{
		ConfigPath:     cfgPath,
		Config:         cfg,
		Version:        "test",
		RunbookHistory: runbook.NewHistory(filepath.Join(dir, "state")),
		RunbookSources: store,
		RunbookTrust:   runbook.NewTrust(filepath.Join(dir, "trust.json")),
	})}
	t.Cleanup(func() { h.m.shutdown() })
	h.send(tea.WindowSizeMsg{Width: 140, Height: 40})
	starts := &programStarts{}
	h.m.startProgram = starts.start
	for h.m.currentPane() != paneRunbooks {
		h.press("down")
	}
	h.press("right")
	return sourceFixture{harness: h, starts: starts, dir: dir, cfgPath: cfgPath}
}

func (f sourceFixture) denYAML() string {
	data, _ := os.ReadFile(f.cfgPath)
	return string(data)
}

func (f sourceFixture) screen() string { return plain(f.m) }

func TestSOpensTheSourcesPopUp(t *testing.T) {
	f := sourceHarness(t)
	if !strings.Contains(f.screen(), "s: sources") {
		t.Errorf("the hint line should offer s:\n%s", f.screen())
	}
	f.press("s")
	screen := f.screen()
	for _, want := range []string{"Runbook sources", "ops", "github.com/acme/ops", "cccccc", "2 of 2 installed", "u: update", "a: add"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the pop-up lacks %q:\n%s", want, screen)
		}
	}
	f.press("esc")
	if strings.Contains(f.screen(), "Runbook sources") {
		t.Error("esc should close the pop-up")
	}
}

func TestSpaceInstallsAndUninstallsASourceRunbook(t *testing.T) {
	f := sourceHarness(t)
	f.press("s", "enter")
	if !strings.Contains(f.screen(), "ops/a") || !strings.Contains(f.screen(), "Alpha") {
		t.Fatalf("enter should list the source's runbooks:\n%s", f.screen())
	}
	f.press("space")
	if !strings.Contains(f.denYAML(), "      exclude:\n        - a\n") {
		t.Fatalf("den.yaml after uninstalling a:\n%s", f.denYAML())
	}
	if _, ok := f.m.runbookReg.Lookup("ops/a"); ok {
		t.Error("ops/a is still listed")
	}
	if !strings.Contains(f.screen(), "Runbook sources") {
		t.Fatal("the pop-up should stay open between toggles")
	}
	f.press("space")
	if strings.Contains(f.denYAML(), "exclude") {
		t.Fatalf("den.yaml after installing a again:\n%s", f.denYAML())
	}
	if _, ok := f.m.runbookReg.Lookup("ops/a"); !ok {
		t.Error("ops/a is not listed again")
	}
	f.press("esc", "esc")
	if strings.Contains(f.screen(), "Runbook sources") {
		t.Error("esc from the runbooks goes back to the sources, and esc again closes")
	}
}

func TestConfirmShowsWhyASourceRunbookAsks(t *testing.T) {
	f := sourceHarness(t)
	f.press("g")
	for range 5 {
		if name, _ := f.m.runbooksPane.Selected(); name == "ops/a" {
			break
		}
		f.press("down")
	}
	f.press("r")
	screen := f.screen()
	for _, want := range []string{"Confirm before running", "ops · github.com/acme/ops @ ccccccc", "new from ops"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the confirmation lacks %q:\n%s", want, screen)
		}
	}
	f.press("n")
}

// E on a source runbook offers the copy, as e does: an agent must not edit a
// checkout the source's other runbooks share.
func TestEOnASourceRunbookOffersAForkNotAnAgent(t *testing.T) {
	fakeAgents(t)
	f := sourceHarness(t)
	f.press("g")
	for range 5 {
		if name, _ := f.m.runbooksPane.Selected(); name == "ops/a" {
			break
		}
		f.press("down")
	}
	f.press("enter", "E")
	if !strings.Contains(f.screen(), "Copy it into your runbooks") || strings.Contains(f.screen(), "What should change") {
		t.Fatalf("E on a source runbook should offer a copy:\n%s", f.screen())
	}
	if len(f.starts.cmds) != 0 {
		t.Fatal("nothing should start in the source's checkout")
	}
}

func TestEOnASourceRunbookOffersAFork(t *testing.T) {
	t.Setenv("VISUAL", "vi")
	f := sourceHarness(t)
	f.press("g")
	for range 5 {
		if name, _ := f.m.runbooksPane.Selected(); name == "ops/a" {
			break
		}
		f.press("down")
	}
	f.press("enter", "e")
	if !strings.Contains(f.screen(), "Copy it into your runbooks") {
		t.Fatalf("e on a source runbook should offer a copy:\n%s", f.screen())
	}
	if view := flatView(f.screen()); !strings.Contains(view, "The copy goes into") ||
		!strings.Contains(view, filepath.Join("mine", "a")+string(filepath.Separator)+" and is yours") {
		t.Errorf("the question should name the folder the copy goes into:\n%s", f.screen())
	}
	if len(f.starts.cmds) != 0 {
		t.Fatal("nothing should be opened in the source's checkout")
	}
	f.press("y")
	copied := filepath.Join(f.dir, "mine", "a", runbook.ManifestName)
	if data, err := os.ReadFile(copied); err != nil || !strings.Contains(string(data), "env: dev") {
		t.Fatalf("the copy: %q, %v", data, err)
	}
	if !f.m.showRunbook || f.m.runbookView.Name() != "a" {
		t.Fatalf("the copy's view should open, got %q", f.m.runbookView.Name())
	}
	if len(f.starts.cmds) != 1 || !strings.Contains(strings.Join(f.starts.cmds[0].Args, " "), filepath.Join(f.dir, "mine", "a")) {
		t.Fatalf("the editor should open the copy: %+v", f.starts.cmds)
	}
}

func TestRemoveASourceFromThePopUp(t *testing.T) {
	f := sourceHarness(t)
	f.press("s", "d")
	if !strings.Contains(f.screen(), "Remove ops?") {
		t.Fatalf("d should ask first:\n%s", f.screen())
	}
	f.press("y")
	if strings.Contains(f.denYAML(), "sources:") {
		t.Fatalf("den.yaml:\n%s", f.denYAML())
	}
	if !strings.Contains(f.screen(), "No runbook sources yet") {
		t.Errorf("the pop-up:\n%s", f.screen())
	}
}

func TestAddASourceFromThePopUp(t *testing.T) {
	// The fetch that follows runs git on a local path that does not exist:
	// no network, and none of the developer's git config.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	f := sourceHarness(t)
	repo := filepath.Join(f.dir, "team-runbooks")
	f.press("s", "a")
	f.send(testkeys.Paste(repo))
	f.press("enter", "enter", "enter", "enter")
	if !strings.Contains(f.denYAML(), "    - name: team-runbooks\n      url: "+repo+"\n") {
		t.Fatalf("den.yaml after adding:\n%s", f.denYAML())
	}
	if !strings.Contains(f.screen(), "team-runbooks") {
		t.Errorf("the pop-up should list the new source at once:\n%s", f.screen())
	}
}

// ── terminal background ──────────────────────────────────────────────────────

// A config reload builds the Runbooks pane again; the palette the terminal
// reported must not fall back to dark with it.
func TestReloadKeepsTheTerminalsPalette(t *testing.T) {
	f := runbookHarness(t)
	f.m.cfgFile = reloadTo(f.m.manager, func() []service.Entry { return nil })
	f.send(bg(false))
	if f.m.runbooksPane.Dark() {
		t.Fatal("the light reply should reach the Runbooks pane")
	}
	f.press("R")
	if !strings.Contains(f.m.flashMsg, "config reloaded") {
		t.Fatalf("R did not reload: flash = %q", f.m.flashMsg)
	}
	if f.m.runbooksPane.Dark() || !f.m.light {
		t.Fatal("the reload rebuilt the Runbooks pane in the dark palette")
	}
}

// The reply reaches a runbook view that is open, and one opened afterwards.
func TestBackgroundColorReachesTheRunbookView(t *testing.T) {
	f := runbookHarness(t)
	f.selectRow("Greet")
	f.press("enter")
	if !f.m.showRunbook || !f.m.runbookView.Dark() {
		t.Fatal("the view should open in the dark palette until the terminal replies")
	}
	f.send(bg(false))
	if f.m.runbookView.Dark() {
		t.Fatal("a light reply should reach the open runbook view")
	}
	f.send(bg(true))
	if !f.m.runbookView.Dark() {
		t.Fatal("a dark reply should reach the open runbook view")
	}
	f.send(bg(false))
	f.press("esc", "enter")
	if !f.m.showRunbook || f.m.runbookView.Dark() {
		t.Fatal("a view opened after the reply should start in its palette")
	}
}
