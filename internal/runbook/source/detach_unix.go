//go:build !windows

package source

import (
	"os/exec"
	"syscall"
)

// detach runs git in a session of its own, without a controlling terminal,
// so that nothing it starts — ssh asking for a passphrase or about a new host
// key — can open /dev/tty and read from den's screen.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
