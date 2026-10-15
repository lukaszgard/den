package configview

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/testkeys"
)

// The pane of an 80×24 terminal: 57 columns of the right-hand side.
const (
	paneWidth  = 57
	paneHeight = 20
)

// newPane is a Config pane over a 60-line file, with warnings on the lines
// given. Each has a hint, and "problem N" in its message.
func newPane(t *testing.T, lines ...int) Model {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&b, "key%d: value\n", i)
	}
	path := filepath.Join(t.TempDir(), "den.yaml")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(path, paneWidth, paneHeight)
	var ps config.Problems
	for i, line := range lines {
		ps = append(ps, config.Problem{
			Severity: config.SeverityWarning, Line: line,
			Msg: fmt.Sprintf("problem %d", i+1), Hint: fmt.Sprintf("fix %d", i+1),
		})
	}
	return m.SetProblems(ps)
}

func press(m Model, keys ...string) Model {
	for _, k := range keys {
		m, _ = m.Update(testkeys.Press(k), true)
	}
	return m
}

func screen(m Model) string { return ansi.Strip(m.View(paneWidth, paneHeight, true)) }

func twelve(t *testing.T) Model {
	t.Helper()
	return newPane(t, 3, 6, 9, 12, 15, 18, 21, 24, 27, 30, 33, 36)
}

func TestTheSummaryShowsFiveProblemsAndCountsTheRest(t *testing.T) {
	// A taller pane than 80×24 gives: there, four fit with their hints.
	m := twelve(t).SetSize(paneWidth, 30)
	view := ansi.Strip(m.View(paneWidth, 30, true))
	for i := 1; i <= 5; i++ {
		if !strings.Contains(view, fmt.Sprintf("problem %d", i)) || !strings.Contains(view, fmt.Sprintf("→ fix %d", i)) {
			t.Errorf("problem %d and its hint should be summarised:\n%s", i, view)
		}
	}
	if strings.Contains(view, "problem 6") {
		t.Errorf("only five are summarised:\n%s", view)
	}
	if !strings.Contains(view, "… and 7 more — p lists them all") {
		t.Errorf("the rest should be counted:\n%s", view)
	}
	four := ansi.Strip(twelve(t).View(paneWidth, paneHeight, true))
	if !strings.Contains(four, "problem 4") || strings.Contains(four, "problem 5") || !strings.Contains(four, "… and 8 more") {
		t.Errorf("at 20 lines four problems fit with their hints:\n%s", four)
	}
	if !strings.Contains(view, "p: problems") || !strings.Contains(view, "n/N: next") {
		t.Errorf("the hint line should offer the list:\n%s", view)
	}
	// The file is still readable under it: its first line shows.
	if !strings.Contains(view, "key1: value") {
		t.Errorf("the file should stay visible:\n%s", view)
	}
}

func TestTheSummaryShrinksOnAShortPaneButNeverHidesTheFile(t *testing.T) {
	m := twelve(t).SetSize(paneWidth, 14)
	view := ansi.Strip(m.View(paneWidth, 14, true))
	if strings.Count(view, "key") < 3 {
		t.Errorf("at least three lines of the file stay:\n%s", view)
	}
	if !strings.Contains(view, "more — p lists them all") {
		t.Errorf("the rest should be counted:\n%s", view)
	}
	if got := strings.Count(view, "\n") + 1; got > 14 {
		t.Errorf("the pane drew %d lines in 14:\n%s", got, view)
	}
}

func TestWithoutProblemsThereIsNoListToOpen(t *testing.T) {
	m := newPane(t)
	m = press(m, "p", "n")
	if m.list {
		t.Fatal("p opened a list with nothing in it")
	}
	if view := screen(m); strings.Contains(view, "p: problems") {
		t.Errorf("no problems, no p:\n%s", view)
	}
}

func TestPListsEveryProblemAndScrollsToThem(t *testing.T) {
	m := press(twelve(t), "p")
	if !m.list {
		t.Fatal("p should open the list")
	}
	seen := map[string]bool{}
	note := func() {
		view := screen(m)
		for i := 1; i <= 12; i++ {
			if strings.Contains(view, fmt.Sprintf("problem %d\n", i)) || strings.Contains(view, fmt.Sprintf("problem %d ", i)) {
				seen[fmt.Sprint(i)] = true
			}
		}
		if got := strings.Count(view, "\n") + 1; got > paneHeight {
			t.Fatalf("the list drew %d lines in %d:\n%s", got, paneHeight, view)
		}
	}
	note()
	for i := 1; i < 12; i++ {
		m = press(m, "down")
		note()
		// The cursor problem is always on screen, with its hint.
		if view := screen(m); !strings.Contains(view, fmt.Sprintf("▸ ! line %d", 3*(i+1))) || !strings.Contains(view, fmt.Sprintf("→ fix %d", i+1)) {
			t.Fatalf("cursor on problem %d is not on screen with its hint:\n%s", i+1, view)
		}
	}
	if len(seen) != 12 {
		t.Errorf("only %d of 12 problems were reachable by scrolling: %v", len(seen), seen)
	}
	if view := screen(m); !strings.Contains(view, "12 of 12") {
		t.Errorf("the count should follow the cursor:\n%s", view)
	}
	// And back up with k, and by pages.
	m = press(m, "k", "home")
	if m.cursor != 0 || !strings.Contains(screen(m), "1 of 12") {
		t.Errorf("home should go to the first problem: cursor %d", m.cursor)
	}
	m = press(m, "pgdown")
	if m.cursor == 0 {
		t.Error("pgdown should move the cursor on")
	}
	m = press(m, "end")
	if m.cursor != 11 {
		t.Errorf("end: cursor %d", m.cursor)
	}
	m = press(m, "pgup")
	if m.cursor >= 11 {
		t.Errorf("pgup should move the cursor back, at %d", m.cursor)
	}
}

func TestTheListWrapsALongMessageAndItsHint(t *testing.T) {
	m := newPane(t, 5)
	long := "services[3] \"a long name\": local port 50432 is used by \"A\" and \"B\": only one can be connected at a time"
	hint := "give each its own local_port, and remember that a default port counts as one too"
	m = m.SetProblems(config.Problems{{Severity: config.SeverityWarning, Line: 5, Msg: long, Hint: hint}})
	view := screen(press(m, "p"))
	// Wrapped, not cut: every word is there, across lines.
	flat := strings.Join(strings.Fields(view), " ")
	for _, want := range []string{"only one can be connected at a time", "as one too", "line 5"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the list lost %q:\n%s", want, view)
		}
	}
	for _, l := range strings.Split(view, "\n") {
		// The path line is as long as the temp folder; the rest must fit.
		if l = strings.TrimRight(l, " "); strings.Contains(l, "den.yaml") {
			continue
		}
		if w := len([]rune(l)); w > paneWidth {
			t.Errorf("line of %d columns in a pane of %d: %q", w, paneWidth, l)
		}
	}
}

func TestEnterShowsTheLineInTheFile(t *testing.T) {
	m := press(twelve(t), "p", "down", "down", "down", "down", "down", "down", "enter") // line 21
	if m.list {
		t.Fatal("enter should go back to the file")
	}
	view := screen(m)
	if m.focus != 21 {
		t.Fatalf("focus = %d, want 21", m.focus)
	}
	// Line 21 leads the file: nothing before it shows.
	if !strings.Contains(view, "▶ 21  key21: value") || strings.Contains(view, "key20: value") {
		t.Errorf("line 21 should be at the top of the file, marked:\n%s", view)
	}
	if !strings.Contains(view, "key22: value") {
		t.Errorf("the lines after it follow:\n%s", view)
	}
}

func TestEnterClearsAFilterThatWouldHideTheLine(t *testing.T) {
	m := twelve(t)
	m = press(m, "/")
	for _, r := range "key1" {
		m, _ = m.Update(testkeys.Press(string(r)), true)
	}
	m = press(m, "esc") // the filter closes and clears; reopen it holding a query
	m.query = "key1"
	m.rebuildViewport()
	m.list = true
	m.cursor = 3 // line 12
	m = press(m, "enter")
	if m.query != "" || m.focus != 12 || !strings.Contains(screen(m), "▶ 12  key12") {
		t.Errorf("query %q focus %d:\n%s", m.query, m.focus, screen(m))
	}
}

func TestEscAndPCloseTheList(t *testing.T) {
	for _, k := range []string{"esc", "p"} {
		m := press(twelve(t), "p")
		if !m.OwnsBack(testkeys.Press("esc")) {
			t.Error("esc in the list closes it, it should not leave the pane")
		}
		m = press(m, k)
		if m.list || !strings.Contains(screen(m), "key1: value") {
			t.Errorf("%s should go back to the file", k)
		}
		if m.OwnsBack(testkeys.Press("esc")) {
			t.Error("esc in the file leaves the pane")
		}
	}
}

func TestNAndShiftNMoveBetweenMarkedLines(t *testing.T) {
	m := newPane(t, 10, 20, 30)
	var at []int
	for _, k := range []string{"n", "n", "n", "n", "N", "N"} {
		m = press(m, k)
		at = append(at, m.focus)
	}
	// Round the end and back: 10 20 30, then 10 again, and backwards from it.
	want := []int{10, 20, 30, 10, 30, 20}
	if fmt.Sprint(at) != fmt.Sprint(want) {
		t.Errorf("n/N visited %v, want %v", at, want)
	}
	if view := screen(m); !strings.Contains(view, "▶ 20  key20: value") {
		t.Errorf("the line jumped to is marked and on screen:\n%s", view)
	}
}

func TestNStartsFromWhereTheViewIs(t *testing.T) {
	m := newPane(t, 10, 20, 30)
	m.vp.SetYOffset(15) // scrolled to line 16
	if got := press(m, "n").focus; got != 20 {
		t.Errorf("n from line 16 went to %d, want 20", got)
	}
	if got := press(m, "N").focus; got != 10 {
		t.Errorf("N from line 16 went to %d, want 10", got)
	}
}

func TestOtherKeysDoNothingInTheListAndEditStillWorks(t *testing.T) {
	m := press(twelve(t), "p", "x", "n", "/")
	if !m.list || m.searching || m.focus != 0 {
		t.Errorf("list %v searching %v focus %d", m.list, m.searching, m.focus)
	}
	if _, cmd := m.Update(testkeys.Press("e"), true); cmd == nil {
		t.Error("e should still edit")
	}
	if _, cmd := m.Update(testkeys.Press("r"), true); cmd == nil {
		t.Error("r should still reload")
	}
}

func TestANewCheckKeepsTheListOpenAndTheCursorInRange(t *testing.T) {
	m := press(twelve(t), "p", "end")
	m = m.SetProblems(m.Problems()[:3])
	if !m.list || m.cursor != 2 {
		t.Errorf("list %v cursor %d after the problems shrank", m.list, m.cursor)
	}
	m = m.SetProblems(nil)
	if m.list || strings.Contains(screen(m), "problem") {
		t.Errorf("with the problems gone the file is back:\n%s", screen(m))
	}
}

func TestEveryKeyOfTheHintLineIsInTheShortcuts(t *testing.T) {
	var shortcuts strings.Builder
	for _, g := range newPane(t, 3).Shortcuts() {
		for _, s := range g.Keys {
			shortcuts.WriteString(s.Keys + " ")
		}
	}
	for _, k := range []string{"p", "n N", "e", "r", "/", "enter", "esc"} {
		if !strings.Contains(shortcuts.String(), k) {
			t.Errorf("the shortcuts lack %q: %s", k, shortcuts.String())
		}
	}
}

// zoneOf is the zone under the first cell of text in the pane, which is what a
// click there would land on.
func zoneOf(t *testing.T, m Model, text string) string {
	t.Helper()
	clean, zones := components.ScanZones(m.View(paneWidth, paneHeight, true))
	for y, line := range strings.Split(ansi.Strip(clean), "\n") {
		if i := strings.Index(line, text); i >= 0 {
			id, _ := zones.At(ansi.StringWidth(line[:i]), y)
			return id
		}
	}
	t.Fatalf("%q is not in the pane:\n%s", text, ansi.Strip(clean))
	return ""
}

// In the list, a click on any line of a problem, its hint too, selects it;
// the summary above the file is not the list and takes no click.
func TestClickSelectsAProblemInTheList(t *testing.T) {
	m := newPane(t, 3, 9)
	if id := zoneOf(t, m, "problem 2"); id != "" {
		t.Fatalf("the summary has the zone %q", id)
	}
	m = press(m, "p")
	m = m.Click(zoneOf(t, m, "fix 2"))
	if !strings.Contains(screen(m), "2 of 2") {
		t.Fatalf("a click on the second problem's hint should select it:\n%s", screen(m))
	}
	m = press(m, "esc")
	if m = m.Click(components.ZoneID("problem", 0)); m.cursor != 1 {
		t.Fatal("a click with the list closed should do nothing")
	}
}
