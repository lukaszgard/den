package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/tui/testkeys"
)

// The keys of a run's log in the runbook view: a search, full screen, the
// timestamps, a copy. See components.LogView for the log itself.

// stamped is a line of the log with the time in front of it.
var stamped = regexp.MustCompile(`\[\d\d:\d\d:\d\d\] line 30`)

// longRun opens Greet's view and runs it, printing thirty lines, a coloured
// error and "done".
func (f runbookFixture) longRun() {
	f.t.Helper()
	script := "#!/bin/sh\ni=1\nwhile [ $i -le 30 ]; do echo \"line $i\"; i=$((i+1)); done\n" +
		"printf '\\033[31mERROR\\033[0m something broke\\n'\necho done\n"
	if err := os.WriteFile(filepath.Join(f.greet, "run.sh"), []byte(script), 0o700); err != nil {
		f.t.Fatal(err)
	}
	f.selectRow("Greet")
	f.press("enter", "r")
	f.waitRun("Greet")
	if view := plain(f.m); !strings.Contains(view, "something broke") {
		f.t.Fatalf("the run's output is not in Run:\n%s", view)
	}
}

// While a search is typed, every key is a letter of it: q does not quit, t
// opens no terminal, a digit picks no tab. enter keeps it, n goes on, and esc
// ends it before it leaves the view.
func TestTypingASearchOfTheRunLogTakesEveryKeyAsText(t *testing.T) {
	f := runbookHarness(t)
	f.longRun()
	f.press("/", "q", "t", "1")
	if f.quit || !f.m.showRunbook || len(f.starts.cmds) != 0 || f.selectedTab("Greet") != "Run" {
		t.Fatalf("q, t and 1 should be typed into the search (quit %v, %d programs started, on %s):\n%s",
			f.quit, len(f.starts.cmds), f.selectedTab("Greet"), plain(f.m))
	}
	if view := plain(f.m); !strings.Contains(view, "/qt1▌") || !strings.Contains(view, "no match") {
		t.Fatalf("the help line should show the query and that it matches nothing:\n%s", view)
	}
	f.press("backspace", "backspace", "backspace", "e", "r", "r", "o", "r")
	if view := plain(f.m); !strings.Contains(view, "/error▌  1/1") {
		t.Fatalf("a lower-case query should find ERROR as it is typed:\n%s", view)
	}
	f.press("enter", "n")
	if view := plain(f.m); !strings.Contains(view, "/error  1/1") || !strings.Contains(view, "n/N: next/previous") {
		t.Fatalf("enter should keep the search, and n stay on the only match:\n%s", view)
	}
	f.press("esc")
	if view := plain(f.m); !f.m.showRunbook || strings.Contains(view, "/error") {
		t.Fatalf("esc should end the search, not leave the view:\n%s", view)
	}
	f.press("esc")
	if f.m.showRunbook {
		t.Fatal("a second esc should leave the view")
	}
}

// f shows the log alone, without the header or the tabs; esc leaves the full
// screen before it leaves the view.
func TestFullScreenShowsTheLogAloneAndEscLeavesItFirst(t *testing.T) {
	f := runbookHarness(t)
	f.longRun()
	f.press("f")
	view := plain(f.m)
	if strings.Contains(view, "Run │ History") || strings.Contains(view, "./run.sh") {
		t.Fatalf("full screen should hide the header and the tabs:\n%s", view)
	}
	if !strings.Contains(view, "something broke") || !strings.Contains(view, "f: leave full screen") || !strings.Contains(view, "line 8") {
		t.Fatalf("full screen should show the log over the whole box, and how to leave:\n%s", view)
	}
	f.press("/", "b", "r", "o", "k", "e", "enter")
	if view := plain(f.m); !strings.Contains(view, "/broke  1/1") || strings.Contains(view, "Run │ History") {
		t.Fatalf("a search should work full screen:\n%s", view)
	}
	f.press("esc", "esc")
	view = plain(f.m)
	if !f.m.showRunbook || !strings.Contains(view, "Run │ History") {
		t.Fatalf("esc should end the search, then the full screen, and leave the view only after:\n%s", view)
	}
	f.press("f", "right")
	if view := plain(f.m); !strings.Contains(view, "Run │ History") || strings.Contains(view, "f: leave full screen") {
		t.Fatalf("moving to another tab should end the full screen:\n%s", view)
	}
}

// T hides the time in front of each line, and stays so in the next view
// opened, until T again.
func TestTimestampsHideForEveryRunbookOpened(t *testing.T) {
	f := runbookHarness(t)
	f.longRun()
	if !stamped.MatchString(plain(f.m)) {
		t.Fatalf("a run's lines should start with their time:\n%s", plain(f.m))
	}
	f.press("T")
	if view := plain(f.m); stamped.MatchString(view) || !strings.Contains(view, "line 30") {
		t.Fatalf("T should hide the times, not the lines:\n%s", view)
	}
	f.press("esc", "enter")
	if view := plain(f.m); !f.m.showRunbook || stamped.MatchString(view) || !strings.Contains(view, "line 30") {
		t.Fatalf("the view opened next should hide the times too:\n%s", view)
	}
	f.press("T")
	if !stamped.MatchString(plain(f.m)) {
		t.Fatalf("T again should show them:\n%s", plain(f.m))
	}
}

// c copies the whole log, not the screen, without its colours, the times as
// they are shown.
func TestCopyTakesTheWholeLogWithoutColours(t *testing.T) {
	copied := clipboard(t)
	f := runbookHarness(t)
	f.longRun()
	f.press("c")
	if !strings.Contains(*copied, "line 1\n") || !strings.Contains(*copied, "ERROR something broke") || strings.Contains(*copied, "\x1b") {
		t.Fatalf("c should copy every line, without escape sequences:\n%q", *copied)
	}
	if !strings.Contains(*copied, "] line 1\n") {
		t.Errorf("the copy should keep the times shown:\n%q", *copied)
	}
	n := strings.Count(*copied, "\n") + 1
	if view := plain(f.m); !strings.Contains(view, "copied "+strconv.Itoa(n)+" lines") {
		t.Errorf("the flash should say how many lines were copied (%d):\n%s", n, view)
	}
	f.press("T", "c")
	if strings.Contains(*copied, "] line 1\n") || !strings.Contains(*copied, "line 1\n") {
		t.Errorf("with the times hidden, the copy should have none:\n%q", *copied)
	}
}

// Nothing run yet, there is nothing to copy: the placeholder is not a log.
func TestCopyBeforeAnyRunCopiesNothing(t *testing.T) {
	copied := clipboard(t)
	f := runbookHarness(t)
	f.selectRow("Greet")
	f.press("enter", "c")
	if *copied != "" || !strings.Contains(plain(f.m), "nothing to copy") {
		t.Fatalf("c before a run should copy nothing, got %q:\n%s", *copied, plain(f.m))
	}
}

// On History's list, / opens the run picked and searches its log; esc ends
// the search, then closes the run, then leaves.
func TestSlashOnHistoryOpensThePickedRun(t *testing.T) {
	f := runbookHarness(t)
	f.twoRuns()
	f.press("right", "/")
	if view := plain(f.m); strings.Contains(view, "▸ ") || !strings.Contains(view, "/▌") {
		t.Fatalf("/ on the runs should open the picked one's log and start a search:\n%s", view)
	}
	f.press("s", "e", "c", "o", "n", "d", "enter")
	if view := plain(f.m); !strings.Contains(view, "/second  1/1") {
		t.Fatalf("the search should find the newest run's line:\n%s", view)
	}
	f.press("esc")
	if view := plain(f.m); !strings.Contains(view, "esc: runs") || strings.Contains(view, "/second") {
		t.Fatalf("esc should end the search and keep the run open:\n%s", view)
	}
	f.press("esc")
	if view := plain(f.m); !strings.Contains(view, "▸ ") {
		t.Fatalf("esc again should go back to the runs:\n%s", view)
	}
	f.press("esc")
	if f.m.showRunbook {
		t.Fatal("a third esc should leave the view")
	}
}

// n stays the terminal tab's rename key: it only goes to the next match
// while a search is on, on the log.
func TestNOnATerminalTabStillRenamesIt(t *testing.T) {
	f := runbookHarness(t)
	f.selectRow("Greet")
	f.press("enter", "t", "ctrl+t")
	if f.m.runbookView.CapturingKeys() {
		t.Fatalf("ctrl+t should give den the keys back:\n%s", plain(f.m))
	}
	f.press("n")
	if !f.m.runbookTabs["Greet"].Current().Alive() || !f.m.runbookView.CapturingKeys() {
		t.Fatalf("n on a terminal's tab should rename it:\n%s", plain(f.m))
	}
}

// Text pasted while a search is typed goes into its query, its first line only:
// a log line holds no line break to match.
func TestPastingIntoASearchOfTheRunLog(t *testing.T) {
	f := runbookHarness(t)
	f.longRun()
	f.press("/")
	f.send(testkeys.Paste("something broke\nand more"))
	if view := plain(f.m); !strings.Contains(view, "/something broke▌  1/1") || len(f.starts.cmds) != 0 {
		t.Fatalf("the paste's first line should be the query:\n%s", view)
	}
}

// The log's line says at all times whether the log follows new lines, the run
// live or done: g pauses it, and the end follows again, "following" in place
// of "paused" rather than no mark at all. Its toggles name what they do next.
func TestTheLogLineAlwaysSaysWhetherItFollows(t *testing.T) {
	f := runbookHarness(t)
	f.longRun()
	if view := plain(f.m); !strings.Contains(view, "following  /: search  s: pause  T: hide time  w: wrap") {
		t.Fatalf("a done run at its end should say it follows, with the log's keys:\n%s", view)
	}
	f.press("g")
	if view := plain(f.m); !strings.Contains(view, "paused") || !strings.Contains(view, "s: follow") {
		t.Fatalf("g should pause the log, and s offer to follow again:\n%s", view)
	}
	f.press("G")
	if view := plain(f.m); !strings.Contains(view, "following  /: search  s: pause") {
		t.Fatalf("G should follow again, and say so:\n%s", view)
	}
	f.press("T", "w")
	if view := plain(f.m); !strings.Contains(view, "T: show time") || !strings.Contains(view, "w: unwrap") {
		t.Fatalf("T and w should name what they do next:\n%s", view)
	}
	f.press("T", "w", "right", "right")
	if view := plain(f.m); strings.Contains(view, "following") || strings.Contains(view, "/: search") {
		t.Fatalf("About has no log, so no log line:\n%s", view)
	}
}
