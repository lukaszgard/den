package tui

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/tui/testkeys"
)

// draftsHarness is a den keeping drafts, with fake claude and codex on PATH so
// n asks what the agent should write.
func draftsHarness(t *testing.T, keep string) runbookFixture {
	t.Helper()
	fakeAgents(t)
	return runbookHarnessOpts(t, fixtureOpts{drafts: true, keep: keep})
}

// typeNew opens n's form and types a name and a request into it.
func (f runbookFixture) typeNew(name, request string) {
	f.t.Helper()
	f.press("n")
	f.send(testkeys.Text(name))
	f.press("enter")
	f.send(testkeys.Text(request))
}

func TestEscInTheNewFormKeepsADraftOnY(t *testing.T) {
	f := draftsHarness(t, "")
	f.typeNew("RDS status", "show my clusters")
	f.press("esc")
	if view := flatView(plain(f.m)); !strings.Contains(view, "Keep as a draft? y: keep n: discard esc: back to it") {
		t.Fatalf("esc with text typed should ask to keep it:\n%s", view)
	}
	f.press("y")
	if f.m.runbooksPane.Modal() {
		t.Fatal("y should close the form")
	}
	if view := plain(f.m); !strings.Contains(view, "saved as draft") {
		t.Errorf("no word that it was kept:\n%s", view)
	}
	drafts := f.m.runbookReg.Drafts()
	if len(drafts) != 1 {
		t.Fatalf("drafts = %+v, want the one kept", drafts)
	}
	if d := drafts[0]; d.Kind != runbook.DraftNew || d.Name != "RDS status" || d.Request != "show my clusters" || d.Agent != "claude" {
		t.Errorf("kept %+v", d)
	}
}

func TestEscInTheNewFormDiscardsOnN(t *testing.T) {
	f := draftsHarness(t, "")
	f.typeNew("RDS status", "show my clusters")
	f.press("esc", "n")
	if f.m.runbooksPane.Modal() {
		t.Fatal("n should close the form")
	}
	if drafts := f.m.runbookReg.Drafts(); len(drafts) != 0 {
		t.Fatalf("n kept %+v", drafts)
	}
}

// esc on the question goes back to the form, the text as it was.
func TestEscOnTheQuestionGoesBackToTheForm(t *testing.T) {
	f := draftsHarness(t, "")
	f.typeNew("RDS status", "show my clusters")
	f.press("esc", "esc")
	view := plain(f.m)
	if !f.m.runbooksPane.Modal() || strings.Contains(view, "Keep as a draft?") || !strings.Contains(view, "show my clusters") {
		t.Fatalf("a second esc should go back to the form, text and all:\n%s", view)
	}
}

func TestEscInAnEmptyFormClosesItWithoutAsking(t *testing.T) {
	f := draftsHarness(t, "")
	f.press("n", "esc")
	if f.m.runbooksPane.Modal() {
		t.Fatalf("an empty form should close on esc:\n%s", plain(f.m))
	}
	if drafts := f.m.runbookReg.Drafts(); len(drafts) != 0 {
		t.Fatalf("an empty form left %+v", drafts)
	}
}

// Without a place to keep drafts, esc closes the form as it always did.
func TestEscWithoutDraftsClosesTheForm(t *testing.T) {
	fakeAgents(t)
	f := runbookHarness(t)
	f.typeNew("RDS status", "show my clusters")
	f.press("esc")
	if f.m.runbooksPane.Modal() {
		t.Fatalf("esc should close the form when no draft can be kept:\n%s", plain(f.m))
	}
}

// ctrl+c quits from the form, and what was typed since the last save is kept.
func TestQuittingWithTheFormOpenKeepsADraft(t *testing.T) {
	f := draftsHarness(t, "")
	f.typeNew("RDS status", "show my clusters")
	f.press("ctrl+c")
	if !f.quit {
		t.Fatal("ctrl+c should quit")
	}
	drafts := f.m.runbookReg.Drafts()
	if len(drafts) != 1 || drafts[0].Request != "show my clusters" {
		t.Fatalf("drafts = %+v, want what was typed", drafts)
	}
}

// E's question keeps a draft too: a change to Greet.
func TestEscInTheEditFormKeepsADraft(t *testing.T) {
	f := draftsHarness(t, "")
	f.selectRow("Greet")
	f.press("E")
	f.send(testkeys.Text("ask for the name"))
	f.press("esc")
	if view := flatView(plain(f.m)); !strings.Contains(view, "Keep as a draft?") {
		t.Fatalf("esc in E's question should ask to keep it:\n%s", view)
	}
	f.press("y")
	if f.m.runbookView.CapturingKeys() {
		t.Fatal("y should close the question")
	}
	drafts := f.m.runbookReg.Drafts()
	if len(drafts) != 1 {
		t.Fatalf("drafts = %+v", drafts)
	}
	if d := drafts[0]; d.Kind != runbook.DraftEdit || d.Runbook != "Greet" || d.Request != "ask for the name" {
		t.Errorf("kept %+v", d)
	}
}

// keepNew types a new runbook's name and request, then keeps them as a draft.
func (f runbookFixture) keepNew(name, request string) {
	f.t.Helper()
	f.typeNew(name, request)
	f.press("esc", "y")
	if len(f.m.runbookReg.Drafts()) == 0 {
		f.t.Fatal("the draft was not kept")
	}
}

// listRows is the list's rows, below its header, as plain text: the right
// of the menu's border.
func listRows(m *AppModel) []string {
	var rows []string
	header := false
	for _, line := range strings.Split(plain(m), "\n") {
		if _, right, ok := strings.Cut(line, "│"); ok {
			line = right
		}
		switch {
		case strings.Contains(line, "STATUS") && strings.Contains(line, "NAME"):
			header = true
		case header && strings.Contains(line, "───"):
		case header && strings.TrimSpace(line) == "":
			return rows
		case header:
			rows = append(rows, line)
		}
	}
	return rows
}

func TestAKeptDraftIsTheFirstRowOfTheList(t *testing.T) {
	f := draftsHarness(t, "")
	f.typeNew("RDS status", "show my clusters")
	f.press("ctrl+j")
	f.send(testkeys.Text("in prod"))
	f.press("esc", "y")
	rows := listRows(f.m)
	if len(rows) == 0 || !strings.Contains(rows[0], "RDS status") || !strings.Contains(rows[0], "draft") ||
		!strings.Contains(rows[0], "show my clusters") || strings.Contains(rows[0], "in prod") {
		t.Fatalf("the first row should be the draft, its request's first line as the description:\n%s", strings.Join(rows, "\n"))
	}
	if !strings.Contains(plain(f.m), "4 entries") {
		t.Errorf("the count should include the draft:\n%s", plain(f.m))
	}
}

func TestEnterOnADraftRowReopensTheForm(t *testing.T) {
	f := draftsHarness(t, "")
	f.keepNew("RDS status", "show my clusters")
	f.selectRow("RDS status")
	f.press("enter")
	view := plain(f.m)
	if !f.m.runbooksPane.Modal() || !strings.Contains(view, "New runbook name") ||
		!strings.Contains(view, "> RDS status") || !strings.Contains(view, "show my clusters") {
		t.Fatalf("enter on a draft should open the form filled in:\n%s", view)
	}
	// Unchanged, esc still asks: there is a draft behind the form.
	f.press("esc")
	if !strings.Contains(plain(f.m), "Keep as a draft?") {
		t.Fatalf("esc on a reopened draft should ask:\n%s", plain(f.m))
	}
	f.press("y")
	if drafts := f.m.runbookReg.Drafts(); len(drafts) != 1 {
		t.Fatalf("keeping it again should not make a second draft: %+v", drafts)
	}
}

func TestDOnADraftRowAsksThenDeletesIt(t *testing.T) {
	f := draftsHarness(t, "")
	f.keepNew("RDS status", "show my clusters")
	f.selectRow("RDS status")
	f.press("D")
	if view := flatView(plain(f.m)); !strings.Contains(view, `Delete the draft "RDS status"? y: delete n: keep`) {
		t.Fatalf("D on a draft should ask:\n%s", view)
	}
	f.press("n")
	if len(f.m.runbookReg.Drafts()) != 1 {
		t.Fatal("n should keep the draft")
	}
	f.press("D", "y")
	if len(f.m.runbookReg.Drafts()) != 0 {
		t.Fatal("y should delete the draft")
	}
	for _, row := range listRows(f.m) {
		if strings.Contains(row, "RDS status") {
			t.Fatalf("the deleted draft is still listed:\n%s", plain(f.m))
		}
	}
}

func TestRunbookKeysOnADraftSayItIsADraft(t *testing.T) {
	f := draftsHarness(t, "")
	f.keepNew("RDS status", "show my clusters")
	for _, k := range []string{"r", "E", "m", "x"} {
		f.selectRow("RDS status")
		f.press(k)
		if view := plain(f.m); !strings.Contains(view, "a draft: enter opens it") || f.m.showRunbook || f.m.runbooksPane.Modal() {
			t.Fatalf("%s on a draft should say what it is:\n%s", k, view)
		}
	}
}

func TestTheFilterMatchesDrafts(t *testing.T) {
	f := draftsHarness(t, "")
	f.keepNew("RDS status", "show my clusters")
	f.press("/")
	f.send(testkeys.Text("cluster"))
	rows := listRows(f.m)
	if len(rows) != 1 || !strings.Contains(rows[0], "RDS status") {
		t.Fatalf("the filter should find the draft by its request:\n%s", strings.Join(rows, "\n"))
	}
}

func TestNOffersTheDraftsFirst(t *testing.T) {
	f := draftsHarness(t, "")
	f.keepNew("RDS status", "show my clusters")
	f.press("n")
	view := plain(f.m)
	if !strings.Contains(view, "New runbook") || !strings.Contains(view, "RDS status") || !strings.Contains(view, "now") ||
		strings.Contains(view, "New runbook name") {
		t.Fatalf("n with a draft should offer a new runbook or the draft:\n%s", view)
	}
	f.press("enter")
	if view := plain(f.m); !strings.Contains(view, "New runbook name") || strings.Contains(view, "> RDS status") {
		t.Fatalf("enter on New runbook should open an empty form:\n%s", view)
	}
	f.press("esc")
	f.press("n", "down", "enter")
	if view := plain(f.m); !strings.Contains(view, "> RDS status") || !strings.Contains(view, "show my clusters") {
		t.Fatalf("enter on the draft should open it:\n%s", view)
	}
}

func TestNWithoutDraftsOpensTheFormAtOnce(t *testing.T) {
	f := draftsHarness(t, "")
	f.press("n")
	if view := plain(f.m); !strings.Contains(view, "New runbook name") {
		t.Fatalf("n without drafts should open the form:\n%s", view)
	}
}

// A draft of a change is a row too; enter, or E on its runbook, opens E's
// question filled in.
func TestADraftOfAChangeReopensFromItsRowAndFromE(t *testing.T) {
	f := draftsHarness(t, "")
	f.selectRow("Greet")
	f.press("E")
	f.send(testkeys.Text("ask for the name"))
	f.press("esc", "y", "esc")
	if f.m.showRunbook {
		t.Fatal("esc should leave the view")
	}
	rows := listRows(f.m)
	if len(rows) == 0 || !strings.Contains(rows[0], "Greet (change)") || !strings.Contains(rows[0], "ask for the name") {
		t.Fatalf("the draft of the change should be the first row:\n%s", strings.Join(rows, "\n"))
	}
	f.selectRow("Greet (change)")
	f.press("enter")
	if view := plain(f.m); !f.m.showRunbook || !strings.Contains(view, "What should change in Greet?") || !strings.Contains(view, "ask for the name") {
		t.Fatalf("enter on the draft should open E's question filled in:\n%s", view)
	}
	f.press("esc", "y", "esc")
	f.selectRow("Greet")
	f.press("E")
	if view := plain(f.m); !strings.Contains(view, "ask for the name") {
		t.Fatalf("E on a runbook with a draft of a change should open it:\n%s", view)
	}
}

// agentTab is the terminal of the runbook's first terminal tab, as its tabs
// hold it: an agent's is wrapped.
func (f runbookFixture) agentTab(name string) liveTerminal {
	f.t.Helper()
	tabs := f.m.runbookTabs[name]
	if tabs == nil || tabs.Len() == 0 {
		f.t.Fatalf("%s has no terminal tab", name)
	}
	return tabs.Term(tabs.Position(1)).(liveTerminal)
}

// agentExits ends the program of the agent started nth, with err, and tells
// den, as its waiter would.
func (f runbookFixture) agentExits(name string, nth int, err error) {
	f.t.Helper()
	term := f.agentTab(name)
	f.starts.terms[nth].exit(err)
	f.send(terminalChangedMsg{term: term})
}

func TestEnterTurnsTheDraftIntoASavedRequest(t *testing.T) {
	f := draftsHarness(t, "")
	f.keepNew("RDS status", "check RDS")
	f.selectRow("RDS status")
	f.press("enter", "enter")
	if len(f.starts.cmds) != 1 || f.starts.cmds[0].Args[0] != "check RDS" {
		t.Fatalf("the agent should get the request: %q", f.starts.cmds)
	}
	if drafts := f.m.runbookReg.Drafts(); len(drafts) != 0 {
		t.Fatalf("the draft is still a draft: %+v", drafts)
	}
	saved, ok := f.m.runbookReg.SavedRequest("RDS status", runbook.DraftNew)
	if !ok || saved.Request != "check RDS" || saved.Folder != filepath.Join(f.root, "rds-status") || saved.Agent != "claude" {
		t.Fatalf("saved request = %+v, %v", saved, ok)
	}
}

// A request sent before the form saved it is kept all the same.
func TestARequestSentAtOnceIsSaved(t *testing.T) {
	f := draftsHarness(t, "")
	f.typeNew("RDS status", "check RDS")
	f.press("enter")
	if _, ok := f.m.runbookReg.SavedRequest("RDS status", runbook.DraftNew); !ok {
		t.Fatal("no saved request")
	}
}

// With no request there is nothing to give an agent again: the draft goes.
func TestEnterWithoutARequestDropsTheDraft(t *testing.T) {
	f := draftsHarness(t, "")
	f.typeNew("Notes", "")
	f.press("esc", "y")
	f.selectRow("Notes")
	f.press("enter", "enter")
	if _, ok := f.m.runbookReg.Lookup("Notes"); !ok {
		t.Fatal("the runbook was not created")
	}
	if drafts := f.m.runbookReg.Drafts(); len(drafts) != 0 {
		t.Fatalf("drafts = %+v", drafts)
	}
	if _, ok := f.m.runbookReg.SavedRequest("Notes", ""); ok {
		t.Fatal("a request was saved without one")
	}
}

// A runbook that cannot be created keeps what was typed as a draft.
func TestARunbookThatCannotBeCreatedKeepsTheDraft(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a read-only folder still takes new folders on Windows")
	}
	f := draftsHarness(t, "")
	f.typeNew("Fresh", "check RDS")
	if err := os.Chmod(f.root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.root, 0o750) })
	f.press("enter")
	if drafts := f.m.runbookReg.Drafts(); len(drafts) != 1 || drafts[0].Request != "check RDS" {
		t.Fatalf("drafts = %+v, want the request kept", drafts)
	}
	if view := plain(f.m); !strings.Contains(view, "kept as a draft") {
		t.Errorf("no word that it was kept:\n%s", view)
	}
}

func TestEStartedSavesTheRequestOfTheChange(t *testing.T) {
	f := draftsHarness(t, "")
	f.selectRow("Greet")
	f.press("E")
	f.send(testkeys.Text("ask for the name"))
	f.press("enter")
	saved, ok := f.m.runbookReg.SavedRequest("Greet", runbook.DraftEdit)
	if !ok || saved.Request != "ask for the name" || saved.Folder != f.greet {
		t.Fatalf("saved request = %+v, %v", saved, ok)
	}
	if drafts := f.m.runbookReg.Drafts(); len(drafts) != 0 {
		t.Fatalf("drafts = %+v", drafts)
	}
}

func TestKeepRequestNeverRemovesItOnceTheAgentStarted(t *testing.T) {
	f := draftsHarness(t, "never")
	f.typeNew("RDS status", "check RDS")
	f.press("enter")
	if len(f.starts.terms) != 1 {
		t.Fatal("the agent did not start")
	}
	if _, ok := f.m.runbookReg.SavedRequest("RDS status", ""); ok {
		t.Fatal("keep_request: never should drop the request once the agent started")
	}
}

func TestKeepRequestNeverKeepsItWhenTheAgentFailsToStart(t *testing.T) {
	f := draftsHarness(t, "never")
	f.starts.err = errors.New("no pty")
	f.typeNew("RDS status", "check RDS")
	f.press("enter")
	if _, ok := f.m.runbookReg.SavedRequest("RDS status", ""); !ok {
		t.Fatal("an agent that never started should leave the request")
	}
}

func TestKeepRequestUntilExit(t *testing.T) {
	f := draftsHarness(t, "until-exit")
	f.typeNew("RDS status", "check RDS")
	f.press("enter")
	saved := func() bool {
		_, ok := f.m.runbookReg.SavedRequest("RDS status", "")
		return ok
	}
	if !saved() {
		t.Fatal("the request should be kept while the agent runs")
	}
	f.agentExits("RDS status", 0, errors.New("exit status 1"))
	if !saved() {
		t.Fatal("an agent that failed should leave the request")
	}

	// Restarted (the question comes in its own test), it then exits cleanly.
	f.press("enter")
	if view := plain(f.m); strings.Contains(view, "Send the request again?") {
		f.press("y")
	}
	if len(f.starts.terms) != 2 {
		t.Fatalf("enter on the exited tab should restart the agent, started %d", len(f.starts.terms))
	}
	f.agentExits("RDS status", 1, nil)
	if saved() {
		t.Fatal("an agent that exited cleanly by itself should take the request with it")
	}
}

// A tab den closes is no clean exit, whatever the program's status says.
func TestKeepRequestUntilExitKeepsItWhenDenClosesTheTab(t *testing.T) {
	f := draftsHarness(t, "until-exit")
	f.typeNew("RDS status", "check RDS")
	f.press("enter")
	term := f.agentTab("RDS status")
	f.m.runbookTabs["RDS status"].StopAll()
	f.send(terminalChangedMsg{term: term})
	if _, ok := f.m.runbookReg.SavedRequest("RDS status", ""); !ok {
		t.Fatal("a tab den stopped should leave the request")
	}
}
