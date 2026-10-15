package components

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Rect is an area of the screen: the cells [X0, X1) of the rows [Y0, Y1). The
// zero Rect is empty.
type Rect struct{ X0, Y0, X1, Y1 int }

// Contains reports whether the cell x, y is in r.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X0 && x < r.X1 && y >= r.Y0 && y < r.Y1
}

// Empty reports whether r has no cell.
func (r Rect) Empty() bool { return r.X1 <= r.X0 || r.Y1 <= r.Y0 }

// Clamp is the cell of r nearest to x, y. r must not be empty.
func (r Rect) Clamp(x, y int) (int, int) {
	return min(max(x, r.X0), r.X1-1), min(max(y, r.Y0), r.Y1-1)
}

// Span is the cells [X0, X1) of the row Y.
type Span struct{ Y, X0, X1 int }

// selected is how the cells of a selection are drawn. Reverse video swaps the
// colours the terminal already has, so it reads on a light and a dark
// background alike, with nothing to learn about either.
var selected = lipgloss.NewStyle().Reverse(true)

// SelectionSpans is the selection from the cell ax, ay to bx, by, both kept in
// area, as a terminal selects: within one row the cells between them; over
// several rows the first row from the earlier cell to the end of the area,
// the rows between whole, and the last row up to the later cell.
func SelectionSpans(area Rect, ax, ay, bx, by int) []Span {
	if area.Empty() {
		return nil
	}
	ax, ay = area.Clamp(ax, ay)
	bx, by = area.Clamp(bx, by)
	if by < ay || (by == ay && bx < ax) {
		ax, ay, bx, by = bx, by, ax, ay
	}
	if ay == by {
		return []Span{{Y: ay, X0: ax, X1: bx + 1}}
	}
	spans := []Span{{Y: ay, X0: ax, X1: area.X1}}
	for y := ay + 1; y < by; y++ {
		spans = append(spans, Span{Y: y, X0: area.X0, X1: area.X1})
	}
	return append(spans, Span{Y: by, X0: area.X0, X1: bx + 1})
}

// SelectedText is the text of frame under spans, one line per span. Each line
// loses its trailing spaces, which are the padding of a panel rather than
// anything written, and so does the end of the text.
func SelectedText(frame string, spans []Span) string {
	lines := strings.Split(frame, "\n")
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		var text string
		if s.Y >= 0 && s.Y < len(lines) {
			text = strings.TrimRight(cells(ansi.Strip(lines[s.Y]), s.X0, s.X1), " ")
		}
		out = append(out, text)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// Highlight draws spans over frame in reverse video. A span keeps to the
// width of its line, so no line grows, and what follows a span is drawn in
// the colours it had.
func Highlight(frame string, spans []Span) string {
	lines := strings.Split(frame, "\n")
	for _, s := range spans {
		if s.Y < 0 || s.Y >= len(lines) {
			continue
		}
		line := lines[s.Y]
		width := ansi.StringWidth(line)
		x0, x1 := max(s.X0, 0), min(s.X1, width)
		if x1 <= x0 {
			continue
		}
		prefix := ansi.Truncate(line, x0, "")
		// Truncate drops a wide character that x0 cuts in half; cells makes
		// its second half a space, and the prefix is padded for its first.
		prefix += strings.Repeat(" ", max(x0-ansi.StringWidth(prefix), 0))
		lines[s.Y] = prefix + "\x1b[m" + selected.Render(cells(ansi.Strip(line), x0, x1)) + suffix(line, x1, width)
	}
	return strings.Join(lines, "\n")
}

// suffix is line from the cell x on, the escape sequences before x included:
// they set the colours that what follows x is drawn in.
func suffix(line string, x, width int) string {
	s := ansi.TruncateLeft(line, x, "")
	// TruncateLeft keeps a wide character that starts before x whole, and
	// the span already covers its first half.
	if ansi.StringWidth(s) > width-x {
		s = " " + ansi.TruncateLeft(line, x+1, "")
	}
	return s
}
