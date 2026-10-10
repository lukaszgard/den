package detail

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// fakeTerm is a terminal without a process: it remembers what it was asked.
type fakeTerm struct {
	screen     string
	dead       bool
	fullScreen bool
	exitErr    error
	closes     int
	cols, rows int
	keys       []string
	pasted     []string
	cursor     *tea.Cursor
}

func (f *fakeTerm) View() string {
	lines := make([]string, max(f.rows, 1))
	lines[0] = f.screen
	return strings.Join(lines, "\n")
}
func (f *fakeTerm) Cursor() *tea.Cursor           { return f.cursor }
func (f *fakeTerm) Resize(cols, rows int)         { f.cols, f.rows = cols, rows }
func (f *fakeTerm) HandleKey(msg tea.KeyPressMsg) { f.keys = append(f.keys, msg.String()) }
func (f *fakeTerm) Paste(text string)             { f.pasted = append(f.pasted, text) }
func (f *fakeTerm) Alive() bool                   { return !f.dead }
func (f *fakeTerm) FullScreen() bool              { return f.fullScreen && !f.dead }
func (f *fakeTerm) ExitErr() error                { return f.exitErr }
func (f *fakeTerm) Close()                        { f.closes++; f.dead = true }

// tabsWith returns tabs holding n live terminals named psql, and the terminals.
func tabsWith(n int) (*Tabs, []*fakeTerm) {
	tabs := NewTabs()
	terms := make([]*fakeTerm, n)
	for i := range terms {
		terms[i] = &fakeTerm{screen: fmt.Sprintf("terminal %d", i+1)}
		tabs.Add("psql", terms[i])
	}
	return tabs, terms
}

func TestAddSelectsTheNewTab(t *testing.T) {
	tabs := NewTabs()
	if tabs.Selected() != 0 || tabs.Current() != nil {
		t.Fatal("new tabs should show the logs")
	}
	first := &fakeTerm{}
	if n, ok := tabs.Add("psql", first); !ok || n != 1 {
		t.Fatalf("Add = %d, %v, want tab 1", n, ok)
	}
	if tabs.Selected() != 1 || tabs.Current() != first || tabs.Name(1) != "psql" {
		t.Fatalf("after Add: selected %d, name %q", tabs.Selected(), tabs.Name(1))
	}
}

func TestAddStopsAtNineTerminals(t *testing.T) {
	tabs, _ := tabsWith(MaxTerminals)
	if !tabs.Full() {
		t.Fatal("nine terminals should be the limit: the digit keys reach no further")
	}
	extra := &fakeTerm{}
	if _, ok := tabs.Add("psql", extra); ok || tabs.Len() != MaxTerminals {
		t.Fatalf("a tenth terminal was added (%d tabs)", tabs.Len())
	}
	if tabs.Selected() != MaxTerminals {
		t.Fatalf("a refused Add moved the selection to %d", tabs.Selected())
	}
}

func TestSelectionWrapsThroughTheLogs(t *testing.T) {
	tabs, _ := tabsWith(2)
	tabs.Select(0)
	var seen []int
	for range 4 {
		tabs.Next()
		seen = append(seen, tabs.Selected())
	}
	if fmt.Sprint(seen) != "[1 2 0 1]" {
		t.Fatalf("Next went through %v, want [1 2 0 1]", seen)
	}
	tabs.Select(0)
	tabs.Prev()
	if tabs.Selected() != 2 {
		t.Fatalf("Prev from the logs selected %d, want the last terminal", tabs.Selected())
	}
	tabs.Select(7)
	if tabs.Selected() != 2 {
		t.Fatalf("Select of a tab that does not exist moved the selection to %d", tabs.Selected())
	}
}

func TestCloseStopsTheTerminalAndKeepsTheSelectionSensible(t *testing.T) {
	cases := []struct {
		name               string
		selected, closeTab int
		wantSelected       int
		wantCurrent        int // which of the original terminals is shown; 0 for the logs
	}{
		{"the selected tab: its right neighbour slides in", 2, 2, 2, 3},
		{"the selected last tab: the one before it", 3, 3, 2, 2},
		{"a tab before the selected one: the same terminal stays selected", 3, 1, 2, 3},
		{"a tab after the selected one: nothing moves", 1, 3, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tabs, terms := tabsWith(3)
			tabs.Select(tc.selected)
			tabs.Close(tc.closeTab)
			if terms[tc.closeTab-1].closes != 1 {
				t.Fatal("closing a tab did not stop its terminal")
			}
			if tabs.Len() != 2 || tabs.Selected() != tc.wantSelected {
				t.Fatalf("after Close: %d tabs, selected %d, want 2 tabs, selected %d", tabs.Len(), tabs.Selected(), tc.wantSelected)
			}
			if tabs.Current() != Terminal(terms[tc.wantCurrent-1]) {
				t.Fatalf("the wrong terminal is shown after the close")
			}
		})
	}

	tabs, _ := tabsWith(1)
	tabs.Close(1)
	if tabs.Len() != 0 || tabs.Selected() != 0 || tabs.Current() != nil {
		t.Fatalf("closing the only terminal should show the logs, selected %d", tabs.Selected())
	}
	tabs.Close(0) // the logs are not a tab to close
	tabs.Close(5)
}

func TestRename(t *testing.T) {
	tabs, _ := tabsWith(2)
	tabs.Rename(2, "  migration  ")
	if tabs.Name(2) != "migration" || tabs.Name(1) != "psql" {
		t.Fatalf("names = %q, %q", tabs.Name(1), tabs.Name(2))
	}
	tabs.Rename(2, "   ")
	if tabs.Name(2) != "migration" {
		t.Fatalf("an empty name replaced %q with %q", "migration", tabs.Name(2))
	}
	tabs.Rename(1, strings.Repeat("ż", 80))
	if got := []rune(tabs.Name(1)); len(got) != MaxNameLen {
		t.Fatalf("a long name was kept at %d characters, want %d", len(got), MaxNameLen)
	}
}

// A restarted client comes back a moment after it was asked for. The tab is
// found by its id, because by then tabs before it may have been closed.
func TestReplaceFindsItsTabAfterOthersMoved(t *testing.T) {
	tabs, terms := tabsWith(3)
	tabs.Rename(3, "reports")
	id := tabs.ID(3)
	terms[2].dead = true

	tabs.Close(1)
	fresh := &fakeTerm{}
	if !tabs.Replace(id, fresh) {
		t.Fatal("Replace did not find the tab that moved from third to second")
	}
	if tabs.Term(2) != Terminal(fresh) || tabs.Name(2) != "reports" {
		t.Fatalf("tab 2 = %q, want the fresh terminal under the old name", tabs.Name(2))
	}
	if terms[2].closes != 1 {
		t.Fatal("the terminal that was replaced was not closed")
	}

	tabs.Close(2)
	late := &fakeTerm{}
	if tabs.Replace(id, late) {
		t.Fatal("Replace found a tab that was closed")
	}
}

// A disconnect stops the clients but keeps the tabs: their names and last
// screens are the user's, and the tabs can be restarted after reconnecting.
func TestStopAllKeepsTheTabs(t *testing.T) {
	tabs, terms := tabsWith(2)
	tabs.Rename(1, "migration")
	if tabs.Running() != 2 {
		t.Fatalf("Running = %d, want 2", tabs.Running())
	}
	tabs.StopAll()
	if tabs.Len() != 2 || tabs.Name(1) != "migration" || tabs.Running() != 0 {
		t.Fatalf("after StopAll: %d tabs, %d running", tabs.Len(), tabs.Running())
	}
	for i, term := range terms {
		if term.closes != 1 {
			t.Fatalf("terminal %d was closed %d times", i+1, term.closes)
		}
	}
}

func TestExitedTabReportsHowItEnded(t *testing.T) {
	tabs, terms := tabsWith(2)
	terms[0].dead = true
	terms[1].dead, terms[1].exitErr = true, errors.New("exit status 2")
	if got := tabs.Exit(1); got != "exited" {
		t.Errorf("clean exit = %q", got)
	}
	if got := tabs.Exit(2); got != "exited: exit status 2" {
		t.Errorf("failed exit = %q", got)
	}
	live, _ := tabsWith(1)
	if got := live.Exit(1); got != "" {
		t.Errorf("a running terminal reports an exit: %q", got)
	}
}
