package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/tui/testkeys"
	"github.com/lukaszgard/den/internal/update"
)

// These tests pin the TUI's key routing — which pane gets a key, which keys
// are global, which panes keep h/esc for themselves — and the service actions
// behind c/d/r/m/H. Regressions there are invisible until someone presses the
// key, so they are driven through AppModel.Update with real key messages.

// fakeSvc is a Service that records what the TUI asked of it.
type fakeSvc struct {
	info    service.ServiceInfo
	updates chan service.StatusUpdate

	mu                       sync.Mutex
	status                   service.Status
	starts, stops, refreshes int
}

func newSvc(typ, name string) *fakeSvc {
	return &fakeSvc{info: service.ServiceInfo{Name: name, Type: typ}, updates: make(chan service.StatusUpdate, 64)}
}

func (f *fakeSvc) Info() service.ServiceInfo { return f.info }
func (f *fakeSvc) State() service.RuntimeState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return service.RuntimeState{Status: f.status}
}
func (f *fakeSvc) Start(context.Context) (<-chan service.StatusUpdate, error) {
	f.mu.Lock()
	f.starts++
	f.status = service.StatusConnected
	f.mu.Unlock()
	return f.updates, nil
}
func (f *fakeSvc) Stop(context.Context) error {
	f.mu.Lock()
	f.stops++
	f.status = service.StatusIdle
	f.mu.Unlock()
	return nil
}
func (f *fakeSvc) RefreshToken(context.Context) error {
	f.mu.Lock()
	f.refreshes++
	f.mu.Unlock()
	return nil
}
func (f *fakeSvc) ConnectCommand() string { return "client " + f.info.Name }
func (f *fakeSvc) Logs() []string         { return nil }
func (f *fakeSvc) LogSeq() uint64         { return 0 }

func (f *fakeSvc) counts() (starts, stops, refreshes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.stops, f.refreshes
}

// fakeTokenSvc holds a refreshable credential.
type fakeTokenSvc struct{ *fakeSvc }

func (fakeTokenSvc) HoldsToken() bool { return true }

// fakeTunnel has the tunnel's runtime toggles.
type fakeTunnel struct {
	*fakeSvc
	reconnect, monitor, health bool
	target                     string
}

func (t *fakeTunnel) ToggleReconnect() bool     { t.reconnect = !t.reconnect; return t.reconnect }
func (t *fakeTunnel) ReconnectEnabled() bool    { return t.reconnect }
func (t *fakeTunnel) ToggleMonitor() bool       { t.monitor = !t.monitor; return t.monitor }
func (t *fakeTunnel) Monitoring() bool          { return t.monitor }
func (t *fakeTunnel) ToggleHealthCheck() bool   { t.health = !t.health; return t.health }
func (t *fakeTunnel) HealthCheckEnabled() bool  { return t.health }
func (t *fakeTunnel) HealthCheckTarget() string { return t.target }

// harness is an AppModel plus the side effects its commands produced.
type harness struct {
	t      testing.TB
	m      *AppModel
	quit   bool
	errors []error
}

func newHarness(t testing.TB, mgr *service.Manager, file *ConfigFile) *harness {
	t.Helper()
	// Point the AWS pane at an empty file so tests never read the developer's
	// real ~/.aws/config.
	awsCfg := filepath.Join(t.TempDir(), "aws-config")
	if err := os.WriteFile(awsCfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{AWS: &config.AWSSpec{ConfigPath: awsCfg, CredentialsPath: awsCfg}}
	h := &harness{t: t, m: New(mgr, Options{ConfigPath: "den.yaml", Config: cfg, Version: "test", ConfigFile: file})}
	// Every test ends as den does when it quits: the clients in the terminal
	// tabs exit and the manager closes, which releases the waiters send gave up
	// on. Otherwise they would stay blocked for good.
	t.Cleanup(func() { h.m.shutdown() })
	h.send(tea.WindowSizeMsg{Width: 140, Height: 40})
	return h
}

// plain is den's screen as text. Lip Gloss v2 always renders colour and style
// escapes, which these tests do not look at; the ones that do read
// View().Content.
func plain(m *AppModel) string { return ansi.Strip(m.View().Content) }

func (h *harness) press(keys ...string) {
	for _, k := range keys {
		h.send(testkeys.Press(k))
	}
}

// click clicks the cell at column x, row y of den's screen. A frame is drawn
// first, as one always is before a click can land on it.
func (h *harness) click(x, y int) {
	h.t.Helper()
	h.m.View()
	h.send(testkeys.Click(x, y))
}

// clickText clicks the first cell of text where den's screen shows it, which
// goes through the zones of the frame that was really drawn.
func (h *harness) clickText(text string) {
	h.t.Helper()
	x, y := screenPos(h.t, h.m, text)
	h.send(testkeys.Click(x, y))
}

// doubleClickText clicks text twice in a row.
func (h *harness) doubleClickText(text string) {
	h.t.Helper()
	h.clickText(text)
	h.clickText(text)
}

// wheel turns the wheel one notch, up or down, with the pointer on the cell
// at column x, row y.
func (h *harness) wheel(x, y int, up bool) {
	h.t.Helper()
	h.m.View()
	h.send(testkeys.Wheel(x, y, up))
}

// send delivers msg, then runs the commands it returned and feeds back the
// messages they produce. Commands that block — the tick and the service event
// waiter — are abandoned after a short wait; nothing here depends on them.
func (h *harness) send(msg tea.Msg) {
	h.t.Helper()
	queue := []tea.Msg{msg}
	for i := 0; len(queue) > 0; i++ {
		if i > 50 {
			h.t.Fatal("message loop did not settle")
		}
		next := queue[0]
		queue = queue[1:]
		model, cmd := h.m.Update(next)
		h.m = model.(*AppModel)
		for _, out := range runCmd(cmd) {
			switch out := out.(type) {
			case tea.QuitMsg:
				h.quit = true
			case msgs.ErrorMsg:
				h.errors = append(h.errors, out.Err)
				queue = append(queue, out)
			case msgs.TickMsg, msgs.ServiceEventMsg, msgs.UpdateCheckResultMsg:
				// Background traffic, not a consequence of the key under test.
			default:
				queue = append(queue, out)
			}
		}
	}
}

func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		if batch, ok := msg.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range batch {
				out = append(out, runCmd(c)...)
			}
			return out
		}
		if msg == nil {
			return nil
		}
		return []tea.Msg{msg}
	case <-time.After(150 * time.Millisecond):
		return nil
	}
}

func managerWith(svcs ...service.Service) *service.Manager {
	mgr := service.NewManager()
	for _, s := range svcs {
		mgr.Register(s)
	}
	return mgr
}

func TestFocusAndMenuNavigation(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "db")), nil)
	if !h.m.leftFocused || h.m.currentPane() != paneConnect {
		t.Fatal("den should start on the Connect pane with the menu focused")
	}
	h.press("tab")
	if h.m.leftFocused {
		t.Fatal("tab should focus the right pane")
	}
	h.press("tab", "down", "down")
	if h.m.currentPane() != paneRunbooks {
		t.Fatalf("pane = %s, want runbooks", h.m.currentPane())
	}
	h.press("right")
	if h.m.leftFocused {
		t.Fatal("→ should focus the right pane")
	}
	h.press("esc")
	if !h.m.leftFocused {
		t.Fatal("esc should return to the menu")
	}
	h.press("?")
	if h.m.currentPane() != paneHelp || h.m.leftFocused {
		t.Fatal("? should open Help and focus it")
	}
	h.press("left", "L")
	if h.m.currentPane() != paneLogs || h.m.leftFocused {
		t.Fatal("L should open Logs and focus it")
	}
}

func TestQuitIsNotTypedIntoSearch(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "db")), nil)
	h.press("right", "/", "q")
	if h.quit {
		t.Fatal("q typed into the Connect filter must not quit")
	}
	if !h.m.connectPane.Searching() {
		t.Fatal("the filter should still be open")
	}
	h.press("ctrl+c")
	if !h.quit {
		t.Fatal("ctrl+c must quit even while filtering")
	}

	h = newHarness(t, managerWith(), nil)
	h.press("q")
	if !h.quit {
		t.Fatal("q from the menu should quit")
	}
}

func TestPanesThatOwnHAndEsc(t *testing.T) {
	tun := &fakeTunnel{fakeSvc: newSvc(service.TypeTunnel, "bastion")}
	h := newHarness(t, managerWith(tun), nil)

	// Connect keeps h for its shortcuts; esc closes them, a second esc leaves.
	h.press("right", "h")
	if h.m.leftFocused || !h.m.shortcuts.Open() {
		t.Fatal("h in Connect should open its shortcuts and keep focus")
	}
	h.press("esc", "esc")
	if !h.m.leftFocused || h.m.shortcuts.Open() {
		t.Fatal("esc should close the Connect shortcuts, and a second esc return to the menu")
	}

	// Tunnel keeps h for its shortcuts; esc closes them, a second esc leaves.
	h.press("down", "right", "h")
	if h.m.leftFocused || !h.m.shortcuts.Open() {
		t.Fatal("h in Tunnel should open its shortcuts and keep focus")
	}
	h.press("esc")
	if h.m.leftFocused || h.m.shortcuts.Open() {
		t.Fatal("esc should close the Tunnel shortcuts first")
	}
	h.press("esc")
	if !h.m.leftFocused {
		t.Fatal("a second esc should return to the menu")
	}

	// Updates keeps h for its help as well.
	for h.m.currentPane() != paneUpdates {
		h.press("down")
	}
	h.press("right", "h")
	if h.m.leftFocused || !h.m.updatesPane.ShowingHelp() {
		t.Fatal("h in Updates should open its help and keep focus")
	}
	h.press("esc")
	if h.m.leftFocused || h.m.updatesPane.ShowingHelp() {
		t.Fatal("esc should close the Updates help first")
	}
}

// The help used to be rendered once, at the 80-column placeholder den starts
// with, and kept that width. A topic now shares the changelog's box, which
// opens without the async render it used to wait for.
func TestHelpAndChangelogFillThePane(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "db")), nil)
	h.send(tea.WindowSizeMsg{Width: 200, Height: 50})
	fits := func(view string) {
		t.Helper()
		if rows := strings.Count(view, "\n") + 1; rows > 50 {
			t.Fatalf("view is %d rows, taller than the 50-row terminal", rows)
		}
	}

	h.press("?")
	fits(plain(h.m))
	h.press("/", "private subnet, reached through a tunnel", "enter")
	view := plain(h.m)
	fits(view)
	if !strings.Contains(view, "╭") || !strings.Contains(view, "RDS and Aurora") {
		t.Fatalf("a help topic should be in the titled box:\n%s", view)
	}
	// At the old 53-column wrap this sentence broke over two lines.
	if !strings.Contains(view, "An RDS or Aurora database in a private subnet, reached through a tunnel and logged in to with an IAM auth token") {
		t.Fatalf("help should wrap at the terminal width, not the startup one:\n%s", view)
	}

	// The topic's search, the topic, the list's search, then the pane.
	h.press("esc", "esc", "esc", "esc")
	if !h.m.leftFocused {
		t.Fatal("four esc should return to the menu")
	}
	for h.m.currentPane() != paneUpdates {
		h.press("down")
	}
	h.press("right", "s")
	if !h.m.updatesPane.ShowingChangelog() {
		t.Fatal("s should open the changelog")
	}
	view = plain(h.m)
	fits(view)
	if !strings.Contains(view, "Changelog") || !strings.Contains(view, "esc/s: close") {
		t.Fatalf("changelog should be in the titled box:\n%s", view)
	}
	h.press("esc")
	if h.m.leftFocused || h.m.updatesPane.ShowingChangelog() {
		t.Fatal("esc should close the changelog and keep focus on Updates")
	}
}

// / searches the help and the changelog. While a search is typed, q, h and R
// are letters of it rather than quit, back and reload; esc undoes one step at
// a time: the topic's search, the topic, the list's search, then the pane.
func TestHelpSearchTakesTheKeys(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "db")), nil)
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	h.press("?", "/", "q", "h", "R")
	if h.quit || h.m.leftFocused || !h.m.helpPane.Searching() || h.m.shortcuts.Open() {
		t.Fatal("q, h and R typed into the help search should be text")
	}
	h.press("backspace", "backspace", "backspace", "pgpass", "enter")
	if h.m.helpPane.Reading() == "" {
		t.Fatal("enter in the search should open the topic it found")
	}
	if view := plain(h.m); !strings.Contains(view, "n N: next/previous") {
		t.Fatalf("the topic should open at the first match of pgpass:\n%s", view)
	}
	h.press("esc")
	if h.m.leftFocused || h.m.helpPane.Reading() == "" {
		t.Fatal("the first esc should clear the topic's search and keep it open")
	}
	h.press("esc")
	if h.m.leftFocused || h.m.helpPane.Reading() != "" || !strings.Contains(plain(h.m), "/pgpass") {
		t.Fatal("the second esc should go back to the list, still searching for pgpass")
	}
	h.press("esc")
	if h.m.leftFocused || h.m.helpPane.Busy() {
		t.Fatal("the third esc should clear the list's search and stay in the help")
	}
	h.press("esc")
	if !h.m.leftFocused {
		t.Fatal("the fourth esc should return to the menu")
	}

	for h.m.currentPane() != paneUpdates {
		h.press("down")
	}
	h.press("right", "s", "/", "q", "s")
	if h.quit || !h.m.updatesPane.ShowingChangelog() {
		t.Fatal("q and s typed into the changelog search should be text")
	}
	h.press("backspace", "backspace", "den", "enter", "esc")
	if !h.m.updatesPane.ShowingChangelog() {
		t.Fatal("esc should clear the changelog search before closing it")
	}
	h.press("esc")
	if h.m.updatesPane.ShowingChangelog() || h.m.leftFocused {
		t.Fatal("the next esc should close the changelog and keep focus on Updates")
	}
}

// A release's notes used to be printed into the Updates pane. They are a whole
// CHANGELOG section, so the frame came out taller than the terminal and Bubble
// Tea dropped its top rows: the header and the menu were gone.
func TestLongReleaseNotesKeepTheFrameOnScreen(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "db")), nil)
	notes := "What this release is about.\n\n" + strings.Repeat("- one more thing that changed\n", 200)
	h.send(msgs.UpdateCheckResultMsg{Latest: "v9.9.9", Notes: notes})
	for h.m.currentPane() != paneUpdates {
		h.press("down")
	}

	onScreen := func(view string) {
		t.Helper()
		lines := strings.Split(view, "\n")
		if len(lines) > 40 {
			t.Fatalf("frame is %d rows, taller than the 40-row terminal", len(lines))
		}
		if !strings.Contains(lines[0], "Developer Environment Navigator") || !strings.Contains(view, "Connect") {
			t.Fatalf("the header and the menu should still be on screen:\n%s", view)
		}
	}

	view := plain(h.m)
	onScreen(view)
	if !strings.Contains(view, "v9.9.9 available") || strings.Contains(view, "one more thing") {
		t.Fatalf("the pane should announce the release without printing its notes:\n%s", view)
	}

	h.press("right", "s")
	view = plain(h.m)
	onScreen(view)
	if !strings.Contains(view, "v9.9.9 — new, not installed yet") || !strings.Contains(view, "What this release is about.") {
		t.Fatalf("s should open the notes in the changelog box:\n%s", view)
	}
}

func TestConnectPaneServiceActions(t *testing.T) {
	plain := newSvc(service.TypeRDS, "plain")
	tok := fakeTokenSvc{newSvc(service.TypeRDS, "token")}
	h := newHarness(t, managerWith(plain, tok), nil)

	h.press("right", "c")
	if starts, _, _ := plain.counts(); starts != 1 {
		t.Fatalf("c started the service %d times, want 1", starts)
	}
	h.press("c")
	if starts, _, _ := plain.counts(); starts != 1 || !strings.Contains(h.m.flashMsg, "already running") {
		t.Fatalf("a second c must warn, not start again (starts=%d, flash=%q)", starts, h.m.flashMsg)
	}
	h.press("r")
	if _, _, refreshes := plain.counts(); refreshes != 0 {
		t.Fatal("r must not refresh a service without a credential")
	}
	h.press("d")
	if _, stops, _ := plain.counts(); stops != 1 {
		t.Fatalf("d stopped the service %d times, want 1", stops)
	}

	h.press("down", "r")
	if _, _, refreshes := tok.counts(); refreshes != 1 {
		t.Fatalf("r refreshed the token %d times, want 1", refreshes)
	}
}

// a on the Connect pane is the toggle the detail view calls auto-reconnect; a
// flash saying only "reconnect" read as if it stopped the token refresh too.
func TestConnectPaneAutoReconnect(t *testing.T) {
	db := &fakeTunnel{fakeSvc: newSvc(service.TypeRDS, "db")}
	h := newHarness(t, managerWith(db), nil)

	h.press("right", "a")
	if !db.reconnect || h.m.flashMsg != "auto-reconnect enabled" {
		t.Fatalf("a: reconnect=%v flash=%q, want it on and named auto-reconnect", db.reconnect, h.m.flashMsg)
	}
	h.press("a")
	if db.reconnect || h.m.flashMsg != "auto-reconnect disabled" {
		t.Fatalf("a again: reconnect=%v flash=%q, want it off", db.reconnect, h.m.flashMsg)
	}
}

func TestTunnelToggles(t *testing.T) {
	tun := &fakeTunnel{fakeSvc: newSvc(service.TypeTunnel, "bastion")}
	h := newHarness(t, managerWith(tun), nil)
	h.press("down", "right")

	h.press("m")
	if !tun.monitor {
		t.Fatal("m should switch monitoring on")
	}
	h.press("r")
	if !tun.reconnect {
		t.Fatal("r should switch reconnect on")
	}
	h.press("H")
	if tun.health || !strings.Contains(h.m.flashMsg, "no health_check configured") {
		t.Fatalf("H without a target must say so (health=%v, flash=%q)", tun.health, h.m.flashMsg)
	}
	tun.target = "intranet:443"
	h.press("H")
	if !tun.health {
		t.Fatal("H should switch the health check on when a target exists")
	}
	h.press("c")
	if starts, _, _ := tun.counts(); starts != 1 {
		t.Fatal("c should connect the tunnel")
	}
}

func TestDetailOverlay(t *testing.T) {
	svc := newSvc(service.TypeRDS, "db")
	h := newHarness(t, managerWith(svc), nil)
	h.press("right", "enter")
	if !h.m.showDetail || h.m.detailModel.Service() != svc {
		t.Fatal("enter should open the detail view of the selected service")
	}
	h.press("c")
	if starts, _, _ := svc.counts(); starts != 1 {
		t.Fatal("c in the detail view should connect")
	}
	h.press("esc")
	if h.m.showDetail {
		t.Fatal("esc should close the detail view")
	}
}

// Bubble Tea drops the top lines of a frame taller than the terminal, so a
// detail view that ignored the status bar under it lost its top border.
func TestDetailOverlayFitsTheTerminal(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "db")), nil)
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.press("right", "enter")
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 30}, {Width: 100, Height: 25}} {
		h.send(size)
		view := plain(h.m)
		if got := lipgloss.Height(view); got != size.Height {
			t.Fatalf("detail view at %dx%d is %d lines:\n%s", size.Width, size.Height, got, view)
		}
		if lines := strings.Split(view, "\n"); !strings.Contains(lines[0], "╭") {
			t.Fatalf("line 0 = %q, want the box's top border", lines[0])
		}
	}
}

func TestReloadKeepsUnchangedRunningConnection(t *testing.T) {
	ctx := context.Background()
	mgr := service.NewManager()
	svc := newSvc(service.TypeRDS, "db")
	entries := []service.Entry{{Key: "rds:db", Spec: "same", Build: func() (service.Service, error) { return svc, nil }}}
	mgr.Reconcile(ctx, entries)
	file := reloadTo(mgr, func() []service.Entry { return entries })
	reloads := 0
	apply := file.Apply
	file.Apply = func(ctx context.Context, cfg *config.Config) service.ReconcileResult {
		reloads++
		return apply(ctx, cfg)
	}
	h := newHarness(t, mgr, file)

	h.press("right", "c", "R")
	if reloads != 1 {
		t.Fatalf("R reloaded %d times, want 1", reloads)
	}
	if _, stops, _ := svc.counts(); stops != 0 {
		t.Fatal("reloading an unchanged config must not stop its running connection")
	}
	if !strings.Contains(h.m.flashMsg, "config reloaded — 1 services, 1 connection kept") {
		t.Fatalf("flash = %q", h.m.flashMsg)
	}
}

// Reload used to rebuild the Connect pane, so the cursor jumped back to the
// first row and enter opened the wrong service.
func TestReloadKeepsConnectCursorOnItsService(t *testing.T) {
	ctx := context.Background()
	mgr := service.NewManager()
	entry := func(name string) service.Entry {
		svc := newSvc(service.TypeRDS, name)
		return service.Entry{Key: "rds:" + name, Spec: name, Build: func() (service.Service, error) { return svc, nil }}
	}
	one, two, three := entry("one"), entry("two"), entry("three")
	mgr.Reconcile(ctx, []service.Entry{one, two, three})
	// "one" is removed, so "three" moves up a row.
	h := newHarness(t, mgr, reloadTo(mgr, func() []service.Entry { return []service.Entry{two, three} }))

	h.press("right", "down", "down", "R")
	if svc, ok := h.m.selectedService(); !ok || svc.Info().Name != "three" {
		t.Fatalf("selected after reload = %v, want three", svc)
	}
}

func TestReloadWithoutConfigAndFailedReload(t *testing.T) {
	h := newHarness(t, managerWith(), nil)
	h.press("right", "R")
	if !strings.Contains(h.m.flashMsg, "no config file to reload") {
		t.Fatalf("flash = %q", h.m.flashMsg)
	}

	failing := &ConfigFile{Check: func() (*config.Config, config.Problems, error) {
		return nil, nil, errors.New("read config den.yaml: permission denied")
	}}
	h = newHarness(t, managerWith(), failing)
	h.press("right", "R")
	if h.m.cfgPrompt == nil || !strings.Contains(plain(h.m), "permission denied") {
		t.Fatalf("a file den cannot read must say why:\n%s", plain(h.m))
	}
	h.press("esc")
	if got := h.m.renderStatusBar(); !strings.Contains(got, "den.yaml has 1 error, den runs the last good version") {
		t.Fatalf("status bar = %q", got)
	}
}

// reloadTo is a ConfigFile whose den.yaml always checks clean and applies as
// the entries next returns.
func reloadTo(mgr *service.Manager, next func() []service.Entry) *ConfigFile {
	return &ConfigFile{
		Check:     func() (*config.Config, config.Problems, error) { return &config.Config{}, nil, nil },
		WouldStop: func(*config.Config) []string { return mgr.WouldStop(next()) },
		Apply: func(ctx context.Context, _ *config.Config) service.ReconcileResult {
			return mgr.Reconcile(ctx, next())
		},
	}
}

// Resizing used to rebuild most panes from scratch: the Connect cursor jumped
// back to the first row and its filter was lost.
func TestResizeKeepsConnectCursorAndFilter(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "one"), newSvc(service.TypeRDS, "two"), newSvc(service.TypeRDS, "three")), nil)
	h.press("right", "down", "down")
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	if svc, ok := h.m.selectedService(); !ok || svc.Info().Name != "three" {
		t.Fatalf("selected after resize = %v, want three", svc)
	}
	h.press("/", "t", "w")
	h.send(tea.WindowSizeMsg{Width: 120, Height: 35})
	if !h.m.connectPane.Searching() {
		t.Fatal("the filter should survive a resize")
	}
	if svc, ok := h.m.selectedService(); !ok || svc.Info().Name != "two" {
		t.Fatalf("selected in filter after resize = %v, want two", svc)
	}
}

// fakeBound is a tunnel that runs over a VPN (auto_connect).
type fakeBound struct {
	*fakeSvc
	vpn  string
	auto bool
}

func (f *fakeBound) BoundVPN() string        { return f.vpn }
func (f *fakeBound) AutoConnect() bool       { return f.auto }
func (f *fakeBound) ToggleAutoConnect() bool { f.auto = !f.auto; return f.auto }

// vpnNow sets the fake VPN's status and returns the event it would send.
func vpnNow(vpn *fakeSvc, status service.Status) msgs.ServiceEventMsg {
	vpn.mu.Lock()
	vpn.status = status
	vpn.mu.Unlock()
	return msgs.ServiceEventMsg{Update: service.StatusUpdate{Info: vpn.info, State: service.RuntimeState{Status: status}}}
}

func TestTunnelFollowsItsVPN(t *testing.T) {
	vpn := newSvc(service.TypeVPN, "Office VPN")
	jump := &fakeBound{fakeSvc: newSvc(service.TypeTunnel, "jump"), vpn: "Office VPN", auto: true}
	lab := &fakeBound{fakeSvc: newSvc(service.TypeTunnel, "lab"), vpn: "Lab VPN", auto: true}
	h := newHarness(t, managerWith(vpn, jump, lab), nil)
	counts := func(f *fakeBound) (int, int) { s, st, _ := f.counts(); return s, st }

	h.send(vpnNow(vpn, service.StatusConnecting))
	if s, _ := counts(jump); s != 0 {
		t.Fatal("a VPN that is still connecting must not start the tunnel")
	}
	h.send(vpnNow(vpn, service.StatusConnected))
	if s, _ := counts(jump); s != 1 || !strings.Contains(h.m.flashMsg, "auto-connect: jump (Office VPN is up)") {
		t.Fatalf("VPN up: starts=%d flash=%q", s, h.m.flashMsg)
	}
	if s, _ := counts(lab); s != 0 {
		t.Fatal("a tunnel bound to another VPN must be left alone")
	}
	h.send(vpnNow(vpn, service.StatusConnected))
	if s, _ := counts(jump); s != 1 {
		t.Fatal("a repeated connected event must not start the tunnel again")
	}

	// A drop stops the tunnel; the VPN's own reconnect brings it back.
	h.send(vpnNow(vpn, service.StatusReconnecting))
	if _, st := counts(jump); st != 1 {
		t.Fatalf("VPN down: stops=%d, want 1", st)
	}
	h.send(vpnNow(vpn, service.StatusConnected))
	if s, _ := counts(jump); s != 2 {
		t.Fatalf("VPN back: starts=%d, want 2", s)
	}

	// d on the tunnel is respected while the VPN stays up.
	h.press("down", "right", "d")
	h.send(vpnNow(vpn, service.StatusConnected))
	if s, st := counts(jump); s != 2 || st != 2 {
		t.Fatalf("after d: starts=%d stops=%d, want 2 and 2", s, st)
	}
	if !strings.Contains(plain(h.m), "Auto-connect") {
		t.Error("the status box should show the auto-connect toggle")
	}

	// v switches it off: the VPN no longer moves the tunnel.
	h.press("v")
	if jump.auto || !strings.Contains(h.m.flashMsg, "auto-connect with Office VPN disabled") {
		t.Fatalf("v: auto=%v flash=%q", jump.auto, h.m.flashMsg)
	}
	h.send(vpnNow(vpn, service.StatusIdle))
	h.send(vpnNow(vpn, service.StatusConnected))
	if s, _ := counts(jump); s != 2 {
		t.Fatalf("auto-connect off: starts=%d, want still 2", s)
	}
}

func TestConnectWithoutItsVPNWarns(t *testing.T) {
	vpn := newSvc(service.TypeVPN, "Office VPN")
	// The warning does not depend on auto-connect: the link alone says the
	// tunnel needs that VPN.
	jump := &fakeBound{fakeSvc: newSvc(service.TypeTunnel, "jump"), vpn: "Office VPN"}
	h := newHarness(t, managerWith(vpn, jump), nil)

	h.press("down", "right", "c")
	if s, _, _ := jump.counts(); s != 1 {
		t.Fatal("c must connect even without the VPN: the user asked")
	}
	if !strings.Contains(h.m.flashMsg, "Office VPN is not connected — jump may not reach its host") {
		t.Fatalf("flash = %q", h.m.flashMsg)
	}

	h.press("d")
	vpnNow(vpn, service.StatusConnected)
	h.press("c")
	if h.m.flashMsg != "connecting..." {
		t.Fatalf("with the VPN up: flash = %q", h.m.flashMsg)
	}
}

// A VPN left up by a previous session is adopted without a status event;
// its tunnel must still come up.
func TestAdoptedVPNStartsItsTunnel(t *testing.T) {
	vpn := newSvc(service.TypeVPN, "Office VPN")
	vpn.status = service.StatusConnected
	jump := &fakeBound{fakeSvc: newSvc(service.TypeTunnel, "jump"), vpn: "Office VPN", auto: true}
	mgr := managerWith(vpn, jump)
	h := newHarness(t, mgr, nil)

	h.send(reconcileRunningVPNs(context.Background(), mgr)())
	if s, _, _ := jump.counts(); s != 1 {
		t.Fatalf("tunnel starts = %d, want 1", s)
	}
	if s, _, _ := vpn.counts(); s != 1 {
		t.Fatalf("the VPN itself should be adopted (started) once, got %d", s)
	}
}

// installNow delivers a finished update check and install for v9.9.9.
func (h *harness) installNow() {
	h.t.Helper()
	if !update.CanReexec {
		h.t.Skip("restart is not offered on this platform")
	}
	h.send(msgs.UpdateCheckResultMsg{Latest: "v9.9.9"})
	h.send(msgs.UpdateInstallResultMsg{})
}

// A restart is a quit: it must go through the same shutdown, and only after
// the user said y in the Updates pane.
func TestRestartAfterInstall(t *testing.T) {
	db, sso := newSvc(service.TypeRDS, "db"), newSvc(service.TypeAWS, "sso")
	for _, s := range []*fakeSvc{db, sso} {
		if _, err := s.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	h := newHarness(t, managerWith(db, sso), nil)
	for h.m.currentPane() != paneUpdates {
		h.press("down")
	}
	h.press("right")
	h.installNow()

	if !h.m.updatesPane.ShowingRestartPrompt() {
		t.Fatal("the install should open the restart prompt in Updates")
	}
	view := plain(h.m)
	if !strings.Contains(view, "Restarting closes 1 connection:") || !strings.Contains(view, "rds  db") {
		t.Fatalf("the prompt should list the open connection:\n%s", view)
	}
	// The SSO token lives in the aws CLI cache and survives the restart.
	if strings.Contains(view, "aws  sso") {
		t.Fatalf("an SSO session is not closed by a restart:\n%s", view)
	}

	h.press("esc")
	if h.m.leftFocused || h.m.updatesPane.ShowingRestartPrompt() || h.quit {
		t.Fatal("esc should answer 'later' and keep focus on Updates")
	}

	h.press("r", "y")
	if !h.quit || !h.m.RestartRequested() {
		t.Fatal("y should quit with a restart requested")
	}
	if _, stops, _ := db.counts(); stops != 1 {
		t.Fatalf("a restart should stop the running connection, got %d stops", stops)
	}
}

// The install can finish while the user types somewhere else. The prompt
// waits in Updates; elsewhere only a flash says so, and a y typed into a
// filter stays text.
func TestInstallElsewhereOnlyFlashes(t *testing.T) {
	db := newSvc(service.TypeRDS, "db")
	h := newHarness(t, managerWith(db), nil)
	h.press("right", "/")
	h.installNow()

	if !strings.Contains(h.m.flashMsg, "v9.9.9 installed — open Updates to restart") {
		t.Fatalf("flash = %q, want a pointer to the Updates pane", h.m.flashMsg)
	}
	h.press("y")
	if h.quit || h.m.RestartRequested() {
		t.Fatal("a y typed into the Connect filter must not restart")
	}
	if !h.m.connectPane.Searching() || !h.m.updatesPane.ShowingRestartPrompt() {
		t.Fatal("the filter should keep the key and the prompt should wait in Updates")
	}
	if _, stops, _ := db.counts(); stops != 0 {
		t.Fatal("nothing should have been stopped")
	}
}

// secretsHarness is a den with one Secrets Manager secret read through an SSO
// profile that has no login yet, and a function that logs in the way
// `aws sso login` does: by writing the token to the SSO cache.
func secretsHarness(t *testing.T) (h *harness, login func()) {
	t.Helper()
	dir := t.TempDir()
	awsCfg := filepath.Join(dir, "config")
	body := "[sso-session corp]\nsso_start_url = https://corp/start\n\n[profile dev]\nsso_session = corp\n"
	if err := os.WriteFile(awsCfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "sso", "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AWS: &config.AWSSpec{ConfigPath: awsCfg, CredentialsPath: awsCfg},
		Secrets: &config.SecretsConfig{AWSSecretsManager: []config.AWSSecretsManagerSecretSpec{
			{Name: "m2m key", SecretID: "/acme/m2m", AWSProfile: "dev"},
		}},
	}
	h = &harness{t: t, m: New(managerWith(), Options{ConfigPath: "den.yaml", Config: cfg, Version: "test"})}
	t.Cleanup(func() { h.m.shutdown() })
	h.send(tea.WindowSizeMsg{Width: 140, Height: 40})
	return h, func() {
		t.Helper()
		tok := `{"startUrl":"https://corp/start","accessToken":"x","expiresAt":"` +
			time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}`
		if err := os.WriteFile(filepath.Join(cache, "corp.json"), []byte(tok), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func (h *harness) secretsView() string { return h.m.secretsPane.View(100, 30, false) }

// den started before the SSO login: the secret says so. Logging in from the
// AWS pane must turn it ready without a reload or a restart.
func TestSecretsTurnReadyAfterSSOLogin(t *testing.T) {
	h, login := secretsHarness(t)
	const expired = "SSO login expired — log in from the AWS pane"
	if !strings.Contains(h.secretsView(), expired) {
		t.Fatalf("without a login the secret must say so:\n%s", h.secretsView())
	}

	aws := service.StatusUpdate{
		Info:  service.ServiceInfo{Name: "corp", Type: service.TypeAWS},
		State: service.RuntimeState{Status: service.StatusConnected},
	}
	// An AWS event that changed nothing (the login just started) changes nothing.
	h.send(msgs.ServiceEventMsg{Update: aws})
	if !strings.Contains(h.secretsView(), expired) {
		t.Fatal("still not logged in: the status must stay")
	}

	login()
	// Another service's event is no reason to look at the logins.
	h.send(msgs.ServiceEventMsg{Update: service.StatusUpdate{Info: service.ServiceInfo{Name: "db", Type: service.TypeRDS}}})
	if !strings.Contains(h.secretsView(), expired) {
		t.Fatal("only an AWS session event re-checks the secrets")
	}
	h.send(msgs.ServiceEventMsg{Update: aws})
	if v := h.secretsView(); strings.Contains(v, expired) || !strings.Contains(v, "ready") {
		t.Fatalf("after the login the secret must be ready:\n%s", v)
	}
}

// A login made in another terminal raises no event. The Secrets pane notices
// it while it is on screen, and leaves the logins alone while it is not.
func TestSecretsNoticeALoginMadeOutsideDen(t *testing.T) {
	h, login := secretsHarness(t)
	const expired = "SSO login expired — log in from the AWS pane"
	login()

	h.send(msgs.TickMsg{Time: time.Now()})
	if !strings.Contains(h.secretsView(), expired) {
		t.Fatal("the Connect pane is on screen: the secrets must not be polled")
	}

	for h.m.currentPane() != paneSecrets {
		h.press("down")
	}
	h.send(msgs.TickMsg{Time: time.Now()})
	if v := h.secretsView(); strings.Contains(v, expired) || !strings.Contains(v, "ready") {
		t.Fatalf("with Secrets on screen the login must show up:\n%s", v)
	}
}

// ── terminal tabs ────────────────────────────────────────────────────────────

// fakeTermSvc is a service whose client den can run in a terminal tab.
type fakeTermSvc struct{ *fakeSvc }

func (fakeTermSvc) HasTerminal() bool { return true }
func (f fakeTermSvc) TerminalCommand() (service.ClientCommand, error) {
	if f.State().Status != service.StatusConnected {
		return service.ClientCommand{}, errors.New(f.info.Name + " is not connected")
	}
	return service.ClientCommand{Tool: "psql", Args: []string{"host=localhost port=50111"}, Env: []string{"PGPASSWORD=tok-1"}}, nil
}

// fakeTerminal is a terminal without a process. Like termsession's, changed
// holds one signal, and closing it signals once more: a client that exits
// wakes its waiter.
type fakeTerminal struct {
	mu         sync.Mutex
	keys       []string
	pasted     []string
	dead       bool
	closes     int
	cols, rows int
	changed    chan struct{}
	exitErr    error
	cursor     *tea.Cursor
}

func (f *fakeTerminal) View() string { return "fake client screen" }
func (f *fakeTerminal) Cursor() *tea.Cursor {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cursor
}
func (f *fakeTerminal) Resize(cols, rows int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cols, f.rows = cols, rows
}
func (f *fakeTerminal) HandleKey(msg tea.KeyPressMsg) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, msg.String())
}
func (f *fakeTerminal) Paste(text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pasted = append(f.pasted, text)
}
func (f *fakeTerminal) Alive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.dead
}
func (f *fakeTerminal) FullScreen() bool { return false }
func (f *fakeTerminal) ExitErr() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exitErr
}

// exit ends the program with err, as one that failed does.
func (f *fakeTerminal) exit(err error) {
	f.mu.Lock()
	f.exitErr = err
	f.mu.Unlock()
	f.Close()
}
func (f *fakeTerminal) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	f.dead = true
	select {
	case f.changed <- struct{}{}:
	default:
	}
}
func (f *fakeTerminal) Changed() <-chan struct{} { return f.changed }
func (f *fakeTerminal) typed() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.keys, " ")
}
func (f *fakeTerminal) closed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes
}

// termStarts records what the root model asked to be started.
type termStarts struct {
	cmds  []service.ClientCommand
	terms []*fakeTerminal
	sizes [][2]int
	err   error
}

func (ts *termStarts) start(cmd service.ClientCommand, cols, rows int) (liveTerminal, error) {
	if ts.err != nil {
		return nil, ts.err
	}
	term := &fakeTerminal{changed: make(chan struct{}, 1)}
	ts.cmds = append(ts.cmds, cmd)
	ts.terms = append(ts.terms, term)
	ts.sizes = append(ts.sizes, [2]int{cols, rows})
	return term, nil
}

// terminalHarness is a den over one connected service with a terminal, its
// detail view open.
func terminalHarness(t testing.TB) (*harness, fakeTermSvc, *termStarts) {
	t.Helper()
	svc := fakeTermSvc{newSvc(service.TypeRDS, "db")}
	if _, err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, managerWith(svc), nil)
	starts := &termStarts{}
	h.m.startTerminal = starts.start
	h.press("right", "enter")
	if !h.m.showDetail {
		t.Fatal("the detail view did not open")
	}
	return h, svc, starts
}

func TestTOpensAClientLoggedIn(t *testing.T) {
	h, _, starts := terminalHarness(t)
	h.press("t")

	if len(starts.cmds) != 1 {
		t.Fatalf("t started %d clients, want 1", len(starts.cmds))
	}
	if cmd := starts.cmds[0]; cmd.Tool != "psql" || len(cmd.Env) != 1 || cmd.Env[0] != "PGPASSWORD=tok-1" {
		t.Fatalf("the client was started as %+v, want the service's command with its credential", cmd)
	}
	cols, rows := h.m.detailModel.TerminalSize()
	if got := starts.sizes[0]; got != [2]int{cols, rows} {
		t.Fatalf("the client started at %v, want the size of the area it is shown in, %dx%d", got, cols, rows)
	}
	if !h.m.detailModel.CapturingKeys() {
		t.Fatal("a freshly opened terminal should have the keyboard")
	}
	if view := plain(h.m); !strings.Contains(view, "1 psql") || !strings.Contains(view, "fake client screen") {
		t.Fatalf("the terminal tab is not on screen:\n%s", view)
	}
}

// A terminal that reports keys unambiguously (the kitty protocol) says so once
// at the start. Where it does, alt+esc is a key the user pressed and the
// client gets it; where it does not, it is two escs that arrived together, and
// the second is den's.
func TestKeyboardEnhancementsDecideWhatAltEscMeans(t *testing.T) {
	altEsc := testkeys.Press("alt+esc")
	reported := tea.KeyboardEnhancementsMsg{Flags: 1}

	t.Run("not reported", func(t *testing.T) {
		h, _, starts := terminalHarness(t)
		h.press("t")
		h.send(altEsc)
		if h.m.detailModel.CapturingKeys() || starts.terms[0].typed() != "esc" {
			t.Fatalf("merged escs should reach the client once and then leave (client got %q)", starts.terms[0].typed())
		}
	})
	t.Run("reported after the tabs exist", func(t *testing.T) {
		h, _, starts := terminalHarness(t)
		h.press("t")
		h.send(reported)
		h.send(altEsc)
		if !h.m.detailModel.CapturingKeys() || starts.terms[0].typed() != "alt+esc" {
			t.Fatalf("alt+esc should go to the client and keep the keyboard on it (client got %q)", starts.terms[0].typed())
		}
	})
	t.Run("reported before the tabs exist", func(t *testing.T) {
		h, _, starts := terminalHarness(t)
		h.send(reported)
		h.press("t")
		h.send(altEsc)
		if !h.m.detailModel.CapturingKeys() || starts.terms[0].typed() != "alt+esc" {
			t.Fatalf("tabs made after the report should know it (client got %q)", starts.terms[0].typed())
		}
	})
	t.Run("reported without disambiguation", func(t *testing.T) {
		h, _, _ := terminalHarness(t)
		h.send(reported)
		h.send(tea.KeyboardEnhancementsMsg{})
		for _, tabs := range h.m.terminals {
			if tabs.KeyDisambiguation() {
				t.Fatal("a reply with no flags should switch it off again")
			}
		}
	})
}

// screenPos is where text starts on den's screen, as the column and row a
// terminal cursor is placed by.
func screenPos(t testing.TB, m *AppModel, text string) (x, y int) {
	t.Helper()
	for row, line := range strings.Split(plain(m), "\n") {
		if col := strings.Index(line, text); col >= 0 {
			return len([]rune(line[:col])), row
		}
	}
	t.Fatalf("%q is not on screen:\n%s", text, plain(m))
	return 0, 0
}

// The cursor of a client being typed into is the terminal's own, at the cell
// the client put it on, in the shape it asked for; nowhere else is there one.
func TestTerminalCursorIsPlacedWhereTheClientPutIt(t *testing.T) {
	h, _, starts := terminalHarness(t)
	h.press("t")
	term := starts.terms[0]
	if h.m.View().Cursor != nil {
		t.Fatal("a client that shows no cursor should leave the terminal cursor hidden")
	}

	term.cursor = tea.NewCursor(4, 1)
	term.cursor.Shape = tea.CursorBar
	x, y := screenPos(t, h.m, "fake client screen")
	c := h.m.View().Cursor
	if c == nil {
		t.Fatal("a client being typed into should get the real cursor")
	}
	if c.X != x+4 || c.Y != y+1 || c.Shape != tea.CursorBar {
		t.Fatalf("cursor at (%d,%d) shape %v, want (%d,%d) as a bar", c.X, c.Y, c.Shape, x+4, y+1)
	}

	// Outside the area the terminal is shown in.
	term.cursor = tea.NewCursor(500, 0)
	if h.m.View().Cursor != nil {
		t.Fatal("a cursor off the screen should not be shown")
	}
	term.cursor = tea.NewCursor(4, 1)

	// den has the keyboard back: the screen is something to read.
	h.press("ctrl+t")
	if h.m.View().Cursor != nil {
		t.Fatal("the cursor should be hidden while den has the keyboard")
	}
	h.press("enter")
	if h.m.View().Cursor == nil {
		t.Fatal("typing again should bring the cursor back")
	}

	// A pop-up covers the tab and its cursor.
	h.m.shortcuts = components.OpenShortcuts("terminal", []components.ShortcutGroup{{Title: "t", Keys: []components.Shortcut{{Keys: "x", Text: "y"}}}})
	if h.m.View().Cursor != nil {
		t.Fatal("a pop-up over the terminal should hide its cursor")
	}
}

// The terminal's title names the pane on screen and counts the connections,
// so den is found among many tabs.
func TestWindowTitleNamesThePaneAndTheConnections(t *testing.T) {
	up := newSvc(service.TypeRDS, "up")
	if _, err := up.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, managerWith(up, newSvc(service.TypeRDS, "down")), nil)
	want := "den · " + paneMetadata[h.m.currentPane()].label + " · 1 connected"
	if got := h.m.View().WindowTitle; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}

	h.press("down")
	if got, label := h.m.View().WindowTitle, paneMetadata[h.m.currentPane()].label; !strings.HasPrefix(got, "den · "+label) {
		t.Fatalf("title = %q, want it to follow the pane (%s)", got, label)
	}

	idle := newHarness(t, managerWith(newSvc(service.TypeRDS, "db")), nil)
	if got := idle.m.View().WindowTitle; strings.Contains(got, "connected") {
		t.Fatalf("title = %q; with nothing connected it should not count", got)
	}
}

// Copying a connect command asks the terminal to set the clipboard (OSC 52)
// when the machine has none, as over SSH, and says so.
func TestCopyFallsBackToTheTerminalClipboard(t *testing.T) {
	saved := components.WriteClipboard
	components.WriteClipboard = func(string) error { return errors.New("no clipboard") }
	t.Cleanup(func() { components.WriteClipboard = saved })

	svc := newSvc(service.TypeRDS, "db")
	h := newHarness(t, managerWith(svc), nil)
	var requests []string
	var flashes []string
	for _, cmd := range h.m.runAction(actCopy, svc) {
		for _, msg := range runCmd(cmd) {
			switch msg := msg.(type) {
			case msgs.StatusFlashMsg:
				flashes = append(flashes, msg.Text)
			default:
				if fmt.Sprintf("%T", msg) == "tea.setClipboardMsg" {
					requests = append(requests, fmt.Sprint(msg))
				}
			}
		}
	}
	if len(requests) != 1 || requests[0] != svc.ConnectCommand() {
		t.Fatalf("the terminal was asked to set %q, want the connect command", requests)
	}
	if len(flashes) != 1 || !strings.Contains(flashes[0], "sent to the terminal") {
		t.Fatalf("flashes = %q, want one that says the terminal was asked", flashes)
	}
}

// ctrl+[ is esc: at a prompt it hands the keyboard back, as esc does.
func TestCtrlBracketLeavesATerminalAtAPrompt(t *testing.T) {
	h, _, starts := terminalHarness(t)
	h.press("t")
	h.press("ctrl+[")
	if h.m.detailModel.CapturingKeys() || starts.terms[0].typed() != "" {
		t.Fatalf("ctrl+[ should leave without reaching the client (it got %q)", starts.terms[0].typed())
	}
}

// Every key den acts on in the detail view is one a database prompt needs.
func TestKeysTypedIntoATerminalDoNotActOnDen(t *testing.T) {
	h, svc, starts := terminalHarness(t)
	h.press("t")
	h.press("q", "c", "d", "r", "backspace", "ctrl+c", "R")

	if h.quit || !h.m.showDetail {
		t.Fatal("a key typed into the terminal quit den or closed the detail view")
	}
	if starts1, stops, refreshes := svc.counts(); starts1 != 1 || stops != 0 || refreshes != 0 {
		t.Fatalf("keys typed into the terminal reached the service: %d starts, %d stops, %d refreshes", starts1, stops, refreshes)
	}
	if got, want := starts.terms[0].typed(), "q c d r backspace ctrl+c R"; got != want {
		t.Fatalf("the client received %q, want %q", got, want)
	}

	h.press("ctrl+t", "esc")
	if h.m.showDetail {
		t.Fatal("after ctrl+t, esc should close the detail view again")
	}
}

func TestTerminalsOutliveTheDetailView(t *testing.T) {
	h, _, starts := terminalHarness(t)
	h.press("t", "ctrl+t", "esc")
	if h.m.showDetail {
		t.Fatal("the detail view should be closed")
	}
	if starts.terms[0].closed() != 0 {
		t.Fatal("closing the detail view stopped the client")
	}

	h.press("enter")
	if view := plain(h.m); !strings.Contains(view, "1 psql") || !strings.Contains(view, "fake client screen") {
		t.Fatalf("the terminal is gone after reopening the detail view:\n%s", view)
	}
	if h.m.detailModel.CapturingKeys() {
		t.Fatal("reopening the detail view should start with den's keys")
	}
	h.press("enter", "a")
	if starts.terms[0].typed() != "a" {
		t.Fatal("the reopened view is not over the same client")
	}
}

func TestSeveralTerminalsPerService(t *testing.T) {
	h, _, starts := terminalHarness(t)
	h.press("t", "ctrl+t", "t", "x")
	if len(starts.terms) != 2 || starts.terms[1].typed() != "x" || starts.terms[0].typed() != "" {
		t.Fatal("the second t should open a second client and give it the keyboard")
	}
	h.press("ctrl+t", "1", "y")
	if starts.terms[0].typed() != "y" {
		t.Fatal("ctrl+t 1 should switch the keyboard to the first client")
	}
}

func TestTerminalNeedsAConnection(t *testing.T) {
	svc := fakeTermSvc{newSvc(service.TypeRDS, "db")}
	h := newHarness(t, managerWith(svc), nil)
	starts := &termStarts{}
	h.m.startTerminal = starts.start
	h.press("right", "enter", "t")

	if len(starts.cmds) != 0 {
		t.Fatal("a client was started for a service that is not connected")
	}
	if !strings.Contains(h.m.flashMsg, "db is not connected") || !h.m.flashWarn {
		t.Fatalf("flash = %q, want a warning that the service is not connected", h.m.flashMsg)
	}
}

func TestClientThatCannotStartIsReported(t *testing.T) {
	h, _, starts := terminalHarness(t)
	starts.err = errors.New("psql not found — brew install libpq")
	h.press("t")

	if !strings.Contains(h.m.flashMsg, "psql not found — brew install libpq") || !h.m.flashWarn {
		t.Fatalf("flash = %q, want a warning with the reason the client did not start", h.m.flashMsg)
	}
	// Not the sticky error line: nothing would clear it once psql is installed.
	if h.m.err != "" {
		t.Fatalf("err = %q, want it left alone", h.m.err)
	}
	if h.m.detailModel.CapturingKeys() {
		t.Fatal("no terminal opened, so the keyboard should stay den's")
	}
}

// A service with nothing den can run for it has no terminal tabs.
func TestNoTerminalWithoutTheCapability(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRedis, "cache")), nil)
	starts := &termStarts{}
	h.m.startTerminal = starts.start
	h.press("right", "c", "enter", "t")
	if len(starts.cmds) != 0 || len(h.m.terminals) != 0 {
		t.Fatal("t opened a terminal for a service without a client den can run")
	}
}

// A disconnect ends the clients' connections and their token goes stale, so
// they are stopped; the tabs stay, to be restarted after reconnecting.
func TestDisconnectStopsClientsAndKeepsTheirTabs(t *testing.T) {
	h, svc, starts := terminalHarness(t)
	h.press("t", "ctrl+t")

	h.send(vpnNow(svc.fakeSvc, service.StatusReconnecting))
	if starts.terms[0].closed() != 0 {
		t.Fatal("a reconnect in progress stopped the client: it may well survive it")
	}

	h.send(vpnNow(svc.fakeSvc, service.StatusIdle))
	if starts.terms[0].closed() != 1 {
		t.Fatal("a disconnect should stop the client")
	}
	if tabs := h.m.terminals[svc]; tabs == nil || tabs.Len() != 1 {
		t.Fatal("a disconnect should keep the tab")
	}

	h.press("enter")
	if len(starts.cmds) != 1 || !strings.Contains(h.m.flashMsg, "db is not connected") {
		t.Fatalf("restarting a tab of a disconnected service: %d starts, flash %q", len(starts.cmds), h.m.flashMsg)
	}

	h.send(vpnNow(svc.fakeSvc, service.StatusConnected))
	h.press("enter", "z")
	if len(starts.terms) != 2 || starts.terms[1].typed() != "z" {
		t.Fatal("enter on an exited tab of a connected service should restart its client and give it the keyboard")
	}
	if tabs := h.m.terminals[svc]; tabs.Len() != 1 {
		t.Fatalf("a restart should reuse the tab, got %d tabs", tabs.Len())
	}
}

func TestQuitStopsTheClients(t *testing.T) {
	h, _, starts := terminalHarness(t)
	h.press("t", "ctrl+t", "esc", "q")
	if !h.quit {
		t.Fatal("q should quit")
	}
	if starts.terms[0].closed() != 1 {
		t.Fatal("quitting den left a client running")
	}
}

func TestReloadDropsTheTerminalsOfARemovedService(t *testing.T) {
	ctx := context.Background()
	mgr := service.NewManager()
	entry := func(name string) (service.Entry, fakeTermSvc) {
		svc := fakeTermSvc{newSvc(service.TypeRDS, name)}
		return service.Entry{Key: "rds:" + name, Spec: name, Build: func() (service.Service, error) { return svc, nil }}, svc
	}
	gone, goneSvc := entry("gone")
	kept, keptSvc := entry("kept")
	mgr.Reconcile(ctx, []service.Entry{gone, kept})
	h := newHarness(t, mgr, reloadTo(mgr, func() []service.Entry { return []service.Entry{kept} }))
	starts := &termStarts{}
	h.m.startTerminal = starts.start

	h.press("right", "c", "enter", "t", "ctrl+t", "esc") // a terminal on "gone"
	h.press("down", "c", "enter", "t", "ctrl+t", "esc")  // and one on "kept"
	if len(starts.terms) != 2 {
		t.Fatalf("%d terminals opened, want one per service", len(starts.terms))
	}

	h.press("R", "y") // "gone" is connected: the reload asks before stopping it
	if starts.terms[0].closed() != 1 || h.m.terminals[goneSvc] != nil {
		t.Fatal("the terminal of a service removed by the reload is still there")
	}
	if starts.terms[1].closed() != 0 || h.m.terminals[keptSvc] == nil {
		t.Fatal("the reload stopped the terminal of a service it kept")
	}
}

func TestRestartPromptListsOpenTerminals(t *testing.T) {
	h, _, _ := terminalHarness(t)
	h.press("t", "ctrl+t", "t", "ctrl+t")
	lines := strings.Join(closedByRestart(h.m.manager, h.m.terminals, h.m.runbookTabs)(), "\n")
	if !strings.Contains(lines, "rds  db") || !strings.Contains(lines, "2 terminals  db") {
		t.Fatalf("a restart closes the connection and its two terminals, the prompt lists:\n%s", lines)
	}
}

// A client's output wakes the root model through one waiter per terminal,
// re-armed only while the client runs.
func TestTerminalWaiterStopsWithTheClient(t *testing.T) {
	term := &fakeTerminal{changed: make(chan struct{}, 1)}
	h := newHarness(t, managerWith(), nil)

	term.changed <- struct{}{}
	msg := waitTerminal(term)()
	_, cmd := h.m.Update(msg)
	if cmd == nil {
		t.Fatal("output from a running client did not re-arm its waiter")
	}

	term.Close() // the client exits, which wakes its waiter
	_, cmd = h.m.Update(waitTerminal(term)())
	if cmd != nil {
		t.Fatal("the waiter of an exited client was re-armed: it would never return")
	}
}

// esc is "back" one step at a time: out of the terminal first, with the
// detail view and the terminal still on screen, and out of the detail view next.
func TestEscLeavesTheTerminalBeforeTheDetailView(t *testing.T) {
	h, _, starts := terminalHarness(t)
	h.press("t", "esc")
	if !h.m.showDetail {
		t.Fatal("esc in a terminal closed the detail view")
	}
	if h.m.detailModel.CapturingKeys() || starts.terms[0].typed() != "" {
		t.Fatalf("esc at a prompt should return the keyboard to den; the client got %q", starts.terms[0].typed())
	}
	if view := plain(h.m); !strings.Contains(view, "fake client screen") {
		t.Fatalf("the terminal should stay on screen after esc:\n%s", view)
	}
	h.press("esc")
	if h.m.showDetail {
		t.Fatal("a second esc should close the detail view")
	}
	if starts.terms[0].closed() != 0 {
		t.Fatal("leaving with esc stopped the client")
	}
}

// A paste is one message, not keys: into a terminal that has the keyboard it
// reaches the client whole (bracketed when the client asked), and when den has
// the keyboard it is dropped rather than read as den's keys.
func TestPasteIntoADetailViewTerminal(t *testing.T) {
	h, svc, starts := terminalHarness(t)
	h.press("t")
	term := starts.terms[0]

	h.send(testkeys.Paste("select 1;\nq"))
	term.mu.Lock()
	pasted := strings.Join(term.pasted, "|")
	term.mu.Unlock()
	if pasted != "select 1;\nq" {
		t.Fatalf("the client was pasted %q, want the text whole", pasted)
	}
	if term.typed() != "" {
		t.Fatalf("a paste reached the client as keys: %q", term.typed())
	}
	if _, stops, _ := svc.counts(); stops != 0 || h.quit || !h.m.showDetail {
		t.Fatal("a pasted q must not act on den")
	}

	// With den holding the keyboard the same paste goes nowhere.
	h.press("esc")
	h.send(testkeys.Paste("dq"))
	term.mu.Lock()
	n := len(term.pasted)
	term.mu.Unlock()
	if n != 1 || term.typed() != "" {
		t.Fatalf("a paste while den had the keyboard reached the client (%d pastes, keys %q)", n, term.typed())
	}
	if _, stops, _ := svc.counts(); stops != 0 || h.quit || !h.m.showDetail {
		t.Fatal("a paste while den had the keyboard acted as keys")
	}
}

// Pasted text lands in the search box of the focused pane, and nowhere while
// the menu has the focus or no box is open.
func TestPasteIntoAPaneFilter(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "one"), newSvc(service.TypeRDS, "two"), newSvc(service.TypeRDS, "three")), nil)

	h.press("right")
	h.send(testkeys.Paste("tw"))
	if h.m.connectPane.Searching() {
		t.Fatal("a paste with no filter open opened one")
	}
	if svc, ok := h.m.selectedService(); !ok || svc.Info().Name != "one" {
		t.Fatalf("a paste with no filter open moved the selection to %v", svc)
	}

	h.press("/")
	h.send(testkeys.Paste("tw"))
	if !h.m.connectPane.Searching() {
		t.Fatal("the filter should stay open after a paste")
	}
	if svc, ok := h.m.selectedService(); !ok || svc.Info().Name != "two" {
		t.Fatalf("selected after pasting tw into the filter = %v, want two", svc)
	}
	if h.quit {
		t.Fatal("pasted text acted as keys")
	}

	// The menu has the focus: the paste is not for the pane's box.
	h.press("enter", "esc", "esc")
	if !h.m.leftFocused {
		t.Fatal("esc should return to the menu")
	}
	h.send(testkeys.Paste("zzz"))
	h.press("right")
	if h.m.connectPane.Searching() {
		t.Fatal("a paste into the menu opened a filter")
	}
}

// With esc leaving a terminal, the d of a query typed a moment too late lands
// on den. It stops every client of the service, so it asks first.
func TestDisconnectInTheDetailViewAsksWhileTerminalsRun(t *testing.T) {
	h, svc, starts := terminalHarness(t)
	h.press("t", "esc", "d")
	if _, stops, _ := svc.counts(); stops != 0 {
		t.Fatal("d disconnected a service with a running terminal without asking")
	}
	if view := plain(h.m); !strings.Contains(view, "disconnect db?") || !strings.Contains(view, "1 running terminal") {
		t.Fatalf("d should ask, saying what it stops:\n%s", view)
	}

	h.press("e") // the next letter of "delete", not an answer
	if _, stops, _ := svc.counts(); stops != 0 || h.m.detailModel.CapturingKeys() {
		t.Fatal("any key but y should keep the connection and close the question")
	}

	h.press("d", "y")
	if _, stops, _ := svc.counts(); stops != 1 {
		t.Fatalf("d then y should disconnect, got %d stops", stops)
	}

	// Nothing to lose once the clients are gone: d acts at once again.
	h.send(vpnNow(svc.fakeSvc, service.StatusIdle))
	if starts.terms[0].closed() != 1 {
		t.Fatal("the disconnect should have stopped the client")
	}
	h.press("c", "d")
	if _, stops, _ := svc.counts(); stops != 2 {
		t.Fatalf("d without running terminals should disconnect at once, got %d stops", stops)
	}
}

// connectedVPN is a VPN up for an hour with its toggles on, as the Office VPN in
// the repo's den.yaml: its description and health target are the long ones.
type connectedVPN struct {
	*fakeSvc
	started time.Time
}

func (v *connectedVPN) State() service.RuntimeState {
	return service.RuntimeState{Status: service.StatusConnected, StartedAt: &v.started}
}
func (v *connectedVPN) ToggleReconnect() bool     { return true }
func (v *connectedVPN) ReconnectEnabled() bool    { return true }
func (v *connectedVPN) ToggleHealthCheck() bool   { return true }
func (v *connectedVPN) HealthCheckEnabled() bool  { return true }
func (v *connectedVPN) HealthCheckTarget() string { return "intranet.example.com:443" }

// Bubble Tea drops the top lines of a frame taller than the terminal: a pane
// one line too tall hid den's header ("den — Developer Environment Navigator"). The VPN
// pane did that on a ~100-column terminal, where its description wrapped; the
// Tunnel, Secrets and Runbooks panes did it whenever nothing was configured.
func TestEveryPaneFitsTheTerminal(t *testing.T) {
	vpn := &connectedVPN{fakeSvc: newSvc(service.TypeVPN, "Office VPN"), started: time.Now().Add(-time.Hour)}
	vpn.info.Environment = "Corporate VPN (openfortivpn + SAML)"
	configs := map[string]func() *service.Manager{
		"nothing configured": func() *service.Manager { return managerWith() },
		"a connected VPN":    func() *service.Manager { return managerWith(newSvc(service.TypeRDS, "EU STAGING"), vpn) },
	}
	for name, mgr := range configs {
		for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 102, Height: 30}, {Width: 160, Height: 62}} {
			h := newHarness(t, mgr(), nil)
			h.send(size)
			h.send(msgs.ServiceEventMsg{Update: service.StatusUpdate{Info: vpn.info, State: vpn.State()}})
			rw, rh := size.Width-leftPaneWidth-1, size.Height-3
			for range allPanes {
				id := h.m.currentPane()
				// The pane itself, so a cut at the root cannot hide a pane that
				// lost its last lines.
				if got := lipgloss.Height(h.m.pane(id).view(rw, rh, false)); got > rh {
					t.Errorf("%s, %dx%d: the %s pane is %d lines, its space is %d", name, size.Width, size.Height, id, got, rh)
				}
				// Any line over and Bubble Tea cuts the header.
				if got := lipgloss.Height(plain(h.m)); got != size.Height {
					t.Errorf("%s, %dx%d: on %s the frame is %d lines, want %d", name, size.Width, size.Height, id, got, size.Height)
				}
				h.press("down")
			}
		}
	}
}

// A pane too tall loses its bottom lines, never den's header above it.
func TestFitHeightCutsAtTheBottom(t *testing.T) {
	if got := fitHeight("a\nb\nc\nd", 2); got != "a\nb" {
		t.Fatalf("fitHeight = %q, want the first two lines", got)
	}
	if got := fitHeight("a\nb", 5); got != "a\nb" {
		t.Fatalf("fitHeight = %q, want a short pane unchanged", got)
	}
}

// ── terminal background ──────────────────────────────────────────────────────

// Colours Help draws in on a light background and not on a dark one: the
// "What it is" heading and the Getting started group, as truecolor escapes.
const lightHelpRGB = "107;70;193" // #6B46C1

// bg is the terminal's reply to the background query for a light or a dark
// terminal.
func bg(dark bool) tea.BackgroundColorMsg {
	if dark {
		return tea.BackgroundColorMsg{Color: lipgloss.Color("#000000")}
	}
	return tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")}
}

func TestInitRequestsTheBackgroundColor(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "db")), nil)
	want := fmt.Sprintf("%T", tea.RequestBackgroundColor())
	for _, msg := range runCmd(h.m.Init()) {
		if fmt.Sprintf("%T", msg) == want {
			return
		}
	}
	t.Fatalf("Init should ask the terminal for its background (%s)", want)
}

// Until the terminal replies, den draws the dark palette, so there is no
// blank first frame; a light reply switches Help — the list and an open topic
// — to the light one, and a dark reply back.
func TestBackgroundColorMsgSwitchesHelpPalette(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "db")), nil)
	h.press("?")
	if !h.m.helpPane.Dark() || h.m.light {
		t.Fatal("den should draw the dark palette until the terminal replies")
	}
	h.press("enter")
	if h.m.helpPane.Reading() == "" {
		t.Fatal("enter should open the selected topic")
	}
	if strings.Contains(h.m.View().Content, lightHelpRGB) {
		t.Fatal("the dark palette should not use the light colours")
	}

	h.send(bg(false))
	if h.m.helpPane.Dark() || !h.m.light {
		t.Fatal("a light background should switch the model and Help to the light palette")
	}
	if !strings.Contains(h.m.View().Content, lightHelpRGB) {
		t.Fatal("the open topic should be drawn in the light colours")
	}
	if h.m.helpPane.Reading() == "" {
		t.Fatal("switching the palette should keep the topic open")
	}
	h.press("esc")
	if !strings.Contains(h.m.View().Content, lightHelpRGB) {
		t.Fatal("the topic list should be drawn in the light colours")
	}

	h.send(bg(true))
	if !h.m.helpPane.Dark() || h.m.light {
		t.Fatal("a dark background should switch back to the dark palette")
	}
	if strings.Contains(h.m.View().Content, lightHelpRGB) {
		t.Fatal("the dark palette should not use the light colours")
	}
}

// The changelog is a copy of the markdown view made when it opens: it takes
// the reply while it is open, and the palette already known when it opens.
func TestBackgroundColorReachesTheChangelog(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "db")), nil)
	for h.m.currentPane() != paneUpdates {
		h.press("down")
	}
	h.press("right", "s")
	if !h.m.updatesPane.ShowingChangelog() || !h.m.updatesPane.ChangelogDark() {
		t.Fatal("the changelog should open in the dark palette until the terminal replies")
	}
	h.send(bg(false))
	if h.m.updatesPane.ChangelogDark() {
		t.Fatal("a light reply should reach the open changelog")
	}
	h.press("esc", "s")
	if !h.m.updatesPane.ShowingChangelog() || h.m.updatesPane.ChangelogDark() {
		t.Fatal("a changelog opened after the reply should start in its palette")
	}
}
