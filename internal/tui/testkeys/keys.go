// Package testkeys builds the key and mouse messages tests send to the TUI.
// Every test goes through Press, Text, Paste, Click, Drag, Release and Wheel
// instead of filling in a tea.KeyPressMsg or a tea.MouseClickMsg by hand, so
// the shape of those messages lives in one file: when Bubble Tea changes it,
// only this file follows.
//
// The messages are built the way Bubble Tea's own parser delivers them, so a
// test sees what a terminal would send: ctrl+letter is the letter with ModCtrl,
// alt+letter has no Text, and an upper-case letter is the lower-case one with
// ModShift.
//
// It imports nothing but Bubble Tea, so any test package can use it without an
// import cycle.
package testkeys

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// named maps the names a key's String() shows, without modifiers, to their code.
var named = map[string]rune{
	"enter":     tea.KeyEnter,
	"tab":       tea.KeyTab,
	"esc":       tea.KeyEscape,
	"backspace": tea.KeyBackspace,
	"delete":    tea.KeyDelete,
	"insert":    tea.KeyInsert,

	"up":     tea.KeyUp,
	"down":   tea.KeyDown,
	"left":   tea.KeyLeft,
	"right":  tea.KeyRight,
	"home":   tea.KeyHome,
	"end":    tea.KeyEnd,
	"pgup":   tea.KeyPgUp,
	"pgdown": tea.KeyPgDown,

	"f1": tea.KeyF1, "f2": tea.KeyF2, "f3": tea.KeyF3, "f4": tea.KeyF4,
	"f5": tea.KeyF5, "f6": tea.KeyF6, "f7": tea.KeyF7, "f8": tea.KeyF8,
	"f9": tea.KeyF9, "f10": tea.KeyF10, "f11": tea.KeyF11, "f12": tea.KeyF12,
	"f13": tea.KeyF13, "f14": tea.KeyF14, "f15": tea.KeyF15, "f16": tea.KeyF16,
	"f17": tea.KeyF17, "f18": tea.KeyF18, "f19": tea.KeyF19, "f20": tea.KeyF20,
}

// ctrlPunct are the control keys that are not ctrl+letter, by the key that
// carries ctrl: ctrl+@ is the NUL character, which Bubble Tea reports as
// ctrl+space.
var ctrlPunct = map[string]rune{
	"@":     tea.KeySpace,
	"space": tea.KeySpace,
	"[":     '[',
	"\\":    '\\',
	"]":     ']',
	"^":     '^',
	"_":     '_',
	"-":     '-',
}

// Press builds the key message for a key name as a key's String() shows it:
// "a", "enter", "space", "ctrl+a", "alt+x", "shift+tab", "ctrl+shift+end",
// "alt+esc". A string that is not a key name, such as "hello", is typed text
// and becomes one message carrying all of it, like Text.
//
// It panics on a combination it cannot build, so a typo in a test fails at the
// line that wrote it.
func Press(s string) tea.KeyPressMsg {
	if s == "" {
		panic("testkeys: Press of an empty key name")
	}
	var mod tea.KeyMod
	base := s
	for {
		switch {
		case strings.HasPrefix(base, "alt+") && len(base) > len("alt+"):
			mod |= tea.ModAlt
			base = strings.TrimPrefix(base, "alt+")
		case strings.HasPrefix(base, "ctrl+") && len(base) > len("ctrl+"):
			mod |= tea.ModCtrl
			base = strings.TrimPrefix(base, "ctrl+")
		case strings.HasPrefix(base, "shift+") && len(base) > len("shift+"):
			mod |= tea.ModShift
			base = strings.TrimPrefix(base, "shift+")
		default:
			return build(s, base, mod)
		}
	}
}

func build(whole, base string, mod tea.KeyMod) tea.KeyPressMsg {
	if base == "space" {
		if mod == 0 {
			return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		}
		if mod.Contains(tea.ModCtrl) && mod&^(tea.ModCtrl|tea.ModAlt) == 0 {
			return tea.KeyPressMsg{Code: tea.KeySpace, Mod: mod}
		}
		if mod == tea.ModAlt {
			return tea.KeyPressMsg{Code: tea.KeySpace, Mod: mod}
		}
		panic(fmt.Sprintf("testkeys: cannot build the key %q", whole))
	}
	if code, ok := named[base]; ok {
		return tea.KeyPressMsg{Code: code, Mod: mod}
	}
	if mod == 0 {
		return Text(base)
	}
	if utf8.RuneCountInString(base) == 1 {
		r, _ := utf8.DecodeRuneInString(base)
		switch {
		case mod.Contains(tea.ModCtrl) && !mod.Contains(tea.ModShift):
			if code, ok := ctrlPunct[base]; ok {
				return tea.KeyPressMsg{Code: code, Mod: mod}
			}
			if r >= 'a' && r <= 'z' {
				return tea.KeyPressMsg{Code: r, Mod: mod}
			}
		case mod == tea.ModAlt:
			return tea.KeyPressMsg{Code: r, Mod: mod}
		}
	}
	panic(fmt.Sprintf("testkeys: cannot build the key %q", whole))
}

// Text is typed text: one message carrying every rune of s, which is how a
// terminal delivers characters that arrive together. A single upper-case
// letter is built as Bubble Tea parses it: the lower-case key with ModShift.
func Text(s string) tea.KeyPressMsg {
	if utf8.RuneCountInString(s) != 1 {
		return tea.KeyPressMsg{Code: tea.KeyExtended, Text: s}
	}
	r, _ := utf8.DecodeRuneInString(s)
	if r < utf8.RuneSelf && unicode.IsUpper(r) {
		return tea.KeyPressMsg{Code: unicode.ToLower(r), ShiftedCode: r, Mod: tea.ModShift, Text: s}
	}
	return tea.KeyPressMsg{Code: r, Text: s}
}

// Paste is text the terminal pasted rather than typed.
func Paste(s string) tea.PasteMsg {
	return tea.PasteMsg{Content: s}
}

// Click is a press of the left button on the cell at column x, row y of the
// screen, both from 0.
func Click(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

// Drag is the pointer moved onto the cell at column x, row y with the left
// button held down, as a terminal reports a drag.
func Drag(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft}
}

// Move is the pointer moved onto the cell at column x, row y with no button
// held, which a terminal reports when it is asked for every move.
func Move(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseNone}
}

// Release is the left button let go of on the cell at column x, row y.
func Release(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}
}

// Wheel is one notch of the wheel, up or down, with the pointer on the cell at
// column x, row y.
func Wheel(x, y int, up bool) tea.MouseWheelMsg {
	button := tea.MouseWheelDown
	if up {
		button = tea.MouseWheelUp
	}
	return tea.MouseWheelMsg{X: x, Y: y, Button: button}
}
