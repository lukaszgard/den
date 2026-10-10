//go:build !windows

package platform

import (
	"errors"

	"golang.org/x/sys/unix"
)

// ProcessAlive reports whether a process with this pid exists. A process of
// another user still counts: signalling it is refused, not unknown.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := unix.Kill(pid, 0)
	return err == nil || errors.Is(err, unix.EPERM)
}
