package help

import (
	"fmt"
	"io/fs"
	"strings"

	den "github.com/lukaszgard/den"
	"github.com/lukaszgard/den/internal/tui/components"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	dim   = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	title = lipgloss.NewStyle().Foreground(lipgloss.Color("#D0D0D0"))
)

// Model is the Help pane: a list of topics grouped under headings, and the
// topic opened from it. The documents are docs/help, embedded at build time,
// so the in-app help and the published docs cannot drift apart.
type Model struct {
	groups []Group
	err    error // the docs did not load; shown instead of the list

	width, height int
	// light is set once the terminal reported a light background; the zero
	// value is the dark palette, which shows until it replies.
	light bool

	cursor int    // the selected topic, an index into visible()
	offset int    // the first list row on screen
	typing bool   // the search box has the keyboard
	query  string // what the list is narrowed to; empty when no search is on

	open    bool   // a topic is open in doc
	reading listed // the open topic
	doc     components.MarkdownView
}

// listed is a topic as the list shows it: with the group it is under, whose
// icon and colour it takes.
type listed struct {
	Topic
	group string
}

// New creates the Help pane over the embedded docs.
func New(width, height int) Model {
	return newModel(den.HelpDocs(), width, height)
}

func newModel(docs fs.FS, width, height int) Model {
	groups, err := Load(docs)
	return Model{groups: groups, err: err}.SetSize(width, height)
}

// SetSize fits the pane to width×height, keeping the list's place and the
// open topic.
func (m Model) SetSize(width, height int) Model {
	m.width, m.height = width, height
	if m.open {
		m.doc.SetSize(width, height)
		m.doc.SetHeader(header(m.reading.Topic, groupLook(m.reading.group), width-2, !m.light))
	}
	return m
}

// SetDark switches the pane to the palette of a dark or a light background,
// which the terminal reports once the program runs.
func (m Model) SetDark(isDark bool) Model {
	m.light = !isDark
	if m.open {
		m.doc.SetDark(isDark)
		return m.SetSize(m.width, m.height)
	}
	return m
}

// Dark reports whether the pane draws for a dark background.
func (m Model) Dark() bool { return !m.light }

// Searching reports that a search box has the keyboard, so keys are text.
func (m Model) Searching() bool {
	return m.typing || (m.open && m.doc.Searching())
}

// Reading is the title of the open topic, or "" while the list shows.
func (m Model) Reading() string {
	if !m.open {
		return ""
	}
	return m.reading.Title
}

// Busy reports that esc, ← and backspace are the pane's own: they close a
// topic or clear a search before they return to the menu.
func (m Model) Busy() bool { return m.open || m.query != "" }

// Shortcuts lists the pane's keys for the h/? pop-up.
func (m Model) Shortcuts() []components.ShortcutGroup {
	if m.open {
		return []components.ShortcutGroup{{Title: "Topic", Keys: []components.Shortcut{
			{Keys: "↑ ↓", Text: "Scroll (pgup, pgdn by page)"},
			{Keys: "/", Text: "Search this topic"},
			{Keys: "n N", Text: "Next / previous match"},
			{Keys: "esc", Text: "Clear the search, then back to the list"},
		}}}
	}
	return []components.ShortcutGroup{{Title: "Topics", Keys: []components.Shortcut{
		{Keys: "↑ ↓", Text: "Move between topics"},
		{Keys: "enter", Text: "Open the topic"},
		{Keys: "/", Text: "Search the text of every topic"},
		{Keys: "esc", Text: "Clear the search"},
	}}}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg, focused bool) (Model, tea.Cmd) {
	// Pasted text goes into whichever search is open, as typed text does.
	if paste, ok := msg.(tea.PasteMsg); ok && focused {
		switch {
		case m.open:
			return m, m.doc.Update(paste)
		case m.typing:
			m.query, m.cursor = m.query+paste.Content, 0
			m.offset = m.scroll(m.rows())
		}
		return m, nil
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || !focused {
		return m, nil
	}
	switch {
	case m.open:
		return m.updateDoc(key)
	case m.typing:
		m = m.typeKey(key)
		m.offset = m.scroll(m.rows())
		return m, nil
	}
	topics := m.visible()
	switch key.String() {
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
	case "down", "j":
		m.cursor = min(m.cursor+1, max(len(topics)-1, 0))
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = max(len(topics)-1, 0)
	case "enter", "right", "l":
		m = m.openTopic()
	case "/":
		m = m.clearSearch()
		m.typing = true
	case "esc", "backspace", "left":
		m = m.clearSearch()
	}
	m.offset = m.scroll(m.rows())
	return m, nil
}

// clearSearch ends the search, leaving the cursor on the topic it was on.
func (m Model) clearSearch() Model {
	at := m.selectedIndexInAll()
	m.typing, m.query, m.cursor = false, "", at
	return m
}

// updateDoc routes a key to the open topic. esc, ← and backspace clear its
// search first, then go back to the list.
func (m Model) updateDoc(key tea.KeyPressMsg) (Model, tea.Cmd) {
	switch key.String() {
	case "esc", "backspace", "left":
		if !m.doc.Searching() && !m.doc.HasQuery() {
			m.open = false
			return m, nil
		}
	}
	cmd := m.doc.Update(key)
	return m, cmd
}

// typeKey edits the search while the box has the keyboard. ↑ and ↓ still move
// through what it found, and enter opens the selected topic.
func (m Model) typeKey(key tea.KeyPressMsg) Model {
	switch key.String() {
	case "esc":
		return m.clearSearch()
	case "enter":
		m.typing = false
		if len(m.visible()) == 0 {
			m.query = ""
			return m
		}
		return m.openTopic()
	case "up":
		m.cursor = max(m.cursor-1, 0)
	case "down":
		m.cursor = min(m.cursor+1, max(len(m.visible())-1, 0))
	case "backspace":
		if m.query == "" {
			m.typing = false
			return m
		}
		runes := []rune(m.query)
		m.query, m.cursor = string(runes[:len(runes)-1]), 0
	default:
		if key.Text == "" {
			return m
		}
		m.query, m.cursor = m.query+key.Text, 0
	}
	return m
}

// openTopic opens the selected topic, at the first match of the search when
// one is on.
func (m Model) openTopic() Model {
	t, ok := m.selected()
	if !ok {
		return m
	}
	m.doc = components.NewMarkdownView(t.Title, t.Body(),
		components.HelpKey{Key: "esc", Text: "back"}, components.HelpKey{Key: "↑↓", Text: "scroll"})
	m.doc.SetLineStyle(headingLine)
	m.doc.SetDark(!m.light)
	m.open, m.reading = true, t
	m = m.SetSize(m.width, m.height)
	if m.query != "" {
		m.doc.Search(m.query)
	}
	return m
}

// visible is the topics the list shows, in order: every topic, or those the
// search finds.
func (m Model) visible() []listed {
	var out []listed
	for _, g := range m.groups {
		for _, t := range g.Topics {
			if m.query == "" || t.Matches(m.query) > 0 {
				out = append(out, listed{t, g.Title})
			}
		}
	}
	return out
}

func (m Model) selected() (listed, bool) {
	topics := m.visible()
	if m.cursor < 0 || m.cursor >= len(topics) {
		return listed{}, false
	}
	return topics[m.cursor], true
}

// selectedIndexInAll is where the selected topic is in the whole list, so
// clearing a search keeps the cursor on it.
func (m Model) selectedIndexInAll() int {
	t, ok := m.selected()
	if !ok {
		return 0
	}
	i := 0
	for _, g := range m.groups {
		for _, other := range g.Topics {
			if other.File == t.File {
				return i
			}
			i++
		}
	}
	return 0
}

// --- rendering ---

// row is one line of the list: a group's heading, a gap, or a topic.
type row struct {
	heading string // the group's title, on its heading row
	group   string // the group a heading or a topic belongs to
	topic   *Topic
	index   int // the topic's index in visible()
}

// rows lays out the groups that have a visible topic, a blank line between
// groups.
func (m Model) rows() []row {
	var out []row
	n := 0
	for _, g := range m.groups {
		var topics []row
		for i := range g.Topics {
			t := &g.Topics[i]
			if m.query == "" || t.Matches(m.query) > 0 {
				topics = append(topics, row{group: g.Title, topic: t, index: n})
				n++
			}
		}
		if len(topics) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, row{})
		}
		out = append(out, row{heading: g.Title, group: g.Title})
		out = append(out, topics...)
	}
	return out
}

func (m Model) View(width, height int, focused bool) string {
	if m.open {
		return m.doc.View()
	}
	m.width, m.height = width, height
	lines := []string{"", m.title(width), ""}
	switch rows := m.rows(); {
	case m.err != nil:
		lines = append(lines, dim.Render("   The help documents did not load:"), dim.Render("   "+m.err.Error()))
	case len(rows) == 0:
		lines = append(lines, dim.Render("   No topic mentions "+m.query+"."))
	default:
		lines = append(lines, m.list(rows, width, focused)...)
	}
	footer := "  " + components.HintLine(width-2, focused, m.hints()...)
	for len(lines)+1 < height {
		lines = append(lines, "")
	}
	if len(lines)+1 > height {
		lines = lines[:max(height-1, 0)]
	}
	return strings.Join(append(lines, footer), "\n")
}

func (m Model) title(width int) string {
	head := lipgloss.NewStyle().Bold(true).Foreground(helpLook.color.resolve(!m.light)).Render("Help")
	switch {
	case m.typing:
		head += "  /" + m.query + "▌"
	case m.query != "":
		head += "  /" + m.query
	}
	n := len(m.visible())
	count := fmt.Sprintf("   %d %s", n, plural(n, "topic", "topics"))
	return "  " + head + dim.Render(ansi.Truncate(count, max(width-lipgloss.Width(head)-4, 0), "…"))
}

func (m Model) hints() []components.HelpKey {
	if m.typing {
		return []components.HelpKey{{Key: "↑↓", Text: "move"}, {Key: "enter", Text: "open"}, {Key: "esc", Text: "clear"}}
	}
	keys := []components.HelpKey{{Key: "↑↓", Text: "move"}, {Key: "enter", Text: "open"}, {Key: "/", Text: "search"}}
	if m.query != "" {
		keys = append(keys, components.HelpKey{Key: "esc", Text: "clear search"})
	}
	return append(keys, components.HelpKey{Key: "h", Text: "keys"})
}

// listRows is how many list rows fit: the pane minus the title and the blank
// lines around it, and the hint.
func (m Model) listRows() int { return max(m.height-4, 3) }

// scroll is the first row on screen: it moves as little as it can to keep the
// cursor, and the heading of its group when it is the group's first topic, in
// a window of listRows rows, two of which are kept for the "more" markers.
func (m Model) scroll(rows []row) int {
	at := m.cursorRow(rows)
	first := at
	if at > 0 && rows[at-1].heading != "" {
		first = at - 1
	}
	inner := max(m.listRows()-2, 1)
	offset := m.offset
	if first < offset {
		offset = first
	}
	if at >= offset+inner {
		offset = at - inner + 1
	}
	return max(min(offset, len(rows)-inner), 0)
}

func (m Model) cursorRow(rows []row) int {
	for i, r := range rows {
		if r.topic != nil && r.index == m.cursor {
			return i
		}
	}
	return 0
}

// list renders the window of rows that holds the cursor, with "more" markers
// where rows are cut off.
func (m Model) list(rows []row, width int, focused bool) []string {
	offset := m.scroll(rows)
	end := min(offset+max(m.listRows()-2, 1), len(rows))

	titleWidth := 0
	for _, r := range rows {
		if r.topic != nil {
			titleWidth = max(titleWidth, lipgloss.Width(r.topic.Title))
		}
	}
	titleWidth = min(titleWidth, max(width/2-4, 10))

	// The markers count topics, not the headings and gaps between them.
	var out []string
	if n := countTopics(rows[:offset]); n > 0 {
		out = append(out, dim.Render(fmt.Sprintf("   ↑ %d more", n)))
	}
	for _, r := range rows[offset:end] {
		out = append(out, m.line(r, width, titleWidth, focused))
	}
	if n := countTopics(rows[end:]); n > 0 {
		out = append(out, dim.Render(fmt.Sprintf("   ↓ %d more", n)))
	}
	return out
}

func countTopics(rows []row) int {
	n := 0
	for _, r := range rows {
		if r.topic != nil {
			n++
		}
	}
	return n
}

func (m Model) line(r row, width, titleWidth int, focused bool) string {
	l := groupLook(r.group)
	switch {
	case r.heading != "":
		return "   " + lipgloss.NewStyle().Bold(true).Foreground(l.color.resolve(!m.light)).Render(l.icon+" "+strings.ToUpper(r.heading))
	case r.topic == nil:
		return ""
	}
	cursor, name := "  ", title
	if r.index == m.cursor {
		cursor = lipgloss.NewStyle().Foreground(l.color.resolve(!m.light)).Render("▸ ")
		name = name.Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Faint(!focused)
	}
	cell := r.topic.Title
	if lipgloss.Width(cell) > titleWidth {
		cell = ansi.Truncate(cell, titleWidth, "…")
	}
	cell += strings.Repeat(" ", titleWidth-lipgloss.Width(cell))
	about := r.topic.Summary
	if m.query != "" {
		n := r.topic.Matches(m.query)
		about = fmt.Sprintf("%d %s", n, plural(n, "match", "matches"))
	}
	line := "   " + cursor + name.Render(cell)
	// 3 indent + 2 cursor + the title column + 3 gap.
	if room := width - 8 - titleWidth; room >= 12 {
		line += "   " + dim.Render(ansi.Truncate(about, room, "…"))
	}
	return components.Zone(components.ZoneID("topic", r.index), line)
}

// Click moves the cursor to the topic the zone id marks; not while a topic is
// open.
func (m Model) Click(id string) Model {
	if i, ok := components.ZoneIndex(id, "topic"); ok && !m.open && i < len(m.visible()) {
		m.cursor = i
		m.offset = m.scroll(m.rows())
	}
	return m
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
