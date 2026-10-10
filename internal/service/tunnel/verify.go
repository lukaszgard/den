package tunnel

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/service"
)

const (
	portCheckTimeout   = 500 * time.Millisecond
	healthCheckTimeout = 2 * time.Second
	// stayOpenWindow is how long the health check waits after the SOCKS reply for
	// the tunnel to hang up. ssh may answer the CONNECT before the far side has
	// accepted, then close the socket once it learns the target is unreachable.
	stayOpenWindow = time.Second
)

// sshArgFlags lists the ssh options that take an argument, so ParseForwards can
// walk clustered flags like "-NTD 5555" the way ssh's getopt does.
const sshArgFlags = "bceilmopBDEFIJLOQRSwW"

// Forward is one local listener an ssh command opens: a -D SOCKS proxy or a -L
// port forward.
type Forward struct {
	Socks    bool   // -D (dynamic SOCKS proxy) rather than -L
	BindAddr string // empty when the flag carried no bind address
	Port     int
	Target   string // host:port a -L forward reaches; empty for -D
}

// ParseForwards extracts the -D and -L listeners from an ssh command line.
// Forms den cannot probe — unix sockets, bracketed IPv6 — are skipped rather
// than guessed at.
func ParseForwards(cmd string) []Forward {
	parts := strings.Fields(cmd)
	var out []Forward
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if len(p) < 2 || p[0] != '-' || p[1] == '-' {
			continue
		}
		for j := 1; j < len(p); j++ {
			c := p[j]
			if !strings.ContainsRune(sshArgFlags, rune(c)) {
				continue
			}
			// The rest of the cluster is the argument, or the next word when empty.
			arg := p[j+1:]
			if arg == "" && i+1 < len(parts) {
				i++
				arg = parts[i]
			}
			if c == 'D' || c == 'L' {
				if f, ok := parseForward(c == 'D', arg); ok {
					out = append(out, f)
				}
			}
			break
		}
	}
	return out
}

// parseForward parses a -D "[bind:]port" or -L "[bind:]port:host:hostport" spec.
func parseForward(socks bool, spec string) (Forward, bool) {
	if spec == "" || strings.ContainsAny(spec, "[/") {
		return Forward{}, false
	}
	fields := strings.Split(spec, ":")
	var f Forward
	var port string
	switch {
	case socks && len(fields) == 1:
		port = fields[0]
	case socks && len(fields) == 2:
		f.BindAddr, port = fields[0], fields[1]
	case !socks && len(fields) == 3:
		port, f.Target = fields[0], fields[1]+":"+fields[2]
	case !socks && len(fields) == 4:
		f.BindAddr, port, f.Target = fields[0], fields[1], fields[2]+":"+fields[3]
	default:
		return Forward{}, false
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return Forward{}, false
	}
	f.Socks, f.Port = socks, n
	return f, true
}

// ProbeAddr is the address to dial to reach this listener locally.
func (f Forward) ProbeAddr() string {
	host := f.BindAddr
	switch host {
	case "", "*", "0.0.0.0", "localhost":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(f.Port))
}

// String renders the forward the way the Tunnel pane lists port mappings.
func (f Forward) String() string {
	if f.Socks {
		return fmt.Sprintf(":%d  SOCKS proxy", f.Port)
	}
	return fmt.Sprintf(":%d  → %s", f.Port, f.Target)
}

// socksForward returns the first -D listener, which the health check dials through.
func socksForward(forwards []Forward) (Forward, bool) {
	for _, f := range forwards {
		if f.Socks {
			return f, true
		}
	}
	return Forward{}, false
}

// check verifies the tunnel carries traffic: every local listener accepts
// connections and, when configured, the health_check target is reachable
// through the SOCKS proxy. It returns the first failure.
func (s *Service) check(forwards []Forward) error {
	if err := checkPorts(forwards); err != nil {
		return err
	}
	return s.checkHealth(forwards)
}

// checkPorts reports the first local listener that refuses connections.
func checkPorts(forwards []Forward) error {
	for _, f := range forwards {
		if !service.Reachable(f.ProbeAddr(), portCheckTimeout) {
			return fmt.Errorf(":%d not accepting connections", f.Port)
		}
	}
	return nil
}

// checkHealth dials the health_check target through the SOCKS proxy; it is a
// no-op when none is configured or the check is switched off at runtime.
func (s *Service) checkHealth(forwards []Forward) error {
	target := s.activeHealthCheck()
	if target == "" {
		return nil
	}
	proxy, ok := socksForward(forwards)
	if !ok {
		return errNoSocks
	}
	return socksConnect(proxy.ProbeAddr(), target, healthCheckTimeout)
}

var errNoSocks = errors.New("health_check needs a -D SOCKS port in the command")

// socksConnect asks the SOCKS5 proxy at proxyAddr to open a TCP connection to
// target, proving traffic flows through the tunnel to the far side. Only the
// no-auth method is offered, which is all ssh -D speaks.
func socksConnect(proxyAddr, target string, timeout time.Duration) error {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid port in %q", target)
	}
	if len(host) > 255 {
		return fmt.Errorf("host name too long: %q", host)
	}

	conn, err := net.DialTimeout("tcp", proxyAddr, timeout)
	if err != nil {
		return fmt.Errorf("SOCKS proxy %s: %w", proxyAddr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	// Greeting: version 5, one method on offer, "no authentication".
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		return socksErr(target, err, timeout)
	}
	var method [2]byte
	if _, err := io.ReadFull(conn, method[:]); err != nil {
		return socksErr(target, err, timeout)
	}
	if method[0] != 5 || method[1] != 0 { //nolint:gosec // G602 false positive: method is a [2]byte
		return fmt.Errorf("SOCKS proxy %s refused the no-auth method", proxyAddr)
	}

	// CONNECT by domain name, so the far side does the DNS lookup.
	req := append([]byte{5, 1, 0, 3, byte(len(host))}, host...) //nolint:gosec // G115: len(host) ≤ 255, checked above
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := conn.Write(req); err != nil {
		return socksErr(target, err, timeout)
	}

	// Reply: VER REP RSV ATYP, then the bound address, which must be consumed
	// so the stay-open probe below does not mistake it for data from the target.
	var reply [4]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return socksErr(target, err, timeout)
	}
	if reply[1] != 0 {
		return fmt.Errorf("%s: %s", target, socksReplyText(reply[1]))
	}
	var bound int
	switch reply[3] {
	case 1:
		bound = net.IPv4len
	case 4:
		bound = net.IPv6len
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(conn, n[:]); err != nil {
			return socksErr(target, err, timeout)
		}
		bound = int(n[0])
	default:
		return fmt.Errorf("SOCKS proxy sent an unknown address type %d", reply[3])
	}
	if _, err := io.ReadFull(conn, make([]byte, bound+2)); err != nil {
		return socksErr(target, err, timeout)
	}

	// A hang-up right after the reply means ssh could not reach the target after
	// all. Silence (the target waits for us to speak) or data both mean it is up.
	_ = conn.SetReadDeadline(time.Now().Add(stayOpenWindow))
	if _, err := conn.Read(make([]byte, 1)); err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
		return socksErr(target, err, timeout)
	}
	return nil
}

// socksErr turns low-level I/O errors from the handshake into something a user
// can act on.
func socksErr(target string, err error, timeout time.Duration) error {
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Errorf("%s: connection closed by the tunnel — far side refused or unreachable", target)
	case errors.Is(err, os.ErrDeadlineExceeded):
		return fmt.Errorf("%s: no answer through the tunnel within %v", target, timeout)
	}
	return fmt.Errorf("%s: %w", target, err)
}

// socksReplyText names the SOCKS5 reply codes from RFC 1928.
func socksReplyText(code byte) string {
	switch code {
	case 1:
		return "general SOCKS server failure"
	case 2:
		return "connection not allowed by ruleset"
	case 3:
		return "network unreachable"
	case 4:
		return "host unreachable"
	case 5:
		return "connection refused"
	case 6:
		return "TTL expired"
	case 7:
		return "command not supported"
	case 8:
		return "address type not supported"
	}
	return fmt.Sprintf("SOCKS reply code %d", code)
}
