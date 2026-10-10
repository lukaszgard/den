package components

import (
	"github.com/lukaszgard/den/internal/tui/msgs"

	"github.com/atotto/clipboard"

	tea "charm.land/bubbletea/v2"
)

// WriteClipboard puts text on the system clipboard. It is a variable so tests
// do not touch the developer's clipboard, or need a machine that has one.
var WriteClipboard = clipboard.WriteAll

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
