package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/msgs"
)

// These measure one redraw. Bubble Tea calls View after every message Update
// handles, so a redraw is the cost of each tick, key and service event, and of
// each chunk of output a client prints in a terminal tab. Compare runs with
// benchstat:
//
//	go test -run '^$' -bench . -count 10 ./internal/tui > new.txt
//
// Lip Gloss v2 renders full colour whatever the terminal, so the numbers are
// those of a real one.

// benchApp is den on a 140×40 terminal over 30 services, the menu focused.
func benchApp(b *testing.B) *AppModel {
	svcs := make([]service.Service, 0, 30)
	for i := range 30 {
		svcs = append(svcs, newSvc(service.TypeRDS, fmt.Sprintf("db-%02d", i)))
	}
	return newHarness(b, managerWith(svcs...), nil).m
}

func BenchmarkView(b *testing.B) {
	m := benchApp(b)
	b.ReportAllocs()
	for b.Loop() {
		_ = m.View().Content
	}
}

// BenchmarkTick is an idle redraw: the tick that keeps timers on screen moving.
func BenchmarkTick(b *testing.B) {
	m := benchApp(b)
	now := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		now = now.Add(tickInterval)
		model, _ := m.Update(msgs.TickMsg{Time: now})
		m = model.(*AppModel)
		_ = m.View().Content
	}
}

// screenTerminal is a fakeTerminal showing a screen full of client output.
type screenTerminal struct {
	*fakeTerminal
	screen string
}

func (s screenTerminal) View() string { return s.screen }

// psqlScreen is what a query leaves on a cols×rows terminal: a bold header and
// rows of a result table.
func psqlScreen(cols, rows int) string {
	lines := make([]string, 0, rows)
	lines = append(lines, "\x1b[1m id  | name                 | created_at\x1b[0m", "-----+----------------------+---------------------")
	for i := len(lines); i < rows; i++ {
		lines = append(lines, fmt.Sprintf(" %3d | customer-%-12d | 2026-09-%02d 12:00:00", i, i*7, i%28+1))
	}
	for i, l := range lines {
		if len(l) > cols {
			lines[i] = l[:cols]
		}
	}
	return strings.Join(lines, "\n")
}

// BenchmarkTerminalOutput is the redraw each chunk of a client's output
// causes: the detail view with a terminal tab open and a full screen in it.
func BenchmarkTerminalOutput(b *testing.B) {
	h, _, _ := terminalHarness(b)
	var term screenTerminal
	h.m.startTerminal = func(_ service.ClientCommand, cols, rows int) (liveTerminal, error) {
		term = screenTerminal{fakeTerminal: &fakeTerminal{changed: make(chan struct{}, 1)}, screen: psqlScreen(cols, rows)}
		return term, nil
	}
	h.press("t")
	if !strings.Contains(plain(h.m), "customer-7") {
		b.Fatalf("the terminal tab is not on screen:\n%s", plain(h.m))
	}
	m := h.m
	msg := terminalChangedMsg{term: term}
	b.ReportAllocs()
	for b.Loop() {
		model, _ := m.Update(msg)
		m = model.(*AppModel)
		_ = m.View().Content
	}
}

// BenchmarkLogsWithLinks is an idle redraw of the Logs pane with an address on
// every line, each found and drawn as a link.
func BenchmarkLogsWithLinks(b *testing.B) {
	m := benchApp(b)
	for i := range 60 {
		m.logger.Info(fmt.Sprintf("fetched https://den-tui.com/releases/v1.%d/latest.json in 20ms", i))
	}
	model, _ := m.Update(msgs.TickMsg{Time: time.Now()})
	m = model.(*AppModel)
	m.leftCursor = m.paneIndexOf(paneLogs)
	if !strings.Contains(m.View().Content, "\x1b]8;;https://den-tui.com/releases/v1.59/latest.json") {
		b.Fatalf("the log's addresses are not links:\n%s", plain(m))
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = m.View().Content
	}
}
