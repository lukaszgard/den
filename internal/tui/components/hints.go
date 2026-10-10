package components

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Every line in den that names keys — a pane's hint line, the bar at the
// bottom, a view's help line, a pop-up's footer, a [y]/[n] question — is built
// here, so they all read the same: "key: action", the key in blue.
// internal/tui/hints_rule_test.go fails on one written by hand.

// HelpKey is one key of a hint line and what it does.
type HelpKey struct{ Key, Text string }

var (
	hintKey  = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C91BF")).Bold(true)
	hintText = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
)

// Hints renders keys as "key: action" pairs, the key in blue, two spaces
// apart.
func Hints(keys ...HelpKey) string {
	return hints(hintKey, hintText, keys)
}

// HintLine is a hint line under a pane: Hints cut to width with …, and faint
// while the pane does not have the keyboard. The caller indents it.
func HintLine(width int, focused bool, keys ...HelpKey) string {
	key, text := hintKey, hintText
	if !focused {
		key, text = key.Faint(true), text.Faint(true)
	}
	line := hints(key, text, keys)
	if lipgloss.Width(line) > width {
		line = ansi.Truncate(line, max(width, 0), "…")
	}
	return line
}

func hints(key, text lipgloss.Style, keys []HelpKey) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = key.Render(k.Key) + text.Render(": "+k.Text)
	}
	return strings.Join(parts, "  ")
}
