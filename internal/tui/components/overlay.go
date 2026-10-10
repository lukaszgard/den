package components

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// backdrop is what is left of a view under a pop-up: readable, and clearly not
// what has the keyboard.
var backdrop = lipgloss.NewStyle().Foreground(lipgloss.Color("#444444"))

// Overlay draws fg centred over bg, the view of a width×height screen, and
// returns a frame of exactly bg's lines: a pop-up must not change the height of
// the view under it, or Bubble Tea drops the top of the frame. bg loses its
// colours, so the pop-up stands out, and with them the escape sequences a cut
// through the middle of a line would otherwise leave open. An fg larger than
// the screen is clipped to it.
//
// The centre is that of the screen, not of bg: of a bg taller than the screen
// only the last height lines are shown, and a pop-up centred on all of it
// would sit too high, or lose its top border.
func Overlay(bg, fg string, width, height int) string {
	lines := strings.Split(bg, "\n")
	box := strings.Split(fg, "\n")
	boxWidth := min(lipgloss.Width(fg), width)
	hidden := max(len(lines)-height, 0)
	top := hidden + max((len(lines)-hidden-len(box))/2, 0)
	left := (width - boxWidth) / 2

	for y, line := range lines {
		plain := ansi.Strip(line)
		row := y - top
		if row < 0 || row >= len(box) {
			lines[y] = backdrop.Render(plain)
			continue
		}
		boxLine := ansi.Truncate(box[row], boxWidth, "")
		boxLine += strings.Repeat(" ", boxWidth-ansi.StringWidth(boxLine))
		// What is past the pop-up ends where the line did, which can be short
		// of the view's width or, for a line that was too long, past it.
		lines[y] = backdrop.Render(cells(plain, 0, left)) + boxLine +
			backdrop.Render(cells(plain, left+boxWidth, ansi.StringWidth(plain)))
	}
	return strings.Join(lines, "\n")
}

// cells returns the cells [from, to) of plain, which carries no escape
// sequences, as exactly to-from cells. A wide character cut in half becomes a
// space, so what follows stays in its column.
func cells(plain string, from, to int) string {
	if to <= from {
		return ""
	}
	s := ansi.Truncate(plain, to, "")
	if from > 0 {
		rest := ansi.StringWidth(s) - from
		cut := ansi.TruncateLeft(s, from, "")
		// TruncateLeft keeps a wide character that starts before from whole.
		if rest > 0 && ansi.StringWidth(cut) > rest {
			cut = " " + ansi.TruncateLeft(s, from+1, "")
		}
		s = cut
	}
	return s + strings.Repeat(" ", max(to-from-ansi.StringWidth(s), 0))
}
