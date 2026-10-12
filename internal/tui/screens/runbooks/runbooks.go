// Package runbooks implements the "Runbooks" pane — user-defined scripts run
// from inside den, with their connection context already set up — and the
// full-screen view of one runbook, with its logs, history and terminals.
package runbooks

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/agent"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Model is the Runbooks pane: the list of runbooks and sequences.
//
// It is a view over the registry: every piece of run state lives there, so a
// window resize, a config reload or a rescan can never orphan a running
// script, and the list is rebuilt whenever the registry's generation moves.
type Model struct {
	reg *runbook.Registry
	ctx context.Context
	// terminals counts the running terminals of a runbook, which the root
	// model owns; nil counts none.
	terminals func(name string) int

	entries []entry
	gen     uint64
	cursor  int
	offset  int
	// drafts are the requests kept for an agent, read when the pane changes
	// them and on Refresh, not on every tick: they are files on disk.
	drafts []runbook.Draft

	searching bool
	query     string

	flow flow
	mig  migration
	del  deletion
	src  sources
	// picker is what n offers when there are drafts; delDraft asks before D
	// deletes one.
	picker   draftPicker
	delDraft draftDeletion
	// naming is the prompt for a new runbook's name and, when an agent is
	// installed, what it should do.
	naming   *newForm
	warnings bool // the warnings pop-up is open

	width, height int
	now           func() time.Time
	// light is set once the terminal reported a light background; the zero
	// value is the dark palette, which shows until it replies.
	light bool
}

// New builds the Runbooks pane over the given registry.
func New(reg *runbook.Registry, ctx context.Context, terminals func(string) int, width, height int) Model {
	m := Model{
		reg:       reg,
		ctx:       ctx,
		terminals: terminals,
		flow:      flow{reg: reg, ctx: ctx},
		mig:       migration{reg: reg},
		del:       deletion{reg: reg},
		src:       sources{reg: reg, ctx: ctx},
		delDraft:  draftDeletion{reg: reg},
		drafts:    reg.Drafts(),
		width:     width,
		height:    height,
		now:       time.Now,
	}
	return m.sync()
}

// sync rebuilds the rows when the registry changed, keeping the cursor on the
// entry it was on.
func (m Model) sync() Model {
	gen := m.reg.Generation()
	if m.entries != nil && gen == m.gen {
		return m
	}
	var current string
	if e, ok := m.selected(); ok {
		current = e.key()
	}
	m.gen, m.entries = gen, entries(m.reg, m.drafts)
	if m.entries == nil {
		m.entries = []entry{}
	}
	for i, e := range m.filtered() {
		if e.key() == current {
			m.cursor = i
		}
	}
	m.cursor = min(m.cursor, max(len(m.filtered())-1, 0))
	return m
}

// WithOutput sends every line of the runs the pane starts to out, with the
// name of the runbook or sequence: the Logs panel shows them.
func (m Model) WithOutput(out func(name, line string)) Model {
	m.flow.out = out
	return m
}

// Refresh reads the drafts again and rebuilds the rows now, rather than at the
// next tick: after a delete, the row must not stay on screen.
func (m Model) Refresh() Model {
	m.drafts = m.reg.Drafts()
	// The entries point into the old drafts: rebuild them, whatever the
	// generation says.
	m.entries = nil
	return m.sync()
}

// SetDark switches the pane to the palette of a dark or a light background,
// which the terminal reports once the program runs. Prompts opened after it
// take the palette; one already open keeps the colours it was drawn in.
func (m Model) SetDark(isDark bool) Model {
	m.light = !isDark
	m.flow.light, m.src.light = m.light, m.light
	if m.naming != nil {
		m.naming.light = m.light
	}
	return m
}

// Dark reports whether the pane draws for a dark background.
func (m Model) Dark() bool { return !m.light }

// SaveDraft writes the open form's draft at once, for a den about to quit:
// what was typed since the last save would be lost.
func (m Model) SaveDraft() {
	if m.naming != nil {
		_ = m.naming.saveDraft()
	}
}

// SetSize resizes in place, keeping the cursor, the filter and any prompt.
func (m Model) SetSize(width, height int) Model {
	m.width, m.height = width, height
	return m
}

// Searching reports whether a text field has the keyboard — the filter, a
// parameter or a new runbook's name — so keys are text, not commands: a q
// typed into one must not quit den.
func (m Model) Searching() bool {
	return m.searching || m.flow.awaitingForm() || m.naming != nil || m.src.awaitingForm() || m.picker.active()
}

// Modal reports that a pop-up is open over the list — a form, a question, the
// warnings or the sources — which keeps the keyboard: tab does not leave it
// for the menu. The / filter is not one: it is part of the list.
func (m Model) Modal() bool {
	return m.flow.active() || m.mig.active() || m.del.active() || m.naming != nil || m.warnings || m.src.active() ||
		m.picker.active() || m.delDraft.active()
}

// AwaitingForm reports whether a form is collecting input. The root model
// uses it to route the form's own messages here.
func (m Model) AwaitingForm() bool {
	return m.flow.awaitingForm() || m.naming != nil || m.src.awaitingForm()
}

// Busy reports whether the pane is in a mode that owns the escape key.
func (m Model) Busy() bool {
	return m.searching || m.flow.active() || m.mig.active() || m.del.active() || m.naming != nil || m.warnings || m.src.active() ||
		m.picker.active() || m.delDraft.active()
}

// Shortcuts lists the keys of the pane for the shortcuts pop-up (h or ?), and
// nothing while a prompt or a question has the keyboard: h is not the help
// there.
func (m Model) Shortcuts() []components.ShortcutGroup {
	if m.Busy() {
		return nil
	}
	return []components.ShortcutGroup{
		{Title: "Runbooks", Note: "Outside den: den run <name>", Keys: []components.Shortcut{
			{Keys: "↑ ↓", Text: "Select a runbook or a sequence (or k, j)"},
			{Keys: "enter", Text: "Open it: logs, history, about, terminals; a draft opens its form again"},
			{Keys: "r", Text: "Run it (parameters and confirmation first)"},
			{Keys: "x", Text: "Cancel its run"},
			{Keys: "n", Text: "New runbook folder; an agent can write it, else it opens in the editor. With drafts, pick one to go on with"},
			{Keys: "E", Text: "Ask its agent to change it (agent: in runbook.yaml or runbooks.defaults)"},
			{Keys: "m", Text: "Move a one-script runbook into its own folder"},
			{Keys: "D", Text: "Delete it for good, with its runs and state, or a draft (asks first)"},
			{Keys: "s", Text: "Runbook sources: your team's runbooks from git"},
			{Keys: "/", Text: "Filter the list"},
			{Keys: "w", Text: "Show the discovery warnings"},
		}},
	}
}

func (m Model) filtered() []entry {
	if m.query == "" {
		return m.entries
	}
	q := strings.ToLower(m.query)
	var out []entry
	for _, e := range m.entries {
		if strings.Contains(strings.ToLower(e.name()), q) || strings.Contains(strings.ToLower(e.description()), q) {
			out = append(out, e)
		}
	}
	return out
}

// Selected is the name of the runbook or sequence under the cursor.
func (m Model) Selected() (string, bool) {
	e, ok := m.selected()
	if !ok {
		return "", false
	}
	return e.name(), true
}

func (m Model) selected() (entry, bool) {
	rows := m.filtered()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return entry{}, false
	}
	return rows[m.cursor], true
}

func (m Model) Update(msg tea.Msg, focused bool) (Model, tea.Cmd) {
	// tab in a pop-up does nothing. A form would take it for "next", which on
	// its last field submits: a runbook made, or run, by a slip of the finger.
	// The new-runbook form is den's own and uses it to reach the agent row.
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "tab" && m.Modal() && m.naming == nil {
		return m, nil
	}
	// A form needs every message, not just keys: its cursor and validation
	// are driven by the others.
	if m.naming != nil {
		return m.updateNaming(msg)
	}
	if m.flow.awaitingForm() {
		var cmd tea.Cmd
		m.flow, cmd = m.flow.update(msg)
		return m, cmd
	}
	if m.src.awaitingForm() {
		var cmd tea.Cmd
		m.src, cmd = m.src.update(msg, m.width)
		return m.sync(), cmd
	}

	switch msg := msg.(type) {
	case msgs.TickMsg:
		m = m.sync()

	case msgs.RunbookSourceDoneMsg:
		var cmd tea.Cmd
		m.src, cmd = m.src.done(msg)
		return m.sync(), cmd

	// Pasted text goes into the filter, as typed text does. A pop-up that
	// takes text is a form, which has had the message above.
	case tea.PasteMsg:
		if focused && m.searching && !m.flow.active() && !m.mig.active() && !m.del.active() && !m.src.active() && !m.warnings {
			m = m.handleSearchPaste(msg.Content)
		}

	case tea.KeyPressMsg:
		if !focused {
			break
		}
		m = m.sync()
		switch {
		case m.flow.active():
			var cmd tea.Cmd
			m.flow, cmd = m.flow.update(msg)
			return m, cmd
		case m.mig.active():
			var cmd tea.Cmd
			m.mig, cmd = m.mig.key(msg)
			return m.sync(), cmd
		case m.del.active():
			var cmd tea.Cmd
			m.del, cmd = m.del.key(msg)
			return m, cmd
		case m.delDraft.active():
			var cmd tea.Cmd
			m.delDraft, cmd = m.delDraft.key(msg)
			return m.Refresh(), cmd
		case m.picker.active():
			var (
				picked bool
				draft  *runbook.Draft
			)
			m.picker, picked, draft = m.picker.key(msg)
			switch {
			case !picked:
				return m, nil
			case draft != nil:
				return m.openDraft(*draft)
			}
			return m.newForm()
		case m.src.active():
			var cmd tea.Cmd
			m.src, cmd = m.src.update(msg, m.width)
			return m.sync(), cmd
		case m.warnings:
			m.warnings = false
			return m, nil
		case m.searching:
			return m.handleSearchKey(msg), nil
		}
		return m.handleListKey(msg)
	}
	return m, nil
}

func (m Model) handleListKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	rows := m.filtered()
	if e, ok := m.selected(); ok && e.isDraft() {
		switch msg.String() {
		case "enter":
			return m.openDraft(*e.draft)
		case "D":
			m.delDraft = m.delDraft.begin(*e.draft)
			return m, nil
		case "r", "x", "m", "E":
			return m, flash(errDraft.Error())
		}
	}
	switch msg.String() {
	case "/":
		m.searching, m.query = true, ""
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
	case "down", "j":
		m.cursor = max(min(m.cursor+1, len(rows)-1), 0)
	case "pgup":
		m.cursor = max(m.cursor-m.listRows(), 0)
	case "pgdown":
		m.cursor = max(min(m.cursor+m.listRows(), len(rows)-1), 0)
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = max(len(rows)-1, 0)
	case "w":
		m.warnings = len(m.reg.Warnings()) > 0
	case "n":
		return m.startNaming()
	case "s":
		m.src = m.src.begin()
	case "E":
		// The view asks what to change, and says why when it cannot.
		if e, ok := m.selected(); ok {
			name := e.name()
			return m, func() tea.Msg { return msgs.OpenRunbookMsg{Name: name, AgentEdit: true} }
		}
	case "m":
		if e, ok := m.selected(); ok {
			var cmd tea.Cmd
			m.mig, cmd = m.mig.begin(e)
			return m, cmd
		}
	case "D":
		if e, ok := m.selected(); ok {
			terms := 0
			if m.terminals != nil {
				terms = m.terminals(e.name())
			}
			var cmd tea.Cmd
			m.del, cmd = m.del.begin(e, terms)
			return m, cmd
		}
	case "x":
		if e, ok := m.selected(); ok && e.isRunning() {
			e.cancel()
			return m, flash(fmt.Sprintf("cancelling %s…", e.name()))
		}
	case "r":
		if e, ok := m.selected(); ok {
			var cmd tea.Cmd
			m.flow, cmd = m.flow.begin(e, m.width, m.height)
			return m, cmd
		}
	case "enter":
		if e, ok := m.selected(); ok {
			name := e.name()
			return m, func() tea.Msg { return msgs.OpenRunbookMsg{Name: name} }
		}
	}
	m.offset = scrollOffset(m.offset, m.cursor, m.listRows(), len(rows))
	return m, nil
}

func (m Model) handleSearchPaste(text string) Model {
	m.query += text
	return m.fitSearch()
}

// fitSearch keeps the cursor and the scroll on the rows the filter left.
func (m Model) fitSearch() Model {
	m.cursor = min(m.cursor, max(len(m.filtered())-1, 0))
	m.offset = scrollOffset(m.offset, m.cursor, m.listRows(), len(m.filtered()))
	return m
}

func (m Model) handleSearchKey(msg tea.KeyPressMsg) Model {
	switch msg.String() {
	case "esc":
		m.searching, m.query = false, ""
	case "enter":
		m.searching = false
	case "backspace":
		if r := []rune(m.query); len(r) > 0 {
			m.query = string(r[:len(r)-1])
		}
	default:
		// Typed characters, but not the space bar: the filter has never
		// taken it.
		if msg.Text != "" && msg.Code != tea.KeySpace {
			m.query += msg.Text
		}
	}
	return m.fitSearch()
}

// startNaming asks for a new runbook's name and, when an agent is
// installed, what it should do; with drafts of new runbooks, it first offers
// them to go on with.
func (m Model) startNaming() (Model, tea.Cmd) {
	if drafts := newDrafts(m.drafts); len(drafts) > 0 {
		m.picker = m.picker.begin(drafts)
		return m, nil
	}
	return m.newForm()
}

// openDraft opens the form d was typed into, filled with it: n's for a new
// runbook, E's in the runbook's view for a change.
func (m Model) openDraft(d runbook.Draft) (Model, tea.Cmd) {
	if d.Kind == runbook.DraftEdit {
		if _, ok := m.reg.Lookup(d.Runbook); !ok {
			return m, warn(d.Runbook + " is no longer listed: D deletes the draft")
		}
		name, id := d.Runbook, d.ID
		return m, func() tea.Msg { return msgs.OpenRunbookMsg{Name: name, AgentEdit: true, Draft: id} }
	}
	m, _ = m.newForm()
	return m, m.naming.fill(d)
}

// newForm opens n's form, empty.
func (m Model) newForm() (Model, tea.Cmd) {
	// Without an agent that can start, the form asks only for the name.
	agents, choice, err := agent.Choices(m.reg.Agents(), m.reg.Agent())
	if err != nil {
		agents = nil
	}
	var cmd tea.Cmd
	m.naming, cmd = newNewForm(m.reg, agents, choice, m.width, m.height, !m.light)
	return m, cmd
}

func (m Model) updateNaming(msg tea.Msg) (Model, tea.Cmd) {
	cmd := m.naming.update(msg)
	if m.naming.closed {
		m.naming = nil
		return m.Refresh(), cmd
	}
	if !m.naming.done {
		return m, cmd
	}
	form := m.naming
	name, request, agentName := form.values()
	m.naming = nil
	dir, err := m.reg.NewRunbook(name, "")
	if err != nil {
		text := err.Error()
		// What was typed is not lost to a name that turned out to be taken.
		if form.worthKeeping() && form.saveDraft() == nil {
			text += " — kept as a draft"
		}
		return m.Refresh(), func() tea.Msg {
			return msgs.StatusFlashMsg{Text: text, Warn: true, Hold: 6 * time.Second}
		}
	}
	var kept tea.Cmd
	if err := form.sent(name, dir); err != nil {
		kept = warn("the request is not kept for a restart: " + err.Error())
	}
	m = m.Refresh()
	if _, ok := lookup(m.reg, name); !ok {
		// A runbook.yaml from a template that does not load: w says why.
		return m, tea.Batch(kept, flash("created, but it did not load — press w"))
	}
	if request != "" {
		return m, tea.Batch(kept, func() tea.Msg { return msgs.OpenRunbookMsg{Name: name, Agent: request, AgentName: agentName} })
	}
	return m, tea.Batch(kept, func() tea.Msg { return msgs.OpenRunbookMsg{Name: name, Edit: true} })
}

// --- rendering ---

// listRows is how many rows the table body has: the pane minus the title and
// the blank lines around it, the table's header and rule, the warnings line
// and the hint.
func (m Model) listRows() int { return max(m.height-7, 3) }

func (m Model) View(width, height int, focused bool) string {
	m.width, m.height = width, height
	view := m.renderList(width, height, focused)
	switch {
	case m.naming != nil:
		// The size View is given, not SetSize's: that one is the terminal's,
		// and a request grown to it would push the pop-up off the pane.
		m.naming.setSize(width, height)
		return components.Overlay(view, m.naming.View(), width, height)
	case m.flow.active():
		return components.Overlay(view, m.flow.view(width, height), width, height)
	case m.mig.active():
		return components.Overlay(view, m.mig.view(width), width, height)
	case m.del.active():
		return components.Overlay(view, m.del.view(width), width, height)
	case m.delDraft.active():
		return components.Overlay(view, m.delDraft.view(width), width, height)
	case m.picker.active():
		return components.Overlay(view, m.picker.view(width, height, m.now()), width, height)
	case m.warnings:
		return components.Overlay(view, m.warningsBox(width, height), width, height)
	case m.src.active():
		return components.Overlay(view, m.src.view(width, height), width, height)
	}
	return view
}

func (m Model) renderList(width, height int, focused bool) string {
	lines := []string{"", m.title(width), ""}
	rows := m.filtered()

	if len(rows) == 0 {
		msg := "No runbooks yet.\n\nA runbook is one of your scripts — bash, Python,\nGo, anything — run with den's AWS profile, region\nand tunnel ports already set.\n\nPoint runbooks.dirs in den.yaml at a folder and\npress n: every folder in it with a runbook.yaml\nis a runbook, with its scripts next to it."
		if m.query != "" {
			msg = "No runbooks match " + m.query + "."
		}
		for l := range strings.SplitSeq(msg, "\n") {
			lines = append(lines, dim.Render("   "+l))
		}
	} else {
		widths := m.layout(width, m.entries)
		lines = append(lines, header(widths)...)
		visible := m.listRows()
		offset := scrollOffset(m.offset, m.cursor, visible, len(rows))
		end := min(offset+max(visible-2, 1), len(rows))
		if offset > 0 {
			lines = append(lines, dim.Render(fmt.Sprintf("   ↑ %d more", offset)))
		}
		for i := offset; i < end; i++ {
			lines = append(lines, m.tableRow(rows[i], i == m.cursor, focused, widths))
		}
		if end < len(rows) {
			lines = append(lines, dim.Render(fmt.Sprintf("   ↓ %d more", len(rows)-end)))
		}
	}

	// The warnings line and the hint stay at the bottom, whatever the list's
	// length: the warnings used to come last and were the first rows cut.
	footer := []string{m.hint(width, focused)}
	if n := len(m.reg.Warnings()); n > 0 {
		footer = append([]string{lipgloss.NewStyle().Foreground(colorWarn).
			Render(fmt.Sprintf("  ⚠ %d %s — w to show", n, plural(n, "warning", "warnings")))}, footer...)
	}
	for len(lines)+len(footer) < height {
		lines = append(lines, "")
	}
	if len(lines)+len(footer) > height {
		lines = lines[:max(height-len(footer), 0)]
	}
	return strings.Join(append(lines, footer...), "\n")
}

// scrollOffset keeps the cursor inside a window of visible rows, two of which
// are kept for the "more" markers, moving it as little as possible.
func scrollOffset(offset, cursor, visible, total int) int {
	inner := max(visible-2, 1)
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+inner {
		offset = cursor - inner + 1
	}
	return max(min(offset, total-inner), 0)
}

func (m Model) title(width int) string {
	head := lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("Runbooks")
	switch {
	case m.searching:
		head += "  /" + m.query + "▌"
	case m.query != "":
		head += "  /" + m.query
	}
	n := len(m.entries)
	return "  " + head + dim.Render(truncate(fmt.Sprintf("   %d %s", n, plural(n, "entry", "entries")), max(width-lipgloss.Width(head)-4, 0)))
}

func (m Model) hint(width int, focused bool) string {
	// x only while the selected entry runs, as in the view: the line must
	// leave room for h, which lists every key.
	keys := []components.HelpKey{key("enter", "open"), key("r", "run")}
	e, ok := m.selected()
	if ok && e.isRunning() {
		keys = append(keys, key("x", "cancel"))
	}
	keys = append(keys, key("n", "new"), key("D", "delete"))
	if ok && e.oldFormat() {
		keys = append(keys, key("m", "move into a folder"))
	}
	keys = append(keys, key("s", "sources"), key("/", "filter"), key("h", "help"))
	switch {
	case m.searching:
		keys = []components.HelpKey{key("enter", "accept"), key("esc", "clear")}
	case m.flow.active():
		keys = m.flow.help()
	case m.mig.active():
		keys = []components.HelpKey{key("y", "move"), key("n", "keep")}
	case m.del.active(), m.delDraft.active():
		keys = []components.HelpKey{key("y", "delete"), key("n", "keep")}
	case m.picker.active():
		keys = []components.HelpKey{key("enter", "open"), key("esc", "cancel")}
	case m.src.active():
		keys = m.src.help()
	}
	return "  " + components.HintLine(width-2, focused, keys...)
}

// warningsBox lists the discovery warnings: a script that looks registered but
// is not would otherwise fail silently.
func (m Model) warningsBox(width, height int) string {
	warns := m.reg.Warnings()
	inner := min(width-8, 100)
	n := len(warns)
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(colorWarn).Render(fmt.Sprintf("%d discovery %s", n, plural(n, "warning", "warnings"))), ""}
	for i, w := range warns {
		if len(lines) >= height-8 {
			lines = append(lines, dim.Render(fmt.Sprintf("…and %d more in den run --list", n-i)))
			break
		}
		where := ""
		if w.Path != "" {
			where = filepath.Base(filepath.Dir(w.Path)) + "/" + filepath.Base(w.Path) + ": "
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#D7C9A7")).Render(truncate(where+w.Msg, inner)))
	}
	lines = append(lines, "", components.Hints(key("any key", "close")))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorWarn).
		Padding(1, 2).Render(strings.Join(lines, "\n"))
}
