package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaszgard/den/internal/service"
)

// The shortcuts pop-up (h or ?) belongs to the root model: it is drawn over
// the whole screen and gets every key while it is open. These tests pin what
// the help screens it replaced got wrong: a frame taller than the terminal,
// and c, d or r acting on a service behind the help.

// popupBox finds the pop-up on screen by its title: the rows of its top and
// bottom border and the columns of its left and right one.
func popupBox(t *testing.T, view string) (top, bottom, left, right int) {
	t.Helper()
	lines := strings.Split(ansi.Strip(view), "\n")
	top, bottom = -1, -1
	for y, line := range lines {
		if before, _, ok := strings.Cut(line, "│  Shortcuts — "); ok && y > 0 {
			top, left = y-1, ansi.StringWidth(before)
			break
		}
	}
	if top < 0 {
		t.Fatalf("no shortcuts pop-up on screen:\n%s", view)
	}
	border := ansi.Cut(lines[top], left, ansi.StringWidth(lines[top]))
	upTo, _, ok := strings.Cut(border, "╮")
	if !strings.HasPrefix(border, "╭") || !ok {
		t.Fatalf("line %d has no top border above the pop-up's title:\n%s", top, view)
	}
	right = left + ansi.StringWidth(upTo)
	for y := top + 1; y < len(lines); y++ {
		if strings.HasPrefix(ansi.Cut(lines[y], left, left+1), "╰") {
			return top, y, left, right
		}
	}
	t.Fatalf("the pop-up has no bottom border:\n%s", view)
	return
}

// assertSameFrame checks that the pop-up left the screen's size alone: a
// frame one line taller loses its top in a real terminal.
func assertSameFrame(t *testing.T, closed, open string, width int) {
	t.Helper()
	if got, want := lipgloss.Height(open), lipgloss.Height(closed); got != want {
		t.Fatalf("with the pop-up open the screen is %d lines, without it %d:\n%s", got, want, open)
	}
	if got, want := strings.Split(ansi.Strip(open), "\n")[0], strings.Split(ansi.Strip(closed), "\n")[0]; got != want {
		t.Fatalf("the first line changed under the pop-up: %q, was %q", got, want)
	}
	for i, line := range strings.Split(open, "\n") {
		if w, was := lipgloss.Width(line), lipgloss.Width(strings.Split(closed, "\n")[i]); w > max(width, was) {
			t.Fatalf("line %d is %d wide on a %d-wide screen:\n%s", i, w, width, open)
		}
	}
}

func TestShortcutsOpenOverEveryServiceType(t *testing.T) {
	types := []string{
		service.TypeRDS, service.TypeRedis, service.TypeDocumentDB, service.TypeRedshift,
		service.TypeOpenSearch, service.TypeNeptune, service.TypeKafka, service.TypeDocker,
		service.TypeTunnel, service.TypeVPN, service.TypeAWS,
	}
	for _, typ := range types {
		for _, open := range []string{"h", "?"} {
			t.Run(typ+" "+open, func(t *testing.T) {
				h := newHarness(t, managerWith(newSvc(typ, "svc")), nil)
				h.press("right", "enter")
				if !h.m.showDetail {
					t.Fatal("the detail view did not open")
				}
				closed := plain(h.m)

				h.press(open)
				if !h.m.shortcuts.Open() {
					t.Fatalf("%s in the detail view should open the shortcuts", open)
				}
				view := plain(h.m)
				assertSameFrame(t, closed, view, 140)
				if got := lipgloss.Height(view); got != 40 {
					t.Fatalf("the screen is %d lines, want the terminal's 40:\n%s", got, view)
				}
				for _, want := range []string{"Shortcuts — svc", "Connection", "esc: close"} {
					if !strings.Contains(view, want) {
						t.Fatalf("the pop-up is missing %q:\n%s", want, view)
					}
				}

				h.press("esc")
				if h.m.shortcuts.Open() || !h.m.showDetail {
					t.Fatal("esc should close the pop-up and stay in the detail view")
				}
				if plain(h.m) != closed {
					t.Fatal("the view is not what it was before the pop-up opened")
				}
				h.press("esc")
				if h.m.showDetail {
					t.Fatal("a second esc should close the detail view")
				}
			})
		}
	}
}

func TestShortcutsAreCentred(t *testing.T) {
	h := newHarness(t, managerWith(fakeTokenSvc{newSvc(service.TypeRDS, "db")}), nil)
	h.press("right", "enter", "h")
	for _, size := range []tea.WindowSizeMsg{{Width: 140, Height: 40}, {Width: 121, Height: 35}, {Width: 100, Height: 24}, {Width: 80, Height: 20}} {
		h.send(size)
		view := plain(h.m)
		top, bottom, left, right := popupBox(t, view)
		if above, below := top, size.Height-1-bottom; above-below > 1 || below-above > 1 || above < 1 {
			t.Fatalf("at %dx%d the pop-up has %d rows above and %d below:\n%s", size.Width, size.Height, above, below, view)
		}
		if before, after := left, size.Width-1-right; before-after > 1 || after-before > 1 {
			t.Fatalf("at %dx%d the pop-up has %d columns left and %d right:\n%s", size.Width, size.Height, before, after, view)
		}
	}
}

// The root model acts on c, d and r, and the detail view on t, x and the
// rest, one keypress from a live connection. Over the pop-up none of them act.
func TestShortcutsTakeEveryKey(t *testing.T) {
	h, svc, starts := terminalHarness(t)
	h.press("h")
	if !strings.Contains(plain(h.m), "Terminal tabs") {
		t.Fatalf("the pop-up does not list this service's terminal keys:\n%s", plain(h.m))
	}

	h.press("t", "c", "d", "r", "x", "n", "1", "enter", "right", "backspace", "R")
	if !h.m.shortcuts.Open() || !h.m.showDetail {
		t.Fatal("only esc, h, ? and q close the pop-up")
	}
	if len(starts.cmds) != 0 {
		t.Fatal("t pressed over the pop-up started a client")
	}
	if n, stops, refreshes := svc.counts(); n != 1 || stops != 0 || refreshes != 0 {
		t.Fatalf("keys pressed over the pop-up reached the service: %d starts, %d stops, %d refreshes", n, stops, refreshes)
	}

	for _, key := range []string{"h", "?", "q", "esc"} {
		h.press(key)
		if h.m.shortcuts.Open() || h.quit || !h.m.showDetail {
			t.Fatalf("%s should close the pop-up and nothing else", key)
		}
		h.press("h")
	}
	h.press("esc", "t")
	if len(starts.cmds) != 1 {
		t.Fatal("with the pop-up closed, t should start a client again")
	}
}

// h and ? are text at a database prompt.
func TestShortcutKeysTypedIntoATerminalAreTheClients(t *testing.T) {
	h, _, starts := terminalHarness(t)
	h.press("t", "h", "?")
	if h.m.shortcuts.Open() {
		t.Fatal("h typed into a terminal opened the shortcuts")
	}
	if got, want := starts.terms[0].typed(), "h ?"; got != want {
		t.Fatalf("the client received %q, want %q", got, want)
	}
}

// t starts a client off the UI goroutine. A pop-up opened before it arrives
// would cover the terminal and take the keys typed into it.
func TestStartedTerminalClosesTheShortcuts(t *testing.T) {
	h, svc, _ := terminalHarness(t)
	h.press("h")
	term := &fakeTerminal{changed: make(chan struct{}, 1)}
	h.send(terminalStartedMsg{svc: svc, name: "psql", term: term})

	if h.m.shortcuts.Open() || !h.m.detailModel.CapturingKeys() {
		t.Fatal("a terminal that takes the keyboard should close the pop-up")
	}
	h.press("h")
	if term.typed() != "h" {
		t.Fatalf("the client received %q, want the h typed after it started", term.typed())
	}
}

// paneHarness is a den with one tunnel and one VPN, focused on pane id.
func paneHarness(t *testing.T, id paneID) (*harness, *fakeTunnel) {
	t.Helper()
	tun := &fakeTunnel{fakeSvc: newSvc(service.TypeTunnel, "bastion")}
	h := newHarness(t, managerWith(tun, newSvc(service.TypeVPN, "office")), nil)
	for h.m.currentPane() != id {
		h.press("down")
	}
	h.press("right")
	if h.m.leftFocused {
		t.Fatalf("the %s pane did not take the focus", id)
	}
	return h, tun
}

// The help of these panes used to be a box as tall as its text, config
// examples included: on anything but a very tall terminal the frame lost its
// top. The pop-up that replaced it fits whatever terminal it gets.
func TestPaneShortcutsFitTheTerminal(t *testing.T) {
	panes := map[paneID]string{
		paneConnect:  "SSM port-forward auto-reconnect on/off",
		paneTunnel:   "Reconnect on/off",
		paneVPN:      "Connect: log in through the browser",
		paneAWS:      "Edit the AWS credentials file",
		paneSecrets:  "Reveal or hide the selected value",
		paneRunbooks: "Cancel its run",
	}
	for id, text := range panes {
		for _, size := range []tea.WindowSizeMsg{{Width: 140, Height: 40}, {Width: 100, Height: 24}, {Width: 80, Height: 18}} {
			t.Run(id, func(t *testing.T) {
				h, _ := paneHarness(t, id)
				h.send(size)
				closed := plain(h.m)

				h.press("h")
				if !h.m.shortcuts.Open() {
					t.Fatalf("h in the %s pane should open its shortcuts", id)
				}
				view := plain(h.m)
				assertSameFrame(t, closed, view, size.Width)
				// A terminal shows the last lines of a frame; the pop-up has to
				// be within them, with a row of the pane above and below it.
				hidden := lipgloss.Height(view) - size.Height
				top, bottom, _, _ := popupBox(t, view)
				if top-hidden < 1 || bottom-hidden > size.Height-2 {
					t.Fatalf("at %dx%d the pop-up runs from row %d to %d of the terminal:\n%s",
						size.Width, size.Height, top-hidden, bottom-hidden, view)
				}
				title := "Shortcuts — " + paneMetadata[id].label
				if !strings.Contains(view, title) || !strings.Contains(view, "esc: close") {
					t.Fatalf("the pop-up should be titled %q and say how to close it:\n%s", title, view)
				}
				// On the short terminals the pop-up scrolls to the rest.
				for range 30 {
					if strings.Contains(plain(h.m), text) {
						break
					}
					h.press("down")
				}
				if !strings.Contains(plain(h.m), text) {
					t.Fatalf("the pop-up never shows %q:\n%s", text, plain(h.m))
				}

				h.press("esc")
				if h.m.shortcuts.Open() || h.m.leftFocused {
					t.Fatal("esc should close the pop-up and keep the pane focused")
				}
				if plain(h.m) != closed {
					t.Fatal("the pane is not what it was before the pop-up opened")
				}
				h.press("?")
				if !h.m.shortcuts.Open() {
					t.Fatalf("? in the %s pane should open its shortcuts too", id)
				}
			})
		}
	}
}

func TestPaneShortcutsTakeEveryKey(t *testing.T) {
	h, tun := paneHarness(t, paneTunnel)
	h.press("h")
	for _, want := range []string{"Start the selected tunnel", "Auto-connect on/off", "Reload den.yaml", "Back to the menu"} {
		if !strings.Contains(plain(h.m), want) {
			t.Fatalf("the Tunnel shortcuts are missing %q:\n%s", want, plain(h.m))
		}
	}

	h.press("c", "m", "r", "enter", "tab", "left")
	if starts, _, _ := tun.counts(); starts != 0 || tun.monitor || tun.reconnect {
		t.Fatalf("keys pressed over the pop-up reached the tunnel: %d starts, monitor %v, reconnect %v", starts, tun.monitor, tun.reconnect)
	}
	if !h.m.shortcuts.Open() || h.m.showDetail || h.m.leftFocused {
		t.Fatal("keys pressed over the pop-up moved the focus or opened the detail view")
	}

	h.press("esc", "c")
	if starts, _, _ := tun.counts(); starts != 1 {
		t.Fatal("with the pop-up closed, c should connect the tunnel")
	}
}

// q over a pop-up closes it; ctrl+c is the one key that still means what it
// means everywhere.
func TestCtrlCQuitsOverPaneShortcuts(t *testing.T) {
	h, _ := paneHarness(t, paneTunnel)
	h.press("h", "q")
	if h.quit || h.m.shortcuts.Open() {
		t.Fatal("q over the pop-up should close it, not quit den")
	}
	h.press("h", "ctrl+c")
	if !h.quit {
		t.Fatal("ctrl+c should quit den with the pop-up open")
	}
}

// While the filter is being typed, h is text, not the help.
func TestRunbooksShortcutsOnlyInTheList(t *testing.T) {
	h, _ := paneHarness(t, paneRunbooks)
	h.press("/", "h")
	if h.m.shortcuts.Open() {
		t.Fatal("h typed into the filter opened the shortcuts")
	}
	h.press("esc", "h")
	if !h.m.shortcuts.Open() {
		t.Fatal("back in the list, h should open the shortcuts")
	}
}

// Panes without shortcuts keep h as the way back to the menu.
func TestPanesWithoutShortcutsKeepH(t *testing.T) {
	for _, id := range []paneID{paneLogs} {
		h, _ := paneHarness(t, id)
		h.press("?")
		if h.m.shortcuts.Open() {
			t.Fatalf("? opened shortcuts the %s pane does not have", id)
		}
		h.press("h")
		if h.m.shortcuts.Open() || !h.m.leftFocused {
			t.Fatalf("h in the %s pane should return to the menu", id)
		}
	}
}

// In the Help pane h is the shortcuts pop-up like everywhere else, and ←
// leaves the pane only from the bare list: in a topic it goes back to the list.
func TestHelpPaneKeys(t *testing.T) {
	h, _ := paneHarness(t, paneHelp)
	h.press("h")
	if !h.m.shortcuts.Open() {
		t.Fatal("h in the Help pane should open its shortcuts")
	}
	h.press("esc", "enter")
	if h.m.helpPane.Reading() == "" {
		t.Fatal("enter should open the selected topic")
	}
	h.press("left")
	if h.m.leftFocused || h.m.helpPane.Reading() != "" {
		t.Fatal("← in a topic should go back to the list, not the menu")
	}
	h.press("left")
	if !h.m.leftFocused {
		t.Fatal("← in the list should return to the menu")
	}
}

// An open topic fits the terminal like the list does.
func TestHelpTopicFitsTheTerminal(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 102, Height: 30}, {Width: 160, Height: 62}} {
		h, _ := paneHarness(t, paneHelp)
		h.send(size)
		h.press("enter")
		rw, rh := size.Width-leftPaneWidth-1, size.Height-3
		if got := lipgloss.Height(h.m.pane(paneHelp).view(rw, rh, true)); got > rh {
			t.Errorf("%dx%d: an open topic is %d lines, its space is %d", size.Width, size.Height, got, rh)
		}
		if got := lipgloss.Height(plain(h.m)); got != size.Height {
			t.Errorf("%dx%d: with a topic open the frame is %d lines", size.Width, size.Height, got)
		}
	}
}
