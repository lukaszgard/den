// Package configview implements the right-panel "Config" view.
// It renders the config file, with what the last check of it found, and
// offers to edit it.
package configview

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const headerLines = 6 // header + path + blank + blank + hint + blank

// maxProblems is the most problems summarised above the file; the rest are
// counted, so a file full of mistakes still leaves room for the file. The
// list that p opens has them all.
const maxProblems = 5

// minFileLines is the lines of the file that stay visible under the summary.
const minFileLines = 3

var (
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87"))
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#E9C46A"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	hintKey    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C91BF")).Bold(true)
)

// Model is the Config right-pane model.
type Model struct {
	configPath string
	errMsg     string
	rawLines   []string
	// problems are what the last check of the file found, and marks the
	// worst of them on each line, by line number.
	problems config.Problems
	marks    map[int]config.Severity
	// visible are the file lines the viewport shows, by number: all of them,
	// or those matching the filter.
	visible []int
	// focus is the file line the last jump landed on; 0 when none.
	focus     int
	vp        viewport.Model
	width     int
	height    int
	searching bool
	query     string

	// list says the problem list replaces the file; cursor is the problem it
	// is on and offset the first line of the list on screen.
	list   bool
	cursor int
	offset int
}

// Searching reports whether the search bar is currently active.
func (m Model) Searching() bool { return m.searching }

// New creates a ConfigView pane from the discovered config path.
func New(cfgPath string, width, height int) Model {
	vp := components.NewViewport(width-2, height-headerLines)
	m := Model{configPath: cfgPath, width: width, height: height, vp: vp}
	m.reload()
	return m
}

// SetSize changes the pane's size without re-reading the config file, keeping
// the filter and scroll position.
func (m Model) SetSize(width, height int) Model {
	m.width = width
	m.height = height
	m.vp.SetWidth(width - 2)
	m.rebuildViewport()
	return m.keepCursorInView()
}

// Refresh reads the file again, keeping the filter and scroll position: den
// has just checked or applied it.
func (m Model) Refresh() Model {
	m.reload()
	return m
}

// SetProblems shows what the last check of the file found: listed above it,
// and marked beside the lines they are on.
func (m Model) SetProblems(ps config.Problems) Model {
	m.problems = ps
	m.marks = map[int]config.Severity{}
	for _, p := range ps {
		if worst, ok := m.marks[p.Line]; p.Line > 0 && (!ok || p.Severity < worst) {
			m.marks[p.Line] = p.Severity
		}
	}
	if len(ps) == 0 {
		m.list, m.focus = false, 0
	}
	m.cursor = max(min(m.cursor, len(ps)-1), 0)
	m.rebuildViewport()
	m = m.keepCursorInView()
	return m
}

// Problems are what the last check of the file found.
func (m Model) Problems() config.Problems { return m.problems }

// rowsOf is the rows a problem takes in the summary: its message, and its hint
// under it.
func rowsOf(p config.Problem) int {
	if p.Hint == "" {
		return 1
	}
	return 2
}

// shownProblems is how many problems are summarised above the file at this
// height: at most maxProblems, and never so many that fewer than minFileLines
// lines of the file are left.
func (m Model) shownProblems() int {
	room := m.height - headerLines - minFileLines - 2 // the blank line and the count
	shown := 0
	for _, p := range m.problems {
		if shown == maxProblems || room < rowsOf(p)+m.moreRow(shown+1) {
			break
		}
		room -= rowsOf(p)
		shown++
	}
	return shown
}

// moreRow is the row of "and N more" when shown problems are listed of all.
func (m Model) moreRow(shown int) int {
	if shown < len(m.problems) {
		return 1
	}
	return 0
}

// problemRows is the rows the summary takes: a blank line, the count, the
// problems shown with their hints, and a line counting the rest.
func (m Model) problemRows() int {
	if len(m.problems) == 0 || m.list {
		return 0
	}
	shown := m.shownProblems()
	rows := 2 + m.moreRow(shown)
	for _, p := range m.problems[:shown] {
		rows += rowsOf(p)
	}
	return rows
}

func (m *Model) reload() {
	if m.configPath == "" {
		m.errMsg = "No config file found.\nCreate den.yaml or " + platform.TildePath(filepath.Join(platform.ConfigDir(), "den.yaml"))
		return
	}
	data, err := os.ReadFile(m.configPath)
	if err != nil {
		m.errMsg = fmt.Sprintf("Cannot read %s:\n%v", m.configPath, err)
		return
	}
	m.errMsg = ""
	m.rawLines = strings.Split(string(data), "\n")
	m.rebuildViewport()
}

func (m *Model) rebuildViewport() {
	m.vp.SetHeight(max(m.height-headerLines-m.problemRows(), 1))
	m.visible = m.visible[:0]
	if len(m.rawLines) == 0 {
		return
	}
	q := strings.ToLower(m.query)
	digits := len(fmt.Sprint(len(m.rawLines)))
	var lines []string
	for i, l := range m.rawLines {
		if q == "" || strings.Contains(strings.ToLower(l), q) {
			number := dimStyle.Render(fmt.Sprintf("%*d", digits, i+1))
			if i+1 == m.focus {
				number = m.markStyle(i + 1).Bold(true).Render(fmt.Sprintf("%*d", digits, i+1))
			}
			line := m.mark(i+1) + number + "  " + highlightYAMLLine(l)
			// Cut, not wrapped: a wrapped line has no number and pushes the
			// lines after it away from theirs.
			lines = append(lines, ansi.Truncate(line, max(m.vp.Width(), 1), "…"))
			m.visible = append(m.visible, i+1)
		}
	}
	m.vp.SetContent(strings.Join(lines, "\n"))
}

// markStyle is the colour of the worst problem on a line: dim when there is
// none.
func (m *Model) markStyle(line int) lipgloss.Style {
	switch sev, ok := m.marks[line]; {
	case !ok:
		return dimStyle
	case sev == config.SeverityError:
		return errorStyle
	default:
		return warnStyle
	}
}

// mark is the gutter beside a line: a bar in the colour of the worst problem
// on it, so the lines the list names stand out while scrolling. The line the
// last jump landed on has an arrow instead.
func (m *Model) mark(line int) string {
	_, marked := m.marks[line]
	switch {
	case line == m.focus && marked:
		return m.markStyle(line).Render("▶") + " "
	case marked:
		return m.markStyle(line).Render("▌") + " "
	default:
		return "  "
	}
}

// markedLines are the file lines with a problem, in order.
func (m Model) markedLines() []int {
	lines := make([]int, 0, len(m.marks))
	for l := range m.marks {
		lines = append(lines, l)
	}
	slices.Sort(lines)
	return lines
}

// goToLine shows file line at the top of the viewport, as far as the end of
// the file lets it, and marks it as the one jumped to. The filter is cleared:
// a line it hides could not be shown.
func (m Model) goToLine(line int) Model {
	m.list, m.searching, m.query, m.focus = false, false, "", line
	m.rebuildViewport()
	if i := slices.Index(m.visible, line); i >= 0 {
		m.vp.SetYOffset(i)
	}
	return m
}

// step moves to the next (dir 1) or previous (dir -1) marked line, from the
// line last jumped to, or from the top of the view when there was none; after
// the last it goes round to the first.
func (m Model) step(dir int) Model {
	lines := m.markedLines()
	if len(lines) == 0 {
		return m
	}
	from := m.focus
	if from == 0 {
		top := 1
		if y := m.vp.YOffset(); y < len(m.visible) {
			top = m.visible[y]
		}
		from = top - 1 // a mark on the first visible line is the next
		if dir < 0 {
			from = top
		}
	}
	var next int
	if dir > 0 {
		i := slices.IndexFunc(lines, func(l int) bool { return l > from })
		if i < 0 {
			i = 0
		}
		next = lines[i]
	} else {
		next = lines[len(lines)-1]
		for _, l := range slices.Backward(lines) {
			if l < from {
				next = l
				break
			}
		}
	}
	return m.goToLine(next)
}

// problemList is the summary above the file: the first problems with their
// hints under them, cut to width, and the count of the rest.
func (m Model) problemList(width int) []string {
	if len(m.problems) == 0 || m.list {
		return nil
	}
	lines := []string{"", "  " + m.summaryStyle().Bold(true).Render(m.problems.Summary())}
	shown := m.shownProblems()
	for _, p := range m.problems[:shown] {
		icon, style := errorStyle.Render("✗"), errorStyle
		if p.Severity == config.SeverityWarning {
			icon, style = warnStyle.Render("!"), warnStyle
		}
		where := ""
		if p.Line > 0 {
			where = dimStyle.Render(fmt.Sprintf("line %d  ", p.Line))
		}
		lines = append(lines, ansi.Truncate("  "+icon+" "+where+style.Render(p.Msg), max(width-2, 1), "…"))
		if p.Hint != "" {
			lines = append(lines, ansi.Truncate("      "+dimStyle.Render("→ "+p.Hint), max(width-2, 1), "…"))
		}
	}
	if rest := len(m.problems) - shown; rest > 0 {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("    … and %d more — p lists them all", rest)))
	}
	return lines
}

// summaryStyle is the colour of the worst problem.
func (m Model) summaryStyle() lipgloss.Style {
	if m.problems.Errors() > 0 {
		return errorStyle
	}
	return warnStyle
}

// ── The problem list ─────────────────────────────────────────────────────────

// listHeight is the rows the problem list has: the pane less its header, the
// footer and the count line.
func (m Model) listHeight() int { return max(m.height-headerLines-1, 1) }

// listEntry renders problem i for the list: every line of it, wrapped to the
// width rather than cut, since the message and the hint are why it is open.
func (m Model) listEntry(i int) []string {
	p := m.problems[i]
	icon, style := errorStyle.Render("✗"), errorStyle
	if p.Severity == config.SeverityWarning {
		icon, style = warnStyle.Render("!"), warnStyle
	}
	cursor := "  "
	if i == m.cursor {
		cursor = hintKey.Render("▸") + " "
	}
	where := ""
	if p.Line > 0 {
		where = fmt.Sprintf("line %d  ", p.Line)
	}
	indent := 2 + 2 + len(where) // cursor, icon, line label
	room := max(m.width-4-indent, 10)

	var out []string
	for j, l := range strings.Split(ansi.Wrap(p.Msg, room, ""), "\n") {
		if j == 0 {
			out = append(out, "  "+cursor+icon+" "+dimStyle.Render(where)+style.Render(l))
			continue
		}
		out = append(out, "  "+strings.Repeat(" ", indent)+style.Render(l))
	}
	if p.Hint != "" {
		for j, l := range strings.Split(ansi.Wrap(p.Hint, max(room-2, 8), ""), "\n") {
			arrow := "  "
			if j == 0 {
				arrow = "→ "
			}
			out = append(out, "  "+strings.Repeat(" ", indent)+dimStyle.Render(arrow+l))
		}
	}
	// Every line of the entry is the problem's, its hint too.
	for j := range out {
		out[j] = components.Zone(components.ZoneID("problem", i), out[j])
	}
	return out
}

// Click moves the list's cursor to the problem the zone id marks.
func (m Model) Click(id string) Model {
	if i, ok := components.ZoneIndex(id, "problem"); ok && m.list && i < len(m.problems) {
		m = m.moveCursor(i)
	}
	return m
}

// listLines is the whole list, and where each problem starts in it.
func (m Model) listLines() (lines []string, starts []int) {
	for i := range m.problems {
		starts = append(starts, len(lines))
		lines = append(lines, m.listEntry(i)...)
	}
	return lines, starts
}

// keepCursorInView scrolls the list so the problem under the cursor shows,
// all of it when it fits.
func (m Model) keepCursorInView() Model {
	if len(m.problems) == 0 {
		m.offset = 0
		return m
	}
	lines, starts := m.listLines()
	h := m.listHeight()
	start := starts[m.cursor]
	end := len(lines)
	if m.cursor+1 < len(starts) {
		end = starts[m.cursor+1]
	}
	switch {
	case start < m.offset:
		m.offset = start
	case end > m.offset+h:
		m.offset = min(end-h, start)
	}
	m.offset = max(min(m.offset, len(lines)-h), 0)
	return m
}

// moveCursor puts the cursor on problem i, clamped, and scrolls to it.
func (m Model) moveCursor(i int) Model {
	m.cursor = max(min(i, len(m.problems)-1), 0)
	return m.keepCursorInView()
}

// pageCursor moves a screen of lines up (-1) or down (1): the cursor goes to
// the first problem that starts on the new page.
func (m Model) pageCursor(dir int) Model {
	lines, starts := m.listLines()
	h := m.listHeight()
	m.offset = max(min(m.offset+dir*h, len(lines)-h), 0)
	i := slices.IndexFunc(starts, func(s int) bool { return s >= m.offset })
	if i < 0 {
		i = len(starts) - 1
	}
	m.cursor = i
	return m.keepCursorInView()
}

// listView is the list as drawn: the count, and the lines from the offset on.
func (m Model) listView() (summary string, body string) {
	lines, _ := m.listLines()
	h := m.listHeight()
	end := min(m.offset+h, len(lines))
	visible := append([]string(nil), lines[min(m.offset, end):end]...)
	for len(visible) < h {
		visible = append(visible, "")
	}
	for i, l := range visible {
		visible[i] = ansi.Truncate(l, max(m.width-2, 1), "…")
	}
	summary = "  " + m.summaryStyle().Bold(true).Render(m.problems.Summary()) +
		dimStyle.Render(fmt.Sprintf("   %d of %d", m.cursor+1, len(m.problems)))
	return summary, strings.Join(visible, "\n")
}

// OwnsBack reports that the key closes the problem list rather than leaving
// the pane.
func (m Model) OwnsBack(msg tea.KeyPressMsg) bool {
	return m.list && msg.String() == "esc"
}

// Shortcuts lists the keys of the pane for the shortcuts pop-up (h or ?).
func (m Model) Shortcuts() []components.ShortcutGroup {
	return []components.ShortcutGroup{
		{Title: "The file", Keys: []components.Shortcut{
			{Keys: "↑ ↓ pgup pgdn", Text: "Scroll the file"},
			{Keys: "/", Text: "Filter the lines"},
			{Keys: "e", Text: "Edit den.yaml in $EDITOR"},
			{Keys: "r", Text: "Read the file again"},
		}},
		{Title: "Problems", Note: "Problems are marked beside their lines.", Keys: []components.Shortcut{
			{Keys: "p", Text: "List every problem and how to fix it"},
			{Keys: "n N", Text: "Next or previous marked line"},
			{Keys: "↑ ↓ pgup pgdn", Text: "In the list: move between problems"},
			{Keys: "enter", Text: "In the list: show the line in the file"},
			{Keys: "esc p", Text: "In the list: back to the file"},
		}},
	}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg, focused bool) (Model, tea.Cmd) {
	if !focused {
		return m, nil
	}
	switch msg := msg.(type) {
	// Pasted text goes into the filter, as typed text does.
	case tea.PasteMsg:
		if m.searching {
			m.query += msg.Content
			m.rebuildViewport()
			m.vp.GotoTop()
		}
		return m, nil
	case tea.KeyPressMsg:
		if m.searching {
			switch msg.String() {
			case "esc":
				m.searching = false
				m.query = ""
				m.rebuildViewport()
				return m, nil
			case "backspace":
				if m.query == "" {
					m.searching = false
				} else {
					runes := []rune(m.query)
					m.query = string(runes[:len(runes)-1])
				}
				m.rebuildViewport()
				return m, nil
			default:
				if msg.Text != "" {
					m.query += msg.Text
					m.rebuildViewport()
					m.vp.GotoTop()
					return m, nil
				}
			}
		} else {
			if m.list {
				if m2, handled := m.listKey(msg); handled {
					return m2, nil
				}
			}
			switch msg.String() {
			case "/":
				if m.list {
					return m, nil // nothing to filter in a list
				}
				m.searching = true
				m.query = ""
				return m, nil
			case "e":
				return m, func() tea.Msg { return msgs.EditConfigMsg{} }
			case "r":
				m.reload()
				return m, func() tea.Msg { return msgs.ConfigReloadMsg{} }
			case "p":
				if len(m.problems) > 0 {
					m.list = true
					m.rebuildViewport()
					return m.keepCursorInView(), nil
				}
				return m, nil
			case "n":
				if !m.list {
					return m.step(1), nil
				}
			case "N":
				if !m.list {
					return m.step(-1), nil
				}
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.vp.SetWidth(msg.Width - 2)
		m.rebuildViewport()
		return m.keepCursorInView(), nil
	}
	if m.list {
		return m, nil // the list takes the keys it knows; the file is not behind it
	}
	// Forward all other keys (arrows, page up/down, etc.) to the viewport.
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// listKey handles a key while the problem list is open; handled is false for
// a key the list leaves to the pane (edit, reload).
func (m Model) listKey(msg tea.KeyPressMsg) (Model, bool) {
	switch msg.String() {
	case "up", "k":
		return m.moveCursor(m.cursor - 1), true
	case "down", "j":
		return m.moveCursor(m.cursor + 1), true
	case "home":
		return m.moveCursor(0), true
	case "end":
		return m.moveCursor(len(m.problems) - 1), true
	case "pgup":
		return m.pageCursor(-1), true
	case "pgdown":
		return m.pageCursor(1), true
	case "esc", "p":
		m.list = false
		m.rebuildViewport()
		return m, true
	case "enter":
		if line := m.problems[m.cursor].Line; line > 0 {
			return m.goToLine(line), true
		}
		m.list = false
		m.rebuildViewport()
		return m, true
	case "e", "r":
		return m, false
	}
	return m, true // anything else does nothing: the file behind it is not shown
}

func (m Model) View(width, height int, focused bool) string {
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#ECFD65")).
		Padding(0, 2).
		Render("Configuration")

	pathLine := ""
	if m.configPath != "" {
		pathLine = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9B9B9B")).
			Padding(0, 2).
			Render(m.configPath)
	}

	var body string
	var summary []string
	switch {
	case m.errMsg != "":
		body = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF5F87")).
			Padding(1, 2).
			Render(m.errMsg)
	case m.list && len(m.problems) > 0:
		var count string
		count, body = m.listView()
		summary = []string{count}
	default:
		body = m.vp.View()
	}

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	hint := ""
	if focused {
		if m.searching {
			filteredCount := 0
			q := strings.ToLower(m.query)
			for _, l := range m.rawLines {
				if strings.Contains(strings.ToLower(l), q) {
					filteredCount++
				}
			}
			prefix := dim.Render("/ ")
			query := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFDF5")).Render(m.query)
			count := dim.Render(fmt.Sprintf("(%d results)", filteredCount))
			hint = prefix + query + "█  " + count
		} else if m.list {
			hint = components.HintLine(width-2, true,
				components.HelpKey{Key: "enter", Text: "go to the line"}, components.HelpKey{Key: "esc", Text: "back"},
				components.HelpKey{Key: "↑↓ pgup pgdn", Text: "move"}, components.HelpKey{Key: "e", Text: "edit"})
		} else {
			keys := []components.HelpKey{
				{Key: "/", Text: "filter config"}, {Key: "↑↓ pgup pgdn", Text: "scroll"},
				{Key: "e", Text: "edit"}, {Key: "r", Text: "reload"},
			}
			if len(m.problems) > 0 {
				// First: the line is cut to the pane's width, and these are why the
				// pane has something to say.
				keys = append([]components.HelpKey{{Key: "p", Text: "problems"}, {Key: "n/N", Text: "next"}}, keys...)
			}
			hint = components.HintLine(width-2, true, keys...)
		}
	} else {
		hint = components.HintLine(width-2, false, components.HelpKey{Key: "→/enter", Text: "focus"})
	}

	parts := []string{"", header, pathLine}
	parts = append(parts, m.problemList(width)...)
	parts = append(parts, summary...)
	parts = append(parts, "", body, "", "  "+hint)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func highlightYAMLLine(l string) string {
	trimmed := strings.TrimLeft(l, " \t")
	indent := l[:len(l)-len(trimmed)]

	if strings.HasPrefix(trimmed, "#") {
		return indent + lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Render(trimmed)
	}
	if idx := strings.Index(trimmed, ":"); idx > 0 {
		key := trimmed[:idx]
		rest := trimmed[idx:]
		return indent +
			lipgloss.NewStyle().Foreground(lipgloss.Color("#6C91BF")).Render(key) +
			lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFDF5")).Render(rest)
	}
	if strings.HasPrefix(trimmed, "- ") {
		return indent +
			lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575")).Render("- ") +
			lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFDF5")).Render(trimmed[2:])
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFDF5")).Render(l)
}
