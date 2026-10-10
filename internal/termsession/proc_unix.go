//go:build !windows

package termsession

import (
	"os/exec"
	"syscall"

	"github.com/charmbracelet/x/xpty"
	"golang.org/x/sys/unix"
)

// sessionAttr makes the program the leader of a new session with the pty as
// its controlling terminal. Without that, ctrl+c is a byte on stdin instead of
// an interrupt, the program is not told about resizes, and a client that
// insists on a terminal for its password prompt has none.
func sessionAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true, Setctty: true}
}

// hangUp tells the program and what it started (a pager, an editor) that the
// terminal is gone, as closing a terminal window does. The session leader's
// pid is its process group's id.
func hangUp(cmd *exec.Cmd) {
	signalGroup(cmd, syscall.SIGHUP)
}

func kill(cmd *exec.Cmd) {
	signalGroup(cmd, syscall.SIGKILL)
}

func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
		_ = cmd.Process.Signal(sig)
	}
}

// foreground is the process group in front on the terminal p is the master
// of: the one that gets its keys. It goes through Control, which holds the
// descriptor open while it is used and fails once the pty is closed: the
// session closes it when the program exits, possibly while a view asks.
func foreground(p xpty.Pty) (int, bool) {
	u, ok := p.(*xpty.UnixPty)
	if !ok {
		return 0, false
	}
	var (
		pgrp int
		err  error
	)
	if cerr := u.Control(func(fd uintptr) { pgrp, err = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP) }); cerr != nil {
		return 0, false
	}
	return pgrp, err == nil
}
