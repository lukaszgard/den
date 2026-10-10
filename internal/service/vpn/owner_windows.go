//go:build windows

package vpn

import "os"

// fileOwnerUID has no meaning on Windows, where the VPN service is unsupported
// anyway (see supportedOS). Reporting "unknown" keeps den on the prompt path.
func fileOwnerUID(os.FileInfo) (uint32, bool) { return 0, false }
