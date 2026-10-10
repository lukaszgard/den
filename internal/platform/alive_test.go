package platform

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

func TestProcessAlive(t *testing.T) {
	if !ProcessAlive(os.Getpid()) {
		t.Fatal("this process reads as gone")
	}
	for _, pid := range []int{0, -1} {
		if ProcessAlive(pid) {
			t.Errorf("ProcessAlive(%d) = true", pid)
		}
	}
	name, args := "sh", []string{"-c", "exit 0"}
	if runtime.GOOS == "windows" {
		name, args = "cmd", []string{"/C", "exit 0"}
	}
	cmd := exec.Command(name, args...)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	// Run has reaped it: what is left of its pid is no process at all.
	if ProcessAlive(cmd.Process.Pid) {
		t.Errorf("a finished child (pid %d) reads as alive", cmd.Process.Pid)
	}
}
