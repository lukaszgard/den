//go:build windows

package process

import (
	"os"
	"os/exec"
)

// setProcessGroup is a no-op: Windows has no POSIX process groups.
func setProcessGroup(cmd *exec.Cmd) {}

// signalGroup always fails on Windows, which cannot deliver SIGINT to another
// process. Callers treat the error as "graceful stop unavailable" and fall
// straight through to killGroup.
func signalGroup(cmd *exec.Cmd, sig os.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(sig)
}

// killGroup force-kills the child process.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// Windows has no process groups: there is never a group to wait for or kill.
func groupAlive(int) bool   { return false }
func killGroupID(int) error { return nil }
func groupID(*exec.Cmd) int { return 0 }
