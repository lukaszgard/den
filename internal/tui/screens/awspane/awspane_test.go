package awspane

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/service"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type fakeSession struct {
	name  string
	state service.RuntimeState
}

func (f *fakeSession) Info() service.ServiceInfo {
	return service.ServiceInfo{Name: f.name, Type: service.TypeAWS}
}
func (f *fakeSession) Start(context.Context) (<-chan service.StatusUpdate, error) { return nil, nil }
func (f *fakeSession) Stop(context.Context) error                                 { return nil }
func (f *fakeSession) RefreshToken(context.Context) error                         { return nil }
func (f *fakeSession) ConnectCommand() string                                     { return "" }
func (f *fakeSession) State() service.RuntimeState                                { return f.state }
func (f *fakeSession) Logs() []string                                             { return nil }
func (f *fakeSession) LogSeq() uint64                                             { return 0 }

// newPane builds the pane over an empty AWS config, so the profile list does
// not depend on the machine's ~/.aws. The path is relative because the header
// prints it whole, and a temporary directory's path would widen every line.
func newPane(t *testing.T, sessions ...service.Service) Model {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := os.WriteFile("config", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return New(sessions, "config", "", 117, 40)
}

func loggedIn(name string, expires time.Time) *fakeSession {
	return &fakeSession{name: name, state: service.RuntimeState{
		TokenExpires: &expires,
		TokenTTL:     8 * time.Hour,
	}}
}

// rowOf is the line of view that names the session.
func rowOf(t *testing.T, view, name string) string {
	t.Helper()
	for line := range strings.SplitSeq(view, "\n") {
		if strings.Contains(line, name) {
			return line
		}
	}
	t.Fatalf("no row for %s in:\n%s", name, view)
	return ""
}

func TestUntilClock(t *testing.T) {
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.Local)

	sameDay := time.Date(2026, 10, 15, 14, 59, 16, 0, time.Local)
	if got, want := untilClock(sameDay, now), "until 14:59:16 "+sameDay.Format("MST"); got != want {
		t.Errorf("same day: got %q, want %q", got, want)
	}

	// 16 October 2026 is a Friday wherever the test runs: the date is local.
	late := time.Date(2026, 10, 15, 22, 0, 0, 0, time.Local)
	nextDay := time.Date(2026, 10, 16, 2, 0, 0, 0, time.Local)
	if got, want := untilClock(nextDay, late), "until Fri 02:00:00 "+nextDay.Format("MST"); got != want {
		t.Errorf("past midnight: got %q, want %q", got, want)
	}
}

func TestSessionRowShowsWhenTheLoginEnds(t *testing.T) {
	expires := time.Now().Add(2*time.Hour + 28*time.Minute)
	m := newPane(t, loggedIn("bmwssorow", expires), &fakeSession{name: "den-corp"})

	view := ansi.Strip(m.View(117, 40, false))

	row := rowOf(t, view, "bmwssorow")
	if !strings.Contains(row, "remaining") {
		t.Errorf("logged-in row has no countdown: %q", row)
	}
	if clock := untilClock(expires, time.Now()); !strings.Contains(row, "· "+clock) {
		t.Errorf("logged-in row does not say %q: %q", clock, row)
	}

	row = rowOf(t, view, "den-corp")
	if strings.Contains(row, "until") || strings.Contains(row, "remaining") {
		t.Errorf("row without a token shows a time: %q", row)
	}
}

func TestNarrowPaneDropsTheClock(t *testing.T) {
	const width = 80
	m := newPane(t, loggedIn("bmwssorow", time.Now().Add(2*time.Hour))).SetSize(width, 40)

	view := ansi.Strip(m.View(width, 40, false))

	row := rowOf(t, view, "bmwssorow")
	if !strings.Contains(row, "remaining") {
		t.Errorf("narrow row lost its countdown: %q", row)
	}
	if strings.Contains(row, "until") {
		t.Errorf("narrow row kept the clock: %q", row)
	}
	// A row too long for the box wraps inside it instead of widening it.
	if !strings.Contains(row, "↵ login") {
		t.Errorf("narrow row wrapped: %q", row)
	}
	for line := range strings.SplitSeq(view, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("line is %d cells, pane is %d: %q", w, width, line)
		}
	}
}

// A login kept alive by its refresh token, with no record of den's, has no
// known end. The Secrets pane counts it as live, so the row must not say "No
// active token" and send the user to log in again.
func TestSessionRowWithoutAKnownEnd(t *testing.T) {
	renewable := &fakeSession{name: "bmwssorow", state: service.RuntimeState{Status: service.StatusConnected}}
	m := newPane(t, renewable, &fakeSession{name: "den-corp"})

	view := ansi.Strip(m.View(117, 40, false))

	row := rowOf(t, view, "bmwssorow")
	if strings.Contains(row, "No active token") || !strings.Contains(row, "logged in") {
		t.Errorf("live row with no known end: %q", row)
	}
	if strings.Contains(row, "until") || strings.Contains(row, "remaining") {
		t.Errorf("row with no known end shows a time: %q", row)
	}
	if row = rowOf(t, view, "den-corp"); !strings.Contains(row, "No active token") {
		t.Errorf("row of a session never logged in: %q", row)
	}
}
