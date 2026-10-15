package aws

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc"

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

// fakeOIDC stands in for IAM Identity Center's CreateToken: it swaps refresh
// token r1 for access token a2 and refresh token r2, valid an hour.
type fakeOIDC struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *fakeOIDC) CreateToken(_ context.Context, in *ssooidc.CreateTokenInput, _ ...func(*ssooidc.Options)) (*ssooidc.CreateTokenOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if in.RefreshToken == nil || *in.RefreshToken != "r1" {
		return nil, errors.New("InvalidGrantException: the refresh token was spent")
	}
	access, refresh := "a2", "r2"
	return &ssooidc.CreateTokenOutput{AccessToken: &access, RefreshToken: &refresh, ExpiresIn: 3600}, nil
}

func (f *fakeOIDC) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// cachedLogin writes the session's token file as the AWS CLI does, its access
// token running out at expires. refresh is the refresh token, "" for none.
func cachedLogin(t *testing.T, dir, session string, expires time.Time, refresh string) string {
	t.Helper()
	tok := map[string]string{
		"startUrl": "https://alpha.example/start", "region": "eu-central-1",
		"accessToken": "a1", "expiresAt": expires.UTC().Format(time.RFC3339),
		"clientId": "c", "clientSecret": "s",
		"registrationExpiresAt": time.Now().AddDate(0, 3, 0).UTC().Format(time.RFC3339),
	}
	if refresh != "" {
		tok["refreshToken"] = refresh
	}
	data, err := json.Marshal(tok)
	if err != nil {
		t.Fatal(err)
	}
	path := config.SSOTokenPath(dir, session)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// renewing is an SSO session whose token renewals go to oidc.
func renewing(t *testing.T, dir string, oidc *fakeOIDC) *Service {
	t.Helper()
	svc := New("", "alpha", "", "https://alpha.example/start", "eu-central-1", "", dir, "")
	svc.newOIDC = func(region string) ssocreds.CreateTokenAPIClient {
		if region != "eu-central-1" {
			t.Errorf("CreateToken in %q, want the session's sso_region", region)
		}
		return oidc
	}
	return svc
}

// The access token lasts an hour; without a renewal the file stays expired
// for as long as nothing else uses AWS, and every reader of it — the Secrets
// pane, den doctor, tools outside den — takes the login for over.
func TestRefreshTokenRenewsAnExpiredToken(t *testing.T) {
	dir := t.TempDir()
	path := cachedLogin(t, dir, "alpha", time.Now().Add(-10*time.Minute), "r1")
	oidc := &fakeOIDC{}
	svc := renewing(t, dir, oidc)
	if st := svc.State(); st.Status != service.StatusIdle {
		t.Fatalf("status = %v before the renewal, want idle", st.Status)
	}

	if err := svc.RefreshToken(context.Background()); err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if oidc.count() != 1 {
		t.Fatalf("CreateToken called %d times, want once", oidc.count())
	}
	tok, ok := config.LoadSSOToken(path)
	if !ok || time.Until(tok.ExpiresAt) < 50*time.Minute || !tok.Matches("", "https://alpha.example/start") {
		t.Fatalf("token file after the renewal = %+v, %v; want an hour more, still the session's", tok, ok)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"a2"`) || !strings.Contains(string(data), `"r2"`) {
		t.Errorf("the file does not hold the new tokens: %s", data)
	}
	if st := svc.State(); st.Status != service.StatusConnected {
		t.Errorf("status = %v after the renewal, want connected", st.Status)
	}
	logs := strings.Join(svc.Logs(), "\n")
	if !strings.Contains(logs, "renewed the SSO token, valid until") {
		t.Errorf("the log does not say the token was renewed:\n%s", logs)
	}
	if strings.Contains(logs, "a2") || strings.Contains(logs, "r2") {
		t.Errorf("a token reached the log:\n%s", logs)
	}

	// Valid again: nothing to renew.
	if err := svc.RefreshToken(context.Background()); err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if oidc.count() != 1 {
		t.Errorf("CreateToken called for a valid token (%d calls)", oidc.count())
	}
}

// A refresh token can be spent once: two renewals at the same moment (the
// minute tick and r) must not both send it.
func TestConcurrentRenewalsSpendTheRefreshTokenOnce(t *testing.T) {
	dir := t.TempDir()
	cachedLogin(t, dir, "alpha", time.Now().Add(-time.Minute), "r1")
	oidc := &fakeOIDC{}
	svc := renewing(t, dir, oidc)

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { _ = svc.RefreshToken(context.Background()) })
	}
	wg.Wait()
	if oidc.count() != 1 {
		t.Errorf("CreateToken called %d times, want once", oidc.count())
	}
	if strings.Contains(strings.Join(svc.Logs(), "\n"), "could not renew") {
		t.Errorf("a renewal failed:\n%s", strings.Join(svc.Logs(), "\n"))
	}
}

func TestRefreshTokenWithoutARefreshTokenDoesNotRenew(t *testing.T) {
	dir := t.TempDir()
	cachedLogin(t, dir, "alpha", time.Now().Add(-10*time.Minute), "")
	oidc := &fakeOIDC{}
	svc := renewing(t, dir, oidc)

	if err := svc.RefreshToken(context.Background()); err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if oidc.count() != 0 {
		t.Errorf("CreateToken called %d times without a refresh token", oidc.count())
	}
	if st := svc.State(); st.Status != service.StatusIdle {
		t.Errorf("status = %v, want idle", st.Status)
	}
}

// A renewal that fails — the IAM Identity Center session ended — leaves the
// state to the files, and says so once rather than on every minute's try.
func TestFailedRenewalIsLoggedOnce(t *testing.T) {
	dir := t.TempDir()
	cachedLogin(t, dir, "alpha", time.Now().Add(-10*time.Minute), "r1")
	oidc := &fakeOIDC{err: errors.New("InvalidGrantException: the session ended")}
	svc := renewing(t, dir, oidc)

	for range 3 {
		if err := svc.RefreshToken(context.Background()); err != nil {
			t.Fatalf("RefreshToken: %v", err)
		}
	}
	if oidc.count() != 3 {
		t.Errorf("CreateToken called %d times, want every try", oidc.count())
	}
	if st := svc.State(); st.Status != service.StatusIdle {
		t.Errorf("status = %v, want idle", st.Status)
	}
	logs := strings.Join(svc.Logs(), "\n")
	if n := strings.Count(logs, "could not renew the SSO token"); n != 1 || !strings.Contains(logs, "the session ended") {
		t.Errorf("want the failure logged once with its reason, got %d:\n%s", n, logs)
	}
}

// The TUI renews every minute, and a login in progress must not be read as
// idle (the browser is still open) nor have its file renewed under it.
func TestRefreshTokenLeavesALoginInProgress(t *testing.T) {
	dir := t.TempDir()
	cachedLogin(t, dir, "alpha", time.Now().Add(-10*time.Minute), "r1")
	oidc := &fakeOIDC{}
	svc := renewing(t, dir, oidc)
	svc.mu.Lock()
	svc.state = service.RuntimeState{Status: service.StatusConnecting}
	svc.mu.Unlock()

	if err := svc.RefreshToken(context.Background()); err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if st := svc.State(); st.Status != service.StatusConnecting {
		t.Errorf("status = %v, want the login still connecting", st.Status)
	}
	if oidc.count() != 0 {
		t.Errorf("CreateToken called %d times during a login", oidc.count())
	}
}
