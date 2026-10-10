//go:build !windows

package process

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child into its own process group so that the whole
// tree it spawns — a shell plus every command it runs — can be signalled as a
// unit. Without this, killing `sh -c "aws ... | grep ..."` leaves the aws and
// grep processes orphaned and still running.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalGroup delivers sig to the child's entire process group, falling back to
// the process alone when it never became a group leader.
func signalGroup(cmd *exec.Cmd, sig os.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	ssig, ok := sig.(syscall.Signal)
	if !ok {
		return cmd.Process.Signal(sig)
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil || pgid != cmd.Process.Pid {
		return cmd.Process.Signal(sig)
	}
	return syscall.Kill(-pgid, ssig)
}

// killGroup force-kills the child's process group.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil || pgid != cmd.Process.Pid {
		return cmd.Process.Kill()
	}
	return syscall.Kill(-pgid, syscall.SIGKILL)
}

// groupAlive reports whether any process of group pgid still exists. The group
// outlives its leader while anything the leader spawned is still running.
func groupAlive(pgid int) bool {
	return pgid > 0 && syscall.Kill(-pgid, 0) == nil
}

// killGroupID force-kills every remaining member of group pgid. Unlike
// killGroup it does not need the leader to be alive to find the group.
func killGroupID(pgid int) error {
	if pgid <= 0 {
		return nil
	}
	return syscall.Kill(-pgid, syscall.SIGKILL)
}

// groupID is the process group a child started with setProcessGroup leads.
func groupID(cmd *exec.Cmd) int {
	if cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}
