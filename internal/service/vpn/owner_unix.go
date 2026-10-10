//go:build !windows

package vpn

import (
	"os"
	"syscall"
)

// fileOwnerUID returns the owning user ID of fi.
func fileOwnerUID(fi os.FileInfo) (uint32, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}
