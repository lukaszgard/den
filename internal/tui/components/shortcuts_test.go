package components

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// manyShortcuts is a pop-up of 5 groups and 27 lines in one column, which is
// what an RDS service with terminal tabs has.
func manyShortcuts() Shortcuts {
	group := func(title string, keys int, note string) ShortcutGroup {
		g := ShortcutGroup{Title: title, Note: note}
		for i := range keys {
			g.Keys = append(g.Keys, Shortcut{Keys: string(rune('a' + i)), Text: fmt.Sprintf("%s key %d does a thing worth a line", title, i+1)})
		}
		return g
	}
	return OpenShortcuts("orders-int", []ShortcutGroup{
		group("First", 3, ""),
		group("Second", 2, "A note under the second group."),
		group("Third", 2, ""),
		group("Fourth", 6, "A note under the fourth group."),
		group("Last", 3, ""),
	})
}

// assertFits checks that the pop-up stays inside a screen of width×height.
func assertFits(t *testing.T, view string, width, height int) {
	t.Helper()
	if got := lipgloss.Height(view); got > height {
		t.Fatalf("the pop-up is %d rows on a %d-row screen:\n%s", got, height, view)
	}
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("line %d is %d wide on a %d-wide screen:\n%s", i, w, width, view)
		}
	}
}

func TestShortcutsOpenAndClose(t *testing.T) {
	if (Shortcuts{}).Open() || OpenShortcuts("nothing", nil).Open() {
		t.Fatal("a pop-up without groups has nothing to show and stays closed")
	}
	s := manyShortcuts()
	if !s.Open() {
		t.Fatal("OpenShortcuts should return an open pop-up")
	}
	for _, key := range []string{"c", "d", "r", "t", "enter", "tab", "backspace", "left"} {
		if s = s.Key(key, 120, 40); !s.Open() {
			t.Fatalf("%s closed the pop-up; only esc, h, ? and q do", key)
		}
	}
	for _, key := range []string{"esc", "h", "?", "q"} {
		if manyShortcuts().Key(key, 120, 40).Open() {
			t.Fatalf("%s should close the pop-up", key)
		}
	}
}

func TestShortcutsInOneColumn(t *testing.T) {
	view := manyShortcuts().View(120, 40)
	assertFits(t, view, 120, 40)
	plain := ansi.Strip(view)
	for _, want := range []string{"Shortcuts — orders-int", "First key 1", "A note under the second group.", "Last key 3", "esc: close"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("the pop-up is missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "scroll") {
		t.Fatalf("a pop-up that fits should not offer to scroll:\n%s", plain)
	}
	// One column: no line holds keys of two groups.
	for line := range strings.SplitSeq(plain, "\n") {
		if strings.Count(line, " key ") > 1 {
			t.Fatalf("with room for one column the groups are side by side:\n%s", plain)
		}
	}
}

// A pop-up taller than the screen would push the frame's top off it. With
// room for two columns it uses them, and then needs no scrolling.
func TestShortcutsInTwoColumnsOnAShortScreen(t *testing.T) {
	view := manyShortcuts().View(120, 24)
	assertFits(t, view, 120, 24)
	plain := ansi.Strip(view)
	if !strings.Contains(plain, "First key 1") || !strings.Contains(plain, "Last key 3") || strings.Contains(plain, "scroll") {
		t.Fatalf("at 120x24 the whole pop-up should fit in two columns:\n%s", plain)
	}
	sideBySide := false
	for line := range strings.SplitSeq(plain, "\n") {
		sideBySide = sideBySide || strings.Count(line, " key ") == 2
	}
	if !sideBySide {
		t.Fatalf("at 120x24 the groups should be side by side:\n%s", plain)
	}
}

func TestShortcutsScrollOnASmallScreen(t *testing.T) {
	const width, height = 80, 24
	s := manyShortcuts()
	shows := func(text string) bool { return strings.Contains(ansi.Strip(s.View(width, height)), text) }

	assertFits(t, s.View(width, height), width, height)
	if !shows("First key 1") || shows("Last key 3") || !shows("↑↓: scroll  esc: close") {
		t.Fatalf("at 80x24 the pop-up should start at its top and offer to scroll:\n%s", s.View(width, height))
	}

	for range 40 {
		s = s.Key("down", width, height)
	}
	assertFits(t, s.View(width, height), width, height)
	if shows("First key 1") || !shows("Last key 3") {
		t.Fatalf("scrolling down should stop at the last line:\n%s", s.View(width, height))
	}
	s = s.Key("up", width, height)
	if shows("Last key 3") || !shows("Last key 2") {
		t.Fatalf("one up from the end should move a single line:\n%s", s.View(width, height))
	}
	for range 40 {
		s = s.Key("k", width, height)
	}
	if !shows("First key 1") {
		t.Fatalf("scrolling up should stop at the first line:\n%s", s.View(width, height))
	}
}

// The scroll offset was counted for the old size. After a resize the pop-up
// must still fit, and show its end rather than nothing.
func TestShortcutsSurviveAResizeWhileScrolled(t *testing.T) {
	s := manyShortcuts()
	for range 40 {
		s = s.Key("down", 80, 24)
	}
	for _, size := range [][2]int{{80, 40}, {60, 16}, {30, 10}, {10, 4}} {
		view := s.View(size[0], size[1])
		for i, line := range strings.Split(view, "\n") {
			if w := lipgloss.Width(line); w > size[0] {
				t.Fatalf("at %v line %d is %d wide:\n%s", size, i, w, view)
			}
		}
		if size[1] >= 10 {
			assertFits(t, view, size[0], size[1])
			if !strings.Contains(ansi.Strip(view), "esc") {
				t.Fatalf("at %v the pop-up lost its footer:\n%s", size, view)
			}
		}
	}
}
