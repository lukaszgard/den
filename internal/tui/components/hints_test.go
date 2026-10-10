package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// "key: action": the key alone in den's blue, the colon with the action.
func TestHintsAreKeyColonActionWithTheKeyInBlue(t *testing.T) {
	if hintKey.GetForeground() != lipgloss.Color("#6C91BF") || !hintKey.GetBold() {
		t.Fatalf("the key style is %v, want bold #6C91BF", hintKey.GetForeground())
	}
	got := Hints(HelpKey{"h", "help"}, HelpKey{"↑↓", "navigate"})
	want := hintKey.Render("h") + hintText.Render(": help") + "  " + hintKey.Render("↑↓") + hintText.Render(": navigate")
	if got != want {
		t.Fatalf("Hints = %q, want %q", got, want)
	}
	if plain := ansi.Strip(got); plain != "h: help  ↑↓: navigate" {
		t.Fatalf("Hints reads %q", plain)
	}
}

func TestHintLineIsCutToTheWidth(t *testing.T) {
	keys := []HelpKey{{"enter", "open"}, {"r", "run"}, {"x", "cancel"}, {"n", "new"}}
	line := HintLine(20, true, keys...)
	if w := lipgloss.Width(line); w > 20 || !strings.HasSuffix(ansi.Strip(line), "…") {
		t.Fatalf("HintLine at 20 columns is %d wide: %q", w, ansi.Strip(line))
	}
	if ansi.Strip(HintLine(80, false, keys...)) != "enter: open  r: run  x: cancel  n: new" {
		t.Fatalf("an unfocused line reads %q", ansi.Strip(HintLine(80, false, keys...)))
	}
}
