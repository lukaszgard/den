package runbooks

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/agent"
	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/tui/testkeys"

	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// wrapRows must count the rows the textarea draws, to the row, or the request
// shows an empty row under its text or loses its first one. The textarea is
// drawn tall, with ~ marking the rows past its text. Double-width characters
// are left out: the textarea's own wrap can let one end a row a column past
// its width, which its viewport then draws on two.
func TestWrapRowsAgreesWithTheTextarea(t *testing.T) {
	words := []string{"a", "list", "the", "clusters", "in", "prod", "with-a-very-long-hyphenated-word", "  ", "x"}
	rnd := rand.New(rand.NewPCG(1, 2))
	for i := range 250 {
		var b strings.Builder
		for range rnd.IntN(40) {
			switch rnd.IntN(12) {
			case 0:
				b.WriteString("\n")
			case 1:
				b.WriteString(strings.Repeat("y", rnd.IntN(70)))
			default:
				b.WriteString(words[rnd.IntN(len(words))])
				b.WriteString(strings.Repeat(" ", rnd.IntN(3)))
			}
		}
		text := b.String()
		if text == "" {
			continue
		}
		width := 5 + rnd.IntN(60)

		ta := textarea.New()
		ta.ShowLineNumbers = false
		ta.Prompt = ""
		ta.MaxHeight, ta.CharLimit = 0, 0
		ta.EndOfBufferCharacter = '~'
		ta.SetWidth(width)
		ta.SetHeight(500)
		ta.SetValue(text)
		drawn := 0
		for _, line := range strings.Split(ansi.Strip(ta.View()), "\n") {
			if strings.HasPrefix(line, "~") {
				break
			}
			drawn++
		}
		if got := textRows(text, ta.Width()); got != drawn {
			t.Fatalf("case %d: textRows(%q, %d) = %d, the textarea draws %d", i, text, ta.Width(), got, drawn)
		}
	}
}

func newFormFixture(t *testing.T, width, height int) *newForm {
	t.Helper()
	return newFormWith(t, width, height, "claude")
}

// newFormWith is the pop-up offering the agents called names, the first one
// picked; none asks only for a name.
func newFormWith(t *testing.T, width, height int, names ...string) *newForm {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "greet"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "greet", runbook.ManifestName), []byte("name: Greet\ncommand: echo hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := runbook.NewRegistry(&config.Config{Runbooks: &config.RunbooksConfig{Dirs: []string{root}}}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var agents []agent.Agent
	for _, n := range names {
		agents = append(agents, agent.Agent{Name: n, Kind: agent.Claude})
	}
	f, _ := newNewForm(reg, agents, 0, width, height, true)
	return f
}

func (f *newForm) typeText(text string) {
	f.update(testkeys.Text(text))
}

// The request grows with what is typed, one row at a time, and once the pane
// has no more room it scrolls, keeping what is being typed in sight. The
// pop-up never leaves the pane.
func TestTheRequestGrowsThenScrollsInsideThePane(t *testing.T) {
	const width, height = 100, 30
	f := newFormFixture(t, width, height)
	f.typeText("Report")
	f.update(testkeys.Press("enter"))
	if !f.onRequest {
		t.Fatal("enter on a free name should go on to the request")
	}

	last := lipgloss.Height(f.View())
	grew, scrolled := false, false
	for i := range 250 {
		f.typeText("word" + strings.Repeat("x", i%7) + " ")
		view := f.View()
		h := lipgloss.Height(view)
		if h > height-2 {
			t.Fatalf("after %d words the pop-up is %d rows, in a pane of %d:\n%s", i+1, h, height, view)
		}
		if h < last {
			t.Fatalf("the pop-up shrank from %d to %d rows while text was added", last, h)
		}
		grew = grew || h > last
		last = h
		plain := ansi.Strip(view)
		if !strings.Contains(plain, "word"+strings.Repeat("x", i%7)) {
			t.Fatalf("the word just typed is out of sight:\n%s", plain)
		}
		if strings.Contains(plain, "rows ") && strings.Contains(plain, "↑↓ scroll") {
			scrolled = true
		}
	}
	if !grew || !scrolled {
		t.Fatalf("grew %v, scrolled %v: want the request to grow, then scroll", grew, scrolled)
	}
	if !strings.Contains(ansi.Strip(f.View()), "╰") {
		t.Fatalf("the pop-up's bottom is cut:\n%s", f.View())
	}

	// Deleting it all shrinks it back to one row.
	for f.request.Value() != "" {
		f.update(testkeys.Press("backspace"))
	}
	empty := newFormFixture(t, width, height)
	empty.typeText("Report")
	empty.update(testkeys.Press("enter"))
	if got, want := lipgloss.Height(f.View()), lipgloss.Height(empty.View()); got != want {
		t.Fatalf("emptied, the pop-up is %d rows, want %d as before any text", got, want)
	}
}

// ↑ on the request's first row, or shift+tab, goes back to the name, which
// can then be changed; the request is kept.
func TestTheNameCanBeChangedFromTheRequest(t *testing.T) {
	f := newFormFixture(t, 100, 30)
	f.typeText("Report")
	f.update(testkeys.Press("enter"))
	f.typeText("first line")
	f.update(testkeys.Press("ctrl+j"))
	f.typeText("second line")
	f.update(testkeys.Press("up"))
	if !f.onRequest {
		t.Fatal("↑ on the request's second line should move within it")
	}
	f.update(testkeys.Press("up"))
	if f.onRequest {
		t.Fatal("↑ on the request's first line should go back to the name")
	}
	f.typeText("s")
	f.update(testkeys.Press("down"))
	f.update(testkeys.Press("shift+tab"))
	if f.onRequest {
		t.Fatal("shift+tab should go back to the name")
	}
	f.update(testkeys.Press("enter"))
	f.update(testkeys.Press("enter"))
	name, request, agentName := f.values()
	if !f.done || name != "Reports" || request != "first line\nsecond line" || agentName != "claude" {
		t.Fatalf("done %v with %q, %q, %q", f.done, name, request, agentName)
	}
}

// A name taken by a runbook, in any case, or by its folder says so as it is
// typed, and enter does not go on.
func TestATakenNameIsRefusedAsItIsTyped(t *testing.T) {
	f := newFormFixture(t, 100, 30)
	if strings.Contains(ansi.Strip(f.View()), "✗") {
		t.Fatalf("an empty name is not a mistake before enter:\n%s", f.View())
	}
	for _, c := range []struct{ name, says string }{
		{"greet", `a runbook named "Greet" exists already`},
		{"Greet!", "greet" + string(filepath.Separator) + " exists already"},
	} {
		f.name.SetValue("")
		f.typeText(c.name)
		// A temporary directory's path is long enough to break anywhere.
		if view := squeeze(f.View()); !strings.Contains(view, squeeze(c.says)) {
			t.Errorf("%q: the form should say %q:\n%s", c.name, c.says, view)
		}
		f.update(testkeys.Press("enter"))
		if f.onRequest || f.done {
			t.Errorf("%q: enter went on with a taken name", c.name)
		}
	}
	f.name.SetValue("")
	f.update(testkeys.Press("enter"))
	if !strings.Contains(ansi.Strip(f.View()), "a runbook needs a name") {
		t.Fatalf("enter on an empty name should say one is needed:\n%s", f.View())
	}
}

// squeeze is view without its styling, borders and blanks: text that wraps in
// a pop-up, wherever it breaks, reads as one word.
func squeeze(view string) string {
	view = strings.NewReplacer("│", " ", "┃", " ").Replace(ansi.Strip(view))
	return strings.Join(strings.Fields(view), "")
}

func (f *newForm) press(keys ...string) {
	for _, k := range keys {
		f.update(testkeys.Press(k))
	}
}

// The agent row is there to pick from two or more agents: with one, or none,
// the form is the one it was.
func TestTheAgentRowIsDrawnOnlyToPickFromSeveral(t *testing.T) {
	for _, c := range []struct {
		names []string
		row   bool
		ask   bool
	}{
		{nil, false, false},
		{[]string{"claude"}, false, true},
		{[]string{"Opus", "Codex"}, true, true},
		{[]string{"Opus", "Codex", "GLM"}, true, true},
	} {
		f := newFormWith(t, 100, 30, c.names...)
		f.press("enter")
		f.typeText("Report")
		f.press("enter")
		view := ansi.Strip(f.View())
		if got := strings.Contains(view, "Agent  ‹"); got != c.row {
			t.Errorf("%d agents: row drawn %v, want %v:\n%s", len(c.names), got, c.row, view)
		}
		if got := strings.Contains(view, "Ask an agent to write it (optional)"); got != c.ask {
			t.Errorf("%d agents: request asked %v, want %v:\n%s", len(c.names), got, c.ask, view)
		}
		if got := strings.Contains(view, "tab: agent"); got != c.row {
			t.Errorf("%d agents: tab hint %v, want %v", len(c.names), got, c.row)
		}
		if c.ask && !strings.Contains(squeeze(view), squeeze("describe the runbook: "+c.names[0]+" writes it in a terminal tab; empty opens run.sh in your editor")) {
			t.Errorf("%d agents: the description does not name the agent:\n%s", len(c.names), view)
		}
		// tab reaches the row, and goes nowhere there is none.
		f.press("tab")
		if f.onAgent != c.row || (c.ask && f.onRequest == c.row) {
			t.Errorf("%d agents: tab moved to request %v, agent row %v", len(c.names), f.onRequest, f.onAgent)
		}
	}
}

// An agent that takes no first prompt reads the request with its
// instructions and waits in its tab: both forms say what to type there.
func TestTheFormsSayToTypeGoForAnAgentWithoutAFirstPrompt(t *testing.T) {
	f := newFormWith(t, 100, 30, "Kimi")
	f.agents[0].Kind = agent.Kimi
	f.press("enter")
	f.typeText("Report")
	f.press("enter")
	if view := ansi.Strip(f.View()); !strings.Contains(squeeze(view), squeeze("Kimi writes it in a terminal tab (type go there to start); empty opens run.sh")) {
		t.Errorf("the new-runbook form does not say to type go:\n%s", view)
	}
	edit, _ := newEditForm(f.reg, agent.Agent{Name: "kimi", Kind: agent.Kimi}, "Greet", 100, 30, true)
	if view := ansi.Strip(edit.View()); !strings.Contains(squeeze(view), squeeze("kimi changes it in a terminal tab, in its folder (type go there to start)")) {
		t.Errorf("the edit form does not say to type go:\n%s", view)
	}
}

// tab, or ↓ from the request's last row, reaches the row; ←/→ pick, around the
// list; ↑ and shift+tab go back; enter creates with the pick.
func TestTheAgentRowPicksWithTheArrows(t *testing.T) {
	f := newFormWith(t, 100, 30, "Opus", "Codex", "GLM")
	f.typeText("Report")
	f.press("enter")
	f.typeText("first")
	f.update(testkeys.Press("ctrl+j"))
	f.typeText("second")

	// ↓ below the request's last row is the row's; above it, the text's.
	f.press("up")
	f.press("down")
	if f.onAgent {
		t.Fatal("↓ on the request's first row left it")
	}
	f.press("down")
	if !f.onAgent || f.onRequest {
		t.Fatal("↓ on the request's last row should reach the agent row")
	}
	if view := ansi.Strip(f.View()); !strings.Contains(view, "Agent  ‹ Opus ›  (1/3)") ||
		!strings.Contains(view, "enter: create  ←/→: agent  ↑: request  esc: cancel") {
		t.Fatalf("the row or its hints are wrong:\n%s", view)
	}

	f.press("right")
	f.press("right")
	if name := func() string { _, _, a := f.values(); return a }(); name != "GLM" {
		t.Fatalf("→ twice picked %q, want GLM", name)
	}
	f.press("right")
	if _, _, a := f.values(); a != "Opus" {
		t.Fatalf("→ past the last wrapped to %q, want Opus", a)
	}
	f.press("left")
	if _, _, a := f.values(); a != "GLM" {
		t.Fatalf("← before the first wrapped to %q, want GLM", a)
	}
	f.press("left")
	if view := ansi.Strip(f.View()); !strings.Contains(view, "Agent  ‹ Codex ›  (2/3)") ||
		!strings.Contains(squeeze(view), squeeze("describe the runbook: Codex writes it")) {
		t.Fatalf("the row and the description should follow the pick:\n%s", view)
	}

	// Typing on the row changes nothing; ↑ and shift+tab are back at the text.
	f.typeText("zzz")
	if f.request.Value() != "first\nsecond" {
		t.Fatalf("typing on the row reached the request: %q", f.request.Value())
	}
	f.press("up")
	if !f.onRequest || f.onAgent {
		t.Fatal("↑ should go back to the request")
	}
	f.press("tab")
	f.press("shift+tab")
	if !f.onRequest {
		t.Fatal("shift+tab should go back to the request")
	}
	f.press("tab", "enter")
	name, request, agentName := f.values()
	if !f.done || name != "Report" || request != "first\nsecond" || agentName != "Codex" {
		t.Fatalf("done %v with %q, %q, %q", f.done, name, request, agentName)
	}
}

// A name taken is found by enter on the row too, and takes the keyboard back.
func TestEnterOnTheAgentRowChecksTheName(t *testing.T) {
	f := newFormWith(t, 100, 30, "Opus", "Codex")
	f.typeText("greet")
	f.press("enter")
	f.press("tab", "enter")
	if f.done || f.onAgent || f.onRequest || f.err == nil {
		t.Fatalf("a taken name was accepted: done %v, err %v", f.done, f.err)
	}
}

// The row is part of what the pop-up measures, so the request still grows and
// scrolls inside the pane with it, and the form keeps its place from row to row.
func TestTheAgentRowKeepsThePopUpInsideThePane(t *testing.T) {
	const width, height = 100, 30
	f := newFormWith(t, width, height, "Opus", "Codex", "GLM")
	f.typeText("Report")
	f.press("enter")
	base := lipgloss.Height(f.View())
	if plain := newFormWith(t, width, height, "claude"); true {
		plain.typeText("Report")
		plain.press("enter")
		if lipgloss.Height(plain.View()) >= base {
			t.Fatalf("the row added nothing to the pop-up: %d rows with it, %d without", base, lipgloss.Height(plain.View()))
		}
	}
	for i := range 250 {
		f.typeText("word" + strings.Repeat("x", i%7) + " ")
		view := f.View()
		if h := lipgloss.Height(view); h > height-2 {
			t.Fatalf("after %d words the pop-up is %d rows, in a pane of %d:\n%s", i+1, h, height, view)
		}
		if !strings.Contains(ansi.Strip(view), "Agent  ‹ Opus ›") {
			t.Fatalf("after %d words the agent row is out of sight:\n%s", i+1, view)
		}
	}
	f.press("tab")
	if view := ansi.Strip(f.View()); !strings.Contains(view, "rows ") || !strings.Contains(view, "╰") {
		t.Fatalf("a long request should scroll with the row focused:\n%s", view)
	}
}

// The agent picked changes how long the request's description is, and so how
// many of its rows fit: coming back to the request, its cursor is still in
// sight, whatever the pane's height.
func TestTheCursorStaysInViewAfterChangingAgent(t *testing.T) {
	long := "A very long agent name that wraps the description onto another row"
	for height := 18; height <= 30; height++ {
		f := newFormWith(t, 60, height, "Opus", long)
		f.typeText("Report")
		f.press("enter")
		for i := range 60 {
			f.typeText("word" + strings.Repeat("x", i%5) + " ")
		}
		f.typeText("LASTWORD")
		f.press("tab", "right", "up")
		if view := ansi.Strip(f.View()); !strings.Contains(view, "LASTWORD") {
			t.Fatalf("height %d: the cursor's row is out of sight after changing agent:\n%s", height, view)
		}
	}
}

// Pasted text lands in the field that has the keyboard, the name or the
// request, and a long paste grows the request by its rows.
func TestPasteGoesToTheFieldWithTheKeyboard(t *testing.T) {
	f := newFormFixture(t, 100, 60)
	f.update(testkeys.Paste("my-runbook"))
	if name, _, _ := f.values(); name != "my-runbook" {
		t.Fatalf("name = %q after a paste into the name", name)
	}
	f.press("enter")
	if !f.onRequest {
		t.Fatal("enter on the name should move to the request")
	}
	before := f.rows()
	f.update(testkeys.Paste("one\ntwo\nthree"))
	if _, request, _ := f.values(); request != "one\ntwo\nthree" {
		t.Fatalf("request = %q after a paste", request)
	}
	if f.rows() != before+2 {
		t.Fatalf("the request has %d rows, want %d", f.rows(), before+2)
	}
}
