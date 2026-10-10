package update

import "errors"

// CanReexec reports whether Reexec can replace the running process. Windows
// has no exec: a child process would be a second den sharing the console
// with the first, so the Updates pane asks for a manual restart instead.
const CanReexec = false

// Reexec is not supported on Windows.
func Reexec() error {
	return errors.New("restarting in place is not supported on Windows")
}
