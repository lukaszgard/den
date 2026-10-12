package tui

import (
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
