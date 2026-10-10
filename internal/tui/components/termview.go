package components

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/vt"

	"github.com/lukaszgard/den/internal/termsession"
)

// TermView is a terminal session as the TUI uses it: the session's own
// screen, size and lifetime, plus the keyboard in Bubble Tea's terms.
type TermView struct {
	*termsession.Session
}

// NewTermView wraps a running session.
func NewTermView(s *termsession.Session) *TermView {
	return &TermView{Session: s}
}

// Cursor is the program's cursor with the shape it asked for, or nil.
func (t *TermView) Cursor() *tea.Cursor {
	c, ok := t.Session.Cursor()
	if !ok {
		return nil
	}
	cur := tea.NewCursor(c.X, c.Y)
	cur.Blink = c.Blink
	switch c.Style {
	case vt.CursorUnderline:
		cur.Shape = tea.CursorUnderline
	case vt.CursorBar:
		cur.Shape = tea.CursorBar
	}
	return cur
}

// HandleKey sends a key to the session's program.
func (t *TermView) HandleKey(msg tea.KeyPressMsg) {
	sendKey(t.Session, msg)
}

// keySink is where keys go: a session, or a bare emulator in the tests.
type keySink interface {
	SendKey(k vt.KeyPressEvent)
	SendText(text string)
	Paste(text string)
}

// plainKeys are the keys the emulator encodes itself, which matters for the
// arrows: a pager switches them to a different encoding. Bubble Tea, the
// emulator and Ultraviolet share one set of key codes, so a code is passed on
// as it is.
var plainKeys = map[rune]bool{
	tea.KeyEnter: true, tea.KeyTab: true, tea.KeyBackspace: true, tea.KeyEscape: true,
	tea.KeyUp: true, tea.KeyDown: true, tea.KeyRight: true, tea.KeyLeft: true,
	tea.KeyHome: true, tea.KeyEnd: true, tea.KeyPgUp: true, tea.KeyPgDown: true,
	tea.KeyDelete: true, tea.KeyInsert: true,
	tea.KeyF1: true, tea.KeyF2: true, tea.KeyF3: true, tea.KeyF4: true,
	tea.KeyF5: true, tea.KeyF6: true, tea.KeyF7: true, tea.KeyF8: true,
	tea.KeyF9: true, tea.KeyF10: true, tea.KeyF11: true, tea.KeyF12: true,
}

// chord is a key with the modifiers that change what it sends, alt aside.
type chord struct {
	code rune
	mod  tea.KeyMod
}

// modifiedKeys are the ctrl and shift variants of the navigation keys. The
// emulator encodes no modifiers on those, so they are sent as the xterm
// sequences readline and the pagers expect (ctrl+arrows move by words).
var modifiedKeys = map[chord]string{
	{tea.KeyUp, tea.ModCtrl}:    "\x1b[1;5A",
	{tea.KeyDown, tea.ModCtrl}:  "\x1b[1;5B",
	{tea.KeyRight, tea.ModCtrl}: "\x1b[1;5C",
	{tea.KeyLeft, tea.ModCtrl}:  "\x1b[1;5D",
	{tea.KeyHome, tea.ModCtrl}:  "\x1b[1;5H",
	{tea.KeyEnd, tea.ModCtrl}:   "\x1b[1;5F",

	{tea.KeyUp, tea.ModShift}:    "\x1b[1;2A",
	{tea.KeyDown, tea.ModShift}:  "\x1b[1;2B",
	{tea.KeyRight, tea.ModShift}: "\x1b[1;2C",
	{tea.KeyLeft, tea.ModShift}:  "\x1b[1;2D",
	{tea.KeyHome, tea.ModShift}:  "\x1b[1;2H",
	{tea.KeyEnd, tea.ModShift}:   "\x1b[1;2F",

	{tea.KeyUp, tea.ModCtrl | tea.ModShift}:    "\x1b[1;6A",
	{tea.KeyDown, tea.ModCtrl | tea.ModShift}:  "\x1b[1;6B",
	{tea.KeyRight, tea.ModCtrl | tea.ModShift}: "\x1b[1;6C",
	{tea.KeyLeft, tea.ModCtrl | tea.ModShift}:  "\x1b[1;6D",
	{tea.KeyHome, tea.ModCtrl | tea.ModShift}:  "\x1b[1;6H",
	{tea.KeyEnd, tea.ModCtrl | tea.ModShift}:   "\x1b[1;6F",

	{tea.KeyPgUp, tea.ModCtrl}:   "\x1b[5;5~",
	{tea.KeyPgDown, tea.ModCtrl}: "\x1b[6;5~",
}

// sendableMods are the modifiers a terminal program can be told about. The
// lock states and super/hyper that a keyboard-enhancing terminal reports would
// stop the emulator, which matches a key as a whole, from recognising it.
const sendableMods = tea.ModShift | tea.ModAlt | tea.ModCtrl

// sendKey translates a Bubble Tea key press for the program in a terminal.
// A key with no terminal encoding is dropped.
//
// The emulator matches a key event as a whole struct, so every event is built
// afresh from the code and the modifiers rather than passed on with the fields
// Bubble Tea adds (shifted and base codes, text).
func sendKey(to keySink, msg tea.KeyPressMsg) {
	k := tea.Key(msg)
	mod := k.Mod & sendableMods
	alt := mod&tea.ModAlt != 0
	rest := mod &^ tea.ModAlt // what is left once alt, an escape prefix, is set aside
	// altPrefixed is s, preceded by escape when alt was held.
	altPrefixed := func(s string) string {
		if alt {
			return "\x1b" + s
		}
		return s
	}

	switch {
	// Typed characters, several at once when they arrived together.
	case k.Text != "" && rest&tea.ModCtrl == 0:
		to.SendText(altPrefixed(k.Text))
	case modifiedKeys[chord{k.Code, rest}] != "":
		to.SendText(altPrefixed(modifiedKeys[chord{k.Code, rest}]))
	// shift+enter, which only a terminal with key disambiguation reports apart
	// from enter, is a new line, not a submit. The emulator does not speak the
	// kitty protocol, so a program here cannot ask for a distinct sequence (and
	// would print one it did not ask for); it is sent the line feed ctrl+j
	// makes, which agent prompts take as a new line and a shell as enter.
	case k.Code == tea.KeyEnter && rest == tea.ModShift:
		to.SendText(altPrefixed("\n"))
	// ctrl and shift change nothing for these keys in a terminal that does not
	// report them: with Bubble Tea v2's key disambiguation on, ctrl+enter,
	// ctrl+backspace and ctrl+tab arrive as their own keys, and a program here
	// has always been sent the plain one.
	// (shift+tab alone is the exception: the emulator has a sequence for it.)
	case k.Code == tea.KeyTab && rest == tea.ModShift:
		to.SendKey(vt.KeyPressEvent{Code: vt.KeyTab, Mod: mod})
	case plainKeys[k.Code] && rest&^(tea.ModCtrl|tea.ModShift) == 0:
		to.SendKey(vt.KeyPressEvent{Code: k.Code, Mod: mod &^ (tea.ModCtrl | tea.ModShift)})
	// ctrl+@ and ctrl+space are the NUL character, which the emulator knows as
	// ctrl+space; typed with shift (the 2 key) a keyboard-enhancing terminal
	// reports it as ctrl+shift+2.
	case rest&tea.ModCtrl != 0 && (k.Code == '@' || k.Code == tea.KeySpace || (k.Code == '2' && rest&tea.ModShift != 0)):
		to.SendKey(vt.KeyPressEvent{Code: vt.KeySpace, Mod: mod &^ tea.ModShift})
	// ctrl+_ is also ctrl+/, ctrl+- and ctrl+shift+- (undo in readline; a plain
	// terminal sends the same character for ctrl+-); ctrl+^ is ctrl+shift+6.
	case rest&tea.ModCtrl != 0 && (k.Code == '/' || k.Code == '-'):
		to.SendKey(vt.KeyPressEvent{Code: '_', Mod: mod&^tea.ModShift | tea.ModCtrl})
	case rest&tea.ModCtrl != 0 && k.Code == '6' && rest&tea.ModShift != 0:
		to.SendKey(vt.KeyPressEvent{Code: '^', Mod: mod&^tea.ModShift | tea.ModCtrl})
	// A control key arrives as its letter with ctrl: ctrl+a is 'a', ctrl+] is
	// ']'. The emulator turns those into the control characters.
	case rest == tea.ModCtrl && (k.Code >= 'a' && k.Code <= 'z' || strings.ContainsRune("[\\]^_", k.Code)):
		to.SendKey(vt.KeyPressEvent{Code: k.Code, Mod: mod})
	// ctrl+shift+a is a different key to a keyboard-enhancing terminal, but the
	// control character is the same one ctrl+a sends.
	case rest == tea.ModCtrl|tea.ModShift && k.Code >= 'a' && k.Code <= 'z':
		to.SendKey(vt.KeyPressEvent{Code: k.Code, Mod: mod &^ tea.ModShift})
	// A character with no text of its own: alt+b is the letter and alt, and
	// alt+shift+b the upper-case letter, whether or not the terminal reports
	// the shifted code.
	case rest&^tea.ModShift == 0 && printable(k.Code):
		code := k.Code
		if rest == tea.ModShift {
			code = k.ShiftedCode
			if code == 0 {
				code = unicode.ToUpper(k.Code)
			}
		}
		to.SendText(altPrefixed(string(code)))
	}
}

// printable reports a code that is a character rather than one of Bubble
// Tea's key codes, which all lie above the last Unicode code point.
func printable(code rune) bool {
	return code <= unicode.MaxRune && unicode.IsPrint(code)
}
