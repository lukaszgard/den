package tunnel

import (
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseForwards(t *testing.T) {
	cases := []struct {
		cmd  string
		want []Forward
	}{
		{cmd: "ssh -N -T -D 5555 host", want: []Forward{{Socks: true, Port: 5555}}},
		{cmd: "ssh -D5555 host", want: []Forward{{Socks: true, Port: 5555}}},
		{cmd: "ssh -NTD 5555 host", want: []Forward{{Socks: true, Port: 5555}}},
		{cmd: "ssh -D 127.0.0.1:5555 host", want: []Forward{{Socks: true, BindAddr: "127.0.0.1", Port: 5555}}},
		{cmd: "ssh -L 8080:app.internal:80 host", want: []Forward{{Port: 8080, Target: "app.internal:80"}}},
		{cmd: "ssh -L8080:app.internal:80 host", want: []Forward{{Port: 8080, Target: "app.internal:80"}}},
		{cmd: "ssh -L 0.0.0.0:8080:app.internal:80 host", want: []Forward{{BindAddr: "0.0.0.0", Port: 8080, Target: "app.internal:80"}}},
		{
			cmd:  "ssh -N -D 5555 -L 5432:db.corp:5432 user@bastion",
			want: []Forward{{Socks: true, Port: 5555}, {Port: 5432, Target: "db.corp:5432"}},
		},
		// An option's argument is never mistaken for a flag: "-o" swallows the
		// next word even though it contains "-D".
		{cmd: "ssh -o ProxyCommand=-D9999 -D 5555 host", want: []Forward{{Socks: true, Port: 5555}}},
		// Forms den cannot probe, and junk, are skipped.
		{cmd: "ssh -L /tmp/sock:/remote/sock host"},
		{cmd: "ssh -D [::1]:5555 host"},
		{cmd: "ssh -D notaport host"},
		{cmd: "ssh -D 70000 host"},
		{cmd: "ssh -L 8080:hostonly host"},
		{cmd: "ssh -D"},
		{cmd: "sleep 600"},
		{cmd: ""},
	}
	for _, c := range cases {
		if got := ParseForwards(c.cmd); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseForwards(%q) = %+v, want %+v", c.cmd, got, c.want)
		}
	}
}

func TestForwardProbeAddrAndString(t *testing.T) {
	cases := []struct {
		f         Forward
		wantProbe string
		wantStr   string
	}{
		{Forward{Socks: true, Port: 5555}, "127.0.0.1:5555", ":5555  SOCKS proxy"},
		{Forward{Socks: true, BindAddr: "*", Port: 5555}, "127.0.0.1:5555", ":5555  SOCKS proxy"},
		{Forward{BindAddr: "localhost", Port: 8080, Target: "app:80"}, "127.0.0.1:8080", ":8080  → app:80"},
		{Forward{BindAddr: "10.0.0.5", Port: 8080, Target: "app:80"}, "10.0.0.5:8080", ":8080  → app:80"},
	}
	for _, c := range cases {
		if got := c.f.ProbeAddr(); got != c.wantProbe {
			t.Errorf("%+v.ProbeAddr() = %q, want %q", c.f, got, c.wantProbe)
		}
		if got := c.f.String(); got != c.wantStr {
			t.Errorf("%+v.String() = %q, want %q", c.f, got, c.wantStr)
		}
	}
}

// fakeSOCKS serves one SOCKS5 exchange: it checks the greeting and CONNECT
// request, answers with rep, then either holds the connection open or hangs up.
// With silent set it accepts the connection and never answers at all.
func fakeSOCKS(t *testing.T, rep byte, hangUp, silent bool) (addr string, gotTarget <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	targets := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if silent {
			time.Sleep(2 * time.Second)
			return
		}
		greeting := make([]byte, 3)
		if _, err := io.ReadFull(conn, greeting); err != nil {
			return
		}
		conn.Write([]byte{5, 0}) //nolint:errcheck
		head := make([]byte, 5)  // VER CMD RSV ATYP LEN
		if _, err := io.ReadFull(conn, head); err != nil {
			return
		}
		rest := make([]byte, int(head[4])+2)
		if _, err := io.ReadFull(conn, rest); err != nil {
			return
		}
		port := int(rest[len(rest)-2])<<8 | int(rest[len(rest)-1])
		targets <- net.JoinHostPort(string(rest[:len(rest)-2]), strconv.Itoa(port))
		conn.Write([]byte{5, rep, 0, 1, 0, 0, 0, 0, 0, 0}) //nolint:errcheck
		if !hangUp {
			time.Sleep(2 * time.Second)
		}
	}()
	return ln.Addr().String(), targets
}

func TestSocksConnectSuccess(t *testing.T) {
	addr, got := fakeSOCKS(t, 0, false, false)
	if err := socksConnect(addr, "intranet.example.com:443", time.Second); err != nil {
		t.Fatalf("socksConnect: unexpected error %v", err)
	}
	if target := <-got; target != "intranet.example.com:443" {
		t.Errorf("proxy was asked for %q, want intranet.example.com:443", target)
	}
}

// A CONNECT request carries the host name's length in one byte: 255 bytes is
// the most it can say, and a longer name must be refused rather than wrapped
// into a request for some other host.
func TestSocksConnectHostNameLength(t *testing.T) {
	addr, got := fakeSOCKS(t, 0, false, false)
	longest := strings.Repeat("a", 255)
	if err := socksConnect(addr, longest+":443", time.Second); err != nil {
		t.Fatalf("socksConnect with a 255-byte host: unexpected error %v", err)
	}
	if target := <-got; target != longest+":443" {
		t.Errorf("proxy was asked for a %d-byte target, want the 255-byte host", len(target))
	}

	addr, got = fakeSOCKS(t, 0, false, false)
	err := socksConnect(addr, longest+"a:443", time.Second)
	if err == nil || !strings.Contains(err.Error(), "host name too long") {
		t.Fatalf("socksConnect with a 256-byte host: error = %v, want it refused", err)
	}
	select {
	case target := <-got:
		t.Errorf("proxy was sent a CONNECT for a %d-byte target", len(target))
	default:
	}
}

func TestSocksConnectRefused(t *testing.T) {
	addr, _ := fakeSOCKS(t, 5, false, false)
	err := socksConnect(addr, "intranet.example.com:443", time.Second)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("socksConnect error = %v, want it to mention connection refused", err)
	}
}

// ssh -D may reply success before the far side has answered, then hang up once
// the target turns out unreachable; that must count as a failure.
func TestSocksConnectHangUpAfterReply(t *testing.T) {
	addr, _ := fakeSOCKS(t, 0, true, false)
	err := socksConnect(addr, "intranet.example.com:443", time.Second)
	if err == nil || !strings.Contains(err.Error(), "closed by the tunnel") {
		t.Fatalf("socksConnect error = %v, want it to report the tunnel hanging up", err)
	}
}

func TestSocksConnectTimeout(t *testing.T) {
	addr, _ := fakeSOCKS(t, 0, false, true)
	start := time.Now()
	err := socksConnect(addr, "intranet.example.com:443", 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no answer") {
		t.Fatalf("socksConnect error = %v, want a timeout", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("socksConnect took %v, want it bounded by the timeout", elapsed)
	}
}

func TestSocksConnectNoProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens here any more
	if err := socksConnect(addr, "intranet.example.com:443", 300*time.Millisecond); err == nil {
		t.Fatal("socksConnect succeeded with no proxy listening")
	}
}
