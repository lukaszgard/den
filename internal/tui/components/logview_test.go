package components

import (
	"strings"
	"testing"
)

func TestLogViewKeepsItsColourAfterAScriptsReset(t *testing.T) {
	l := NewLogView(40, 2)
	if l.base == "" {
		t.Fatal("no base colour sequence")
	}
	for name, reset := range map[string]string{"0": "\x1b[0m", "empty": "\x1b[m", "default fg": "\x1b[39m", "reset+bold": "\x1b[0;1m"} {
		l.SetContent([]string{"\x1b[32m✔" + reset + " ok"})
		want := "✔" + reset + l.base + " ok"
		if got := l.View(); !strings.Contains(got, want) {
			t.Errorf("%s: view = %q, want the base colour back right after the reset (%q)", name, got, want)
		}
	}
}

func TestLogViewLeavesColoursAndPlainLinesAlone(t *testing.T) {
	l := NewLogView(40, 3)
	l.SetContent([]string{"plain", "\x1b[1;31mred\x1b[22m bold off", "\x1b[38;5;0mblack\x1b[38;2;0;0;0m rgb"})
	got := l.View()
	for _, want := range []string{"plain", "\x1b[1;31mred\x1b[22m bold off", "\x1b[38;5;0mblack\x1b[38;2;0;0;0m rgb"} {
		if !strings.Contains(got, want) {
			t.Errorf("view = %q, want %q untouched: none of these resets the colour", got, want)
		}
	}
}

func TestResetsColour(t *testing.T) {
	for params, want := range map[string]bool{
		"": true, "0": true, "39": true, "1;0": true, "0;32": true,
		"1": false, "32": false, "1;31": false, "22": false,
		"38;5;0": false, "38;2;0;0;0": false, "48;5;0;32": false, "38:2::0:0:0": false,
		"38;5;0;0": true, "38;2;1;2;3;39": true,
	} {
		if got := resetsColour(params); got != want {
			t.Errorf("resetsColour(%q) = %v, want %v", params, got, want)
		}
	}
}
