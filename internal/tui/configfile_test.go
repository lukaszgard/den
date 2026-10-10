package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/msgs"
)

// fileHarness is den over a real den.yaml in a temp dir: its tunnels are the
// services, one fake each, built again only when their entry changes — the
// same Check, WouldStop and Apply the CLI hands the TUI, over fakes.
type fileHarness struct {
	*harness
	path    string
	svcs    map[string]*fakeSvc // the last service built for each tunnel
	applies int
}

const fileHeader = "aws: {config_path: %s, credentials_path: %s}\n"

func newFileHarness(t *testing.T, yaml string) *fileHarness {
	t.Helper()
	dir := t.TempDir()
	awsCfg := filepath.Join(dir, "aws-config")
	if err := os.WriteFile(awsCfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fileHarness{path: filepath.Join(dir, "den.yaml"), svcs: map[string]*fakeSvc{}}
	f.write(t, strings.ReplaceAll(fileHeader, "%s", awsCfg)+yaml)

	mgr := service.NewManager()
	entries := func(cfg *config.Config) []service.Entry {
		var out []service.Entry
		for _, tun := range cfg.Tunnels {
			out = append(out, service.Entry{Key: "tunnel:" + tun.Name, Spec: tun, Build: func() (service.Service, error) {
				svc := newSvc(service.TypeTunnel, tun.Name)
				f.svcs[tun.Name] = svc
				return svc, nil
			}})
		}
		return out
	}
	file := &ConfigFile{
		Check:     func() (*config.Config, config.Problems, error) { return config.LoadChecked(f.path) },
		WouldStop: func(cfg *config.Config) []string { return mgr.WouldStop(entries(cfg)) },
		Apply: func(ctx context.Context, cfg *config.Config) service.ReconcileResult {
			f.applies++
			return mgr.Reconcile(ctx, entries(cfg))
		},
	}
	cfg, problems, err := file.Check()
	if err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		mgr.Reconcile(context.Background(), entries(cfg))
	}
	f.harness = &harness{t: t, m: New(mgr, Options{ConfigPath: f.path, Config: cfg, ConfigProblems: problems, Version: "test", ConfigFile: file})}
	t.Cleanup(func() { f.m.shutdown() })
	f.send(tea.WindowSizeMsg{Width: 140, Height: 40})
	return f
}

func (f *fileHarness) write(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(f.path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *fileHarness) read() string {
	f.t.Helper()
	data, err := os.ReadFile(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}

// editor makes the next e run edit on den.yaml in place of a real editor,
// which would need the terminal.
func (f *fileHarness) editor(edit func(string) string) {
	f.m.startEditor = func(path string, done func(error) tea.Msg) tea.Cmd {
		return func() tea.Msg {
			if edit != nil {
				data, _ := os.ReadFile(path)
				_ = os.WriteFile(path, []byte(edit(string(data))), 0o600)
			}
			return done(nil)
		}
	}
}

// openConfig puts the Config pane's keys under the next press.
func (f *fileHarness) openConfig() {
	f.t.Helper()
	i := f.m.paneIndexOf(paneConfig)
	if i < 0 {
		f.t.Fatal("no Config pane in the menu")
	}
	f.m.leftCursor, f.m.leftFocused = i, false
}

func (f *fileHarness) serviceNames() []string {
	var names []string
	for _, s := range f.m.manager.Services() {
		names = append(names, s.Info().Name)
	}
	return names
}

func appendTunnel(name, command string) func(string) string {
	return func(s string) string { return s + "  - name: " + name + "\n    command: " + command + "\n" }
}

const oneTunnel = "tunnels:\n  - name: a\n    command: ssh a\n"

func TestEditingDenYamlAppliesItWhenTheEditorCloses(t *testing.T) {
	f := newFileHarness(t, oneTunnel)
	f.editor(appendTunnel("b", "ssh b"))
	f.openConfig()
	f.press("e")

	if got := f.serviceNames(); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("services after the edit = %v, want [a b]", got)
	}
	if f.m.cfgPrompt != nil || !strings.Contains(f.m.flashMsg, "config reloaded — 2 services") {
		t.Fatalf("a clean edit applies without asking; prompt = %v, flash = %q", f.m.cfgPrompt, f.m.flashMsg)
	}
	if !strings.Contains(plain(f.m), "name: b") {
		t.Error("the Config pane should show the file as edited")
	}
}

func TestClosingTheEditorWithoutAChangeDoesNothing(t *testing.T) {
	f := newFileHarness(t, oneTunnel)
	f.editor(nil)
	f.openConfig()
	f.press("e")
	if f.applies != 0 || f.m.flashMsg != "den.yaml unchanged" {
		t.Fatalf("applies = %d, flash = %q; an untouched file is not applied again", f.applies, f.m.flashMsg)
	}
}

func TestABrokenEditKeepsTheRunningConfigAndCanBeUndone(t *testing.T) {
	f := newFileHarness(t, oneTunnel)
	a := f.svcs["a"]
	f.press("right", "c") // a is connected
	original := f.read()

	f.editor(func(s string) string { return s + "  - name: b\n" }) // no command
	f.openConfig()
	f.press("e")

	if f.m.cfgPrompt == nil || !f.m.cfgPrompt.failed() {
		t.Fatal("an edit with an error must open the pop-up that says so")
	}
	view := plain(f.m)
	for _, want := range []string{"den.yaml has 1 error", "keeps running the last good version", `tunnel[1] "b": command is required`, "edit again", "undo the edit"} {
		if !strings.Contains(view, want) {
			t.Errorf("pop-up lacks %q:\n%s", want, view)
		}
	}
	if got := f.serviceNames(); !slices.Equal(got, []string{"a"}) || a.State().Status != service.StatusConnected {
		t.Fatalf("a broken file must change nothing: services %v, a %s", got, a.State().Status)
	}

	f.press("x") // not one of the pop-up's keys: it stays, and x reaches nothing behind it
	if f.m.cfgPrompt == nil {
		t.Fatal("a key the pop-up does not use closed it")
	}
	f.press("u")
	if f.read() != original {
		t.Fatalf("u should put den.yaml back as it was:\n%s", f.read())
	}
	if f.m.cfgPrompt != nil || f.m.cfgProblems.Errors() != 0 || !strings.HasPrefix(f.m.flashMsg, "den.yaml restored") {
		t.Fatalf("after the undo: prompt %v, problems %v, flash %q", f.m.cfgPrompt, f.m.cfgProblems, f.m.flashMsg)
	}
	if _, stops, _ := a.counts(); stops != 0 {
		t.Fatal("undoing to the running config must not touch a's connection")
	}
}

func TestClosingTheErrorPopUpLeavesTheProblemsInSight(t *testing.T) {
	f := newFileHarness(t, oneTunnel)
	f.editor(func(s string) string { return s + "  - name: b\n" })
	f.openConfig()
	f.press("e", "esc")

	if f.m.cfgPrompt != nil {
		t.Fatal("esc should close the pop-up")
	}
	f.m.flashMsg = ""
	if got := f.m.renderStatusBar(); !strings.Contains(got, "den.yaml has 1 error, den runs the last good version") {
		t.Errorf("status bar = %q", got)
	}
	if got := f.m.configPane.Problems(); got.Errors() != 1 || got[0].Line != 5 {
		t.Errorf("Config pane problems = %v, want the error on line 5", got)
	}
	if !strings.Contains(ansi.Strip(f.m.renderLeft(30)), "Config ●") {
		t.Error("the Config menu entry should carry a dot while den.yaml has problems")
	}

	// Fixed in the editor, the file applies and the problems go away.
	f.editor(func(s string) string { return s + "    command: ssh b\n" })
	f.press("e")
	if f.m.cfgProblems.Errors() != 0 || !slices.Equal(f.serviceNames(), []string{"a", "b"}) {
		t.Fatalf("after the fix: problems %v, services %v", f.m.cfgProblems, f.serviceNames())
	}
}

// "e: edit again" keeps what the file held before the first edit, so u
// undoes them all rather than going back to a broken one.
func TestEditAgainKeepsTheOriginalForUndo(t *testing.T) {
	f := newFileHarness(t, oneTunnel)
	original := f.read()
	f.editor(func(s string) string { return s + "  - name: b\n" })
	f.openConfig()
	f.press("e")
	f.editor(func(s string) string { return s + "  - name: c\n" })
	f.press("e") // edit again, still broken
	if f.m.cfgPrompt == nil || f.m.cfgPrompt.problems.Errors() != 2 {
		t.Fatalf("prompt = %+v, want both broken tunnels", f.m.cfgPrompt)
	}
	f.press("u")
	if f.read() != original {
		t.Fatalf("u restored a file that is not the original:\n%s", f.read())
	}
}

func TestAReloadThatStopsAConnectionAsksFirst(t *testing.T) {
	f := newFileHarness(t, oneTunnel)
	a := f.svcs["a"]
	f.press("right", "c")
	f.editor(func(s string) string { return strings.Replace(s, "ssh a", "ssh -v a", 1) })
	f.openConfig()
	f.press("e")

	if f.m.cfgPrompt == nil || f.m.cfgPrompt.failed() || !strings.Contains(plain(f.m), "• a") {
		t.Fatalf("changing a running tunnel should ask first, naming it:\n%s", plain(f.m))
	}
	f.press("n")
	if _, stops, _ := a.counts(); stops != 0 || f.applies != 0 {
		t.Fatal("n must leave the running connection alone")
	}
	f.m.flashMsg = ""
	if got := f.m.renderStatusBar(); !strings.Contains(got, "den.yaml not applied: it stops a — R applies it") {
		t.Errorf("status bar = %q", got)
	}

	f.press("R")
	if f.m.cfgPrompt == nil {
		t.Fatal("R should ask again")
	}
	f.press("y")
	if _, stops, _ := a.counts(); stops != 1 || f.svcs["a"] == a {
		t.Fatal("y should stop a and build it again from its new entry")
	}
	if f.m.configStatus() != "" {
		t.Errorf("status = %q after applying", f.m.configStatus())
	}
}

// Adding a panel to menu_order shows it at once, without a restart.
func TestMenuOrderAppliesOnReload(t *testing.T) {
	f := newFileHarness(t, "menu_order: [connect, config]\n"+oneTunnel)
	if f.m.paneIndexOf(paneRunbooks) >= 0 {
		t.Fatal("runbooks is not in menu_order yet")
	}
	f.editor(func(s string) string {
		return strings.Replace(s, "[connect, config]", "[connect, runbooks, config]", 1)
	})
	f.openConfig()
	f.press("e")

	if f.m.paneIndexOf(paneRunbooks) != 1 {
		t.Fatalf("menu after the reload = %v, want runbooks second", f.m.menuOrder)
	}
	if f.m.currentPane() != paneConfig || f.m.leftFocused {
		t.Fatal("the cursor should stay on the Config pane it was on")
	}

	// Taking away the pane the cursor is on hands the keyboard to the menu.
	f.press("left", "up")
	if f.m.currentPane() != paneRunbooks {
		t.Fatalf("on %s, want runbooks", f.m.currentPane())
	}
	f.m.leftFocused = false
	f.write(t, strings.Replace(f.read(), "runbooks, ", "", 1))
	f.press("R")
	if f.m.paneIndexOf(paneRunbooks) >= 0 || !f.m.leftFocused || f.m.currentPane() == "" {
		t.Fatalf("menu %v, cursor %d, focused %v", f.m.menuOrder, f.m.leftCursor, f.m.leftFocused)
	}
}

func TestWarningsApplyAndShowInTheConfigPane(t *testing.T) {
	f := newFileHarness(t, oneTunnel)
	f.editor(func(s string) string { return s + "    reconect: true\n" })
	f.openConfig()
	f.press("e")

	if f.m.cfgPrompt != nil || f.applies != 1 {
		t.Fatalf("a file with warnings only is applied without a pop-up; prompt %v", f.m.cfgPrompt)
	}
	if !f.m.flashWarn || !strings.Contains(f.m.flashMsg, "1 warning, see Config") {
		t.Errorf("flash = %q", f.m.flashMsg)
	}
	if screen := plain(f.m); !strings.Contains(screen, `unknown key "reconect", ignored`) || !strings.Contains(screen, `→ did you mean "reconnect"?`) {
		t.Errorf("the Config pane should list the warning with its hint:\n%s", screen)
	}
}

func TestStartingWithABrokenDenYamlOpensTheConfigPane(t *testing.T) {
	f := newFileHarness(t, "tunnels:\n  - name: a\n")
	if f.m.cfg != nil || len(f.m.manager.Services()) != 0 {
		t.Fatal("a broken file must not be applied at startup")
	}
	if f.m.currentPane() != paneConfig || f.m.cfgPrompt == nil {
		t.Fatalf("den should open on the Config pane with the problems shown; pane %s", f.m.currentPane())
	}
	if !strings.Contains(plain(f.m), "den runs without it until it is fixed") {
		t.Errorf("pop-up:\n%s", plain(f.m))
	}

	f.editor(func(s string) string { return s + "    command: ssh a\n" })
	f.press("e")
	if f.m.cfg == nil || !slices.Equal(f.serviceNames(), []string{"a"}) || f.m.cfgProblems.Errors() != 0 {
		t.Fatalf("the fix should apply: cfg %v, services %v, problems %v", f.m.cfg, f.serviceNames(), f.m.cfgProblems)
	}
}

// config.PaneNames is what the check and the schema accept; the menu must
// have exactly those panels, in that default order.
func TestPaneNamesAreTheMenusPanels(t *testing.T) {
	var ids []string
	for _, id := range defaultMenuOrder {
		ids = append(ids, string(id))
	}
	if !slices.Equal(ids, config.PaneNames) {
		t.Errorf("defaultMenuOrder = %v, config.PaneNames = %v", ids, config.PaneNames)
	}
	if len(paneMetadata) != len(config.PaneNames) {
		t.Errorf("%d panes have metadata, config.PaneNames has %d", len(paneMetadata), len(config.PaneNames))
	}
}

func headerRow(m *AppModel) string {
	return strings.SplitN(plain(m), "\n", 2)[0]
}

func TestHeaderShowsTheTitle(t *testing.T) {
	f := newFileHarness(t, oneTunnel)
	if got := headerRow(f.m); !strings.Contains(got, config.DefaultTitle) {
		t.Fatalf("header = %q, want the default title", got)
	}

	f2 := newFileHarness(t, "title: Acme platform\n"+oneTunnel)
	got := headerRow(f2.m)
	if !strings.Contains(got, "Acme platform") || strings.Contains(got, "Navigator") {
		t.Fatalf("header = %q, want the configured title only", got)
	}
}

func TestOverlongTitleStaysOneRow(t *testing.T) {
	f := newFileHarness(t, "title: "+strings.Repeat("long ", 60)+"\n"+oneTunnel)
	f.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := plain(f.m)
	if rows := strings.Count(view, "\n") + 1; rows > 24 {
		t.Fatalf("frame has %d rows on a 24-row terminal", rows)
	}
	row := headerRow(f.m)
	if !strings.HasSuffix(strings.TrimRight(row, " "), "…") {
		t.Fatalf("header = %q, want one row cut with …", row)
	}
	if second := strings.Split(view, "\n")[1]; strings.Contains(second, "long") {
		t.Fatalf("title wrapped onto the second row: %q", second)
	}
}

func TestTitleAppliesOnReload(t *testing.T) {
	f := newFileHarness(t, oneTunnel)
	f.editor(func(s string) string { return "title: Acme\n" + s })
	f.openConfig()
	f.press("e")

	if got := headerRow(f.m); !strings.Contains(got, "Acme") || strings.Contains(got, "Navigator") {
		t.Fatalf("header after the reload = %q, want Acme", got)
	}
}

// junkKeys are n keys den does not know, one warning each, on lines 5 to 4+n
// of a file that is the aws: header line, then oneTunnel.
func junkKeys(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "    junk%d: 1\n", i)
	}
	return b.String()
}

func TestStartingWithWarningsSaysSoOnceAndCountsThemInTheMenu(t *testing.T) {
	f := newFileHarness(t, oneTunnel+junkKeys(3))
	if f.m.cfgPrompt != nil {
		t.Fatal("warnings alone open no pop-up")
	}
	if !f.m.flashWarn || f.m.flashMsg != "den.yaml: 3 warnings — p in Config lists them" {
		t.Errorf("flash = %q (warn %v)", f.m.flashMsg, f.m.flashWarn)
	}
	if !strings.Contains(plain(f.m), "den.yaml: 3 warnings — p in Config lists them") {
		t.Errorf("the status bar should show it:\n%s", plain(f.m))
	}
	if !strings.Contains(ansi.Strip(f.m.renderLeft(30)), "Config ● 3") {
		t.Errorf("the menu should count the problems:\n%s", ansi.Strip(f.m.renderLeft(30)))
	}
	// Once: the flash times out, and nothing brings it back.
	f.send(msgs.TickMsg{Time: time.Now().Add(time.Minute)})
	f.send(msgs.TickMsg{Time: time.Now().Add(2 * time.Minute)})
	if f.m.flashMsg != "" {
		t.Errorf("flash = %q after it timed out", f.m.flashMsg)
	}

	clean := newFileHarness(t, oneTunnel)
	if clean.m.flashMsg != "" || strings.Contains(ansi.Strip(clean.m.renderLeft(30)), "●") {
		t.Errorf("a clean file has no flash and no dot: %q", clean.m.flashMsg)
	}
}

func TestStartingWithErrorsDoesNotFlashWarnings(t *testing.T) {
	f := newFileHarness(t, "tunnels:\n  - name: a\n"+junkKeys(1))
	if f.m.flashMsg != "" {
		t.Errorf("the pop-up speaks for errors, flash = %q", f.m.flashMsg)
	}
}

func TestEveryWarningIsReachableInTheConfigPaneAt80x24(t *testing.T) {
	f := newFileHarness(t, oneTunnel+junkKeys(12))
	f.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	f.openConfig()
	f.press("p")

	seen := map[int]bool{}
	note := func() {
		view := plain(f.m)
		for i := 1; i <= 12; i++ {
			if strings.Contains(view, fmt.Sprintf(`unknown key "junk%d"`, i)) {
				seen[i] = true
			}
		}
	}
	note()
	for range 12 {
		f.press("down")
		note()
	}
	if len(seen) != 12 {
		t.Errorf("%d of 12 warnings reachable by scrolling at 80×24:\n%s", len(seen), plain(f.m))
	}

	// enter lands on the line the cursor problem is on: the last one, line 16.
	f.press("enter")
	view := plain(f.m)
	if !strings.Contains(view, "▶ 16  ") || !strings.Contains(view, "junk12: 1") {
		t.Errorf("enter should show line 16 marked:\n%s", view)
	}
	if f.m.leftFocused {
		t.Error("enter in the list stays in the pane")
	}

	// n and N walk the marked lines; the arrow follows.
	f.press("n")
	if !strings.Contains(plain(f.m), "▶  5  ") {
		t.Errorf("n after the last mark goes round to the first (line 5):\n%s", plain(f.m))
	}
}

func TestEscClosesTheProblemListBeforeLeavingThePane(t *testing.T) {
	f := newFileHarness(t, oneTunnel+junkKeys(2))
	f.openConfig()
	f.press("p")
	if !strings.Contains(plain(f.m), "1 of 2") {
		t.Fatalf("p should open the list:\n%s", plain(f.m))
	}
	f.press("esc")
	if f.m.leftFocused || strings.Contains(plain(f.m), "1 of 2") {
		t.Fatalf("esc closes the list and stays in the pane; leftFocused %v", f.m.leftFocused)
	}
	f.press("esc")
	if !f.m.leftFocused {
		t.Error("esc in the file returns to the menu")
	}
}

func TestHOpensTheConfigPanesShortcuts(t *testing.T) {
	f := newFileHarness(t, oneTunnel+junkKeys(1))
	f.openConfig()
	f.press("h")
	view := plain(f.m)
	if !f.m.shortcuts.Open() || !strings.Contains(view, "List every problem") {
		t.Errorf("h should open the shortcuts:\n%s", view)
	}
}

func TestTheErrorPopUpSaysWhereTheRestOfTheProblemsAre(t *testing.T) {
	f := newFileHarness(t, oneTunnel)
	f.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	f.editor(func(s string) string {
		for i := range 30 {
			s += fmt.Sprintf("  - name: b%d\n", i) // no command
		}
		return s
	})
	f.openConfig()
	f.press("e")
	if f.m.cfgPrompt == nil {
		t.Fatal("no pop-up")
	}
	if view := plain(f.m); !strings.Contains(view, "more — p in Config lists them") {
		t.Errorf("the pop-up lists what fits and says where the rest is:\n%s", view)
	}
	// And it is true: the pane lists them all.
	f.press("esc")
	f.openConfig()
	f.press("p", "end")
	if view := plain(f.m); !strings.Contains(view, "30 of 30") {
		t.Errorf("the list should hold all 30:\n%s", view)
	}
}
