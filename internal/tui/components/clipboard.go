package components

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/lukaszgard/den/internal/tui/msgs"

	"github.com/atotto/clipboard"

	tea "charm.land/bubbletea/v2"
)

// WriteClipboard and ReadClipboard reach the system clipboard. They are
// variables so tests do not touch the developer's clipboard, or need a machine
// that has one.
var (
	WriteClipboard = clipboard.WriteAll
	ReadClipboard  = clipboard.ReadAll
)

// ClipboardClearAfter is how long a copied secret stays on the clipboard. A
// variable so a test need not wait for it.
var ClipboardClearAfter = 30 * time.Second

// CopyThen copies text and returns what after makes of the outcome. The system
// clipboard comes first: it holds on after den exits and needs nothing from the
// terminal. Where there is none (no xclip or wl-copy on Linux, a session over
// SSH) the terminal is asked to set it with OSC 52, which most terminals take
// and which travels over SSH. It cannot be told whether it worked, so
// viaTerminal says only that it was tried.
func CopyThen(text string, after func(viaTerminal bool) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		if WriteClipboard(text) == nil {
			return after(false)
		}
		return tea.BatchMsg{
			tea.SetClipboard(text),
			func() tea.Msg { return after(true) },
		}
	}
}

// Copy is CopyThen that flashes done in the status bar.
func Copy(text, done string) tea.Cmd {
	return CopyThen(text, func(viaTerminal bool) tea.Msg {
		if viaTerminal {
			done += " (sent to the terminal)"
		}
		return msgs.StatusFlashMsg{Text: done}
	})
}

// CopySecret copies a value that stays valid after it is copied, and empties
// the clipboard ClipboardClearAfter later. what names it in the flash. Only the
// value's hash waits for the clear, so the value itself is not kept alive.
func CopySecret(value, what string) tea.Cmd {
	sum := Digest(value)
	after := ClipboardClearAfter
	return CopyThen(value, func(viaTerminal bool) tea.Msg {
		text := fmt.Sprintf("%s copied — clipboard clears in %s", what, after)
		if viaTerminal {
			text += " (sent to the terminal)"
		}
		return tea.BatchMsg{
			func() tea.Msg { return msgs.StatusFlashMsg{Text: text} },
			tea.Tick(after, func(time.Time) tea.Msg { return ClearCopied(sum, viaTerminal, nil)() }),
		}
	})
}

// ClearCopied empties the clipboard, unless the user has since copied something
// else, which is theirs to keep; cleared is the message of having emptied it. A
// value that went through the terminal cannot be checked, since the terminal's
// clipboard cannot be read: the secret should not outlive its time, so that one
// is emptied the same way it was set.
func ClearCopied(sum string, viaTerminal bool, cleared tea.Msg) tea.Cmd {
	if viaTerminal {
		return tea.Batch(
			tea.SetClipboard(""),
			func() tea.Msg { return cleared },
		)
	}
	return func() tea.Msg {
		cur, err := ReadClipboard()
		if err != nil || Digest(cur) != sum {
			return nil
		}
		_ = WriteClipboard("")
		return cleared
	}
}

// Digest is the hash ClearCopied compares the clipboard against.
func Digest(s string) string {
	h := sha256.Sum256([]byte(s))
	return string(h[:])
}
