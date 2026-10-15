package vpn

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
