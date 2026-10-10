//go:build windows

package source

import "os/exec"

// detach is a no-op on Windows, which has no controlling terminal for git to
// prompt on; the environment keeps it from asking.
func detach(*exec.Cmd) {}
