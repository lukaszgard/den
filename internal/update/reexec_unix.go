//go:build !windows

package update

import (
	"os"
	"syscall"
)

// CanReexec reports whether Reexec can replace the running process.
const CanReexec = true

// Reexec replaces the running process with the binary at Executable() — after
// an install, the new version — keeping its arguments and environment, so a
// `-c den.yaml` start comes back with the same config. It only returns on
// failure. The caller must have restored the terminal first: the new process
// inherits it as it is.
func Reexec() error {
	exe, err := Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ()) //nolint:gosec // re-running den itself with its own arguments
}
