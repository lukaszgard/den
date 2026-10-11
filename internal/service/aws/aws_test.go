package aws

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
)

func TestTokenTTL(t *testing.T) {
	issued := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)

	tests := []struct {
		name            string
		issued, expires time.Time
		want            time.Duration
	}{
		{"normal 8h token", issued, issued.Add(8 * time.Hour), 8 * time.Hour},
		{"missing issue time", time.Time{}, issued.Add(8 * time.Hour), defaultTokenTTL},
		{"expiry before issue", issued, issued.Add(-time.Hour), defaultTokenTTL},
		{"zero-length", issued, issued, defaultTokenTTL},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tokenTTL(tc.issued, tc.expires); got != tc.want {
				t.Errorf("tokenTTL = %v, want %v", got, tc.want)
			}
		})
	}
}

// A zero TokenTTL makes the detail overlay draw a 15-minute bar, which is wrong
// by orders of magnitude for an 8-hour SSO token. Assert it is populated.
func TestCheckTokenSetsTTLFromDenCache(t *testing.T) {
	dir := t.TempDir()
	issued := time.Now().Add(-time.Hour)
	expires := time.Now().Add(7 * time.Hour)
	if err := config.WriteDenCache(dir, "alpha", issued, expires); err != nil {
		t.Fatalf("WriteDenCache: %v", err)
	}

	svc := New("Alpha Org", "alpha", "the alpha org", "https://alpha.example/start", "eu-central-1", "", dir, "")

	st := svc.State()
	if st.Status != service.StatusConnected {
		t.Fatalf("status = %v, want connected", st.Status)
	}
	if st.TokenTTL <= 0 {
		t.Fatal("TokenTTL not set — the TTL bar would fall back to 15 minutes")
	}
	// 8h ± a second of test execution.
	if d := st.TokenTTL - 8*time.Hour; d > time.Second || d < -time.Second {
		t.Errorf("TokenTTL = %v, want ~8h", st.TokenTTL)
	}
}

// A token found in the cache at startup has no login of den's watching it, so
// nothing resets the status when it runs out. It must not stay connected: the
// row read "token: expired" while c answered "already running".
func TestExpiredTokenIsNotConnected(t *testing.T) {
	dir := t.TempDir()
	if err := config.WriteDenCache(dir, "alpha", time.Now().Add(-time.Hour), time.Now().Add(7*time.Hour)); err != nil {
		t.Fatalf("WriteDenCache: %v", err)
	}
	svc := New("", "alpha", "", "https://alpha.example/start", "eu-central-1", "", dir, "")
	if st := svc.State(); st.Status != service.StatusConnected {
		t.Fatalf("status = %v, want connected while the token is valid", st.Status)
	}

	// The clock moves past the expiry with no re-read of the cache.
	svc.mu.Lock()
	expired := time.Now().Add(-time.Minute)
	svc.state.TokenExpires = &expired
	svc.mu.Unlock()

	st := svc.State()
	if st.Status != service.StatusIdle {
		t.Errorf("status = %v, want idle so c logs in again", st.Status)
	}
	if st.TokenExpires == nil {
		t.Error("TokenExpires dropped: the row should keep saying the token expired")
	}
}

// A login started while an earlier one's expiry watch still runs must end that
// watch: its next tick would re-read the stale cache and set the new login idle
// while the browser is still open.
func TestStartEndsThePreviousLoginsWatch(t *testing.T) {
	svc := New("", "alpha", "", "https://alpha.example/start", "eu-central-1", "", t.TempDir(), "")
	ended := false
	svc.cancel = func() { ended = true }
	t.Setenv("PATH", t.TempDir()) // no aws: the login fails at once

	if _, err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !ended {
		t.Error("the previous login's watch is still running")
	}
	_ = svc.Stop(context.Background())
}

func TestCheckTokenNoCacheIsIdle(t *testing.T) {
	svc := New("", "alpha", "", "https://alpha.example/start", "eu-central-1", "", t.TempDir(), "")
	st := svc.State()
	if st.Status != service.StatusIdle {
		t.Errorf("status = %v, want idle", st.Status)
	}
	if st.TokenTTL != 0 || st.TokenExpires != nil {
		t.Errorf("stale token state: ttl=%v expires=%v", st.TokenTTL, st.TokenExpires)
	}
}

// The display name is cosmetic; every AWS-facing use must be the session name,
// or `aws sso login` is invoked with something that is not in ~/.aws/config.
func TestDisplayNameNeverReachesTheCLI(t *testing.T) {
	svc := New("Acme SSO", "acme-sso", "", "https://example/start", "eu-central-1", "", t.TempDir(), "")

	if got := svc.Info().Name; got != "Acme SSO" {
		t.Errorf("Info().Name = %q, want the display name", got)
	}
	if got := svc.Session(); got != "acme-sso" {
		t.Errorf("Session() = %q, want the section name", got)
	}
	if want := "aws sso login --sso-session acme-sso"; svc.ConnectCommand() != want {
		t.Errorf("ConnectCommand() = %q, want %q", svc.ConnectCommand(), want)
	}
}

func TestNameDefaultsToSession(t *testing.T) {
	svc := New("", "alpha", "", "", "", "", t.TempDir(), "")
	if got := svc.Info().Name; got != "alpha" {
		t.Errorf("Info().Name = %q, want %q", got, "alpha")
	}
}

// The login must reach the file den.yaml names (the CLI reads it from
// AWS_CONFIG_FILE) and a failure must show the CLI's own message, not just
// "exit status 255".
func TestLoginFailureShowsTheCLIMessage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake aws is a shell script")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'starting' >&2\necho \"cannot read $AWS_CONFIG_FILE\" >&2\nexit 255\n"
	if err := os.WriteFile(filepath.Join(bin, "aws"), []byte(script), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AWS_CONFIG_FILE", "/den/config-den")

	svc := New("", "alpha", "", "", "", "", t.TempDir(), "")
	if _, err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	deadline := time.Now().Add(5 * time.Second)
	for svc.State().Status != service.StatusError {
		if time.Now().After(deadline) {
			t.Fatalf("status = %v, want error", svc.State().Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	err := svc.State().Error
	if err == nil || !strings.Contains(err.Error(), "cannot read /den/config-den") {
		t.Errorf("error = %v, want the CLI's last line with the config path", err)
	}
}
