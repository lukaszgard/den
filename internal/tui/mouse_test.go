package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/testkeys"

	tea "charm.land/bubbletea/v2"
)

// panelX is a column inside the panel, right of the menu and its separator.
const panelX = leftPaneWidth + 10

// configHarness is den over cfg and svcs, its AWS files pointed at awsCfg.
func configHarness(t *testing.T, cfg *config.Config, svcs ...service.Service) *harness {
	t.Helper()
	h := &harness{t: t, m: New(managerWith(svcs...), Options{ConfigPath: "den.yaml", Config: cfg, Version: "test"})}
	t.Cleanup(func() { h.m.shutdown() })
	h.send(tea.WindowSizeMsg{Width: 140, Height: 40})
	return h
}

// awsFiles writes an AWS config file holding body and returns its spec.
func awsFiles(t *testing.T, body string) *config.AWSSpec {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return &config.AWSSpec{ConfigPath: path, CredentialsPath: path}
}

// openPane selects id in the menu, keeping the menu focused.
func (h *harness) openPane(id paneID) {
	h.t.Helper()
	h.m.leftCursor = h.m.paneIndexOf(id)
	if h.m.leftCursor < 0 {
		h.t.Fatalf("%s is not in the menu", id)
	}
}

// den asks the terminal for clicks and the wheel, and what it hands the
// terminal carries none of the marks its zones are found by.
func TestTheViewCapturesTheMouseAndCarriesNoMarks(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "orders-db")), nil)
	v := h.m.View()
	if v.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("MouseMode = %v, want cell motion", v.MouseMode)
	}
	if strings.Contains(v.Content, "\x1b_") {
		t.Fatal("a zone mark reached the terminal")
	}
}

// A click on a menu row shows its panel with the menu still focused, as the
// arrows do; a double-click opens it, as enter does.
func TestClickingTheMenu(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "orders-db")), nil)
	h.clickText("Tunnel")
	if h.m.currentPane() != paneTunnel || !h.m.leftFocused {
		t.Fatalf("pane %s, menu focused %v: a click on Tunnel should show it from the menu", h.m.currentPane(), h.m.leftFocused)
	}
	h.doubleClickText("Runbooks")
	if h.m.currentPane() != paneRunbooks || h.m.leftFocused {
		t.Fatal("a double-click on Runbooks should open its panel")
	}
	h.clickText("Tunnel")
	if h.m.currentPane() != paneTunnel || !h.m.leftFocused {
		t.Fatal("a click on the menu should give it the focus back")
	}
}

// A click on a row of the panel focuses the panel and selects the row; a
// double-click does what enter does there.
func TestClickingAConnectRow(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "orders-db"), newSvc(service.TypeRDS, "billing-db")), nil)
	h.clickText("billing-db")
	if h.m.leftFocused || h.m.connectPane.Cursor() != 1 {
		t.Fatalf("menu focused %v, cursor %d: the click should focus the panel on billing-db", h.m.leftFocused, h.m.connectPane.Cursor())
	}
	h.doubleClickText("orders-db")
	if !h.m.showDetail || h.m.detailModel.Service().Info().Name != "orders-db" {
		t.Fatal("a double-click on orders-db should open its detail view")
	}
}

// Two clicks on different rows are two clicks, not a double-click.
func TestTwoClicksOnDifferentRowsOpenNothing(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "orders-db"), newSvc(service.TypeRDS, "billing-db")), nil)
	h.clickText("billing-db")
	h.clickText("orders-db")
	if h.m.showDetail || h.m.connectPane.Cursor() != 0 {
		t.Fatal("a click on another row should only move the cursor")
	}
}

// The wheel over the panel moves through it as the arrows did before den took
// the mouse, without taking the focus from the menu.
func TestTheWheelOverThePanel(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "orders-db"), newSvc(service.TypeRDS, "billing-db")), nil)
	h.wheel(panelX, 5, false)
	if h.m.connectPane.Cursor() != 1 || !h.m.leftFocused {
		t.Fatalf("cursor %d, menu focused %v: the wheel should move the panel's cursor, not the focus", h.m.connectPane.Cursor(), h.m.leftFocused)
	}
	h.wheel(panelX, 5, true)
	if h.m.connectPane.Cursor() != 0 {
		t.Fatal("the wheel up should move the cursor back")
	}
}

// The wheel over the menu does nothing: a turn too many would switch panels.
func TestTheWheelOverTheMenuIsIgnored(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "orders-db")), nil)
	h.wheel(3, 5, false)
	if h.m.leftCursor != 0 {
		t.Fatalf("the wheel over the menu moved it to %d", h.m.leftCursor)
	}
}

// A stray click must never answer a pop-up, nor reach what it covers.
func TestClicksAreIgnoredUnderAPopUp(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "orders-db")), nil)
	x, y := screenPos(t, h.m, "Tunnel")
	h.press("tab", "h")
	if !h.m.shortcuts.Open() {
		t.Fatal("h should open the shortcuts")
	}
	h.click(x, y)
	if !h.m.shortcuts.Open() || h.m.currentPane() != paneConnect {
		t.Fatal("a click under the shortcuts pop-up should do nothing")
	}

	f := runbookHarness(t)
	f.selectRow("Ask")
	x, y = screenPos(t, f.m, "Tunnel")
	f.press("r")
	if !f.m.runbooksPane.Modal() {
		t.Fatal("r on Ask should ask for its parameter")
	}
	f.click(x, y)
	if f.m.currentPane() != paneRunbooks || !f.m.runbooksPane.Modal() {
		t.Fatal("a click while a pane asks something should do nothing")
	}
	f.wheel(panelX, 10, false)
	if !f.m.runbooksPane.Modal() {
		t.Fatal("the wheel must not answer a pane's question")
	}
}

// The shortcuts pop-up scrolls with the wheel, wherever the pointer is.
func TestTheShortcutsPopUpScrollsWithTheWheel(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "orders-db")), nil)
	h.send(tea.WindowSizeMsg{Width: 140, Height: 14})
	h.press("tab", "h")
	before := plain(h.m)
	h.wheel(3, 3, false)
	if plain(h.m) == before {
		t.Fatal("the wheel should scroll the shortcuts")
	}
	h.wheel(3, 3, true)
	if plain(h.m) != before || !h.m.shortcuts.Open() {
		t.Fatal("the wheel up should scroll the shortcuts back")
	}
}

// ── a click in each panel with rows ──────────────────────────────────────────

func TestClickingARunbook(t *testing.T) {
	f := runbookHarness(t)
	f.selectRow("Greet")
	f.press("esc")
	f.clickText("Ask")
	if e, ok := f.m.runbooksPane.Selected(); !ok || e != "Ask" || f.m.leftFocused {
		t.Fatalf("selected %q: a click should select Ask and focus the panel", e)
	}
	f.doubleClickText("Greet")
	if !f.m.showRunbook || f.m.runbookView.Name() != "Greet" {
		t.Fatal("a double-click on Greet should open its view")
	}
}

func TestClickingATunnelAndAVPN(t *testing.T) {
	h := newHarness(t, managerWith(
		&fakeTunnel{fakeSvc: newSvc(service.TypeTunnel, "bastion-a")}, &fakeTunnel{fakeSvc: newSvc(service.TypeTunnel, "bastion-b")},
		newSvc(service.TypeVPN, "office-vpn"), newSvc(service.TypeVPN, "lab-vpn")), nil)
	h.openPane(paneTunnel)
	h.clickText("bastion-b")
	if svc, ok := h.m.tunnelPane.SelectedService(); !ok || svc.Info().Name != "bastion-b" || h.m.leftFocused {
		t.Fatal("a click should select bastion-b and focus the panel")
	}
	h.doubleClickText("bastion-a")
	if !h.m.showDetail || h.m.detailModel.Service().Info().Name != "bastion-a" {
		t.Fatal("a double-click on a tunnel should open its detail view, as enter does")
	}
	h.press("esc")
	h.openPane(paneVPN)
	h.clickText("lab-vpn")
	if svc, ok := h.m.vpnPane.SelectedService(); !ok || svc.Info().Name != "lab-vpn" {
		t.Fatal("a click should select lab-vpn")
	}
}

func TestClickingADockerService(t *testing.T) {
	h := newHarness(t, managerWith(newSvc(service.TypeDocker, "postgres-local"), newSvc(service.TypeDocker, "redis-local")), nil)
	h.openPane(paneCreateEnv)
	h.clickText("redis-local")
	if svc, ok := h.m.createEnvPane.SelectedService(); !ok || svc.Info().Name != "redis-local" || h.m.leftFocused {
		t.Fatal("a click should select redis-local and focus the panel")
	}
}

func TestClickingAnAWSProfile(t *testing.T) {
	aws := awsFiles(t, "[profile alpha-profile]\nregion = eu-west-1\n\n[profile beta-profile]\nregion = eu-central-1\n")
	h := configHarness(t, &config.Config{AWS: aws})
	h.openPane(paneAWS)
	h.clickText("beta-profile")
	if hint := h.m.awsPane.SelectionHint(); !strings.Contains(hint, "beta-profile") || h.m.leftFocused {
		t.Fatalf("selection %q: a click should select beta-profile and focus the panel", hint)
	}
}

func TestClickingASecret(t *testing.T) {
	cfg := &config.Config{
		AWS: awsFiles(t, ""),
		Secrets: &config.SecretsConfig{AWSSecretsManager: []config.AWSSecretsManagerSecretSpec{
			{Name: "m2m key", SecretID: "/acme/m2m", AWSProfile: "dev"},
			{Name: "db password", SecretID: "/acme/db", AWSProfile: "dev"},
		}},
	}
	h := configHarness(t, cfg)
	h.openPane(paneSecrets)
	if strings.Contains(plain(h.m), "/acme/db") {
		t.Fatal("the box shows the second secret before it is selected")
	}
	h.clickText("db password")
	if !strings.Contains(plain(h.m), "/acme/db") || h.m.leftFocused {
		t.Fatalf("a click should select db password, whose details the box shows, and focus the panel:\n%s", plain(h.m))
	}
}

func TestClickingAConfigProblem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "den.yaml")
	if err := os.WriteFile(path, []byte(strings.Repeat("# line\n", 12)), 0o600); err != nil {
		t.Fatal(err)
	}
	problems := config.Problems{
		{Severity: config.SeverityWarning, Line: 3, Msg: "first problem"},
		{Severity: config.SeverityWarning, Line: 9, Msg: "second problem", Hint: "fix it"},
	}
	h := &harness{t: t, m: New(managerWith(), Options{ConfigPath: path, Config: &config.Config{AWS: awsFiles(t, "")},
		Version: "test", ConfigProblems: problems})}
	t.Cleanup(func() { h.m.shutdown() })
	h.send(tea.WindowSizeMsg{Width: 140, Height: 40})
	h.openPane(paneConfig)
	h.press("right", "p")
	h.clickText("fix it")
	if view := plain(h.m); !strings.Contains(view, "2 of 2") {
		t.Fatalf("a click on the second problem's hint should select it:\n%s", view)
	}
}

func TestClickingAHelpTopic(t *testing.T) {
	h := newHarness(t, managerWith(), nil)
	h.openPane(paneHelp)
	h.clickText("Files and folders")
	if h.m.leftFocused || h.m.helpPane.Reading() != "" {
		t.Fatal("a click on a topic should focus the panel, not open the topic")
	}
	h.press("enter")
	if got := h.m.helpPane.Reading(); got != "Files and folders" {
		t.Fatalf("enter after the click opened %q, want the topic clicked", got)
	}
	h.press("esc")
	h.doubleClickText("Transports")
	if got := h.m.helpPane.Reading(); got != "Transports" {
		t.Fatalf("a double-click opened %q, want Transports", got)
	}
}

// In the detail view a click on the client's screen types into it, a click on
// Logs takes the keyboard back, and the wheel reaches the client being typed
// into as the arrows did.
func TestTheMouseInTheDetailView(t *testing.T) {
	h, _, starts := terminalHarness(t)
	h.press("t", "ctrl+t")
	h.clickText("fake client screen")
	if !h.m.detailModel.CapturingKeys() {
		t.Fatal("a click on the client's screen should type into it")
	}
	h.wheel(panelX, 10, true)
	if got := starts.terms[0].typed(); got != "up" {
		t.Fatalf("the client got %q, want the wheel as ↑", got)
	}
	h.clickTab("Logs")
	if h.m.detailModel.CapturingKeys() || h.m.detailModel.View() == "" || strings.Contains(plain(h.m), "fake client screen") {
		t.Fatal("a click on Logs should show the logs, den having the keys")
	}
}

// ── selecting text with a drag ───────────────────────────────────────────────

// clipboard swaps the system clipboard for one the test reads, so a drag
// never touches the developer's.
func clipboard(t *testing.T) *string {
	t.Helper()
	copied := new(string)
	saved := components.WriteClipboard
	components.WriteClipboard = func(text string) error { *copied = text; return nil }
	t.Cleanup(func() { components.WriteClipboard = saved })
	return copied
}

// dragOver presses the left button on the cell x0, y0, drags to x1, y1 and
// lets go there, a frame drawn before each as the terminal shows one.
func (h *harness) dragOver(x0, y0, x1, y1 int) {
	h.t.Helper()
	h.m.View()
	h.send(testkeys.Click(x0, y0))
	h.m.View()
	h.send(testkeys.Drag(x1, y1))
	h.m.View()
	h.send(testkeys.Release(x1, y1))
}

// reverse is how a selection is drawn.
const reverse = "\x1b[7m"

// A drag over the panel copies the panel's text and nothing of the menu
// beside it, however far left it goes, and says how much it copied.
func TestADragCopiesThePanelsText(t *testing.T) {
	copied := clipboard(t)
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "orders-db"), newSvc(service.TypeRDS, "billing-db")), nil)
	x0, y0 := screenPos(t, h.m, "orders-db")
	x1, y1 := screenPos(t, h.m, "billing-db")
	h.dragOver(x0, y0, x1+len("billing-db")-1, y1)
	lines := strings.Split(*copied, "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "orders-db") || !strings.HasSuffix(lines[1], "billing-db") {
		t.Fatalf("copied %q, want orders-db's row from the name on and billing-db's up to its name", *copied)
	}
	for _, menu := range []string{"Connect", "Tunnel", "│"} {
		if strings.Contains(*copied, menu) {
			t.Fatalf("copied %q, which has %q from outside the panel", *copied, menu)
		}
	}
	if !strings.Contains(plain(h.m), "copied 2 lines") {
		t.Fatalf("the status bar should say what was copied:\n%s", plain(h.m))
	}

	// Dragged on into the menu, the selection stops at the panel's edge.
	h.dragOver(x1+3, y1, 2, y0)
	if strings.Contains(*copied, "Connect") || !strings.Contains(*copied, "orders-db") || !strings.HasSuffix(*copied, "bill") {
		t.Fatalf("copied %q, want orders-db's row from the panel's edge, then billing-db's up to bill", *copied)
	}
}

// A click is not a drag: it selects its row, and copies nothing.
func TestAClickCopiesNothing(t *testing.T) {
	copied := clipboard(t)
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "orders-db"), newSvc(service.TypeRDS, "billing-db")), nil)
	x, y := screenPos(t, h.m, "billing-db")
	h.dragOver(x, y, x, y)
	if *copied != "" || h.m.flashMsg != "" || strings.Contains(h.m.View().Content, reverse) {
		t.Fatalf("a click copied %q and flashed %q", *copied, h.m.flashMsg)
	}
	if h.m.leftFocused || h.m.connectPane.Cursor() != 1 {
		t.Fatal("the click should still select billing-db")
	}
}

// The selection shows in reverse video after the button is let go, until a
// key, which still does what it does.
func TestTheSelectionShowsUntilAKey(t *testing.T) {
	clipboard(t)
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "orders-db"), newSvc(service.TypeRDS, "billing-db")), nil)
	if strings.Contains(h.m.View().Content, reverse) {
		t.Fatal("the screen is in reverse video before any drag")
	}
	x, y := screenPos(t, h.m, "orders-db")
	h.dragOver(x, y, x+5, y)
	if !strings.Contains(h.m.View().Content, reverse+"orders") {
		t.Fatalf("the selection should show in reverse video: %q", h.m.View().Content)
	}
	h.press("down")
	if strings.Contains(h.m.View().Content, reverse) || h.m.connectPane.Cursor() != 1 {
		t.Fatal("a key should clear the selection and still move the cursor")
	}
	h.dragOver(x, y, x+5, y)
	h.wheel(panelX, 5, false)
	if strings.Contains(h.m.View().Content, reverse) {
		t.Fatal("the wheel should clear the selection")
	}
}

// A drag is not a click: a click right after one, on the row it started on,
// is a first click, not the second of a double-click.
func TestAClickAfterADragIsNotADoubleClick(t *testing.T) {
	clipboard(t)
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "orders-db")), nil)
	x, y := screenPos(t, h.m, "orders-db")
	h.dragOver(x, y, x+4, y)
	h.click(x, y)
	if h.m.showDetail {
		t.Fatal("a click after a drag opened the detail view as a double-click")
	}
}

// In the detail view a drag keeps to the inside of the box: its border is not
// text to copy.
func TestADragInTheDetailViewCopiesNoBorder(t *testing.T) {
	copied := clipboard(t)
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "orders-db")), nil)
	h.doubleClickText("orders-db")
	if !h.m.showDetail {
		t.Fatal("a double-click should open the detail view")
	}
	h.dragOver(0, 1, h.m.width-1, h.m.height-1)
	if !strings.Contains(*copied, "orders-db") {
		t.Fatalf("copied %q, want the inside of the box", *copied)
	}
	for _, corner := range []string{"╭", "╮", "╰", "╯"} {
		if strings.Contains(*copied, corner) {
			t.Fatalf("copied %q, which has the corner %s", *copied, corner)
		}
	}
	for _, line := range strings.Split(*copied, "\n") {
		if strings.HasPrefix(line, "│") || strings.HasSuffix(line, "│") {
			t.Fatalf("copied the box's side in %q", line)
		}
	}
	if strings.Contains(*copied, "esc: back") {
		t.Fatal("a drag that starts in the box should not reach the help line under it")
	}

	// The help line under the box is a row of its own.
	x, y := screenPos(t, h.m, "esc: back")
	h.dragOver(x, y, x+8, y-5)
	if *copied != "esc: back" {
		t.Fatalf("copied %q, want the help line's own text", *copied)
	}
}

// While a pop-up is up a drag selects nothing, as a click answers nothing.
func TestADragUnderAPopUpSelectsNothing(t *testing.T) {
	copied := clipboard(t)
	h := newHarness(t, managerWith(newSvc(service.TypeRDS, "orders-db")), nil)
	h.press("tab", "h")
	h.dragOver(panelX, 3, panelX+20, 10)
	if *copied != "" || strings.Contains(h.m.View().Content, reverse) || !h.m.shortcuts.Open() {
		t.Fatalf("a drag under the shortcuts copied %q", *copied)
	}
}
