package testkeys

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Every key name the suite sends. want is what String() of the message must
// read: the input itself.
func TestPressBuildsEveryKeyTheSuiteSends(t *testing.T) {
	const (
		ctrl  = tea.ModCtrl
		alt   = tea.ModAlt
		shift = tea.ModShift
	)
	cases := []struct {
		in   string
		code rune
		mod  tea.KeyMod
		text string
		want string
	}{
		// Typed text.
		{in: "a", code: 'a', text: "a", want: "a"},
		{in: "/", code: '/', text: "/", want: "/"},
		{in: "N", code: 'n', mod: shift, text: "N", want: "N"},
		{in: "1", code: '1', text: "1", want: "1"},
		{in: "żółw", code: tea.KeyExtended, text: "żółw", want: "żółw"},
		{in: "needle three", code: tea.KeyExtended, text: "needle three", want: "needle three"},
		{in: "pgp", code: tea.KeyExtended, text: "pgp", want: "pgp"},
		{in: "+", code: '+', text: "+", want: "+"},

		// Named keys.
		{in: "space", code: tea.KeySpace, text: " ", want: "space"},
		{in: "enter", code: tea.KeyEnter, want: "enter"},
		{in: "tab", code: tea.KeyTab, want: "tab"},
		{in: "shift+tab", code: tea.KeyTab, mod: shift, want: "shift+tab"},
		{in: "esc", code: tea.KeyEscape, want: "esc"},
		{in: "backspace", code: tea.KeyBackspace, want: "backspace"},
		{in: "delete", code: tea.KeyDelete, want: "delete"},
		{in: "up", code: tea.KeyUp, want: "up"},
		{in: "down", code: tea.KeyDown, want: "down"},
		{in: "left", code: tea.KeyLeft, want: "left"},
		{in: "right", code: tea.KeyRight, want: "right"},
		{in: "home", code: tea.KeyHome, want: "home"},
		{in: "end", code: tea.KeyEnd, want: "end"},
		{in: "pgup", code: tea.KeyPgUp, want: "pgup"},
		{in: "pgdown", code: tea.KeyPgDown, want: "pgdown"},
		{in: "f5", code: tea.KeyF5, want: "f5"},
		{in: "f20", code: tea.KeyF20, want: "f20"},

		// Control keys.
		{in: "ctrl+a", code: 'a', mod: ctrl, want: "ctrl+a"},
		{in: "ctrl+c", code: 'c', mod: ctrl, want: "ctrl+c"},
		{in: "ctrl+d", code: 'd', mod: ctrl, want: "ctrl+d"},
		{in: "ctrl+i", code: 'i', mod: ctrl, want: "ctrl+i"},
		{in: "ctrl+j", code: 'j', mod: ctrl, want: "ctrl+j"},
		{in: "ctrl+m", code: 'm', mod: ctrl, want: "ctrl+m"},
		{in: "ctrl+r", code: 'r', mod: ctrl, want: "ctrl+r"},
		{in: "ctrl+t", code: 't', mod: ctrl, want: "ctrl+t"},
		{in: "ctrl+z", code: 'z', mod: ctrl, want: "ctrl+z"},
		// NUL is what ctrl+@ and ctrl+space send; Bubble Tea names it ctrl+space.
		{in: "ctrl+@", code: tea.KeySpace, mod: ctrl, want: "ctrl+space"},
		{in: "ctrl+space", code: tea.KeySpace, mod: ctrl, want: "ctrl+space"},
		{in: "ctrl+\\", code: '\\', mod: ctrl, want: "ctrl+\\"},
		{in: "ctrl+]", code: ']', mod: ctrl, want: "ctrl+]"},
		{in: "ctrl+-", code: '-', mod: ctrl, want: "ctrl+-"},
		{in: "ctrl+h", code: 'h', mod: ctrl, want: "ctrl+h"},
		{in: "ctrl+i", code: 'i', mod: ctrl, want: "ctrl+i"},
		{in: "ctrl+m", code: 'm', mod: ctrl, want: "ctrl+m"},
		{in: "ctrl+_", code: '_', mod: ctrl, want: "ctrl+_"},

		// Modified navigation keys.
		{in: "ctrl+left", code: tea.KeyLeft, mod: ctrl, want: "ctrl+left"},
		{in: "ctrl+right", code: tea.KeyRight, mod: ctrl, want: "ctrl+right"},
		{in: "ctrl+pgup", code: tea.KeyPgUp, mod: ctrl, want: "ctrl+pgup"},
		{in: "shift+up", code: tea.KeyUp, mod: shift, want: "shift+up"},
		{in: "ctrl+shift+end", code: tea.KeyEnd, mod: ctrl | shift, want: "ctrl+shift+end"},

		// Alt.
		{in: "alt+b", code: 'b', mod: alt, want: "alt+b"},
		{in: "alt+x", code: 'x', mod: alt, want: "alt+x"},
		{in: "alt+esc", code: tea.KeyEscape, mod: alt, want: "alt+esc"},
		{in: "alt+backspace", code: tea.KeyBackspace, mod: alt, want: "alt+backspace"},
		{in: "alt+left", code: tea.KeyLeft, mod: alt, want: "alt+left"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := Press(tc.in)
			if got.Code != tc.code || got.Mod != tc.mod || got.Text != tc.text {
				t.Fatalf("Press(%q) = {Code: %q, Mod: %v, Text: %q}, want {Code: %q, Mod: %v, Text: %q}",
					tc.in, got.Code, got.Mod, got.Text, tc.code, tc.mod, tc.text)
			}
			if got.String() != tc.want {
				t.Fatalf("Press(%q).String() = %q, want %q", tc.in, got.String(), tc.want)
			}
		})
	}
}

func TestTextIsOneMessageOfAllItsRunes(t *testing.T) {
	m := Text("echo url=$DEN_URL")
	if m.Text != "echo url=$DEN_URL" || m.Mod != 0 {
		t.Fatalf("Text built %+v", m)
	}
	// Text never reads its argument as a key name.
	if got := Text("enter"); got.Code == tea.KeyEnter || got.String() != "enter" || got.Text != "enter" {
		t.Fatalf("Text(\"enter\") = %+v, want the five letters", got)
	}
}

func TestPasteIsAPasteMessage(t *testing.T) {
	m := Paste("select 1;\nselect 2;")
	if m.Content != "select 1;\nselect 2;" {
		t.Fatalf("Paste built %+v", m)
	}
}

func TestPressPanicsOnACombinationItCannotBuild(t *testing.T) {
	for _, in := range []string{"", "ctrl+foo", "shift+a", "alt+nothing", "ctrl+shift+a"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Press(%q) built a message, want a panic", in)
				}
			}()
			Press(in)
		}()
	}
}
