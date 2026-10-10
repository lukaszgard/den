//go:build windows

package platform

import "golang.org/x/sys/windows"

// stillActive is the exit code Windows reports for a process still running.
const stillActive = 259

// ProcessAlive reports whether a process with this pid is running. Opening it
// is not enough: a handle someone still holds keeps an exited process
// openable, so its exit code is asked as well.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Access denied means it exists, as someone else's.
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil {
		return true
	}
	return code == stillActive
}
