package runbooks

import (
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The icon is drawn in its language's colour (#FFD43B, 24-bit); the label after
// it is not. Lip Gloss v2 renders full colour whatever the terminal.
func TestKindCellColoursTheIconOnly(t *testing.T) {
	cell := kindCell("python", "python")
	icon, label, ok := strings.Cut(cell, "\x1b[m")
	if !ok || !strings.Contains(icon, "38;2;255;212;59") || !strings.Contains(icon, "\ue73c") {
		t.Fatalf("cell %q: want the python glyph in python yellow", cell)
	}
	if label != " python" {
		t.Errorf("label %q: want it plain after the icon", label)
	}
	if got := ansi.Strip(kindCell("seq", "seq · 2")); got != "\uf0cb seq · 2" {
		t.Errorf("a sequence's cell reads %q", got)
	}
}

func TestKindIconsForEveryKindDenNames(t *testing.T) {
	for _, kind := range []string{"perl", "pwsh", "terraform"} {
		if kindIcons[kind].glyph == "" || kindIcons[kind].glyph == otherIcon.glyph {
			t.Errorf("%s has no icon of its own", kind)
		}
	}
	for _, kind := range []string{"kubectl", "cobol"} {
		if got := ansi.Strip(kindCell(kind, kind)); got != otherIcon.glyph+" "+kind {
			t.Errorf("%s reads %q: want the gear", kind, got)
		}
	}
}

// One cell each: a glyph outside the BMP's private use area may be counted
// two cells wide by the terminal, shifting every column after TYPE.
func TestKindIconsAreOneCellWide(t *testing.T) {
	all := []kindIcon{otherIcon}
	for _, icon := range kindIcons {
		all = append(all, icon)
	}
	for _, icon := range all {
		r, size := utf8.DecodeRuneInString(icon.glyph)
		if size != len(icon.glyph) || r < 0xE000 || r > 0xF8FF || lipgloss.Width(icon.glyph) != 1 {
			t.Errorf("glyph %U is not one rune of the BMP's private use area, one cell wide", r)
		}
	}
}
