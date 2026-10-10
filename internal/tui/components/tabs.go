package components

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Terminal is a program running in a terminal, as a tab shows it. TermView is
// the real one.
type Terminal interface {
	// View renders the screen, one line per row.
	View() string
	// Cursor is where the program's cursor is, from the top left of what View
	// renders, or nil while it hides it or has exited.
	Cursor() *tea.Cursor
	Resize(cols, rows int)
	HandleKey(msg tea.KeyPressMsg)
	// Paste sends text the terminal pasted, bracketed when the program asked
	// for that so a pasted newline is not mistaken for enter.
	Paste(text string)
	// Alive reports whether the program is still running.
	Alive() bool
	// FullScreen reports that a program which handles every key itself has
	// the terminal — an editor, a pager — rather than a prompt.
	FullScreen() bool
	// ExitErr is how it ended; nil after a clean exit and while it runs.
	ExitErr() error
	// Close stops the program. Its last screen stays readable.
	Close()
}

const (
	// MaxTerminals is how many terminals one view can hold: the digit keys
	// 1-9 select them.
	MaxTerminals = 9
	// MaxNameLen caps a tab's name, in characters, so a few tabs still fit a
	// narrow window.
	MaxNameLen = 24
)

// Tabs are the terminals opened next to a view's fixed tabs: "Logs" for a
// service, "Logs", "History" and "About" for a runbook. Positions 0 to
// Fixed()-1 are the fixed tabs; the terminals follow in the order they were
// opened, and are numbered from 1.
//
// The root model owns the Tabs of each service and runbook, and hands them to
// a view that is rebuilt every time it opens: this is what lets the
// terminals, their names and the selection outlive it.
type Tabs struct {
	fixed    []string
	items    []*tab
	selected int
	nextID   int
	// disambiguated is set when the terminal den runs in reports keys
	// unambiguously (the kitty keyboard protocol): a bare esc is then never
	// the first half of an alt sequence.
	disambiguated bool
}

type tab struct {
	id   int
	name string
	term Terminal
}

// NewTabs returns tabs with the given fixed tabs and no terminal yet.
func NewTabs(fixed ...string) *Tabs { return &Tabs{fixed: fixed} }

// SetKeyDisambiguation records whether the terminal den runs in tells keys
// apart unambiguously, which Bubble Tea reports once at the start. The root
// model sets it, on the tabs it already has and on those it makes later.
func (t *Tabs) SetKeyDisambiguation(on bool) { t.disambiguated = on }

// KeyDisambiguation reports what SetKeyDisambiguation recorded.
func (t *Tabs) KeyDisambiguation() bool { return t.disambiguated }

// Fixed is how many fixed tabs come before the terminals.
func (t *Tabs) Fixed() int { return len(t.fixed) }

// FixedName is the name of fixed tab p.
func (t *Tabs) FixedName(p int) string {
	if p >= 0 && p < len(t.fixed) {
		return t.fixed[p]
	}
	return ""
}

// SetFixed replaces the fixed tabs, as when a runbook's About tab is switched
// off, keeping the terminals and the selection: on the same terminal, or on a
// fixed tab that is still there.
func (t *Tabs) SetFixed(fixed ...string) {
	if t.selected >= len(t.fixed) {
		t.selected += len(fixed) - len(t.fixed)
	} else {
		t.selected = min(t.selected, len(fixed)-1)
	}
	t.fixed = fixed
	t.selected = max(t.selected, 0)
}

// Len is the number of terminals.
func (t *Tabs) Len() int { return len(t.items) }

// count is the number of tabs, fixed ones included.
func (t *Tabs) count() int { return len(t.fixed) + len(t.items) }

// Full reports that no further terminal fits.
func (t *Tabs) Full() bool { return len(t.items) >= MaxTerminals }

// Selected is the position of the tab on screen.
func (t *Tabs) Selected() int { return t.selected }

// Select shows the tab at position p, if there is one.
func (t *Tabs) Select(p int) {
	if p >= 0 && p < t.count() {
		t.selected = p
	}
}

// SelectFixed shows the fixed tab called name, if there is one: by name, as
// SetFixed may have moved it since it was noted.
func (t *Tabs) SelectFixed(name string) {
	if i := slices.Index(t.fixed, name); i >= 0 {
		t.selected = i
	}
}

// Next and Prev move the selection through every tab, wrapping at either end.
func (t *Tabs) Next() { t.selected = (t.selected + 1) % t.count() }
func (t *Tabs) Prev() { t.selected = (t.selected + t.count() - 1) % t.count() }

// Number is the number a terminal at position p is shown and selected with,
// 0 for a fixed tab.
func (t *Tabs) Number(p int) int {
	if t.at(p) == nil {
		return 0
	}
	return p - len(t.fixed) + 1
}

// Position is where terminal number n sits, or -1 when there is none.
func (t *Tabs) Position(n int) int {
	if n < 1 || n > len(t.items) {
		return -1
	}
	return len(t.fixed) + n - 1
}

// at returns the terminal tab at position p, or nil for a fixed tab and for a
// tab that does not exist.
func (t *Tabs) at(p int) *tab {
	i := p - len(t.fixed)
	if i < 0 || i >= len(t.items) {
		return nil
	}
	return t.items[i]
}

// Term is the terminal at position p, or nil.
func (t *Tabs) Term(p int) Terminal {
	if tb := t.at(p); tb != nil {
		return tb.term
	}
	return nil
}

// Current is the selected terminal, or nil while a fixed tab is shown.
func (t *Tabs) Current() Terminal { return t.Term(t.selected) }

// Name is the name of the terminal at position p.
func (t *Tabs) Name(p int) string {
	if tb := t.at(p); tb != nil {
		return tb.name
	}
	return ""
}

// ID identifies the terminal at position p for as long as it exists, whatever
// its position becomes; 0 when there is no such terminal.
func (t *Tabs) ID(p int) int {
	if tb := t.at(p); tb != nil {
		return tb.id
	}
	return 0
}

// Exit says how the program of the terminal at p ended, or "" while it runs.
func (t *Tabs) Exit(p int) string {
	tb := t.at(p)
	if tb == nil || tb.term.Alive() {
		return ""
	}
	if err := tb.term.ExitErr(); err != nil {
		return "exited: " + err.Error()
	}
	return "exited"
}

// Running counts the terminals whose program is still running.
func (t *Tabs) Running() int {
	n := 0
	for _, tb := range t.items {
		if tb.term.Alive() {
			n++
		}
	}
	return n
}

// Add appends a terminal and selects it. It reports the new tab's position,
// or false when the tabs are full; the caller then still owns term.
func (t *Tabs) Add(name string, term Terminal) (p int, ok bool) {
	if t.Full() {
		return 0, false
	}
	t.nextID++
	t.items = append(t.items, &tab{id: t.nextID, name: name, term: term})
	t.selected = t.count() - 1
	return t.selected, true
}

// Replace puts a fresh terminal into the tab with the given id, keeping its
// name and place, and stops the one it held. It reports false when that tab
// has been closed in the meantime; the caller then still owns term.
func (t *Tabs) Replace(id int, term Terminal) bool {
	for _, tb := range t.items {
		if tb.id == id {
			tb.term.Close()
			tb.term = term
			return true
		}
	}
	return false
}

// Rename names the terminal at position p. A blank name keeps the old one.
func (t *Tabs) Rename(p int, name string) {
	tb := t.at(p)
	name = strings.TrimSpace(name)
	if tb == nil || name == "" {
		return
	}
	if r := []rune(name); len(r) > MaxNameLen {
		name = string(r[:MaxNameLen])
	}
	tb.name = name
}

// Close stops the program of the terminal at position p and removes its tab.
// The selection stays on the tab it was on; when that is the one closed, it
// moves to the tab that takes its place, else the one before it.
func (t *Tabs) Close(p int) {
	tb := t.at(p)
	if tb == nil {
		return
	}
	tb.term.Close()
	i := p - len(t.fixed)
	t.items = append(t.items[:i], t.items[i+1:]...)
	if t.selected > p || t.selected >= t.count() {
		t.selected--
	}
}

// StopAll stops every program and keeps the tabs, with their names and last
// screens.
func (t *Tabs) StopAll() {
	for _, tb := range t.items {
		tb.term.Close()
	}
}
