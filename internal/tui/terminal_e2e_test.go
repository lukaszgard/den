//go:build !windows

package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/termsession"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/testkeys"
)

// The unit tests drive the model with key messages over fake terminals. This
// one runs the program itself: the test binary re-executed as den on a pty,
// with a real shell as the client in its terminal tabs. It is what catches a
// frame that does not survive a real terminal, a key that never arrives, or a
// client left behind when den quits.

const e2eAppEnv = "DEN_TERMINAL_E2E_APP"

// e2eApp is den without the startup update check, which would go to the network.
type e2eApp struct{ *AppModel }

func (a e2eApp) Init() tea.Cmd {
	return tea.Batch(tickCmd(tickInterval), waitForUpdate(a.manager))
}

func (a e2eApp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := a.AppModel.Update(msg)
	return e2eApp{m.(*AppModel)}, cmd
}

// TestHelperDen is not a test: it is den, started by TestTerminalTabsEndToEnd.
func TestHelperDen(t *testing.T) {
	if os.Getenv(e2eAppEnv) != "1" {
		return
	}
	svc := fakeTermSvc{newSvc(service.TypeRDS, "db")}
	if _, err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	awsCfg := filepath.Join(t.TempDir(), "aws-config")
	if err := os.WriteFile(awsCfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{AWS: &config.AWSSpec{ConfigPath: awsCfg, CredentialsPath: awsCfg}}
	m := New(managerWith(svc), Options{Config: cfg, Version: "test"})
	// The service's command, run as a shell instead of psql: what is under
	// test is the terminal, and a shell needs no database.
	m.startTerminal = func(cmd service.ClientCommand, cols, rows int) (liveTerminal, error) {
		s, err := termsession.Start(termsession.Command{
			Path: "/bin/sh",
			Env:  append(cmd.Env, "PS1=client> ", "ENV=", "HISTFILE="),
		}, cols, rows)
		if err != nil {
			return nil, err
		}
		return components.NewTermView(s), nil
	}
	if _, err := tea.NewProgram(e2eApp{m}).Run(); err != nil {
		t.Fatal(err)
	}
}

// screen is den as a user sees it: the outer terminal's text.
type screen struct {
	t *testing.T
	s *termsession.Session
}

func (sc screen) text() string { return ansi.Strip(sc.s.View()) }

func (sc screen) waitFor(what string, ok func(text string) bool) string {
	sc.t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		text := sc.text()
		if ok(text) {
			return text
		}
		select {
		case <-sc.s.Changed():
		case <-time.After(25 * time.Millisecond):
		case <-deadline:
			sc.t.Fatalf("timed out waiting for %s; den shows:\n%s", what, text)
		}
	}
}

func (sc screen) waitText(text string) string {
	sc.t.Helper()
	return sc.waitFor("“"+text+"”", func(s string) bool { return strings.Contains(s, text) })
}

func (sc screen) waitGone(text string) string {
	sc.t.Helper()
	return sc.waitFor("“"+text+"” to go away", func(s string) bool { return !strings.Contains(s, text) })
}

func (sc screen) typeText(text string) { sc.s.SendText(text) }
func (sc screen) enter()               { sc.s.SendKey(vt.KeyPressEvent{Code: vt.KeyEnter}) }
func (sc screen) esc()                 { sc.s.SendKey(vt.KeyPressEvent{Code: vt.KeyEscape}) }
func (sc screen) ctrl(r rune)          { sc.s.SendKey(vt.KeyPressEvent{Code: r, Mod: vt.ModCtrl}) }
func (sc screen) run(line string)      { sc.typeText(line); sc.enter() }

// assertFrame checks that den's frame starts at the top of the terminal: a
// view one line too tall loses its first lines without any error.
func (sc screen) assertFrame(rows int) {
	sc.t.Helper()
	lines := strings.Split(sc.text(), "\n")
	if len(lines) != rows {
		sc.t.Fatalf("the screen has %d lines, want %d", len(lines), rows)
	}
	if !strings.Contains(lines[0], "╭") {
		sc.t.Fatalf("line 0 = %q, want the detail box's top border:\n%s", lines[0], sc.text())
	}
}

func TestTerminalTabsEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("runs den on a pty")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh on this machine")
	}
	s, err := termsession.Start(termsession.Command{
		Path: os.Args[0],
		Args: []string{"-test.run=^TestHelperDen$"},
		// A home of its own: den must not read the developer's files.
		Env: []string{e2eAppEnv + "=1", "HOME=" + t.TempDir()},
	}, 120, 36)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	den := screen{t: t, s: s}

	// Into the detail view of the connected service.
	den.waitText("Developer Environment Navigator")
	den.typeText("l") // focus the Connect pane
	den.enter()
	den.waitText("t: terminal")
	den.assertFrame(36)

	// h opens the shortcuts over the view, in the same frame. esc closes them
	// and leaves the detail view open.
	den.typeText("h")
	den.waitText("Shortcuts — db")
	den.waitText("esc: close")
	den.assertFrame(36)
	den.esc()
	den.waitGone("Shortcuts — db")
	den.waitText("t: terminal")
	den.assertFrame(36)

	// t opens a client that is logged in: the credential is in its environment.
	den.typeText("t")
	den.waitText("client>")
	den.waitText("TERMINAL")
	den.run("echo sum=$((40+2)) pw=$PGPASSWORD")
	den.waitText("sum=42 pw=tok-1")
	den.assertFrame(36)

	// The keys den acts on are the client's while it has the keyboard: d
	// would disconnect, c connect, q quit.
	den.run("cat -v")
	den.typeText("dcq")
	den.enter()
	// Once echoed by the terminal as typed, once printed by cat.
	den.waitFor("the keys to reach the client", func(s string) bool { return strings.Count(s, "│dcq") == 2 })
	den.ctrl('d')

	// esc at the prompt is den's "back": it returns the keyboard and leaves
	// the detail view and the terminal on screen. enter types again.
	den.waitText("esc/ctrl+t")
	den.esc()
	text := den.waitGone("TERMINAL")
	if !strings.Contains(text, "1 psql") || !strings.Contains(text, "sum=42") {
		t.Fatalf("esc should leave the terminal on screen:\n%s", text)
	}
	den.enter()
	den.waitText("TERMINAL")

	// A full-screen program keeps its first esc. This one switches to the
	// alternate screen as an editor does; den asks for esc twice, and passes
	// a single one on.
	den.run(`printf '\033[?1049h'; cat -v`)
	den.waitText("esc esc/ctrl+t")
	den.esc()
	den.typeText("still-here")
	den.enter()
	den.waitText("^[still-here")
	den.waitText("TERMINAL")
	// A second esc right after the first is den's: only the first reaches
	// the program.
	den.esc()
	time.Sleep(50 * time.Millisecond)
	den.esc()
	den.waitGone("TERMINAL")
	den.enter()
	den.waitText("TERMINAL")
	den.typeText("after")
	den.enter()
	den.waitFor("one esc, then after", func(s string) bool {
		return strings.Contains(s, "^[after") && !strings.Contains(s, "^[^[after")
	})
	den.ctrl('d')
	den.run(`printf '\033[?1049l'`)
	den.waitText("esc/ctrl+t")
	den.waitText("sum=42")

	// ctrl+c interrupts the client's job, not den.
	den.run("sleep 600")
	den.waitFor("the sleep to be running", func(s string) bool { return strings.Contains(s, "sleep 600") })
	time.Sleep(200 * time.Millisecond)
	den.ctrl('c')
	den.run("echo after-interrupt")
	den.waitText("after-interrupt")

	// A second tab, renamed.
	den.ctrl('t')
	den.waitGone("TERMINAL")
	den.typeText("t")
	den.waitText("2 psql")
	den.run("echo in-second")
	den.waitText("in-second")
	den.ctrl('t')
	den.waitGone("TERMINAL")
	den.typeText("n")
	den.waitText("enter: save")
	den.typeText("-b")
	den.enter()
	den.waitText("2 psql-b")

	// A digit jumps back to the first client, whose screen is still there.
	den.typeText("1")
	text = den.waitText("TERMINAL")
	if !strings.Contains(text, "after-interrupt") || strings.Contains(text, "in-second") {
		t.Fatalf("tab 1 should show the first client's screen:\n%s", text)
	}

	// The client follows the size of the area den gives it.
	s.Resize(100, 30)
	den.run("stty size")
	den.waitFor("the client to report 96 columns", func(s string) bool { return regexp.MustCompile(`\b\d+ 96\b`).MatchString(s) })
	den.assertFrame(30)

	// An exited client keeps its tab and restarts in it.
	den.run("echo last-words; exit 3")
	den.waitText("exited: exit status 3")
	den.waitText("last-words")
	den.typeText("dxq") // type-ahead after the exit is not den's
	time.Sleep(150 * time.Millisecond)
	if text := den.text(); !strings.Contains(text, "exited: exit status 3") || !strings.Contains(text, "2 psql-b") {
		t.Fatalf("keys typed after the exit acted on den:\n%s", text)
	}
	den.enter()
	den.waitGone("exited: exit status 3")
	den.waitText("TERMINAL")
	den.run("echo restarted")
	den.waitText("restarted")

	// Leaving the detail view and coming back keeps the tabs.
	den.ctrl('t')
	den.waitGone("TERMINAL")
	den.esc()
	den.waitGone("2 psql-b")
	den.enter()
	den.waitText("2 psql-b")
	den.waitText("restarted")

	// d would stop both clients, so it asks; any key but y keeps them.
	den.typeText("d")
	den.waitText("disconnect db? it stops 2 running terminals")
	den.typeText("e")
	den.waitGone("disconnect db?")

	// Quit den, and both clients must be gone with it.
	var pids []string
	for _, tab := range []string{"1", "2"} {
		den.typeText(tab)
		den.waitText("TERMINAL")
		den.run("echo pid-" + tab + "=$$")
		pid := regexp.MustCompile("pid-" + tab + `=(\d+)`)
		text := den.waitFor("the client's pid", pid.MatchString)
		pids = append(pids, pid.FindStringSubmatch(text)[1])
		den.ctrl('t')
		den.waitGone("TERMINAL")
	}
	den.esc()
	den.waitGone("2 psql-b")
	den.typeText("q")
	select {
	case <-s.Done():
	case <-time.After(15 * time.Second):
		t.Fatalf("den did not quit; it shows:\n%s", den.text())
	}
	deadline := time.Now().Add(10 * time.Second)
	for _, pid := range pids {
		for exec.Command("kill", "-0", pid).Run() == nil {
			if time.Now().After(deadline) {
				t.Fatalf("client %s outlived den", pid)
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
}

// startClient is replaced in every other test. This runs the real one: a
// catalog tool found on this machine and started on a pty.
func TestStartClientRunsACatalogTool(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no ssh on this machine")
	}
	term, err := startClient(service.ClientCommand{Tool: "ssh", Args: []string{"-V"}}, 80, 24)
	if err != nil {
		t.Fatalf("startClient: %v", err)
	}
	t.Cleanup(term.Close)
	deadline := time.After(15 * time.Second)
	for term.Alive() {
		select {
		case <-term.Changed():
		case <-deadline:
			t.Fatalf("ssh -V did not finish; screen:\n%s", ansi.Strip(term.View()))
		}
	}
	if view := ansi.Strip(term.View()); !strings.Contains(view, "OpenSSH") {
		t.Fatalf("the tool's output is not on screen:\n%s", view)
	}
}

func TestStartClientNamesAMissingTool(t *testing.T) {
	_, err := startClient(service.ClientCommand{Tool: "den-test-no-such-client"}, 80, 24)
	if err == nil || !strings.Contains(err.Error(), "den-test-no-such-client not found") {
		t.Fatalf("err = %v, want it to name the missing client", err)
	}
}

// A shell tab runs the user's shell, which is no tool of the catalog, with
// what the service put into its environment.
func TestShellToolStartsTheUsersShell(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a shell on a pty")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh on this machine")
	}
	t.Setenv("SHELL", "/bin/sh")

	term, err := startClient(service.ClientCommand{
		Tool: service.ShellTool,
		Env:  []string{"DEN_URL=http://localhost:59200", "PS1=shell> ", "ENV=", "HISTFILE="},
	}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(term.Close)

	term.HandleKey(testkeys.Text("echo url=$DEN_URL"))
	term.HandleKey(testkeys.Press("enter"))
	deadline := time.After(15 * time.Second)
	for !strings.Contains(ansi.Strip(term.View()), "url=http://localhost:59200") {
		select {
		case <-term.Changed():
		case <-time.After(25 * time.Millisecond):
		case <-deadline:
			t.Fatalf("the shell never printed DEN_URL; it shows:\n%s", ansi.Strip(term.View()))
		}
	}
}
