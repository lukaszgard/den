package service

import (
	"fmt"
	"net"
	"time"
)

// PortReady polls a local TCP port for up to ~1.5s, returning true once it
// accepts a connection. SSM can log "forwarding" before the socket is actually
// listening, so callers use this to confirm a tunnel is genuinely up.
func PortReady(port int) bool {
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if checkPortOpen(port) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// checkPortOpen returns true if a TCP connection to localhost:port succeeds quickly.
func checkPortOpen(port int) bool {
	return Reachable(fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
}

// Reachable returns true if a TCP connection to addr (host:port) succeeds within timeout.
func Reachable(addr string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
