package vpn

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
)

// fakePATH replaces PATH with a directory holding only these shell scripts, so
// the test sees the processes and interfaces they describe, and nothing else den
// might run (sudo, osascript, pkexec, openfortivpn) is found.
func fakePATH(t *testing.T, scripts map[string]string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fakes are shell scripts")
	}
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

// New used to probe the health_check of a tunnel left from an earlier session
// on den's main goroutine. A dead tunnel's split DNS made every probe wait out
// its timeout, and den took about 7 s to open. New now only looks for the
// process and ppp0, and Start's goroutine probes.
func TestFoundTunnelIsCheckedInTheBackground(t *testing.T) {
	for _, c := range []struct {
		name   string
		alive  bool
		want   service.Status
		errHas string
	}{
		{"dead", false, service.StatusError, "appears dead"},
		{"alive", true, service.StatusConnected, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			target := ln.Addr().String()
			if c.alive {
				defer ln.Close()
			} else {
				ln.Close() // refused at once, like a probe with nothing behind it
			}
			// An openfortivpn running with ppp0 up; pkexec is what Linux checks
			// for. No sudo or osascript, so Stop's teardown reaches nothing real.
			fakePATH(t, map[string]string{
				"pgrep":        "echo 4242",
				"ifconfig":     "echo 'ppp0: flags=8051<UP,POINTOPOINT,RUNNING,MULTICAST> mtu 1354'",
				"ip":           "exit 0",
				"openfortivpn": "exit 0",
				"pkexec":       "exit 1",
			})

			begin := time.Now()
			s, err := New("test", &config.VPNSpec{Gateway: "vpn.example.com", HealthCheck: target})
			if err != nil {
				t.Skipf("vpn.New unsupported on this platform: %v", err)
			}
			if took := time.Since(begin); took > 500*time.Millisecond {
				t.Errorf("New took %v; it must not probe", took)
			}
			if st := s.State(); st.Status != service.StatusConnecting {
				t.Fatalf("seeded %s, want connecting until the check answers", st.Status)
			}

			updates, err := s.Start(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var st service.RuntimeState
			for st.Status != c.want {
				select {
				case u := <-updates:
					st = u.State
				case <-time.After(5 * time.Second):
					t.Fatalf("no %s update; state %+v", c.want, s.State())
				}
			}
			if c.errHas != "" && (st.Error == nil || !strings.Contains(st.Error.Error(), c.errHas)) {
				t.Errorf("error = %v, want it to mention %q", st.Error, c.errHas)
			}
			if err := s.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A Stop ends the tunnel New found, so the next Start must connect, not adopt.
func TestStartAdoptsOnlyOnce(t *testing.T) {
	fakePATH(t, map[string]string{
		"pgrep":        "echo 4242",
		"ifconfig":     "echo 'ppp0: flags=8051<UP,POINTOPOINT,RUNNING,MULTICAST> mtu 1354'",
		"ip":           "exit 0",
		"openfortivpn": "exit 0",
		"pkexec":       "exit 1",
	})
	s, err := New("test", &config.VPNSpec{Gateway: "vpn.example.com"})
	if err != nil {
		t.Skipf("vpn.New unsupported on this platform: %v", err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.mu.RLock()
	found := s.found
	s.mu.RUnlock()
	if found {
		t.Error("after Stop, a Start would adopt the tunnel Stop just killed")
	}
}

// A stop that left openfortivpn hung in its logout used to be followed by a
// second openfortivpn on top of it, whose SAML login could only time out while
// holding a gateway session slot. Whatever is running when a connect starts is
// a process den could not take over or stop, so the connect refuses, and a
// reconnect gives up instead of retrying it.
func TestRunOnceRefusesASecondOpenfortivpn(t *testing.T) {
	s := newSvc(t, &config.VPNSpec{Reconnect: true})
	// No sudo, osascript or pkexec on PATH: an attempt past the check fails
	// with their error, not this one.
	fakePATH(t, map[string]string{"pgrep": "echo 4242"})

	s.runOnce(t.Context())

	st := s.State()
	if st.Status != service.StatusError || st.Error == nil || !strings.Contains(st.Error.Error(), "openfortivpn is still running (pid 4242)") {
		t.Fatalf("state = %+v, want an error naming pid 4242", st)
	}
	s.mu.RLock()
	outcome := s.outcome
	s.mu.RUnlock()
	if outcome != outcomeFatal {
		t.Errorf("outcome = %v, want outcomeFatal", outcome)
	}
	if _, retry := s.nextRetry(t.Context(), 0); retry {
		t.Error("a reconnect would start a second openfortivpn")
	}
}
