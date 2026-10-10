//go:build !windows

package termsession

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// These tests run real programs on a real pty: what they guard — that the
// child has a terminal, sees its size, gets its keys and is cleaned up — is
// exactly what a fake would assume.

const wait = 5 * time.Second

func shell(t *testing.T, script string, env ...string) *Session {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on this machine")
	}
	s, err := Start(Command{Path: sh, Args: []string{"-c", script}, Env: env}, 80, 24)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// waitView waits until the screen satisfies ok, waking on Changed the way the
// TUI does.
func waitView(t *testing.T, s *Session, what string, ok func(view string) bool) string {
	t.Helper()
	deadline := time.After(wait)
	for {
		view := s.View()
		if ok(view) {
			return view
		}
		select {
		case <-s.Changed():
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timed out waiting for %s; screen:\n%s", what, view)
		}
	}
}

func waitText(t *testing.T, s *Session, text string) string {
	t.Helper()
	return waitView(t, s, "“"+text+"” on screen", func(v string) bool { return strings.Contains(v, text) })
}

func waitDone(t *testing.T, s *Session) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(wait):
		t.Fatalf("the session did not end; screen:\n%s", s.View())
	}
}

func TestOutputReachesTheScreen(t *testing.T) {
	s := shell(t, `printf 'hello-den'; read x`)
	waitText(t, s, "hello-den")
}

func TestViewHasOneLinePerRow(t *testing.T) {
	s := shell(t, `printf 'one\ntwo'; read x`)
	view := waitText(t, s, "two")
	if got := strings.Count(view, "\n") + 1; got != 24 {
		t.Fatalf("view has %d lines, want the 24 rows of the terminal:\n%s", got, view)
	}
}

func TestTextAndKeysReachTheProcess(t *testing.T) {
	s := shell(t, `read x; echo "got:$x"`)
	s.SendText("abc")
	s.SendKey(vt.KeyPressEvent{Code: vt.KeyEnter})
	waitText(t, s, "got:abc")
}

func TestPasteReachesTheProcess(t *testing.T) {
	s := shell(t, `read x; echo "got:$x"`)
	s.Paste("pasted words")
	s.SendKey(vt.KeyPressEvent{Code: vt.KeyEnter})
	waitText(t, s, "got:pasted words")
}

func TestEnvReachesTheProcess(t *testing.T) {
	s := shell(t, `echo "secret=$DEN_TEST_SECRET term=$TERM home=${HOME:+set}"; read x`, "DEN_TEST_SECRET=s3cr3t")
	view := waitText(t, s, "secret=s3cr3t")
	// The client draws for the emulator, not for whatever terminal den runs in.
	if !strings.Contains(view, "term=xterm-256color") {
		t.Fatalf("TERM is not the emulator's:\n%s", view)
	}
	if !strings.Contains(view, "home=set") {
		t.Fatalf("den's own environment was not inherited:\n%s", view)
	}
}

// den's own LINES and COLUMNS describe the terminal den runs in. A pager that
// trusts them over the tab's real size would draw for the wrong one.
// A runbook's terminal opens in the runbook's folder.
func TestDirIsWhereTheProcessStarts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runbook-home")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	// The base name only: a temp path is wider than the 80-column screen.
	s, err := Start(Command{Path: "/bin/sh", Args: []string{"-c", `echo "in:$(basename "$PWD")"; sleep 5`}, Dir: dir}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	waitText(t, s, "in:runbook-home")
}

func TestOuterTerminalSizeIsNotInherited(t *testing.T) {
	t.Setenv("LINES", "7")
	t.Setenv("COLUMNS", "7")
	s := shell(t, `env | grep -cE '^(LINES|COLUMNS)=' ; echo "counted"; read x`)
	view := waitText(t, s, "counted")
	if !strings.HasPrefix(strings.TrimSpace(view), "0") {
		t.Fatalf("the program inherited den's LINES/COLUMNS:\n%s", view)
	}
}

func TestProcessSeesTheTerminalSize(t *testing.T) {
	s := shell(t, `stty size; read x; stty size; read x`)
	waitText(t, s, "24 80")
	s.Resize(100, 30)
	s.SendKey(vt.KeyPressEvent{Code: vt.KeyEnter})
	view := waitText(t, s, "30 100")
	if got := strings.Count(view, "\n") + 1; got != 30 {
		t.Fatalf("view has %d lines after the resize, want 30", got)
	}
}

// Without a controlling terminal ctrl+c would be a byte on stdin, not an
// interrupt, and a runaway query could only be stopped by closing the tab.
//
// The script execs, so one process with the default disposition takes the
// signal whenever it arrives. A trap around a forked sleep would not do: a
// signal landing in the fork before it becomes sleep is swallowed there, and
// the shell then waits out the sleep.
func TestCtrlCInterruptsTheForegroundJob(t *testing.T) {
	s := shell(t, `echo ready; exec sleep 600`)
	waitText(t, s, "ready")
	s.SendKey(vt.KeyPressEvent{Code: 'c', Mod: vt.ModCtrl})
	waitDone(t, s)
	var exit *exec.ExitError
	if err := s.ExitErr(); !errors.As(err, &exit) {
		t.Fatalf("ExitErr = %v, want the interrupt", err)
	}
	if ws, ok := exit.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Fatalf("the job ended with %v, want it killed by SIGINT", exit)
	}
}

func TestExitEndsTheSessionAndKeepsTheScreen(t *testing.T) {
	s := shell(t, `echo 'FATAL: PAM authentication failed'; exit 3`)
	waitDone(t, s)
	if s.Alive() {
		t.Fatal("Alive after the process exited")
	}
	var exit *exec.ExitError
	if err := s.ExitErr(); !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("ExitErr = %v, want exit status 3", err)
	}
	if view := s.View(); !strings.Contains(view, "PAM authentication failed") {
		t.Fatalf("the last screen is gone after the exit:\n%s", view)
	}
}

func TestCleanExitHasNoError(t *testing.T) {
	s := shell(t, `exit 0`)
	waitDone(t, s)
	if err := s.ExitErr(); err != nil {
		t.Fatalf("ExitErr = %v after exit 0", err)
	}
}

func TestChangedFiresWhenTheProcessExits(t *testing.T) {
	s := shell(t, `read x`)
	// Drain what starting up signalled, so the next signal is the exit's.
	time.Sleep(100 * time.Millisecond)
	select {
	case <-s.Changed():
	default:
	}
	s.SendKey(vt.KeyPressEvent{Code: vt.KeyEnter})
	deadline := time.After(wait)
	for s.Alive() {
		select {
		case <-s.Changed():
		case <-deadline:
			t.Fatal("nothing signalled the exit: a waiter would sleep through it")
		}
	}
}

func TestCloseStopsARunningProcess(t *testing.T) {
	s := shell(t, `echo ready; sleep 600`)
	waitText(t, s, "ready")
	s.Close()
	waitDone(t, s)
	s.Close() // closing twice is harmless
}

// psql's pager and editor are its children; closing the tab must not leave
// them behind holding the terminal.
func TestCloseStopsTheProcessGroup(t *testing.T) {
	s := shell(t, `sh -c 'echo child:$$; sleep 600' & wait`)
	view := waitText(t, s, "child:")
	pid := strings.Fields(view[strings.Index(view, "child:")+len("child:"):])[0]
	s.Close()
	waitDone(t, s)
	deadline := time.Now().Add(wait)
	for exec.Command("kill", "-0", pid).Run() == nil {
		if time.Now().After(deadline) {
			t.Fatalf("child %s outlived the session", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestInputAfterTheExitIsDropped(t *testing.T) {
	s := shell(t, `exit 0`)
	waitDone(t, s)
	done := make(chan struct{})
	go func() {
		s.SendText("typed into a dead terminal")
		s.SendKey(vt.KeyPressEvent{Code: vt.KeyEnter})
		s.Paste("pasted too")
		s.Resize(90, 20)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(wait):
		t.Fatal("input to an exited session blocked: the TUI would freeze")
	}
}

// The cursor is the terminal's own, placed by the view from Cursor, so the
// screen carries no reverse-video cell for it.
func TestCursorIsReportedWhileTheProcessRuns(t *testing.T) {
	const reverse = "\x1b[7m"
	s := shell(t, `printf 'prompt> '; read x`)
	view := waitText(t, s, "prompt>")
	if strings.Contains(view, reverse) {
		t.Fatalf("the screen draws a cursor of its own:\n%q", view)
	}
	c, ok := s.Cursor()
	if !ok || c.X != len("prompt> ") || c.Y != 0 {
		t.Fatalf("cursor = %+v, %v; want it after the prompt, at column 8 of row 0", c, ok)
	}
	if c.Style != vt.CursorBlock || !c.Blink {
		t.Fatalf("cursor = %+v; a program that asked for nothing gets a blinking block", c)
	}
	s.SendKey(vt.KeyPressEvent{Code: vt.KeyEnter})
	waitDone(t, s)
	if c, ok := s.Cursor(); ok {
		t.Fatalf("a cursor %+v is still reported after the exit", c)
	}
}

// A program picks the shape (vim's bar in insert mode) and can hide the
// cursor.
func TestCursorFollowsWhatTheProgramAsksFor(t *testing.T) {
	s := shell(t, `printf '\033[6 qbar'; read x; printf '\033[?25lhidden'; read y`)
	waitText(t, s, "bar")
	c, ok := s.Cursor()
	if !ok || c.Style != vt.CursorBar || c.Blink {
		t.Fatalf("cursor = %+v, %v; want a steady bar", c, ok)
	}
	s.SendKey(vt.KeyPressEvent{Code: vt.KeyEnter})
	waitText(t, s, "hidden")
	if c, ok := s.Cursor(); ok {
		t.Fatalf("cursor %+v reported while the program hides it", c)
	}
	s.SendKey(vt.KeyPressEvent{Code: vt.KeyEnter})
	waitDone(t, s)
}

func TestStartReportsAMissingProgram(t *testing.T) {
	if _, err := Start(Command{Path: "/nonexistent/den-test-client"}, 80, 24); err == nil {
		t.Fatal("Start of a missing program returned no error")
	}
}

func waitFullScreen(t *testing.T, s *Session, want bool) {
	t.Helper()
	deadline := time.After(wait)
	for s.FullScreen() != want {
		select {
		case <-s.Changed():
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatalf("FullScreen stayed %v, want %v; screen:\n%s", !want, want, s.View())
		}
	}
}

// An editor or a pager takes the whole terminal and every key with it, esc
// above all. A view that wants esc for itself has to know when that is so.
func TestFullScreenFollowsTheProgram(t *testing.T) {
	// What vim and less send around their screen.
	s := shell(t, `read x; printf '\033[?1049h'; read x; printf '\033[?1049l'; read x`)
	if s.FullScreen() {
		t.Fatal("a program at its prompt is not full-screen")
	}
	s.SendKey(vt.KeyPressEvent{Code: vt.KeyEnter})
	waitFullScreen(t, s, true)
	s.SendKey(vt.KeyPressEvent{Code: vt.KeyEnter})
	waitFullScreen(t, s, false)
}

// interactive starts a shell with job control, as a user's shell in a tab is:
// what it runs is a job of its own, in front of it on the terminal.
func interactive(t *testing.T) *Session {
	t.Helper()
	s, err := Start(Command{Path: "/bin/sh", Args: []string{"-i"}, Env: []string{"PS1=job$ ", "ENV=", "HISTFILE="}}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	waitText(t, s, "job$")
	return s
}

// A pager started with -X keeps the normal screen but switches the arrows to
// application mode: a job doing that has every key, esc included.
func TestAJobThatSwitchesTheArrowsIsFullScreen(t *testing.T) {
	s := interactive(t)
	s.SendText(`sh -c "printf '\033[?1h\033='; read x; printf '\033[?1l\033>'"`)
	s.SendKey(vt.KeyPressEvent{Code: vt.KeyEnter})
	waitFullScreen(t, s, true)
	s.SendKey(vt.KeyPressEvent{Code: vt.KeyEnter})
	waitFullScreen(t, s, false)
}

// zsh with oh-my-zsh switches the arrows to application mode at every prompt
// (echoti smkx). That is the shell reading a line, not an editor: esc has to
// stay den's, as it is at psql's prompt.
func TestAPromptThatSwitchesTheArrowsIsNotFullScreen(t *testing.T) {
	s := shell(t, `printf '\033[?1h\033=ready'; read x`)
	waitText(t, s, "ready")
	if s.FullScreen() {
		t.Fatal("the program itself at its prompt counts as full-screen because it switched the arrows")
	}
}

func TestExitedProgramIsNotFullScreen(t *testing.T) {
	s := shell(t, `printf '\033[?1049h'; exit 0`)
	waitDone(t, s)
	if s.FullScreen() {
		t.Fatal("a program that exited on the alternate screen still counts as full-screen")
	}
}
