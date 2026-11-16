package components

import (
	"regexp"
	"strings"

	"github.com/lukaszgard/den/internal/process"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// LogView wraps a bubbles viewport for scrollable log display. Besides
// scrolling, it searches the log (every match lit up, the view jumping from one
// to the next), hides the time in front of each line, wraps long lines, and
// follows new lines until told not to, the way k9s's log view does.
type LogView struct {
	vp     viewport.Model
	ready  bool
	width  int
	height int
	// base is the escape sequence that sets the text colour of the view.
	base string

	// raw are the lines as last set, and rows what the view shows of each.
	raw  []string
	rows []*logLine
	// memo is rows by the line they were made from: a log is set again whole
	// on every new line, and the work on thousands of unchanged coloured lines
	// (keepBase's regexp, the strip, the search) was most of a refresh.
	// Replaced on every rebuild, and a logLine is never changed once made, so
	// a copy of the view keeps its own.
	memo map[string]*logLine

	// hideStamps cuts the [15:04:05] in front of each line.
	hideStamps bool
	// paused stops the view from following new lines. The zero value follows:
	// a log view always went with the newest lines.
	paused bool

	// query is the text searched for, re how it is found, and matches every
	// place it is, in order.
	query   string
	re      *regexp.Regexp
	matches []match
	// cur is the match the view jumped to, an index into matches; -1 for none.
	cur int
	// anchor is the line at the top of the view as the search began: as the
	// query is typed, the view goes to the first match at or below it, not to
	// the first of the whole log.
	anchor int
}

// logLine is what the view made of one line of the log.
type logLine struct {
	text  string // the line as given, its time cut when hidden
	kept  string // text with keepBase applied: what is shown
	plain string // text without escape sequences: what a search reads and a copy takes
	width int    // cells of plain, which a wrapped line's height comes from
	// query is what hits and marked were made for.
	query  string
	hits   [][2]int // the cells of plain that match it
	marked string   // kept with every hit lit up; "" without hits
}

// match is one place the query was found: a line, the cells it covers, and
// which of the line's hits it is.
type match struct{ line, hit, col0, col1 int }

var (
	// logColour is the colour of a log line's own text, under any colour the
	// process printed.
	logColour = lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
	// A match sets its own text and background colours, so it reads on a dark
	// and a light terminal alike; the one jumped to is the brighter.
	matchStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#1a1a1a")).Background(lipgloss.Color("#A89A4E"))
	currentMatchStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1a1a1a")).Background(lipgloss.Color("#ECFD65"))
)

// NewLogView creates a LogView with the given dimensions.
func NewLogView(width, height int) LogView {
	vp := NewViewport(width, height)
	vp.Style = logColour
	// f is the log's full screen key in the runbook view, not a page down.
	vp.KeyMap.PageDown.SetKeys("pgdown", "space")
	// The sequence lipgloss itself puts before the text, so it is the colour
	// the program's colour profile later turns into what the terminal has.
	base, _, _ := strings.Cut(logColour.Render("x"), "x")
	return LogView{vp: vp, width: width, height: height, ready: true, base: base, cur: -1}
}

// sgr matches a colour or style sequence, with its parameters in the group.
var sgr = regexp.MustCompile("\x1b\\[([0-9;:]*)m")

// resetsColour reports whether an SGR sequence with these parameters puts the
// text back to the terminal's default colour: a reset (0, or none) or the
// default foreground (39). The extended colours, 38 and 48, carry numbers of
// their own, such as the 0 in 38;5;0 (black), that are not resets.
func resetsColour(params string) bool {
	if params == "" {
		return true
	}
	fields := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "0", "00", "39":
			return true
		case "38", "48", "58":
			// 38;5;n is three fields, 38;2;r;g;b five.
			if i+1 < len(fields) && fields[i+1] == "2" {
				i += 4
			} else {
				i += 2
			}
		}
	}
	return false
}

// keepBase puts the view's text colour back after every sequence in line that
// resets the colour: a process that prints a green mark and resets would
// otherwise leave the rest of the line in the terminal's default colour,
// among grey lines. A lit-up match ends with a reset too.
func (l *LogView) keepBase(line string) string {
	if !strings.Contains(line, "\x1b[") {
		return line
	}
	return sgr.ReplaceAllStringFunc(line, func(seq string) string {
		if resetsColour(sgr.FindStringSubmatch(seq)[1]) {
			return seq + l.base
		}
		return seq
	})
}

// SetContent updates the displayed log lines. The view goes to the newest
// while it follows them, and stays where it is while paused. A search stays
// on, its matches found again in the new lines.
func (l *LogView) SetContent(lines []string) {
	l.raw = lines
	l.rebuild()
}

// rebuild makes the rows again from raw, finds the query's matches, and shows
// them.
func (l *LogView) rebuild() {
	l.index()
	l.paint()
}

// index makes rows and matches from raw, reusing what memo has. The match the
// view jumped to stays the current one, by its line and its place in it.
func (l *LogView) index() {
	old, hadCur := l.current()
	rows := make([]*logLine, len(l.raw))
	memo := make(map[string]*logLine, len(l.raw))
	var matches []match
	for i, line := range l.raw {
		if l.hideStamps {
			line = process.CutTimestamp(line)
		}
		r, ok := l.memo[line]
		if !ok {
			plain := ansi.Strip(line)
			r = &logLine{text: line, kept: l.keepBase(line), plain: plain, width: ansi.StringWidth(plain)}
		}
		if r.query != l.query {
			// A new one: another copy of the view may still hold r.
			found := *r
			found.query, found.hits, found.marked = l.query, nil, ""
			if l.re != nil {
				if found.hits = findHits(l.re, found.plain); len(found.hits) > 0 {
					found.marked = l.keepBase(markHits(line, found.hits, -1))
				}
			}
			r = &found
		}
		memo[line], rows[i] = r, r
		for j, h := range r.hits {
			matches = append(matches, match{line: i, hit: j, col0: h[0], col1: h[1]})
		}
	}
	l.rows, l.memo, l.matches, l.cur = rows, memo, matches, -1
	if !hadCur || len(matches) == 0 {
		return
	}
	l.cur = l.firstFrom(old.line)
	for i := l.cur; i < len(matches) && matches[i].line == old.line; i++ {
		if matches[i].hit == old.hit {
			l.cur = i
			break
		}
	}
}

// paint hands the rows to the viewport, the current match brighter than the
// rest, and goes to the bottom while following.
func (l *LogView) paint() {
	lines := make([]string, len(l.rows))
	for i, r := range l.rows {
		lines[i] = r.kept
		if r.marked != "" {
			lines[i] = r.marked
		}
	}
	// Only the current match's line is painted on every call: memo holds each
	// line once, lit up the same way whichever match is the current one.
	if c, ok := l.current(); ok {
		r := l.rows[c.line]
		lines[c.line] = l.keepBase(markHits(r.text, r.hits, c.hit))
	}
	l.vp.SetContentLines(lines)
	if !l.paused {
		l.vp.GotoBottom()
	}
}

// findHits is where re matches plain, in cells.
func findHits(re *regexp.Regexp, plain string) [][2]int {
	found := re.FindAllStringIndex(plain, -1)
	if len(found) == 0 {
		return nil
	}
	hits := make([][2]int, 0, len(found))
	for _, f := range found {
		c0 := ansi.StringWidth(plain[:f[0]])
		c1 := c0 + ansi.StringWidth(plain[f[0]:f[1]])
		if c1 > c0 {
			hits = append(hits, [2]int{c0, c1})
		}
	}
	return hits
}

// markHits lights up hits in line, the one numbered cur brighter.
func markHits(line string, hits [][2]int, cur int) string {
	ranges := make([]lipgloss.Range, len(hits))
	for i, h := range hits {
		style := matchStyle
		if i == cur {
			style = currentMatchStyle
		}
		ranges[i] = lipgloss.NewRange(h[0], h[1], style)
	}
	return lipgloss.StyleRanges(line, ranges...)
}

// queryRegexp finds q as plain text, with smart case: a query without capitals
// matches any case, one with capitals only its own.
func queryRegexp(q string) *regexp.Regexp {
	expr := regexp.QuoteMeta(q)
	if strings.ToLower(q) == q {
		expr = "(?i)" + expr
	}
	return regexp.MustCompile(expr)
}

// current is the match the view jumped to.
func (l *LogView) current() (match, bool) {
	if l.cur < 0 || l.cur >= len(l.matches) {
		return match{}, false
	}
	return l.matches[l.cur], true
}

// firstFrom is the first match at or below line, or the first of all when
// there is none below it: a search wraps around.
func (l *LogView) firstFrom(line int) int {
	for i, m := range l.matches {
		if m.line >= line {
			return i
		}
	}
	return 0
}

// SetQuery searches the log for q, lighting up every match, and jumps to the
// first at or below the line that was at the top when the search began. ""
// ends the search.
func (l *LogView) SetQuery(q string) {
	if q == l.query {
		return
	}
	if l.query == "" {
		l.anchor = l.topLine()
	}
	l.query, l.re = q, nil
	if q != "" {
		l.re = queryRegexp(q)
	}
	l.index()
	if len(l.matches) == 0 {
		l.paint()
		return
	}
	l.cur = l.firstFrom(l.anchor)
	l.jump()
}

// ClearQuery ends the search. The view stays where it is.
func (l *LogView) ClearQuery() { l.SetQuery("") }

// Query is the text searched for, "" when there is no search.
func (l *LogView) Query() string { return l.query }

// Next jumps to the next match, from the last to the first.
func (l *LogView) Next() { l.step(1) }

// Prev jumps to the previous match, from the first to the last.
func (l *LogView) Prev() { l.step(-1) }

func (l *LogView) step(d int) {
	n := len(l.matches)
	if n == 0 {
		return
	}
	if l.cur < 0 {
		// New lines brought the first matches: start from what is on screen.
		l.cur = l.firstFrom(l.topLine())
		if d < 0 {
			l.cur = (l.cur - 1 + n) % n
		}
	} else {
		l.cur = (l.cur + d + n) % n
	}
	l.jump()
}

// jump shows the current match about a third of the way down the view, with
// lines above it for context, and stops following: the next line of a live
// run would pull the view away from it.
func (l *LogView) jump() {
	l.paused = true
	l.paint()
	if c, ok := l.current(); ok {
		l.vp.SetYOffset(l.rowOf(c.line) - l.vp.Height()/3)
	}
}

// MatchInfo is the number of the current match, from 1 (0 for none), and how
// many there are.
func (l *LogView) MatchInfo() (cur, total int) {
	return l.cur + 1, len(l.matches)
}

// heightOf is how many rows r takes on screen.
func (l *LogView) heightOf(r *logLine) int {
	w := l.vp.Width()
	if !l.vp.SoftWrap || w <= 0 {
		return 1
	}
	return max(1, (r.width+w-1)/w)
}

// rowOf is the row of the whole log that line starts on. The viewport's
// offset counts rows, which are lines only while lines are not wrapped.
func (l *LogView) rowOf(line int) int {
	if !l.vp.SoftWrap {
		return line
	}
	row := 0
	for _, r := range l.rows[:min(line, len(l.rows))] {
		row += l.heightOf(r)
	}
	return row
}

// topLine is the line at the top of the view.
func (l *LogView) topLine() int {
	y := l.vp.YOffset()
	if !l.vp.SoftWrap {
		return y
	}
	row := 0
	for i, r := range l.rows {
		if row += l.heightOf(r); y < row {
			return i
		}
	}
	return len(l.rows)
}

// keepTop shows line at the top again after the rows changed height, unless
// the view follows the newest lines.
func (l *LogView) keepTop(line int) {
	if l.paused {
		l.vp.SetYOffset(l.rowOf(line))
	} else {
		l.vp.GotoBottom()
	}
}

// SetHideStamps hides or shows the time in front of each line.
func (l *LogView) SetHideStamps(hide bool) {
	if hide == l.hideStamps {
		return
	}
	top := l.topLine()
	l.hideStamps = hide
	l.rebuild()
	l.keepTop(top)
}

// SetWrap wraps lines wider than the view, which are otherwise cut at its edge.
func (l *LogView) SetWrap(wrap bool) {
	if wrap == l.vp.SoftWrap {
		return
	}
	top := l.topLine()
	l.vp.SoftWrap = wrap
	l.keepTop(top)
}

// Following reports whether the view goes with new lines.
func (l *LogView) Following() bool { return !l.paused }

// ToggleFollow stops following new lines, or goes to the newest and follows
// them again.
func (l *LogView) ToggleFollow() {
	if l.paused {
		l.GotoBottom()
		return
	}
	l.paused = true
}

// PlainText is the whole log as shown, without its colours: what a copy takes.
func (l *LogView) PlainText() string {
	lines := make([]string, len(l.rows))
	for i, r := range l.rows {
		lines[i] = r.plain
	}
	return strings.Join(lines, "\n")
}

// Len is how many lines the log has.
func (l *LogView) Len() int { return len(l.rows) }

// Resize updates the viewport dimensions. A view following the tail keeps
// following it: the scroll offset was computed for the old height, so a
// shorter view would hide the newest lines and a taller one would leave blank
// rows under them. A paused one keeps its top line, which wrapped lines of a
// new width would otherwise move.
func (l *LogView) Resize(width, height int) {
	top := l.topLine()
	l.width = width
	l.height = height
	l.vp.SetWidth(width)
	l.vp.SetHeight(height)
	l.keepTop(top)
}

// GotoBottom shows the newest lines and follows them from there.
func (l *LogView) GotoBottom() {
	l.paused = false
	l.vp.GotoBottom()
}

// Update handles viewport key messages, and g and G for the top and the
// bottom. Going to the top, or scrolling up, stops following new lines;
// going to the bottom follows them again.
func (l *LogView) Update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "g", "home":
			l.vp.GotoTop()
			l.paused = true
			return nil
		case "G", "end":
			l.GotoBottom()
			return nil
		}
	}
	y := l.vp.YOffset()
	var cmd tea.Cmd
	l.vp, cmd = l.vp.Update(msg)
	if l.vp.YOffset() != y {
		l.paused = !l.vp.AtBottom()
	}
	return cmd
}

// View renders the log view.
func (l *LogView) View() string {
	return l.vp.View()
}
