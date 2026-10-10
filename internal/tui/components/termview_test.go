package components

import (
	"bytes"
	"io"
	"testing"

	"github.com/lukaszgard/den/internal/tui/testkeys"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/vt"
)

// emulatorSink sends keys to a bare emulator, the same one a session uses, so
// the tests see the bytes a client would receive rather than an intermediate
// value this package made up.
type emulatorSink struct{ emu *vt.Emulator }

func (e emulatorSink) SendKey(k vt.KeyPressEvent) { e.emu.SendKey(k) }
func (e emulatorSink) SendText(text string)       { e.emu.SendText(text) }
func (e emulatorSink) Paste(text string)          { e.emu.Paste(text) }

// received returns what the client is sent for msg. setup, when given, is
// written to the emulator first, as output of the client that switches a mode.
func received(t *testing.T, msg tea.Msg, setup string) string {
	t.Helper()
	emu := vt.NewEmulator(80, 24)
	var got bytes.Buffer
	read := make(chan struct{})
	go func() {
		defer close(read)
		_, _ = io.Copy(&got, emu)
	}()
	if setup != "" {
		_, _ = emu.WriteString(setup)
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		sendKey(emulatorSink{emu}, msg)
	case tea.PasteMsg:
		emu.Paste(msg.Content)
	default:
		t.Fatalf("received cannot send a %T", msg)
	}
	_ = emu.InputPipe().(io.Closer).Close()
	<-read
	return got.String()
}

func TestKeysReachTheClientAsATerminalSendsThem(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyPressMsg
		want string
	}{
		{"a letter", testkeys.Press("a"), "a"},
		{"several runes at once", testkeys.Text("żółw"), "żółw"},
		{"space", testkeys.Press("space"), " "},
		{"enter", testkeys.Press("enter"), "\r"},
		{"tab", testkeys.Press("tab"), "\t"},
		{"shift+tab", testkeys.Press("shift+tab"), "\x1b[Z"},
		{"backspace", testkeys.Press("backspace"), "\x7f"},
		{"esc", testkeys.Press("esc"), "\x1b"},
		{"delete", testkeys.Press("delete"), "\x1b[3~"},
		{"up", testkeys.Press("up"), "\x1b[A"},
		{"down", testkeys.Press("down"), "\x1b[B"},
		{"right", testkeys.Press("right"), "\x1b[C"},
		{"left", testkeys.Press("left"), "\x1b[D"},
		{"home", testkeys.Press("home"), "\x1b[H"},
		{"end", testkeys.Press("end"), "\x1b[F"},
		{"page up", testkeys.Press("pgup"), "\x1b[5~"},
		{"page down", testkeys.Press("pgdown"), "\x1b[6~"},
		{"f5", testkeys.Press("f5"), "\x1b[15~"},
		// The ones a database prompt lives on.
		{"ctrl+c cancels a query", testkeys.Press("ctrl+c"), "\x03"},
		{"ctrl+d ends the session", testkeys.Press("ctrl+d"), "\x04"},
		{"ctrl+a", testkeys.Press("ctrl+a"), "\x01"},
		{"ctrl+r searches history", testkeys.Press("ctrl+r"), "\x12"},
		{"ctrl+z", testkeys.Press("ctrl+z"), "\x1a"},
		{"ctrl+@", testkeys.Press("ctrl+@"), "\x00"},
		{"ctrl+\\", testkeys.Press("ctrl+\\"), "\x1c"},
		{"ctrl+]", testkeys.Press("ctrl+]"), "\x1d"},
		{"ctrl+_ undoes", testkeys.Press("ctrl+_"), "\x1f"},
		// Alt is an escape prefix: readline's word motions.
		{"alt+b", testkeys.Press("alt+b"), "\x1bb"},
		{"alt+backspace", testkeys.Press("alt+backspace"), "\x1b\x7f"},
		{"alt+left", testkeys.Press("alt+left"), "\x1b\x1b[D"},
		{"ctrl+right moves a word", testkeys.Press("ctrl+right"), "\x1b[1;5C"},
		{"ctrl+left moves a word", testkeys.Press("ctrl+left"), "\x1b[1;5D"},
		{"shift+up", testkeys.Press("shift+up"), "\x1b[1;2A"},
		{"ctrl+shift+end", testkeys.Press("ctrl+shift+end"), "\x1b[1;6F"},
		{"ctrl+page up", testkeys.Press("ctrl+pgup"), "\x1b[5;5~"},
		// A key with no terminal encoding sends nothing rather than garbage.
		{"f20", testkeys.Press("f20"), ""},

		// What Bubble Tea v2 delivers: a control key is its letter with ctrl,
		// alt+letter has no text, and a capital is the lower-case key with shift.
		{"ctrl+space is NUL", testkeys.Press("ctrl+space"), "\x00"},
		{"ctrl+@ named by its character", tea.KeyPressMsg{Code: '@', Mod: tea.ModCtrl}, "\x00"},
		{"ctrl+[ is escape", tea.KeyPressMsg{Code: '[', Mod: tea.ModCtrl}, "\x1b"},
		{"ctrl+h is backspace as readline reads it", testkeys.Press("ctrl+h"), "\x08"},
		{"ctrl+i", testkeys.Press("ctrl+i"), "\t"},
		{"ctrl+m", testkeys.Press("ctrl+m"), "\r"},
		{"ctrl+j", testkeys.Press("ctrl+j"), "\n"},
		// Key disambiguation reports ctrl+shift+a as its own key; the program
		// is sent the control character, as it was by a plain terminal.
		{"ctrl+shift+a", tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl | tea.ModShift}, "\x01"},
		{"ctrl+shift+z", tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl | tea.ModShift}, "\x1a"},
		{"alt+ctrl+shift+a", tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt | tea.ModCtrl | tea.ModShift}, "\x1b\x01"},
		{"ctrl+shift+a with caps lock", tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl | tea.ModShift | tea.ModCapsLock}, "\x01"},
		{"alt+ctrl+a", tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt | tea.ModCtrl}, "\x1b\x01"},
		{"alt+capital letter", tea.KeyPressMsg{Code: 'b', ShiftedCode: 'B', Mod: tea.ModAlt | tea.ModShift}, "\x1bB"},
		{"alt+space", testkeys.Press("alt+space"), "\x1b "},
		{"alt+enter", testkeys.Press("alt+enter"), "\x1b\r"},
		{"alt+esc", testkeys.Press("alt+esc"), "\x1b\x1b"},
		{"a capital letter", testkeys.Press("N"), "N"},
		{"a character with no ascii form", testkeys.Press("ż"), "ż"},
		{"an emoji", testkeys.Text("🙂"), "🙂"},
		{"a grapheme cluster of several runes", testkeys.Text("e\u0301"), "e\u0301"},
		{"alt+ctrl+left", tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt | tea.ModCtrl}, "\x1b\x1b[1;5D"},
		{"shift+home", testkeys.Press("shift+home"), "\x1b[1;2H"},
		{"ctrl+home", testkeys.Press("ctrl+home"), "\x1b[1;5H"},
		{"ctrl+down", testkeys.Press("ctrl+down"), "\x1b[1;5B"},
		{"ctrl+shift+left", testkeys.Press("ctrl+shift+left"), "\x1b[1;6D"},
		{"ctrl+page down", testkeys.Press("ctrl+pgdown"), "\x1b[6;5~"},
		{"insert", testkeys.Press("insert"), "\x1b[2~"},
		{"f1", testkeys.Press("f1"), "\x1bOP"},
		{"alt+f5", tea.KeyPressMsg{Code: tea.KeyF5, Mod: tea.ModAlt}, "\x1b\x1b[15~"},
		// shift+enter is a new line for an agent's prompt; the emulator speaks no
		// kitty protocol, so the line feed ctrl+j sends stands for it.
		{"shift+enter", testkeys.Press("shift+enter"), "\n"},
		{"alt+shift+enter", testkeys.Press("alt+shift+enter"), "\x1b\n"},
		// What a terminal with key disambiguation (Ghostty, kitty, iTerm2,
		// WezTerm) reports where a plain terminal sends the bare key: the
		// program is sent the bare key either way.
		{"ctrl+- is ctrl+_", testkeys.Press("ctrl+-"), "\x1f"},
		{"ctrl+enter", testkeys.Press("ctrl+enter"), "\r"},
		{"ctrl+backspace", testkeys.Press("ctrl+backspace"), "\x7f"},
		{"shift+backspace", testkeys.Press("shift+backspace"), "\x7f"},
		{"ctrl+tab", testkeys.Press("ctrl+tab"), "\t"},
		{"shift+esc", testkeys.Press("shift+esc"), "\x1b"},
		{"shift+f5", tea.KeyPressMsg{Code: tea.KeyF5, Mod: tea.ModShift}, "\x1b[15~"},
		{"ctrl+/ is ctrl+_", tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl}, "\x1f"},
		{"ctrl+shift+- is ctrl+_", tea.KeyPressMsg{Code: '-', Mod: tea.ModCtrl | tea.ModShift}, "\x1f"},
		{"ctrl+shift+6 is ctrl+^", tea.KeyPressMsg{Code: '6', Mod: tea.ModCtrl | tea.ModShift}, "\x1e"},
		{"ctrl+shift+2 is NUL", tea.KeyPressMsg{Code: '2', Mod: tea.ModCtrl | tea.ModShift}, "\x00"},
		{"alt+shift+letter without a shifted code", tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt | tea.ModShift}, "\x1bB"},
		// Keys the terminal has no way to tell the program about.
		{"ctrl+digit", tea.KeyPressMsg{Code: '1', Mod: tea.ModCtrl}, ""},
		{"a media key", tea.KeyPressMsg{Code: tea.KeyMediaPlay}, ""},
		// Lock states and super that a keyboard-enhancing terminal reports are
		// not part of the key: the emulator would not know ctrl+a with caps lock.
		{"ctrl+a with caps lock", tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl | tea.ModCapsLock}, "\x01"},
		{"up with num lock", tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModNumLock}, "\x1b[A"},
		{"a letter with super held", tea.KeyPressMsg{Code: 'a', Text: "a", Mod: tea.ModSuper}, "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := received(t, tc.msg, ""); got != tc.want {
				t.Fatalf("the client received %q, want %q", got, tc.want)
			}
		})
	}
}

// A pager switches the terminal to application cursor keys; arrows sent the
// normal way would scroll nothing there.
func TestArrowsFollowTheClientsCursorKeyMode(t *testing.T) {
	const applicationCursorKeys = "\x1b[?1h"
	if got, want := received(t, testkeys.Press("up"), applicationCursorKeys), "\x1bOA"; got != want {
		t.Fatalf("up in application cursor mode sent %q, want %q", got, want)
	}
}

// A paste is one message. Typed out key by key, its newlines would run each
// line of a pasted statement as it arrives.
func TestPasteIsBracketedWhenTheClientAsksForIt(t *testing.T) {
	paste := testkeys.Paste("select 1;\nselect 2;")
	if got, want := received(t, paste, ""), "select 1;\nselect 2;"; got != want {
		t.Fatalf("plain paste sent %q, want %q", got, want)
	}
	const bracketedPaste = "\x1b[?2004h"
	if got, want := received(t, paste, bracketedPaste), "\x1b[200~select 1;\nselect 2;\x1b[201~"; got != want {
		t.Fatalf("bracketed paste sent %q, want %q", got, want)
	}
}
