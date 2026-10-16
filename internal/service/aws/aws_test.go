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
	"github.com/aws/aws-sdk-go-v2/service/ssooidc/types"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/secrets"
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
	cachedLogin(t, dir, "alpha", time.Now().Add(-10*time.Minute), "r1")

	svc := New("Alpha Org", "alpha", "the alpha org", "https://alpha.example/start", "eu-central-1", "", dir, "")

	st := svc.State()
	if st.Status != service.StatusConnected {
		t.Fatalf("status = %v, want connected", st.Status)
	}
	if st.TokenExpires == nil || !st.TokenExpires.Equal(expires) {
		t.Errorf("TokenExpires = %v, want den's record %v", st.TokenExpires, expires)
	}
	if st.TokenTTL <= 0 {
		t.Fatal("TokenTTL not set — the TTL bar would fall back to 15 minutes")
	}
	// 8h ± a second of test execution.
	if d := st.TokenTTL - 8*time.Hour; d > time.Second || d < -time.Second {
		t.Errorf("TokenTTL = %v, want ~8h", st.TokenTTL)
	}
}

// aws sso logout deletes the CLI's token files and leaves den.json. The pane
// read den's record first and kept counting down a login the Secrets pane
// already called expired.
func TestLogoutEndsTheLoginWhileDensRecordRuns(t *testing.T) {
	dir := t.TempDir()
	if err := config.WriteDenCache(dir, "alpha", time.Now().Add(-time.Hour), time.Now().Add(7*time.Hour)); err != nil {
		t.Fatalf("WriteDenCache: %v", err)
	}

	st := New("", "alpha", "", "https://alpha.example/start", "eu-central-1", "", dir, "").State()
	if st.Status != service.StatusIdle || st.TokenExpires != nil {
		t.Errorf("state = %v, expires %v; want idle with no token", st.Status, st.TokenExpires)
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
	cachedLogin(t, dir, "alpha", time.Now().Add(time.Hour), "r1")
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
	// meanwhile runs inside the call, before it answers: what another process
	// does with the file while the request is out.
	meanwhile func()
}

func (f *fakeOIDC) CreateToken(_ context.Context, in *ssooidc.CreateTokenInput, _ ...func(*ssooidc.Options)) (*ssooidc.CreateTokenOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.meanwhile != nil {
		f.meanwhile()
	}
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
	return registeredLogin(t, dir, session, expires, refresh, time.Now().AddDate(0, 3, 0))
}

// registeredLogin is cachedLogin with the client registration, which the
// refresh token needs, running out at registered.
func registeredLogin(t *testing.T, dir, session string, expires time.Time, refresh string, registered time.Time) string {
	t.Helper()
	tok := map[string]string{
		"startUrl": "https://alpha.example/start", "region": "eu-central-1",
		"accessToken": "a1", "expiresAt": expires.UTC().Format(time.RFC3339),
		"clientId": "c", "clientSecret": "s",
		"registrationExpiresAt": registered.UTC().Format(time.RFC3339),
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
	// The refresh token keeps the login alive, as the Secrets pane counts it;
	// only its end is unknown until the token is renewed.
	if st := svc.State(); st.Status != service.StatusConnected || st.TokenExpires != nil {
		t.Fatalf("before the renewal: status = %v, expires %v; want connected with no known end", st.Status, st.TokenExpires)
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
	if st := svc.State(); st.Status != service.StatusConnected || st.TokenExpires == nil || time.Until(*st.TokenExpires) < 50*time.Minute {
		t.Errorf("after the renewal: status = %v, expires %v; want connected for the new token's hour", st.Status, st.TokenExpires)
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

// A renewal that fails on the way (no network, a timeout) says nothing about
// the session: the state stays with the files, nothing is recorded, and the
// failure is logged once rather than on every minute's try.
func TestFailedRenewalIsLoggedOnce(t *testing.T) {
	dir := t.TempDir()
	cachedLogin(t, dir, "alpha", time.Now().Add(-10*time.Minute), "r1")
	oidc := &fakeOIDC{err: errors.New("dial tcp: i/o timeout")}
	svc := renewing(t, dir, oidc)

	for range 3 {
		if err := svc.RefreshToken(context.Background()); err != nil {
			t.Fatalf("RefreshToken: %v", err)
		}
	}
	if oidc.count() != 3 {
		t.Errorf("CreateToken called %d times, want every try", oidc.count())
	}
	if st := svc.State(); st.Status != service.StatusConnected || st.TokenExpires != nil {
		t.Errorf("status = %v, expires %v; want still connected with no known end", st.Status, st.TokenExpires)
	}
	if _, ok := config.ReadDenCache(dir).Sessions["alpha"]; ok {
		t.Error("a failure on the way recorded the session as ended")
	}
	logs := strings.Join(svc.Logs(), "\n")
	if n := strings.Count(logs, "could not renew the SSO token"); n != 1 || !strings.Contains(logs, "i/o timeout") {
		t.Errorf("want the failure logged once with its reason, got %d:\n%s", n, logs)
	}
}

// IAM Identity Center refusing the refresh token is the end of the session.
// Nothing on disk said so: after a login made outside den, the Secrets pane
// and den doctor took the refresh token for a live login for as long as the
// client registration lasted, months. den's record of the session now ends,
// so every reader, in this den or another process, calls it expired.
func TestEndedSessionReadsExpiredEverywhere(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"invalid grant", &types.InvalidGrantException{Message: ptr("the session ended")}},
		{"expired token", &types.ExpiredTokenException{}},
		{"unauthorized client", &types.UnauthorizedClientException{}},
		{"invalid client", &types.InvalidClientException{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, cache := awsHome(t)
			cachedLogin(t, cache, "alpha", time.Now().Add(-10*time.Minute), "r1")
			logins := secrets.NewAWSLogins(filepath.Join(home, "config"))
			if got := logins.Check("alpha-prof", time.Now()); got != "" {
				t.Fatalf("before the renewal: Check = %q, want a usable login", got)
			}
			oidc := &fakeOIDC{err: tc.err}
			svc := renewing(t, cache, oidc)

			if err := svc.RefreshToken(context.Background()); err != nil {
				t.Fatalf("RefreshToken: %v", err)
			}
			if st := svc.State(); st.Status != service.StatusIdle {
				t.Errorf("status = %v, want idle", st.Status)
			}
			entry, ok := config.ReadDenCache(cache).Sessions["alpha"]
			if !ok || entry.ExpiresAt.After(time.Now()) {
				t.Errorf("den's record = %+v, %v; want one that has ended", entry, ok)
			}
			if got := logins.Check("alpha-prof", time.Now()); got == "" {
				t.Error("the Secrets pane still takes the ended session for a login")
			}

			// A new login, by den or the CLI, brings it back.
			cachedLogin(t, cache, "alpha", time.Now().Add(time.Hour), "r1")
			if err := svc.RefreshToken(context.Background()); err != nil {
				t.Fatalf("RefreshToken: %v", err)
			}
			if st := svc.State(); st.Status != service.StatusConnected {
				t.Errorf("after a new login: status = %v, want connected", st.Status)
			}
			if got := logins.Check("alpha-prof", time.Now()); got != "" {
				t.Errorf("after a new login: Check = %q", got)
			}
		})
	}
}

// The CLI or another den may spend the refresh token a moment before den
// does: IAM Identity Center then refuses den's, but the file holds a renewed
// login, and that is not the end of the session.
func TestRenewalBeatenByAnotherProcessRecordsNothing(t *testing.T) {
	dir := t.TempDir()
	cachedLogin(t, dir, "alpha", time.Now().Add(-10*time.Minute), "r1")
	oidc := &fakeOIDC{err: &types.InvalidGrantException{}}
	oidc.meanwhile = func() {
		// A file written in the same instant could keep its mod time.
		time.Sleep(10 * time.Millisecond)
		cachedLogin(t, dir, "alpha", time.Now().Add(time.Hour), "r2")
	}
	svc := renewing(t, dir, oidc)

	if err := svc.RefreshToken(context.Background()); err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if _, ok := config.ReadDenCache(dir).Sessions["alpha"]; ok {
		t.Error("a refusal after another process renewed the login recorded the session as ended")
	}
	if st := svc.State(); st.Status != service.StatusConnected || st.TokenExpires == nil {
		t.Errorf("status = %v, expires %v; want connected on the other process's token", st.Status, st.TokenExpires)
	}
}

// The AWS pane and the Secrets pane read the same files; they used two rules
// and disagreed, the pane counting down hours while every secret said "SSO
// login expired". They must now agree on every state the caches can be in.
func TestTheAWSPaneAndTheSecretsAgree(t *testing.T) {
	now := time.Now()
	type files struct {
		token      time.Time // the access token's expiry; zero for no file
		refresh    string
		registered time.Time
		written    time.Time // the file's mod time; zero for now
		den        time.Time // den's record's end; zero for none
	}
	months := now.AddDate(0, 3, 0)
	for _, tc := range []struct {
		name string
		files
		live bool
	}{
		{"den login, access token ran out unused", files{token: now.Add(-20 * time.Minute), refresh: "r1", registered: months, den: now.Add(6 * time.Hour)}, true},
		{"den login, token valid", files{token: now.Add(time.Hour), refresh: "r1", registered: months, den: now.Add(6 * time.Hour)}, true},
		{"logged out, den's record runs", files{den: now.Add(6 * time.Hour)}, false},
		{"login outside den, refreshable", files{token: now.Add(-20 * time.Minute), refresh: "r1", registered: months}, true},
		{"login outside den, token valid", files{token: now.Add(time.Hour), registered: months}, true},
		{"den's record ran out, file not written since", files{token: now.Add(-2 * time.Hour), refresh: "r1", registered: months, written: now.Add(-3 * time.Hour), den: now.Add(-time.Hour)}, false},
		{"no refresh token, expired", files{token: now.Add(-time.Minute), registered: months}, false},
		{"registration ran out", files{token: now.Add(-time.Minute), refresh: "r1", registered: now.Add(-time.Hour)}, false},
		{"never logged in", files{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, cache := awsHome(t)
			if !tc.token.IsZero() {
				path := registeredLogin(t, cache, "alpha", tc.token, tc.refresh, tc.registered)
				if !tc.written.IsZero() {
					if err := os.Chtimes(path, tc.written, tc.written); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !tc.den.IsZero() {
				if err := config.WriteDenCache(cache, "alpha", tc.den.Add(-8*time.Hour), tc.den); err != nil {
					t.Fatal(err)
				}
			}

			pane := New("", "alpha", "", "https://alpha.example/start", "eu-central-1", "", cache, "").State().Status == service.StatusConnected
			reason := secrets.NewAWSLogins(filepath.Join(home, "config")).Check("alpha-prof", time.Now())
			if pane != tc.live || (reason == "") != tc.live {
				t.Errorf("AWS pane connected = %v, Secrets reason = %q; want both to say live = %v", pane, reason, tc.live)
			}
		})
	}
}

// awsHome is an AWS config directory with session alpha and a profile using
// it, and the SSO cache beside the config, where the CLI keeps it.
func awsHome(t *testing.T) (home, cache string) {
	t.Helper()
	home = t.TempDir()
	cache = config.SSOCacheDir(filepath.Join(home, "config"))
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "[sso-session alpha]\nsso_start_url = https://alpha.example/start\nsso_region = eu-central-1\n\n" +
		"[profile alpha-prof]\nsso_session = alpha\n"
	if err := os.WriteFile(filepath.Join(home, "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, cache
}

func ptr(s string) *string { return &s }

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
