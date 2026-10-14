package components

import (
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/tui/testkeys"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type stubTerm struct {
	dead   bool
	full   bool
	keys   []string
	pasted []string
	closed int
	cursor *tea.Cursor
}

func (s *stubTerm) View() string                  { return "" }
func (s *stubTerm) Cursor() *tea.Cursor           { return s.cursor }
func (s *stubTerm) Resize(int, int)               {}
func (s *stubTerm) HandleKey(msg tea.KeyPressMsg) { s.keys = append(s.keys, msg.String()) }
func (s *stubTerm) Paste(text string)             { s.pasted = append(s.pasted, text) }
func (s *stubTerm) Alive() bool                   { return !s.dead }
func (s *stubTerm) FullScreen() bool              { return s.full }
func (s *stubTerm) ExitErr() error                { return nil }
func (s *stubTerm) Close()                        { s.closed++; s.dead = true }

// A runbook's view has three fixed tabs before its terminals. Terminals are
// still numbered from 1, which is what the digit keys and the labels use.
func TestTabsWithSeveralFixedTabs(t *testing.T) {
	tabs := NewTabs("Logs", "History", "About")
	if tabs.Fixed() != 3 || tabs.Current() != nil {
		t.Fatal("new tabs should show the first fixed tab and no terminal")
	}
	tabs.Next()
	tabs.Next()
	if tabs.Selected() != 2 {
		t.Fatalf("selected %d, want About at 2: the fixed tabs cycle without any terminal", tabs.Selected())
	}
	tabs.Next()
	if tabs.Selected() != 0 {
		t.Fatalf("selected %d, want a wrap to Logs", tabs.Selected())
	}

	one, two := &stubTerm{}, &stubTerm{}
	if p, _ := tabs.Add("zsh", one); p != 3 || tabs.Number(p) != 1 {
		t.Fatalf("first terminal at %d (number %d), want position 3, number 1", p, tabs.Number(p))
	}
	tabs.Add("vim", two)
	if tabs.Position(2) != 4 || tabs.Position(3) != -1 || tabs.Number(1) != 0 {
		t.Fatal("positions and numbers do not map onto each other")
	}
	tabs.Close(4)
	if tabs.Selected() != 3 || two.closed != 1 {
		t.Fatalf("selected %d after closing the last tab, want the one before it", tabs.Selected())
	}
	tabs.Close(3)
	if tabs.Selected() != 2 || tabs.Len() != 0 {
		t.Fatalf("selected %d after closing the only terminal, want the last fixed tab", tabs.Selected())
	}

	tabs.SelectFixed("History")
	if tabs.Selected() != 1 {
		t.Fatalf("selected %d, want History at 1", tabs.Selected())
	}
	tabs.SelectFixed("Files")
	if tabs.Selected() != 1 {
		t.Fatalf("selected %d after naming no fixed tab, want the selection kept", tabs.Selected())
	}
}

func TestTabStripOnAFixedTabLeavesItsKeysToTheView(t *testing.T) {
	tabs := NewTabs("Logs", "History", "About")
	opened := -1
	strip := NewTabStrip(tabs, func(id int) tea.Cmd { opened = id; return nil })

	for _, k := range []string{"x", "n", "enter", "r"} {
		if handled, _ := strip.Key(testkeys.Press(k)); handled {
			t.Errorf("%q on a fixed tab was taken by the strip", k)
		}
	}
	if handled, _ := strip.Key(testkeys.Press("right")); !handled || tabs.Selected() != 1 {
		t.Fatal("→ should move through the fixed tabs even without a terminal")
	}
	if handled, _ := strip.Key(testkeys.Press("t")); !handled || opened != 0 {
		t.Fatal("t should ask for a new terminal")
	}

	term := &stubTerm{}
	tabs.Add("zsh", term)
	tabs.Select(0)
	if handled, _ := strip.Key(testkeys.Press("1")); !handled || tabs.Current() != Terminal(term) || !strip.Typing() {
		t.Fatal("1 should select terminal 1 and type into it")
	}
	strip.Key(testkeys.Press("q"))
	if strings.Join(term.keys, "") != "q" {
		t.Fatalf("keys reached the terminal as %q", term.keys)
	}
	strip.Key(testkeys.Press("ctrl+t"))
	if strip.Typing() {
		t.Fatal("ctrl+t should give den the keys back")
	}
	if line, ok := strip.ModeHelp(HelpKey{"esc", "back"}); !ok || !strings.Contains(line, "back") {
		t.Fatalf("help on a terminal tab = %q, %v", line, ok)
	}
	tabs.Select(1)
	if _, ok := strip.ModeHelp(); ok {
		t.Fatal("on a fixed tab the view's own help applies")
	}
}

// In an editor the first esc is the editor's; a second within DoubleEsc is
// den's. One long after the last is the editor's again.
func TestTabStripEscTwiceLeavesAnEditor(t *testing.T) {
	tabs := NewTabs("Logs")
	term := &stubTerm{full: true}
	tabs.Add("vim", term)
	tabs.Select(1)
	s := NewTabStrip(tabs, nil)
	s.StartTyping()

	s.Key(testkeys.Press("esc"))
	s.escAt = time.Now().Add(-2 * DoubleEsc)
	s.Key(testkeys.Press("esc"))
	if !s.Typing() || strings.Join(term.keys, " ") != "esc esc" {
		t.Fatalf("two escs far apart should both reach the editor, which got %v", term.keys)
	}
	s.Key(testkeys.Press("esc"))
	if s.Typing() || strings.Join(term.keys, " ") != "esc esc" {
		t.Fatalf("an esc right after the last should leave, not reach the editor (%v)", term.keys)
	}

	// Leaving and coming back starts the count again.
	s.StartTyping()
	s.Key(testkeys.Press("esc"))
	s.Key(testkeys.Press("ctrl+t"))
	s.StartTyping()
	s.Key(testkeys.Press("esc"))
	if !s.Typing() {
		t.Fatal("an esc from before den had the keys should not count")
	}
}

// A keyboard-enhancing terminal reports ctrl+[ as that key rather than as the
// escape character it is, and it has to do what esc does.
func TestTabStripCtrlBracketIsEsc(t *testing.T) {
	// At a prompt it leaves.
	tabs := NewTabs("Logs")
	term := &stubTerm{}
	tabs.Add("psql", term)
	tabs.Select(1)
	s := NewTabStrip(tabs, nil)
	s.StartTyping()
	s.Key(testkeys.Press("ctrl+["))
	if s.Typing() || len(term.keys) != 0 {
		t.Fatalf("ctrl+[ at a prompt should leave without reaching it (typing %v, keys %v)", s.Typing(), term.keys)
	}

	// In an editor it is the editor's first, and counts toward the second.
	tabs = NewTabs("Logs")
	editor := &stubTerm{full: true}
	tabs.Add("vim", editor)
	tabs.Select(1)
	s = NewTabStrip(tabs, nil)
	s.StartTyping()
	s.Key(testkeys.Press("ctrl+["))
	if !s.Typing() || strings.Join(editor.keys, " ") != "ctrl+[" {
		t.Fatalf("the first ctrl+[ should reach the editor, which got %v", editor.keys)
	}
	s.Key(testkeys.Press("esc"))
	if s.Typing() {
		t.Fatal("an esc right after a ctrl+[ should leave")
	}

	// With alt it is a key of its own, as alt+esc is not a bare esc.
	s.StartTyping()
	s.Key(testkeys.Press("alt+ctrl+["))
	if !s.Typing() {
		t.Fatal("alt+ctrl+[ is not a bare esc")
	}
}

// Esc cancels a tab rename, and so does ctrl+[.
func TestTabStripCtrlBracketCancelsARename(t *testing.T) {
	tabs := NewTabs("Logs")
	tabs.Add("zsh", &stubTerm{})
	tabs.Select(1)
	s := NewTabStrip(tabs, nil)
	s.startRename(1)
	s.Key(testkeys.Press("ctrl+["))
	if s.renaming {
		t.Fatal("ctrl+[ should cancel the rename")
	}
}

// A paste is one message: into the terminal being typed into, into a tab name
// being edited, and nowhere else, so the view's own fields get the rest.
func TestTabStripPasteGoesToTheTerminalOrTheNameBeingEdited(t *testing.T) {
	tabs := NewTabs("Logs")
	term := &stubTerm{}
	tabs.Add("zsh", term)
	s := NewTabStrip(tabs, nil)

	tabs.Select(0)
	if handled, _ := s.Paste(testkeys.Paste("x")); handled || len(term.pasted) != 0 {
		t.Fatal("a paste on a fixed tab belongs to the view")
	}

	tabs.Select(1)
	if handled, _ := s.Paste(testkeys.Paste("x")); handled {
		t.Fatal("a paste while den has the keys belongs to the view")
	}
	s.StartTyping()
	if handled, _ := s.Paste(testkeys.Paste("select 1;\nselect 2;")); !handled || strings.Join(term.pasted, "|") != "select 1;\nselect 2;" {
		t.Fatalf("a paste while typing should reach the terminal whole, got %q", term.pasted)
	}
	// The paste is not read as keys: den's own keys stay out of it.
	if len(term.keys) != 0 {
		t.Fatalf("a paste reached the terminal as keys %v", term.keys)
	}
	term.dead = true
	if handled, _ := s.Paste(testkeys.Paste("late")); !handled || len(term.pasted) != 1 {
		t.Fatal("a paste into an exited program is taken and dropped")
	}

	s.Leave()
	s.startRename(1)
	if handled, _ := s.Paste(testkeys.Paste(" new")); !handled || s.nameInput.Value() != "zsh new" {
		t.Fatalf("a paste while renaming should land in the name, got %q", s.nameInput.Value())
	}
}

// Without key disambiguation two escs read at once arrive as alt+esc: the first
// is the editor's, the second den's. With it alt+esc is the key the user
// pressed, and the editor gets it.
func TestTabStripAltEscDependsOnKeyDisambiguation(t *testing.T) {
	for _, tc := range []struct {
		name          string
		disambiguated bool
		leaves        bool
		keys          string
	}{
		{"merged escs in a plain terminal", false, true, "esc"},
		{"alt+esc where keys are disambiguated", true, false, "alt+esc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tabs := NewTabs("Logs")
			editor := &stubTerm{full: true}
			tabs.Add("vim", editor)
			tabs.Select(1)
			tabs.SetKeyDisambiguation(tc.disambiguated)
			s := NewTabStrip(tabs, nil)
			s.StartTyping()

			s.Key(testkeys.Press("alt+esc"))
			if s.Typing() == tc.leaves || strings.Join(editor.keys, " ") != tc.keys {
				t.Fatalf("typing %v, editor got %v; want leaves=%v and %q", s.Typing(), editor.keys, tc.leaves, tc.keys)
			}
		})
	}
}

func TestNormalizeEscMakesCtrlBracketEsc(t *testing.T) {
	for in, want := range map[string]string{
		"ctrl+[":     "esc",
		"esc":        "esc",
		"alt+ctrl+[": "ctrl+alt+[",
		"ctrl+]":     "ctrl+]",
		"a":          "a",
	} {
		if got := NormalizeEsc(testkeys.Press(in)).String(); got != want {
			t.Errorf("NormalizeEsc(%s) = %s, want %s", in, got, want)
		}
	}
}

// The strip places a terminal's cursor on the screen only while that terminal
// is typed into, and only inside the area it is shown in.
func TestTabStripCursor(t *testing.T) {
	tabs := NewTabs("Logs")
	term := &stubTerm{cursor: tea.NewCursor(3, 2)}
	term.cursor.Shape = tea.CursorUnderline
	tabs.Add("psql", term)
	s := NewTabStrip(tabs, nil)

	if s.Cursor(1, 5, 80, 20) != nil {
		t.Fatal("a fixed tab has no terminal cursor")
	}
	tabs.Select(1)
	if s.Cursor(1, 5, 80, 20) != nil {
		t.Fatal("a terminal den has the keyboard from is not typed into")
	}
	s.StartTyping()
	c := s.Cursor(1, 5, 80, 20)
	if c == nil || c.X != 4 || c.Y != 7 || c.Shape != tea.CursorUnderline {
		t.Fatalf("cursor = %+v, want (4,7) as an underline", c)
	}
	if term.cursor.X != 3 || term.cursor.Y != 2 {
		t.Fatal("placing the cursor must not move the terminal's own")
	}
	for _, off := range []*tea.Cursor{tea.NewCursor(80, 0), tea.NewCursor(0, 20), tea.NewCursor(-1, 0), nil} {
		term.cursor = off
		if got := s.Cursor(1, 5, 80, 20); got != nil {
			t.Fatalf("cursor %+v is outside the 80x20 area but %+v was shown", off, got)
		}
	}
}

// The tabs sit between two rules: the one closing the header above them, and
// one of the bar's own under them, as wide as the bar.
func TestTabStripBarHasARuleUnderTheTabs(t *testing.T) {
	tabs := NewTabs("Run", "History")
	tabs.Add("zsh", &stubTerm{})
	for name, s := range map[string]TabStrip{"tabs": NewTabStrip(tabs, nil), "no tabs": NewTabStrip(nil, nil)} {
		lines := strings.Split(ansi.Strip(s.Bar(40)), "\n")
		if len(lines) != TabBarLines {
			t.Fatalf("%s: the bar has %d lines, want %d:\n%s", name, len(lines), TabBarLines, strings.Join(lines, "\n"))
		}
		if lines[1] != strings.Repeat("─", 40) {
			t.Errorf("%s: under the tabs %q, want a rule 40 cells wide", name, lines[1])
		}
	}
}
