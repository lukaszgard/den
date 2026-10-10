package components

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
)

func withClipboard(t *testing.T, write func(string) error) {
	t.Helper()
	saved := WriteClipboard
	WriteClipboard = write
	t.Cleanup(func() { WriteClipboard = saved })
}

func TestCopyWritesTheSystemClipboard(t *testing.T) {
	var got string
	withClipboard(t, func(s string) error { got = s; return nil })

	msg := Copy("psql -h localhost", "copied!")()
	if got != "psql -h localhost" {
		t.Fatalf("the clipboard holds %q", got)
	}
	if flash, ok := msg.(msgs.StatusFlashMsg); !ok || flash.Text != "copied!" {
		t.Fatalf("got %#v, want the plain flash", msg)
	}
}

// With no system clipboard the terminal is asked, and the flash says so.
func TestCopyFallsBackToTheTerminal(t *testing.T) {
	withClipboard(t, func(string) error { return errors.New("no xclip") })

	batch, ok := Copy("psql -h localhost", "copied!")().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("want a batch of the OSC 52 request and the flash, got %#v", batch)
	}
	if got := batch[0](); fmt.Sprintf("%T:%v", got, got) != "tea.setClipboardMsg:psql -h localhost" {
		t.Fatalf("the terminal was asked %T %v", got, got)
	}
	flash, ok := batch[1]().(msgs.StatusFlashMsg)
	if !ok || flash.Text != "copied! (sent to the terminal)" {
		t.Fatalf("got %#v, want the flash to say the terminal was asked", batch[1]())
	}
}
