package detail

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/tui/testkeys"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Bubble Tea keeps only the last lines of a frame taller than the terminal, so
// a view one line too tall loses its top border without any error. These tests
// pin the height of the view instead of eyeballing it.

type fakeSvc struct {
	typ   string // service type; redis when empty
	cmd   string
	state service.RuntimeState
	logs  []string
}

func (f *fakeSvc) Info() service.ServiceInfo {
	typ := f.typ
	if typ == "" {
		typ = service.TypeRedis
	}
	return service.ServiceInfo{Name: "cache", Type: typ}
}
func (f *fakeSvc) Start(context.Context) (<-chan service.StatusUpdate, error) { return nil, nil }
func (f *fakeSvc) Stop(context.Context) error                                 { return nil }
func (f *fakeSvc) RefreshToken(context.Context) error                         { return nil }
func (f *fakeSvc) ConnectCommand() string                                     { return f.cmd }
func (f *fakeSvc) State() service.RuntimeState                                { return f.state }
func (f *fakeSvc) Logs() []string                                             { return f.logs }
func (f *fakeSvc) LogSeq() uint64                                             { return uint64(len(f.logs)) }

func logLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("log line %d", i+1)
	}
	return lines
}

// assertFrame checks that view fills exactly height lines and that the box
// starts on the first of them: a blank row above the frame is a row the logs
// or the terminal could have had.
func assertFrame(t *testing.T, view string, height int) {
	t.Helper()
	if got := lipgloss.Height(view); got != height {
		t.Fatalf("view is %d lines, want %d:\n%s", got, height, view)
	}
	if lines := strings.Split(view, "\n"); !strings.Contains(lines[0], "╭") {
		t.Fatalf("line 0 = %q, want the box's top border", lines[0])
	}
}

func TestViewFitsItsHeight(t *testing.T) {
	expires := time.Now().Add(10 * time.Minute)
	cases := []struct {
		name          string
		width, height int
		svc           *fakeSvc
	}{
		{"plain", 120, 30, &fakeSvc{cmd: "redis-cli -h localhost -p 50223"}},
		{"token bar", 120, 30, &fakeSvc{
			cmd:   "redis-cli -h localhost -p 50223",
			state: service.RuntimeState{TokenExpires: &expires, TokenTTL: 15 * time.Minute},
		}},
		{"wrapping connect command", 60, 20, &fakeSvc{
			cmd: "psql 'host=localhost port=50223 dbname=orders user=orders_readonly sslmode=verify-full sslrootcert=/tmp/global-bundle.pem'",
		}},
		{"more logs than fit", 120, 30, &fakeSvc{cmd: "redis-cli", logs: logLines(200)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New(tc.svc, nil, tc.width, tc.height)
			view := m.View()
			assertFrame(t, view, tc.height)
			if n := len(tc.svc.logs); n > 0 && !strings.Contains(view, tc.svc.logs[n-1]) {
				t.Fatalf("the newest log line %q is not visible:\n%s", tc.svc.logs[n-1], view)
			}
		})
	}
}

// The token section appears once a credential is minted, after the view was
// sized; the logs have to give up the rows it takes.
func TestViewFitsWhenTokenSectionAppears(t *testing.T) {
	svc := &fakeSvc{cmd: "redis-cli", logs: logLines(200)}
	m := New(svc, nil, 120, 30)
	assertFrame(t, m.View(), 30)

	expires := time.Now().Add(10 * time.Minute)
	m, _ = m.Update(msgs.ServiceEventMsg{Update: service.StatusUpdate{
		Info:  svc.Info(),
		State: service.RuntimeState{Status: service.StatusConnected, TokenExpires: &expires},
	}})
	view := m.View()
	assertFrame(t, view, 30)
	if !strings.Contains(view, "log line 200") {
		t.Fatalf("the newest log line is not visible after the token section appeared:\n%s", view)
	}
}

func TestViewFitsAfterResize(t *testing.T) {
	svc := &fakeSvc{cmd: "redis-cli", logs: logLines(200)}
	m := New(svc, nil, 120, 30)
	for _, size := range []tea.WindowSizeMsg{{Width: 100, Height: 20}, {Width: 140, Height: 45}} {
		m, _ = m.Update(size)
		view := m.View()
		assertFrame(t, view, size.Height)
		if !strings.Contains(view, "log line 200") {
			t.Fatalf("the newest log line is not visible at %dx%d:\n%s", size.Width, size.Height, view)
		}
	}
}

// A data store's toggle is the a key, named auto-reconnect everywhere else; a
// bare "Reconnect" read as if it governed the token too. Tunnels and VPNs keep
// "Reconnect", the name of their r key.
func TestToggleTagsNameTheReconnectToggleAsItsKey(t *testing.T) {
	cases := []struct {
		svc        service.Service
		want, lack string
	}{
		{&tokenSvc{fakeSvc: &fakeSvc{typ: service.TypeRDS}}, "Auto-reconnect: ", ""},
		{&toggleSvc{fakeSvc: &fakeSvc{typ: service.TypeTunnel}}, "Reconnect: ", "Auto-reconnect"},
		{&toggleSvc{fakeSvc: &fakeSvc{typ: service.TypeVPN}}, "Reconnect: ", "Auto-reconnect"},
	}
	for _, tc := range cases {
		tags := toggleTags(tc.svc)
		if !strings.Contains(tags, tc.want) || (tc.lack != "" && strings.Contains(tags, tc.lack)) {
			t.Errorf("%s: toggle tags %q, want %q", tc.svc.Info().Type, ansi.Strip(tags), tc.want)
		}
	}
}

// ── terminal tabs ────────────────────────────────────────────────────────────

// produced runs cmd and returns the messages it yields, batches unpacked.
func produced(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, produced(c)...)
		}
		return out
	default:
		return []tea.Msg{msg}
	}
}

// press sends keys one by one and collects what the view asked the root model for.
func press(m Model, keys ...string) (Model, []tea.Msg) {
	var out []tea.Msg
	for _, k := range keys {
		var cmd tea.Cmd
		m, cmd = m.Update(testkeys.Press(k))
		out = append(out, produced(cmd)...)
	}
	return m, out
}

func opens(out []tea.Msg) []msgs.OpenTerminalMsg {
	var o []msgs.OpenTerminalMsg
	for _, msg := range out {
		if open, ok := msg.(msgs.OpenTerminalMsg); ok {
			o = append(o, open)
		}
	}
	return o
}

// rdsView is a detail view over n live terminals, with the logs selected.
func rdsView(n, width, height int) (Model, *Tabs, []*fakeTerm) {
	tabs, terms := tabsWith(n)
	tabs.Select(0)
	svc := &fakeSvc{cmd: `psql "host=localhost port=50111 dbname=orders"`, logs: logLines(50)}
	return New(svc, tabs, width, height), tabs, terms
}

func TestViewFitsWithTerminalTabs(t *testing.T) {
	t.Run("a terminal tab replaces the logs", func(t *testing.T) {
		m, tabs, _ := rdsView(2, 120, 30)
		tabs.Select(2)
		m, _ = m.Update(msgs.TickMsg{})
		view := m.View()
		assertFrame(t, view, 30)
		if !strings.Contains(view, "terminal 2") || strings.Contains(view, "log line 50") {
			t.Fatalf("tab 2 should show its terminal instead of the logs:\n%s", view)
		}
		for _, label := range []string{"Logs", "1 psql", "2 psql"} {
			if !strings.Contains(view, label) {
				t.Fatalf("the tab bar is missing %q:\n%s", label, view)
			}
		}
	})
	t.Run("the logs tab still shows the logs", func(t *testing.T) {
		m, _, _ := rdsView(2, 120, 30)
		view := m.View()
		assertFrame(t, view, 30)
		if !strings.Contains(view, "log line 50") || strings.Contains(view, "terminal 1") {
			t.Fatalf("the logs tab should show the logs:\n%s", view)
		}
	})
	t.Run("nine long names on a narrow window", func(t *testing.T) {
		m, tabs, _ := rdsView(MaxTerminals, 60, 20)
		for n := 1; n <= MaxTerminals; n++ {
			tabs.Rename(n, fmt.Sprintf("a rather long tab name %d", n))
		}
		for _, sel := range []int{0, 1, 5, MaxTerminals} {
			tabs.Select(sel)
			m, _ = m.Update(msgs.TickMsg{})
			view := m.View()
			assertFrame(t, view, 20)
			for i, line := range strings.Split(view, "\n") {
				if w := lipgloss.Width(line); w > 60 {
					t.Fatalf("with tab %d selected, line %d is %d wide in a 60-wide window:\n%s", sel, i, w, view)
				}
			}
			if sel > 0 && !strings.Contains(view, fmt.Sprintf("%d a rather", sel)) {
				t.Fatalf("the selected tab %d is not in the tab bar:\n%s", sel, view)
			}
		}
	})
	t.Run("while renaming, asking to close, and typing", func(t *testing.T) {
		m, tabs, _ := rdsView(2, 100, 25)
		tabs.Select(1)
		m, _ = press(m, "n")
		assertFrame(t, m.View(), 25)
		m, _ = press(m, "esc", "x")
		assertFrame(t, m.View(), 25)
		m, _ = press(m, "esc", "enter")
		assertFrame(t, m.View(), 25)
	})
}

func TestSelectedTerminalGetsTheLogArea(t *testing.T) {
	m, tabs, terms := rdsView(2, 120, 30)
	cols, rows := m.TerminalSize()
	if cols != 116 || rows < 5 {
		t.Fatalf("TerminalSize = %dx%d, want 116 columns and the rows under the header", cols, rows)
	}
	tabs.Select(1)
	m, _ = m.Update(msgs.TickMsg{})
	if terms[0].cols != cols || terms[0].rows != rows {
		t.Fatalf("the shown terminal is %dx%d, want %dx%d", terms[0].cols, terms[0].rows, cols, rows)
	}
	if terms[1].cols != 0 {
		t.Fatal("a terminal that is not on screen was resized")
	}

	m, _ = m.Update(tea.WindowSizeMsg{Width: 90, Height: 40})
	cols, rows = m.TerminalSize()
	if terms[0].cols != 86 || terms[0].cols != cols || terms[0].rows != rows {
		t.Fatalf("after a resize the terminal is %dx%d, want %dx%d", terms[0].cols, terms[0].rows, cols, rows)
	}
	assertFrame(t, m.View(), 40)
}

// In terminal mode the keyboard is the client's: the keys den would act on
// are exactly the ones a database prompt needs.
func TestTerminalModeSendsEveryKeyToTheClient(t *testing.T) {
	m, tabs, terms := rdsView(1, 120, 30)
	tabs.Select(1)
	m = m.StartTyping()
	if !m.CapturingKeys() {
		t.Fatal("terminal mode should capture the keyboard")
	}
	m, out := press(m, "q", "d", "c", "x", "n", "t", "tab", "backspace", "ctrl+c", "1")
	if len(out) != 0 {
		t.Fatalf("keys typed into the terminal made den act: %v", out)
	}
	if got, want := strings.Join(terms[0].keys, " "), "q d c x n t tab backspace ctrl+c 1"; got != want {
		t.Fatalf("the client received %q, want %q", got, want)
	}
	if tabs.Len() != 1 {
		t.Fatal("x typed into the terminal closed the tab")
	}

	m, _ = press(m, "ctrl+t")
	if m.CapturingKeys() {
		t.Fatal("ctrl+t should hand the keyboard back to den")
	}
	if len(terms[0].keys) != 10 {
		t.Fatal("ctrl+t itself was sent to the client")
	}
	if tabs.Selected() != 1 {
		t.Fatal("ctrl+t should leave the terminal on screen")
	}
}

func TestDenKeysMoveBetweenTabs(t *testing.T) {
	m, tabs, terms := rdsView(3, 120, 30)
	steps := []struct {
		key  string
		want int
	}{{"right", 1}, {"tab", 2}, {"left", 1}, {"shift+tab", 0}, {"left", 3}, {"right", 0}}
	for _, s := range steps {
		m, _ = press(m, s.key)
		if tabs.Selected() != s.want {
			t.Fatalf("after %s tab %d is selected, want %d", s.key, tabs.Selected(), s.want)
		}
		if m.CapturingKeys() {
			t.Fatalf("%s should only select, not start typing", s.key)
		}
	}

	m, _ = press(m, "2")
	if tabs.Selected() != 2 || !m.CapturingKeys() {
		t.Fatal("a digit should jump to that terminal and start typing")
	}
	m, _ = press(m, "ctrl+t", "7")
	if tabs.Selected() != 2 || m.CapturingKeys() {
		t.Fatal("a digit without a terminal should do nothing")
	}
	m, _ = press(m, "enter", "a")
	if !m.CapturingKeys() || strings.Join(terms[1].keys, "") != "a" {
		t.Fatal("enter on a terminal tab should start typing into it")
	}
}

func TestTAsksForANewTerminalUnlessFull(t *testing.T) {
	m, _, _ := rdsView(1, 120, 30)
	_, out := press(m, "t")
	if o := opens(out); len(o) != 1 || o[0].TabID != 0 || o[0].ServiceName != "cache" {
		t.Fatalf("t asked for %+v, want one new terminal for the service", o)
	}

	full, _, _ := rdsView(MaxTerminals, 120, 30)
	_, out = press(full, "t")
	if len(opens(out)) != 0 {
		t.Fatal("t asked for a tenth terminal")
	}
	if len(out) != 1 {
		t.Fatalf("a refused t should explain itself in the status bar, got %v", out)
	}
}

// A service without a client den can run has no tabs: t is not a key there
// and the view is the one it always was.
func TestNoTabsWithoutTheCapability(t *testing.T) {
	m := New(&fakeSvc{cmd: "redis-cli", logs: logLines(3)}, nil, 120, 30)
	m, out := press(m, "t", "right", "1", "n", "x", "enter")
	if len(out) != 0 || m.CapturingKeys() {
		t.Fatalf("terminal keys acted on a service without terminals: %v", out)
	}
	if view := m.View(); !strings.Contains(view, "─ Logs") || strings.Contains(view, "terminal") {
		t.Fatalf("the view of a service without terminals changed:\n%s", view)
	}
}

func TestRenameEditsTheNameInPlace(t *testing.T) {
	m, tabs, terms := rdsView(2, 120, 30)
	tabs.Select(2)

	m, _ = press(m, "n")
	if !m.CapturingKeys() {
		t.Fatal("the name field should capture the keyboard: backspace is also den's back key")
	}
	m, out := press(m, "backspace", "backspace", "backspace", "backspace", "d", "x", "q", "enter")
	if len(out) != 0 || len(terms[1].keys) != 0 {
		t.Fatal("keys typed into the name field went elsewhere")
	}
	if tabs.Name(2) != "dxq" || m.CapturingKeys() {
		t.Fatalf("name = %q, want dxq and the field closed", tabs.Name(2))
	}

	m, _ = press(m, "n", "z", "esc")
	if tabs.Name(2) != "dxq" || m.CapturingKeys() {
		t.Fatalf("esc should cancel the rename, name = %q", tabs.Name(2))
	}

	tabs.Select(0)
	m, _ = press(m, "n")
	if m.CapturingKeys() {
		t.Fatal("the logs tab has no name to edit")
	}
}

func TestCloseAsksOnlyWhileTheClientRuns(t *testing.T) {
	m, tabs, terms := rdsView(3, 120, 30)
	tabs.Select(2)

	m, _ = press(m, "x")
	if tabs.Len() != 3 || !m.CapturingKeys() {
		t.Fatal("x on a running client should ask first")
	}
	if !strings.Contains(m.View(), "2 psql") || !strings.Contains(m.View(), "y ") {
		t.Fatalf("the prompt does not name the tab and the key:\n%s", m.View())
	}
	m, _ = press(m, "n")
	if tabs.Len() != 3 || m.CapturingKeys() || terms[1].closes != 0 {
		t.Fatal("any key but y should keep the tab")
	}
	m, _ = press(m, "x", "y")
	if tabs.Len() != 2 || terms[1].closes != 1 || m.CapturingKeys() {
		t.Fatal("y should close the tab")
	}

	terms[2].dead = true
	tabs.Select(2)
	m, _ = press(m, "x")
	if tabs.Len() != 1 || m.CapturingKeys() {
		t.Fatal("an exited tab should close without asking")
	}
}

// A client that dies while being typed into must not turn the rest of the
// line into den commands: d would disconnect the service, x close the tab.
func TestDeadTerminalSwallowsTypeAhead(t *testing.T) {
	m, tabs, terms := rdsView(2, 120, 30)
	tabs.Select(1)
	m = m.StartTyping()
	terms[0].dead = true
	terms[0].exitErr = fmt.Errorf("exit status 2")

	m, out := press(m, "d", "x", "c", "q", "t", "n")
	if len(out) != 0 || tabs.Len() != 2 || !m.CapturingKeys() {
		t.Fatalf("keys typed after the client died acted on den: %v, %d tabs", out, tabs.Len())
	}
	if len(terms[0].keys) != 0 {
		t.Fatal("keys were sent to a dead client")
	}
	if view := m.View(); !strings.Contains(view, "exited: exit status 2") {
		t.Fatalf("the view does not say how the client ended:\n%s", view)
	}

	_, out = press(m, "enter")
	if o := opens(out); len(o) != 1 || o[0].TabID != tabs.ID(1) {
		t.Fatalf("enter asked for %+v, want a restart of tab 1", o)
	}
	m, _ = press(m, "ctrl+t")
	if m.CapturingKeys() {
		t.Fatal("ctrl+t should leave a dead terminal too")
	}
	_, out = press(m, "enter")
	if o := opens(out); len(o) != 1 || o[0].TabID != tabs.ID(1) {
		t.Fatalf("enter in den mode on an exited tab asked for %+v, want its restart", o)
	}
}

// Nothing reads the keyboard of an exited client, so esc can mean what it
// means everywhere else in den: back.
func TestEscLeavesAnExitedTerminal(t *testing.T) {
	m, tabs, terms := rdsView(1, 120, 30)
	tabs.Select(1)
	m = m.StartTyping()
	terms[0].dead = true
	if view := m.View(); !strings.Contains(view, "esc/ctrl+t") {
		t.Fatalf("the help line of an exited terminal does not offer esc:\n%s", view)
	}

	m, out := press(m, "esc")
	if m.CapturingKeys() {
		t.Fatal("esc should hand the keyboard back to den once the client has exited")
	}
	if len(out) != 0 || tabs.Selected() != 1 || tabs.Len() != 1 {
		t.Fatalf("esc should only leave terminal mode: %v, tab %d of %d", out, tabs.Selected(), tabs.Len())
	}
}

// At a prompt esc is den's "back", as everywhere else: one esc returns the
// keyboard to den and leaves the terminal on screen.
func TestEscLeavesATerminalAtItsPrompt(t *testing.T) {
	m, tabs, terms := rdsView(1, 120, 30)
	tabs.Select(1)
	m = m.StartTyping()
	if view := m.View(); !strings.Contains(view, "esc/ctrl+t") {
		t.Fatalf("the help line does not offer esc at a prompt:\n%s", view)
	}

	m, out := press(m, "esc")
	if m.CapturingKeys() || len(terms[0].keys) != 0 {
		t.Fatalf("esc at a prompt should leave terminal mode and not reach the client (client got %v)", terms[0].keys)
	}
	if len(out) != 0 || tabs.Selected() != 1 {
		t.Fatalf("esc should only return the keyboard: %v, tab %d", out, tabs.Selected())
	}
}

// vim, opened with \e, and the pager live on esc. While one of them has the
// terminal, a single esc is theirs; a second right after it, or ctrl+t, gives
// den the keys.
func TestEscBelongsToAFullScreenProgram(t *testing.T) {
	m, tabs, terms := rdsView(1, 120, 30)
	tabs.Select(1)
	m = m.StartTyping()
	terms[0].fullScreen = true
	if view := m.View(); !strings.Contains(view, "esc esc/ctrl+t") {
		t.Fatalf("the help line should ask for esc twice while a full-screen program runs:\n%s", view)
	}

	m, _ = press(m, "esc")
	if !m.CapturingKeys() || strings.Join(terms[0].keys, " ") != "esc" {
		t.Fatalf("one esc should go to the full-screen program, which got %v", terms[0].keys)
	}
	m, _ = press(m, "esc")
	if m.CapturingKeys() || strings.Join(terms[0].keys, " ") != "esc" {
		t.Fatalf("a second esc should give den the keys, not reach the program (it got %v)", terms[0].keys)
	}

	// A key between two escs makes them two single ones: vim's esc i esc.
	m = m.StartTyping()
	terms[0].keys = nil
	m, _ = press(m, "esc", "i", "esc")
	if !m.CapturingKeys() || strings.Join(terms[0].keys, " ") != "esc i esc" {
		t.Fatalf("esc i esc should all reach the program, which got %v", terms[0].keys)
	}

	// Two escs read at once arrive as alt+esc: one for the program, one for den.
	terms[0].keys = nil
	m, _ = m.Update(testkeys.Press("alt+esc"))
	if m.CapturingKeys() || strings.Join(terms[0].keys, " ") != "esc" {
		t.Fatalf("alt+esc should pass one esc on and leave (the program got %v)", terms[0].keys)
	}

	m = m.StartTyping()
	terms[0].fullScreen = false // the editor was closed: back at the prompt
	m, _ = press(m, "esc")
	if m.CapturingKeys() {
		t.Fatal("back at the prompt, one esc should leave terminal mode again")
	}
}

// esc makes leaving a terminal easy, and the next keys are then den's. d
// stops every client of the service, so it asks while any of them runs.
func TestDisconnectAsksWhileTerminalsRun(t *testing.T) {
	m, _, terms := rdsView(2, 120, 30)
	if m.RunningTerminals() != 2 {
		t.Fatalf("RunningTerminals = %d, want 2", m.RunningTerminals())
	}

	m = m.AskDisconnect()
	if !m.CapturingKeys() {
		t.Fatal("the question should capture the next key")
	}
	if view := m.View(); !strings.Contains(view, "2 running terminals") || !strings.Contains(view, "y ") {
		t.Fatalf("the prompt does not say what a disconnect stops:\n%s", view)
	}
	assertFrame(t, m.View(), 30)
	m, out := press(m, "e")
	if len(out) != 0 || m.CapturingKeys() {
		t.Fatalf("any key but y should keep the connection: %v", out)
	}

	m = m.AskDisconnect()
	m, out = press(m, "y")
	if m.CapturingKeys() || len(out) != 1 {
		t.Fatalf("y should ask the root model to disconnect: %v", out)
	}
	if d, ok := out[0].(msgs.DisconnectMsg); !ok || d.ServiceName != "cache" {
		t.Fatalf("y produced %#v, want a disconnect of the service", out[0])
	}

	terms[0].dead, terms[1].dead = true, true
	if m.RunningTerminals() != 0 {
		t.Fatal("exited terminals are nothing a disconnect would stop")
	}
	if New(&fakeSvc{cmd: "redis-cli"}, nil, 120, 30).RunningTerminals() != 0 {
		t.Fatal("a service without terminals has none running")
	}
}

// The help line is cut at the terminal's width. A service with a token has so
// many keys that t, named last, was lost on anything narrower than 121 columns:
// the terminal tabs were there and nothing on screen said so.
func TestHelpLineNamesTheTerminalKeyOnANarrowTerminal(t *testing.T) {
	tabs := NewTabs()
	tabs.Add("psql", &fakeTerm{})
	tabs.Select(0) // back to the logs, where the service's own keys are listed
	m := New(&tokenSvc{fakeSvc: &fakeSvc{typ: service.TypeRDS, cmd: "psql"}}, tabs, 80, 40)

	lines := strings.Split(strings.TrimRight(ansi.Strip(m.View()), "\n"), "\n")
	help := lines[len(lines)-1]
	for _, want := range []string{"h: help", "t: terminal", "←→: tabs"} {
		if !strings.Contains(help, want) {
			t.Errorf("an 80-column help line does not name %q:\n%s", want, help)
		}
	}
}

// The tab bar is two lines, the tabs and a rule under them, and a terminal's
// body starts under both: its first line is the row after the rule.
func TestBodyStartsUnderTheRuleBelowTheTabs(t *testing.T) {
	m, tabs, terms := rdsView(1, 120, 30)
	tabs.Select(1)
	terms[0].screen = "first line of the client"
	m, _ = m.Update(msgs.TickMsg{})
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	_, _, top := m.bodyArea(m.upperSection())
	if !strings.Contains(lines[top-1], strings.Repeat("─", 100)) || !strings.Contains(lines[top-2], "Logs │ 1 ") {
		t.Fatalf("rows %d and %d should be the tabs and the rule under them:\n%s", top-2, top-1, strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[top], "first line of the client") {
		t.Fatalf("row %d should be the terminal's first line:\n%s", top, strings.Join(lines, "\n"))
	}
}

// cellOf is the cell where text starts in the view.
func cellOf(t *testing.T, m Model, text string) (x, y int) {
	t.Helper()
	clean, _ := components.ScanZones(m.View())
	for y, line := range strings.Split(ansi.Strip(clean), "\n") {
		if i := strings.Index(line, text); i >= 0 {
			return ansi.StringWidth(line[:i]), y
		}
	}
	t.Fatalf("%q is not in the view:\n%s", text, ansi.Strip(clean))
	return 0, 0
}

// zoneOf is the zone under the first cell of text in the view, which is what
// a click there would land on.
func zoneOf(t *testing.T, m Model, text string) string {
	t.Helper()
	x, y := cellOf(t, m, text)
	_, zones := components.ScanZones(m.View())
	id, _ := zones.At(x, y)
	return id
}

// A click on a tab selects it, a click on a client's screen types into it,
// and while a disconnect waits for its answer no click is one.
func TestClickingTheTabs(t *testing.T) {
	m, tabs, _ := rdsView(2, 120, 30)
	m, _ = m.Click(zoneOf(t, m, "2 psql"), false)
	if tabs.Selected() != 2 || m.CapturingKeys() {
		t.Fatalf("selected %d: a click on 2 psql should select it, den keeping the keys", tabs.Selected())
	}
	x, y := cellOf(t, m, "terminal 2")
	if !m.TerminalAt(x, y) || m.TerminalAt(x, 0) || m.TerminalAt(0, y) {
		t.Fatal("TerminalAt should cover the client's screen and nothing else")
	}
	m, _ = m.Click("body", false)
	if !m.CapturingKeys() {
		t.Fatal("a click on the client's screen should type into it")
	}
	m, _ = m.Click(zoneOf(t, m, "Logs"), false)
	if tabs.Selected() != 0 || m.CapturingKeys() {
		t.Fatal("a click on Logs should go back to the logs, den having the keys")
	}
	logs := zoneOf(t, m, "1 psql")
	m = m.AskDisconnect()
	if m, _ = m.Click(logs, false); tabs.Selected() != 0 || !m.CapturingKeys() {
		t.Fatal("a click while a disconnect is asked should do nothing")
	}
}

// The wheel scrolls the logs, but not while a client's screen hides them.
func TestTheWheelScrollsTheLogs(t *testing.T) {
	m, tabs, _ := rdsView(1, 120, 30)
	bottom := ansi.Strip(m.View())
	m, _ = m.Wheel(true)
	if ansi.Strip(m.View()) == bottom {
		t.Fatal("the wheel up should scroll the logs")
	}
	m, _ = m.Wheel(false)
	tabs.Select(1)
	m, _ = m.Wheel(true)
	tabs.Select(0)
	if ansi.Strip(m.View()) != bottom {
		t.Fatal("the wheel over a client's screen scrolled the logs under it")
	}
}
