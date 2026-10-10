package components

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Shortcut is one line of a shortcuts pop-up: a key, or several separated by
// a space, and what it does.
//
// Texts stay under some 40 characters: two columns of them then fit a
// 120-column terminal, which is what keeps a pop-up whole on a short one.
type Shortcut struct{ Keys, Text string }

// ShortcutGroup is a titled block of shortcuts.
type ShortcutGroup struct {
	Title string
	Keys  []Shortcut
	// Note is a line under the keys, for what needs no key at all.
	Note string
}

// Shortcuts is the pop-up that says what the keys of a pane or a service do.
// Whatever shows it draws View over its own screen with Overlay and hands it
// every key while it is open: a key pressed over a pop-up must not act on what
// is behind it. The zero value is a closed pop-up.
//
// It always fits the screen it is given. The help screens it replaces were
// boxes as tall as their text, and a frame taller than the terminal loses its
// top in Bubble Tea.
type Shortcuts struct {
	subject string
	groups  []ShortcutGroup
	scroll  int
}

// OpenShortcuts returns an open pop-up for subject, the pane or service the
// keys belong to. Without groups there is nothing to show and it stays closed.
func OpenShortcuts(subject string, groups []ShortcutGroup) Shortcuts {
	return Shortcuts{subject: subject, groups: groups}
}

// Open reports whether the pop-up is on screen.
func (s Shortcuts) Open() bool { return len(s.groups) > 0 }

// Key handles a key pressed while the pop-up is open on a screen of
// width×height, and returns the pop-up after it: closed, scrolled, or as it
// was for every key that is not the pop-up's.
func (s Shortcuts) Key(key string, width, height int) Shortcuts {
	lines, rows := s.body(width, height)
	last := max(len(lines)-rows, 0)
	switch key {
	case "esc", "h", "?", "q":
		return Shortcuts{}
	case "up", "k":
		s.scroll = max(min(s.scroll, last)-1, 0)
	case "down", "j":
		s.scroll = min(s.scroll+1, last)
	}
	return s
}

var (
	shortcutHead = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ECFD65"))
	shortcutKey  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#6C91BF"))
	shortcutText = lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
	shortcutNote = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
)

const (
	// shortcutsChrome is the rows of the pop-up around its body: the border,
	// the title and the footer, each of those two with a blank line.
	shortcutsChrome = 6
	// shortcutsFrame is the columns around it: the border and its padding.
	shortcutsFrame = 6
	// shortcutsMargin is the rows of the screen left above and below a pop-up
	// that has to scroll, so that it still reads as one and not as a screen.
	shortcutsMargin = 1
	shortcutsGap    = "    "
)

func (g ShortcutGroup) height() int {
	h := 1 + len(g.Keys)
	if g.Note != "" {
		h++
	}
	return h
}

// shortcutColumn renders groups one under another, a blank line between them,
// with the texts lined up after the column's widest key.
func shortcutColumn(groups []ShortcutGroup) []string {
	keyWidth := 0
	for _, g := range groups {
		for _, k := range g.Keys {
			keyWidth = max(keyWidth, lipgloss.Width(k.Keys))
		}
	}
	var lines []string
	for i, g := range groups {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, shortcutHead.Render(g.Title))
		for _, k := range g.Keys {
			pad := strings.Repeat(" ", keyWidth-lipgloss.Width(k.Keys))
			lines = append(lines, "  "+shortcutKey.Render(k.Keys)+pad+"  "+shortcutText.Render(k.Text))
		}
		if g.Note != "" {
			lines = append(lines, "  "+shortcutNote.Render(g.Note))
		}
	}
	return lines
}

// splitShortcuts is where to break groups into two columns of about the same
// height.
func splitShortcuts(groups []ShortcutGroup) int {
	columnHeight := func(gs []ShortcutGroup) int {
		h := len(gs) - 1
		for _, g := range gs {
			h += g.height()
		}
		return h
	}
	best, bestHeight := 1, -1
	for i := 1; i < len(groups); i++ {
		if h := max(columnHeight(groups[:i]), columnHeight(groups[i:])); bestHeight < 0 || h < bestHeight {
			best, bestHeight = i, h
		}
	}
	return best
}

// body lays the groups out for a screen of width×height, and reports how many
// of the lines fit. One column reads best; two are used when one is taller than
// the screen and both fit side by side. What still does not fit is scrolled.
func (s Shortcuts) body(width, height int) (lines []string, rows int) {
	rows = max(height-shortcutsChrome-2*shortcutsMargin, 1)
	one := shortcutColumn(s.groups)
	if len(one) <= rows || len(s.groups) < 2 {
		return one, rows
	}
	split := splitShortcuts(s.groups)
	two := lipgloss.JoinHorizontal(lipgloss.Top,
		strings.Join(shortcutColumn(s.groups[:split]), "\n"),
		shortcutsGap,
		strings.Join(shortcutColumn(s.groups[split:]), "\n"),
	)
	if lipgloss.Width(two) > width-shortcutsFrame {
		return one, rows
	}
	return strings.Split(two, "\n"), rows
}

// View draws the pop-up for a screen of width×height: never wider than it and,
// down to a screen of a few rows, never taller.
func (s Shortcuts) View(width, height int) string {
	lines, rows := s.body(width, height)
	footer := []HelpKey{{"esc", "close"}}
	if len(lines) > rows {
		// A resize can leave the offset past the end.
		from := min(s.scroll, len(lines)-rows)
		lines = lines[from : from+rows]
		footer = append([]HelpKey{{"↑↓", "scroll"}}, footer...)
	}

	content := []string{shortcutHead.Render("Shortcuts") + shortcutNote.Render(" — "+s.subject), ""}
	content = append(content, lines...)
	content = append(content, "", Hints(footer...))
	inner := max(width-shortcutsFrame, 1)
	for i, line := range content {
		content[i] = ansi.Truncate(line, inner, "…")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#6C91BF")).
		Padding(0, 2).
		Render(strings.Join(content, "\n"))
}
