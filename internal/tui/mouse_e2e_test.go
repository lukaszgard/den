//go:build !windows

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/termsession"
)

// at is where text starts on the screen, as the column and row a terminal
// reports a click on it, both from 0.
func (sc screen) at(text string) (x, y int) {
	sc.t.Helper()
	screen := sc.waitText(text)
	for row, line := range strings.Split(screen, "\n") {
		if i := strings.Index(line, text); i >= 0 {
			return ansi.StringWidth(line[:i]), row
		}
	}
	panic("unreachable: waitText saw it")
}

// mouse sends what a terminal in SGR mode sends for button at x, y: the cell
// counted from 1, M for a press and m for a release.
func (sc screen) mouse(button, x, y int, release bool) {
	end := "M"
	if release {
		end = "m"
	}
	sc.typeText(fmt.Sprintf("\x1b[<%d;%d;%d%s", button, x+1, y+1, end))
}

func (sc screen) clickAt(x, y int) {
	sc.mouse(0, x, y, false)
	sc.mouse(0, x, y, true)
}

func (sc screen) click(text string) { sc.clickAt(sc.at(text)) }

// doubleClick clicks twice where text is now: the frame the first click
// draws may show the same text somewhere else first.
func (sc screen) doubleClick(text string) {
	x, y := sc.at(text)
	sc.clickAt(x, y)
	sc.clickAt(x, y)
}

// drag presses the left button on the first cell of from, moves to the last
// cell of to and lets go there: 32 is the left button held while the pointer
// moves.
func (sc screen) drag(from, to string) {
	x0, y0 := sc.at(from)
	x1, y1 := sc.at(to)
	x1 += ansi.StringWidth(to) - 1
	sc.mouse(0, x0, y0, false)
	sc.mouse(32, x1, y1, false)
	sc.mouse(0, x1, y1, true)
}

// wheelDown is one notch of the wheel down with the pointer on text.
func (sc screen) wheelDown(text string) {
	x, y := sc.at(text)
	sc.mouse(65, x, y, false)
}

// den on a real terminal, driven by the mouse as a terminal reports it: den
// must ask for it, read the SGR sequences, and land each click on what the
// frame Bubble Tea drew shows there.
func TestMouseEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("runs den on a pty")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh on this machine")
	}
	root := filepath.Join(t.TempDir(), "runbooks")
	for _, name := range []string{"Alpha", "Beta"} {
		folder := filepath.Join(root, strings.ToLower(name))
		if err := os.MkdirAll(folder, 0o750); err != nil {
			t.Fatal(err)
		}
		manifest := "name: " + name + "\ncommand: ./run.sh\n"
		if err := os.WriteFile(filepath.Join(folder, runbook.ManifestName), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "run.sh"), []byte("#!/bin/sh\necho hi\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	clip := filepath.Join(t.TempDir(), "clipboard")
	s, err := termsession.Start(termsession.Command{
		Path: os.Args[0],
		Args: []string{"-test.run=^TestHelperDenRunbooks$"},
		Env:  []string{runbookE2EEnv + "=" + root, clipboardE2EEnv + "=" + clip, "HOME=" + t.TempDir(), "SHELL=/bin/sh"},
	}, 120, 36)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	den := screen{t: t, s: s}

	// A double-click on the menu opens Runbooks; the wheel over the list
	// moves to Beta, and enter opens it.
	den.waitText("Developer Environment Navigator")
	den.doubleClick("Runbooks")
	den.waitText("Alpha")
	den.wheelDown("Alpha")
	den.waitFor("the cursor on Beta", func(s string) bool {
		for line := range strings.SplitSeq(s, "\n") {
			if strings.Contains(line, "▸") && strings.Contains(line, "Beta") {
				return true
			}
		}
		return false
	})
	// enter opens Beta only if the double-click gave the list the focus.
	den.enter()
	den.waitText("NEVER RUN")
	den.assertFrame(36)

	// A click on a tab of the view shows it.
	den.click("History")
	den.waitText("No runs kept yet.")

	// esc goes back to the list, where a double-click opens Alpha.
	den.esc()
	den.waitGone("NEVER RUN")
	den.doubleClick("Alpha")
	den.waitFor("Alpha's view", func(s string) bool {
		return strings.Contains(s, "NEVER RUN") && strings.Contains(s, "runbook  Alpha")
	})

	// Back on the list, a drag from Alpha to Beta copies both rows of the
	// panel, and nothing of the menu beside it.
	den.esc()
	den.waitGone("NEVER RUN")
	den.drag("Alpha", "Beta")
	den.waitText("copied 2 lines")
	data, err := os.ReadFile(clip)
	if err != nil {
		t.Fatal(err)
	}
	copied := string(data)
	if !strings.HasPrefix(copied, "Alpha") || !strings.HasSuffix(copied, "Beta") || strings.Count(copied, "\n") != 1 ||
		strings.Contains(copied, "Runbooks") {
		t.Fatalf("copied %q, want Alpha's row from its name and Beta's up to its name", copied)
	}
}
