//go:build windows

package termsession

import (
	"os"
	"strings"
	"testing"
	"time"
)

// CI has no Windows runner, so these run only when someone runs the tests on
// Windows. They cover what differs there: ConPTY instead of a pty, and a
// process that exec.Cmd.Wait cannot wait for.

func TestWindowsOutputAndExit(t *testing.T) {
	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		comspec = `C:\Windows\System32\cmd.exe`
	}
	s, err := Start(Command{Path: comspec, Args: []string{"/c", "echo hello-den"}}, 80, 24)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Close)

	select {
	case <-s.Done():
	case <-time.After(10 * time.Second):
		t.Fatalf("the session did not end; screen:\n%s", s.View())
	}
	if view := s.View(); !strings.Contains(view, "hello-den") {
		t.Fatalf("the program's output is not on screen:\n%s", view)
	}
	if err := s.ExitErr(); err != nil {
		t.Fatalf("ExitErr = %v after a clean exit", err)
	}
}

func TestWindowsCloseStopsARunningProcess(t *testing.T) {
	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		comspec = `C:\Windows\System32\cmd.exe`
	}
	s, err := Start(Command{Path: comspec}, 80, 24)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	s.Close()
	select {
	case <-s.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not end an interactive cmd.exe")
	}
}
