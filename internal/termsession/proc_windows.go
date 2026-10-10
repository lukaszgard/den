//go:build windows

package termsession

import (
	"os/exec"
	"syscall"

	"github.com/charmbracelet/x/xpty"
)

// ConPTY attaches the program to the pseudo console by itself.
func sessionAttr() *syscall.SysProcAttr { return nil }

// Windows has no hangup to send: closing the pseudo console, which the session
// does once the program is gone, ends whatever is still attached to it.
func hangUp(cmd *exec.Cmd) { kill(cmd) }

func kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// foreground cannot be told on a pseudo console: FullScreen then goes by the
// terminal modes alone, as it always did.
func foreground(xpty.Pty) (int, bool) { return 0, false }
