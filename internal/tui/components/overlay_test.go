package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// block is rows lines of cols cells, all ch.
func block(ch string, cols, rows int) string {
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = strings.Repeat(ch, cols)
	}
	return strings.Join(lines, "\n")
}

// A pop-up that changes the height or the width of the view under it breaks
// the frame: Bubble Tea drops the top of one that is too tall.
func TestOverlayKeepsTheBackgroundSize(t *testing.T) {
	out := Overlay(block(".", 20, 7), block("#", 6, 3), 20, 7)
	lines := strings.Split(ansi.Strip(out), "\n")
	want := []string{
		"....................",
		"....................",
		".......######.......",
		".......######.......",
		".......######.......",
		"....................",
		"....................",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("overlay =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

// The background keeps its text but not its colours, and a line cut in the
// middle of a coloured run must not leak that colour into the pop-up.
func TestOverlayDropsTheBackgroundStyling(t *testing.T) {
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")).Bold(true)
	bg := strings.Join([]string{red.Render("aaaaaaaaaa"), red.Render("bbbbbbbbbb"), red.Render("cccccccccc")}, "\n")
	out := Overlay(bg, "XX", 10, 3)

	if got := ansi.Strip(out); got != "aaaaaaaaaa\nbbbbXXbbbb\ncccccccccc" {
		t.Fatalf("overlay = %q", got)
	}
	if want := backdrop.Render("bbbb") + "XX" + backdrop.Render("bbbb"); strings.Split(out, "\n")[1] != want {
		t.Fatalf("the covered line is %q, want the pop-up between two backdrop runs, %q", strings.Split(out, "\n")[1], want)
	}
}

// A wide character takes two cells. One cut in half by the pop-up's edge must
// not move what follows it by a column.
func TestOverlayKeepsColumnsOverWideCharacters(t *testing.T) {
	bg := strings.Join([]string{"日本語日本語日本語日", "a日本語日本語日本語b", "...................."}, "\n")
	for _, fg := range []string{"####", "#####", "######"} {
		out := Overlay(bg, block(fg, 1, 3), 20, 3)
		for y, line := range strings.Split(ansi.Strip(out), "\n") {
			if w := ansi.StringWidth(line); w != 20 {
				t.Fatalf("with %q line %d is %d cells, want 20: %q", fg, y, w, line)
			}
			before, _, ok := strings.Cut(line, fg)
			if !ok {
				t.Fatalf("with %q line %d lost the pop-up: %q", fg, y, line)
			}
			if got, want := ansi.StringWidth(before), (20-len(fg))/2; got != want {
				t.Fatalf("with %q the pop-up starts in column %d on line %d, want %d: %q", fg, got, y, want, line)
			}
		}
	}
}

func TestOverlayClipsAPopupLargerThanTheBackground(t *testing.T) {
	out := Overlay(block(".", 10, 3), block("#", 14, 5), 10, 3)
	if got := ansi.Strip(out); got != block("#", 10, 3) {
		t.Fatalf("overlay = %q, want the background's size, all pop-up", got)
	}
}

// Lines of the background are not all the same length. A short one is padded
// up to the pop-up and no further; one longer than the view keeps its end.
func TestOverlayOverLinesOfAnyLength(t *testing.T) {
	out := Overlay("..\n..............\n..........", "##\n##", 10, 3)
	if got := ansi.Strip(out); got != "..  ##\n....##........\n.........." {
		t.Fatalf("overlay = %q", got)
	}
}

// Bubble Tea shows the last lines of a frame taller than the terminal. The
// pop-up is centred on what is shown, so none of it is in the lines that are
// not.
func TestOverlayCentresOnTheScreenNotTheFrame(t *testing.T) {
	out := Overlay(block(".", 6, 9), block("#", 2, 4), 6, 5)
	want := []string{"......", "......", "......", "......", "..##..", "..##..", "..##..", "..##..", "......"}
	if got := ansi.Strip(out); got != strings.Join(want, "\n") {
		t.Fatalf("overlay =\n%s\nwant the pop-up within the last 5 lines", got)
	}
}
