package applogs

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/lukaszgard/den/internal/tui/applog"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/tui/testkeys"
)

// The panel used to watch the log's length, which stops changing once the
// log holds its last 1000 lines: a long session stopped showing new ones.
func TestNewLinesShowAfterTheLogIsFull(t *testing.T) {
	logger := applog.New()
	for range 1000 {
		logger.Info("old")
	}
	m := New(logger, 80, 20)
	logger.Info("newest")
	m, _ = m.Update(msgs.TickMsg{Time: time.Now()}, true)
	if view := ansi.Strip(m.View(80, 20, true)); !strings.Contains(view, "newest") {
		t.Fatalf("the newest line is not shown:\n%s", view)
	}
}

// o hides what the runs print and shows it again; the title and the hint say
// which, and only a focused panel takes the key.
func TestOHidesAndShowsTheRunOutput(t *testing.T) {
	logger := applog.New()
	logger.Info("den's own line")
	logger.Output("backup", "a run's line")
	m := New(logger, 80, 20)
	view := func() string { return ansi.Strip(m.View(80, 20, true)) }

	if v := view(); !strings.Contains(v, "a run's line") || !strings.Contains(v, "o: hide run output") || m.OutputHidden() {
		t.Fatalf("run output is shown at first:\n%s", v)
	}
	m, _ = m.Update(testkeys.Press("o"), false)
	if m.OutputHidden() {
		t.Fatal("an unfocused panel took o")
	}
	m, _ = m.Update(testkeys.Press("o"), true)
	if v := view(); !m.OutputHidden() || strings.Contains(v, "a run's line") || !strings.Contains(v, "den's own line") ||
		!strings.Contains(v, "run output hidden") || !strings.Contains(v, "o: show run output") {
		t.Fatalf("o should hide the run output:\n%s", v)
	}
	m, _ = m.Update(testkeys.Press("o"), true)
	if v := view(); m.OutputHidden() || !strings.Contains(v, "a run's line") {
		t.Fatalf("o again should show it:\n%s", v)
	}
}

func TestScrollKeysGoToTheLogAndASizeChangeKeepsIt(t *testing.T) {
	logger := applog.New()
	for i := range 60 {
		logger.Info("line " + strings.Repeat("x", i%7))
	}
	m := New(logger, 80, 20)
	before := ansi.Strip(m.View(80, 20, true))
	m, _ = m.Update(testkeys.Press("pgup"), true)
	if after := ansi.Strip(m.View(80, 20, true)); after == before {
		t.Error("pgup should scroll the log")
	}
	m = m.SetSize(100, 30)
	if v := ansi.Strip(m.View(100, 30, true)); len(strings.Split(v, "\n")) != 30 {
		t.Errorf("the view should follow the new height, got %d lines", len(strings.Split(v, "\n")))
	}
}
