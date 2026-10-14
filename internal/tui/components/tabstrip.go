package components

import (
	"fmt"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/tui/msgs"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// TabStrip is the keyboard and the tab bar of a view with terminal tabs: the
// service detail view and the runbook view. It owns the modes in which the
// keys are not the view's own — typing into a terminal, renaming a tab,
// confirming a close — so both views behave the same in them.
type TabStrip struct {
	tabs *Tabs
	// open asks the root model, which owns the sessions, for a terminal in a
	// new tab (tabID 0) or again in an exited one.
	open func(tabID int) tea.Cmd

	// typing is terminal mode: the keys belong to the selected terminal's
	// program, except ctrl+t, esc at a prompt, and a second esc in a row in an
	// editor. It stays on after that program exits, so the rest of a line
	// typed into a dying client is not read as den's keys.
	typing bool
	// escAt is when the last key passed to a full-screen program was esc: a
	// second esc soon after leaves.
	escAt time.Time
	// renaming is set while the selected tab's name is being edited.
	renaming  bool
	nameInput textinput.Model
	// confirmClose is set while x on a running program waits for its answer.
	confirmClose bool
}

// NewTabStrip returns the strip over tabs, which may be nil for a view
// without terminals.
func NewTabStrip(tabs *Tabs, open func(tabID int) tea.Cmd) TabStrip {
	return TabStrip{tabs: tabs, open: open}
}

// Tabs are the tabs the strip works on; nil for a view without terminals.
func (s TabStrip) Tabs() *Tabs { return s.tabs }

// Current is the terminal on screen, or nil while a fixed tab is.
func (s TabStrip) Current() Terminal {
	if s.tabs == nil {
		return nil
	}
	return s.tabs.Current()
}

// Typing reports terminal mode. Renaming and ConfirmingClose report the
// other two modes.
func (s TabStrip) Typing() bool          { return s.typing }
func (s TabStrip) Renaming() bool        { return s.renaming }
func (s TabStrip) ConfirmingClose() bool { return s.confirmClose }

// Capturing reports that the strip needs every key: a terminal is being typed
// into, a tab is being renamed, or a close waits for its answer.
func (s TabStrip) Capturing() bool { return s.typing || s.renaming || s.confirmClose }

// StartTyping hands the keyboard to the terminal on screen, if one is.
func (s *TabStrip) StartTyping() {
	if s.Current() != nil {
		s.typing, s.renaming, s.confirmClose = true, false, false
		s.escAt = time.Time{}
	}
}

// Leave ends whichever mode the strip is in.
func (s *TabStrip) Leave() { s.typing, s.renaming, s.confirmClose = false, false, false }

// EscLeaves reports whether esc, in terminal mode, returns the keyboard to
// den. It does at a prompt and on an exited program. It does not while an
// editor, a pager or an agent has the terminal: esc is how those are used, so
// there the first esc is theirs and a second within DoubleEsc leaves.
func (s TabStrip) EscLeaves() bool {
	term := s.Current()
	return term == nil || !term.Alive() || !term.FullScreen()
}

// DoubleEsc is how soon a second esc must follow the first to leave an editor
// or an agent: quick enough to be meant, slow enough that leaving vim's insert
// mode and then pressing esc again a moment later still reaches vim.
const DoubleEsc = 500 * time.Millisecond

// isEsc is the bare esc key, or ctrl+[, which a terminal with key
// disambiguation reports as that instead of the escape character it is.
func isEsc(msg tea.KeyPressMsg) bool {
	return msg.Code == tea.KeyEscape || (msg.Code == '[' && msg.Mod&sendableMods == tea.ModCtrl)
}

// NormalizeEsc turns ctrl+[ into esc. A terminal with key disambiguation
// reports them apart, but they are one key: the control character 0x1b.
func NormalizeEsc(msg tea.KeyPressMsg) tea.KeyPressMsg {
	if isEsc(msg) && msg.Code != tea.KeyEscape {
		return tea.KeyPressMsg{Code: tea.KeyEscape, Mod: msg.Mod &^ tea.ModCtrl}
	}
	return msg
}

// isLeaveKey is the key that hands the keyboard back from a terminal whatever
// runs in it. esc does too at a prompt (see EscLeaves), but an editor keeps its
// first esc, so one key has to be the program's never: ctrl+t costs only
// readline's transpose-characters, and unlike ctrl+] it is one keystroke on
// every keyboard layout.
func isLeaveKey(msg tea.KeyPressMsg) bool {
	return msg.Code == 't' && msg.Mod&sendableMods == tea.ModCtrl
}

// Cursor is the real cursor for the terminal being typed into: its position
// inside a cols×rows body that starts at column x, row y of the screen. It is
// nil when a fixed tab is on screen, when den has the keyboard (the terminal is
// then something to read, not to type into), and when the program hides its
// cursor or keeps it off the screen.
func (s TabStrip) Cursor(x, y, cols, rows int) *tea.Cursor {
	term := s.Current()
	if !s.typing || term == nil {
		return nil
	}
	c := term.Cursor()
	if c == nil || c.X < 0 || c.Y < 0 || c.X >= cols || c.Y >= rows {
		return nil
	}
	placed := *c
	placed.X += x
	placed.Y += y
	return &placed
}

// Paste handles text the terminal pasted and reports whether the strip took
// it: into the terminal being typed into, or into a tab name being edited.
// Anywhere else the view's own fields get it.
func (s *TabStrip) Paste(msg tea.PasteMsg) (handled bool, cmd tea.Cmd) {
	if s.tabs == nil {
		return false, nil
	}
	switch {
	case s.typing:
		if term := s.tabs.Current(); term != nil && term.Alive() {
			term.Paste(msg.Content)
		}
	case s.renaming:
		s.nameInput, cmd = s.nameInput.Update(msg)
	case s.confirmClose:
		// Any key but y keeps the program, and a paste is not a y.
		s.confirmClose = false
	default:
		return false, nil
	}
	return true, cmd
}

// Key handles the keys of the tabs and reports whether msg was one. Anything
// else, including x, n and enter on a fixed tab, is the view's to handle.
func (s *TabStrip) Key(msg tea.KeyPressMsg) (handled bool, cmd tea.Cmd) {
	if s.tabs == nil {
		return false, nil
	}
	sel := s.tabs.Selected()
	term := s.tabs.Current()

	// esc is the bare key only: with alt it is a key of its own, and the escape
	// sequences of arrows and function keys arrive as those keys.
	esc := isEsc(msg)
	alt := msg.Mod.Contains(tea.ModAlt)

	switch {
	case s.typing:
		switch {
		case isLeaveKey(msg) || term == nil:
			s.typing = false
		case esc && !alt && s.EscLeaves():
			s.typing = false
		// The second esc of two in a row: the first went to the program,
		// which has done with it what esc does there; this one is den's.
		case esc && !alt && time.Since(s.escAt) < DoubleEsc:
			s.typing, s.escAt = false, time.Time{}
		// Two escs read at once arrive as alt+esc: the first is the
		// program's, the second den's, as when they come apart. A terminal that
		// disambiguates keys never merges them, so there alt+esc is the key it
		// says and goes to the program.
		case esc && alt && term.Alive() && !s.tabs.KeyDisambiguation():
			term.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
			s.typing, s.escAt = false, time.Time{}
		case term.Alive():
			s.escAt = time.Time{}
			if esc && !alt {
				s.escAt = time.Now()
			}
			term.HandleKey(msg)
		case msg.Code == tea.KeyEnter:
			return true, s.open(s.tabs.ID(sel))
		}
		return true, nil

	case s.renaming:
		switch {
		case msg.Code == tea.KeyEnter:
			s.tabs.Rename(sel, s.nameInput.Value())
			s.renaming = false
		case isEsc(msg):
			s.renaming = false
		default:
			s.nameInput, _ = s.nameInput.Update(msg)
		}
		return true, nil

	case s.confirmClose:
		s.confirmClose = false
		if msg.String() == "y" {
			s.tabs.Close(sel)
		}
		return true, nil
	}

	switch k := msg.String(); k {
	case "t":
		if s.tabs.Full() {
			return true, func() tea.Msg {
				return msgs.StatusFlashMsg{Text: fmt.Sprintf("%d terminals is the limit — close one with x", MaxTerminals), Warn: true}
			}
		}
		return true, s.open(0)
	case "right", "tab":
		if s.tabs.count() < 2 {
			return false, nil
		}
		s.tabs.Next()
	case "left", "shift+tab":
		if s.tabs.count() < 2 {
			return false, nil
		}
		s.tabs.Prev()
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if p := s.tabs.Position(int(k[0] - '0')); p >= 0 {
			s.tabs.Select(p)
			s.typing, s.escAt = true, time.Time{}
		}
	case "enter":
		switch {
		case term == nil:
			return false, nil
		case term.Alive():
			s.typing, s.escAt = true, time.Time{}
		default:
			return true, s.open(s.tabs.ID(sel))
		}
	case "n":
		if term == nil {
			return false, nil
		}
		s.startRename(sel)
	case "x":
		switch {
		case term == nil:
			return false, nil
		case term.Alive():
			s.confirmClose = true
		default:
			s.tabs.Close(sel)
		}
	default:
		return false, nil
	}
	return true, nil
}

// Click handles a click on the bar, or on the body of the terminal on screen
// (the zone "body", which the view marks), and reports whether id was the
// strip's. A click on a tab selects it and takes the keyboard back from a
// terminal: picking a tab is an act of its own, not a key for the program. A
// double-click on a terminal's tab does what enter does there, and a click on
// a live terminal's body hands it the keyboard; an exited one is restarted
// with a key, never by a stray click. While a tab is renamed or its close
// confirmed, every click is swallowed: none of them is an answer.
func (s *TabStrip) Click(id string, double bool) (handled bool, cmd tea.Cmd) {
	if s.tabs == nil {
		return false, nil
	}
	if s.renaming || s.confirmClose {
		return true, nil
	}
	switch id {
	case "tab:prev":
		s.typing = false
		s.tabs.Prev()
		return true, nil
	case "tab:next":
		s.typing = false
		s.tabs.Next()
		return true, nil
	case "body":
		if term := s.Current(); term != nil && term.Alive() {
			s.StartTyping()
		}
		return true, nil
	}
	p, ok := ZoneIndex(id, "tab")
	if !ok || p >= s.tabs.count() {
		return false, nil
	}
	s.typing = false
	s.tabs.Select(p)
	if double && s.tabs.Term(p) != nil {
		return s.Key(tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	return true, nil
}

func (s *TabStrip) startRename(p int) {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = MaxNameLen
	// Nothing delivers blink ticks to this field; a static cursor stays visible.
	StaticCursor(&in)
	in.SetValue(s.tabs.Name(p))
	in.CursorEnd()
	in.Focus()
	s.nameInput = in
	s.renaming = true
}

var (
	tabRule     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C91BF"))
	tabIdle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
	tabSelected = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFDF5"))
	// tabTyping marks the tab that has the keyboard, so it is never a guess
	// whether a key goes to den or to the program.
	tabTyping  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1a1a1a")).Background(lipgloss.Color("#ECFD65"))
	tabDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	tabRunning = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575"))
)

const tabSeparator = " │ "

// TabBarLines is how many rows Bar takes: the tabs, and the rule under them.
const TabBarLines = 2

// Bar is what sits between a view's header and its body, at most width cells:
// "─ Logs" for a view without terminals, and the tabs otherwise, then a rule
// under them like the one closing the header above, so the tabs read as a
// row of their own rather than the body's first line.
func (s TabStrip) Bar(width int) string {
	return s.tabsLine(width) + "\n" + tabDim.Render(strings.Repeat("─", max(width, 1)))
}

func (s TabStrip) tabsLine(width int) string {
	if s.tabs == nil {
		return tabRule.Render("─ Logs ")
	}
	sel := s.tabs.Selected()
	labels := make([]string, s.tabs.count())
	widths := make([]int, len(labels))
	for p := range labels {
		labels[p] = s.label(p, p == sel)
		widths[p] = lipgloss.Width(labels[p])
	}

	const prefix = "─ "
	budget := width - lipgloss.Width(prefix)
	from, to := visibleTabs(widths, sel, budget)
	for p := from; p < to; p++ {
		labels[p] = Zone(ZoneID("tab", p), labels[p])
	}
	line := strings.Join(labels[from:to], tabDim.Render(tabSeparator))
	if from > 0 {
		line = Zone("tab:prev", tabDim.Render("‹ ")) + line
	}
	if to < len(labels) {
		line += Zone("tab:next", tabDim.Render(" ›"))
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(tabRule.Render(prefix) + line)
}

// label renders the tab at position p: a fixed tab's name, or a terminal's
// number, name and whether its program still runs.
func (s TabStrip) label(p int, selected bool) string {
	style := tabIdle
	switch {
	case selected && s.typing:
		style = tabTyping
	case selected:
		style = tabSelected
	}
	if p < s.tabs.Fixed() {
		return style.Render(s.tabs.FixedName(p))
	}
	n := s.tabs.Number(p)
	if selected && s.renaming {
		return style.Render(fmt.Sprintf("%d ", n)) + s.nameInput.View()
	}
	dot := tabDim.Render("○")
	if s.tabs.Term(p).Alive() {
		dot = tabRunning.Render("●")
	}
	return style.Render(fmt.Sprintf("%d %s", n, s.tabs.Name(p))) + " " + dot
}

// visibleTabs picks the run of tabs [from, to) around the selected one that
// fits budget cells, growing right then left, with room kept for the marks
// that say tabs were left out.
func visibleTabs(widths []int, sel, budget int) (from, to int) {
	const marks = 4 // "‹ " and " ›"
	total := 0
	for _, w := range widths {
		total += w
	}
	if total+(len(widths)-1)*lipgloss.Width(tabSeparator) <= budget {
		return 0, len(widths)
	}
	from, to = sel, sel+1
	used := widths[sel] + marks
	for grew := true; grew; {
		grew = false
		if to < len(widths) && used+lipgloss.Width(tabSeparator)+widths[to] <= budget {
			used += lipgloss.Width(tabSeparator) + widths[to]
			to++
			grew = true
		}
		if from > 0 && used+lipgloss.Width(tabSeparator)+widths[from-1] <= budget {
			used += lipgloss.Width(tabSeparator) + widths[from-1]
			from--
			grew = true
		}
	}
	return from, to
}

// HelpNote renders the highlighted note a help line can start with.
func HelpNote(text string) string {
	return "  " + lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ECFD65")).Render(text)
}

// ModeHelp is the help line for the strip's own modes and for a terminal tab
// on screen, or ok false when a fixed tab has the keyboard and the view's own
// keys apply. extra are the view's keys to offer on a terminal tab after the
// strip's own, ending with its way back.
func (s TabStrip) ModeHelp(extra ...HelpKey) (line string, ok bool) {
	if s.tabs == nil {
		return "", false
	}
	sel := s.tabs.Selected()
	// The line says how many escs leave, so it is never a guess whether the
	// key goes to den or to the editor that has the terminal.
	leave := HelpKey{"esc esc/ctrl+t", "den keys"}
	if s.EscLeaves() {
		leave.Key = "esc/ctrl+t"
	}
	switch {
	case s.typing && s.tabs.Exit(sel) != "":
		return HelpNote(s.tabs.Exit(sel)) + "  " + Hints(HelpKey{"enter", "restart"}, leave), true
	case s.typing:
		return HelpNote("TERMINAL") + "  " + Hints(leave), true
	case s.renaming:
		return HelpNote("rename") + "  " + Hints(HelpKey{"enter", "save"}, HelpKey{"esc", "cancel"}), true
	case s.confirmClose:
		return HelpNote(fmt.Sprintf("close %d %s? its program is still running", s.tabs.Number(sel), s.tabs.Name(sel))) +
			"  " + Hints(HelpKey{"y", "close"}, HelpKey{"any other key", "keep"}), true
	case s.tabs.Term(sel) == nil:
		return "", false
	}
	enter := HelpKey{"enter", "type"}
	prefix := ""
	if exit := s.tabs.Exit(sel); exit != "" {
		enter, prefix = HelpKey{"enter", "restart"}, HelpNote(exit)
	}
	keys := []HelpKey{{"h", "help"}, enter, {"←→", "tabs"}, {"1-9", "jump"}, {"t", "new"}, {"n", "rename"}, {"x", "close"}}
	return prefix + "  " + Hints(append(keys, extra...)...), true
}
