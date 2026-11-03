package components

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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

// flatten runs cmd and every command it batches, in order, and returns the
// messages.
func flatten(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, flatten(c)...)
		}
		return out
	case nil:
		return nil
	default:
		return []tea.Msg{msg}
	}
}

// fakeClipboard is a system clipboard in memory; writes records what was put on
// it, in order. It makes the clear come at once.
func fakeClipboard(t *testing.T) (contents *string, writes *[]string) {
	t.Helper()
	contents, writes = new(string), new([]string)
	withClipboard(t, func(s string) error {
		*contents = s
		*writes = append(*writes, s)
		return nil
	})
	savedRead, savedAfter := ReadClipboard, ClipboardClearAfter
	ReadClipboard = func() (string, error) { return *contents, nil }
	ClipboardClearAfter = time.Millisecond
	t.Cleanup(func() { ReadClipboard, ClipboardClearAfter = savedRead, savedAfter })
	return contents, writes
}

func flashText(out []tea.Msg) string {
	for _, msg := range out {
		if f, ok := msg.(msgs.StatusFlashMsg); ok {
			return f.Text
		}
	}
	return ""
}

func TestCopySecretPutsItOnTheClipboardThenEmptiesIt(t *testing.T) {
	contents, writes := fakeClipboard(t)

	flatten(CopySecret("hunter2", "password"))

	if len(*writes) != 2 || (*writes)[0] != "hunter2" {
		t.Fatalf("writes = %q, want the value and then the clear", *writes)
	}
	if *contents != "" {
		t.Fatalf("the clipboard still holds %q", *contents)
	}
}

// What the user copied since is theirs to keep.
func TestCopySecretLeavesWhatWasCopiedSince(t *testing.T) {
	contents, _ := fakeClipboard(t)
	ReadClipboard = func() (string, error) { return "copied since", nil }

	flatten(CopySecret("hunter2", "password"))

	if *contents != "hunter2" {
		t.Fatalf("the clipboard holds %q: the clear must not have touched it", *contents)
	}
}

// A clipboard den cannot read must not be emptied blindly when it can be read
// next time: an error says nothing about what is on it.
func TestCopySecretDoesNotClearAClipboardItCannotRead(t *testing.T) {
	_, writes := fakeClipboard(t)
	ReadClipboard = func() (string, error) { return "", errors.New("no clipboard") }

	flatten(CopySecret("hunter2", "password"))

	if len(*writes) != 1 {
		t.Fatalf("writes = %q, want only the copy", *writes)
	}
}

func TestCopySecretFlashSaysWhatAndUntilWhen(t *testing.T) {
	fakeClipboard(t)
	ClipboardClearAfter = 30 * time.Millisecond

	got := flashText(flatten(CopySecret("hunter2", "password")))

	if want := "password copied — clipboard clears in 30ms"; got != want {
		t.Fatalf("flash = %q, want %q", got, want)
	}
}

// Without a system clipboard the terminal sets it, and the clear goes the same
// way, unchecked: that clipboard cannot be read, and the secret must not
// outlive its time.
func TestCopySecretThroughTheTerminalClearsItThere(t *testing.T) {
	fakeClipboard(t)
	withClipboard(t, func(string) error { return errors.New("no xclip") })

	out := flatten(CopySecret("hunter2", "password"))

	var asked []string
	for _, msg := range out {
		if fmt.Sprintf("%T", msg) == "tea.setClipboardMsg" {
			asked = append(asked, fmt.Sprint(msg))
		}
	}
	if len(asked) != 2 || asked[0] != "hunter2" || asked[1] != "" {
		t.Fatalf("the terminal was asked for %q, want the value and then the clear", asked)
	}
	if flash := flashText(out); !strings.Contains(flash, "sent to the terminal") {
		t.Fatalf("flash = %q, want it to say the terminal was asked", flash)
	}
}
