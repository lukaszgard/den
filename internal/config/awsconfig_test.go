package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCache(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestListSSOTokensKeepsLoginsOnly(t *testing.T) {
	dir := t.TempDir()
	writeCache(t, dir, "a.json", `{"startUrl":"https://corp/start","accessToken":"x","expiresAt":"2030-01-02T03:04:05Z"}`)
	writeCache(t, dir, "b.json", `{"sessionName":"corp","accessToken":"y","expiresAt":"2031-01-02T03:04:05UTC",`+
		`"refreshToken":"r","registrationExpiresAt":"2031-04-02T03:04:05Z"}`)
	// A refresh token without a registration expiry den can read is not
	// refreshable: den cannot tell for how long.
	writeCache(t, dir, "c.json", `{"sessionName":"corp","accessToken":"y","expiresAt":"2031-01-02T03:04:05Z","refreshToken":"r"}`)
	// What else lives in the cache: a client registration, den's own record,
	// a file that is not JSON, a login whose expiry cannot be read.
	writeCache(t, dir, "client.json", `{"clientId":"c","clientSecret":"s","expiresAt":"2030-01-02T03:04:05Z"}`)
	writeCache(t, dir, "den.json", `{"sessions":{}}`)
	writeCache(t, dir, "broken.json", `{`)
	writeCache(t, dir, "undated.json", `{"startUrl":"https://corp/start","accessToken":"z","expiresAt":"soon"}`)
	writeCache(t, dir, "notes.txt", `{"startUrl":"https://corp/start","accessToken":"x","expiresAt":"2030-01-02T03:04:05Z"}`)

	got := ListSSOTokens(dir)
	if len(got) != 3 {
		t.Fatalf("tokens = %+v, want the three logins", got)
	}
	if got[0].StartURL != "https://corp/start" || got[0].ExpiresAt.Year() != 2030 || got[0].IssuedAt.IsZero() ||
		got[0].File != "a.json" || !got[0].RefreshableUntil.IsZero() {
		t.Errorf("first token = %+v", got[0])
	}
	if got[1].SessionName != "corp" || got[1].ExpiresAt.Year() != 2031 ||
		!got[1].RefreshableUntil.Equal(time.Date(2031, 4, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("second token = %+v", got[1])
	}
	if !got[2].RefreshableUntil.IsZero() {
		t.Errorf("third token = %+v, want it not refreshable", got[2])
	}
	if !got[0].Matches("", "https://corp/start") || !got[1].Matches("corp", "") {
		t.Error("a token must match by start URL or by session name")
	}
	if got[0].Matches("", "") || got[0].Matches("other", "https://other/start") {
		t.Error("a token must not match an empty or a different login")
	}
}

func TestListSSOTokensWithoutACache(t *testing.T) {
	if got := ListSSOTokens(filepath.Join(t.TempDir(), "none")); got != nil {
		t.Errorf("tokens = %+v, want none", got)
	}
}

// ReadSSOToken is the matching login that runs out last, whatever order the
// files sort in: the cache keeps the files of a renamed session, and a stale
// one must not hide a fresh login from the AWS pane, den doctor or the secrets.
func TestReadSSOTokenIsTheLatestMatch(t *testing.T) {
	dir := t.TempDir()
	writeCache(t, dir, "a.json", `{"startUrl":"https://corp/start","accessToken":"x","expiresAt":"2020-01-02T03:04:05Z"}`)
	writeCache(t, dir, "b.json", `{"startUrl":"https://corp/start","accessToken":"y","expiresAt":"2031-01-02T03:04:05Z"}`)
	writeCache(t, dir, "c.json", `{"startUrl":"https://corp/start","accessToken":"z","expiresAt":"2030-01-02T03:04:05Z"}`)

	tok := ReadSSOToken("", "https://corp/start", dir)
	if tok == nil || !tok.ExpiresAt.Equal(time.Date(2031, 1, 2, 3, 4, 5, 0, time.UTC)) || tok.IssuedAt.IsZero() {
		t.Fatalf("token = %+v, want b.json's", tok)
	}
	if ReadSSOToken("other", "https://other/start", dir) != nil {
		t.Error("an unknown session has no token")
	}
}

// The CLI names an sso-session's login after the session's SHA-1, and that is
// the one file the SDK refreshes.
func TestSSOTokenPath(t *testing.T) {
	want := filepath.Join("cache", "f151939f6e0d2575a600de87ff5e0ddb07a33850.json")
	if got := SSOTokenPath("cache", "bmwssorow"); got != want {
		t.Errorf("SSOTokenPath = %q, want %q", got, want)
	}
}

// With an sso-session the access token lasts an hour, and the CLI and SDKs
// swap in a new one from the refresh token until the IAM Identity Center
// session ends. Reading the access token's expiry as a logout blocked every
// secret for as long as nothing happened to use AWS.
func TestFindSSOLogin(t *testing.T) {
	now := time.Date(2026, 10, 15, 9, 48, 0, 0, time.UTC)
	const session, url = "corp", "https://corp/start"
	own := filepath.Base(SSOTokenPath("", session))
	denUntil := now.Add(3 * time.Hour)
	denRanOut := now.Add(-time.Hour)
	// tok is the session's own login: written at written, the access token
	// running out at expires, refreshable until refreshable (zero: not at all).
	tok := func(written, expires, refreshable time.Time) SSOToken {
		return SSOToken{StartURL: url, File: own, SSOTokenInfo: SSOTokenInfo{
			IssuedAt: written, ExpiresAt: expires, RefreshableUntil: refreshable}}
	}
	den := func(expires time.Time) *DenCache {
		return &DenCache{Sessions: map[string]DenCacheEntry{session: {IssuedAt: expires.Add(-8 * time.Hour), ExpiresAt: expires}}}
	}
	registration := now.AddDate(0, 3, 0)
	expired := now.Add(-10 * time.Minute) // written an hour before that

	cases := []struct {
		name   string
		tokens []SSOToken
		den    *DenCache
		want   SSOLogin
	}{
		{"access token valid", []SSOToken{tok(now, now.Add(time.Hour), time.Time{})}, nil,
			SSOLogin{Found: true, Live: true, Until: now.Add(time.Hour)}},
		{"access token expired, refreshable", []SSOToken{tok(expired.Add(-time.Hour), expired, registration)}, nil,
			SSOLogin{Found: true, Live: true}},
		{"refreshable, den's login still running", []SSOToken{tok(expired.Add(-time.Hour), expired, registration)}, den(denUntil),
			SSOLogin{Found: true, Live: true, Until: denUntil}},
		// den renewed the token half an hour before its login ran out.
		{"den's login ran out, file not written since", []SSOToken{tok(denRanOut.Add(-30*time.Minute), denRanOut.Add(30*time.Minute), registration)}, den(denRanOut),
			SSOLogin{Found: true, Until: denRanOut.Add(30 * time.Minute)}},
		{"expired before den's login ran out", []SSOToken{tok(now.Add(-3*time.Hour), now.Add(-2*time.Hour), time.Time{})}, den(denRanOut),
			SSOLogin{Found: true, Until: denRanOut}},
		{"file written after den's login ran out", []SSOToken{tok(denRanOut.Add(time.Minute), expired, registration)}, den(denRanOut),
			SSOLogin{Found: true, Live: true}},
		{"registration expired", []SSOToken{tok(expired.Add(-time.Hour), expired, now.Add(-time.Minute))}, nil,
			SSOLogin{Found: true, Until: expired}},
		{"not refreshable", []SSOToken{tok(expired.Add(-time.Hour), expired, time.Time{})}, nil,
			SSOLogin{Found: true, Until: expired}},
		// The SDK reads only the session's own file: a renamed session's
		// login for the same start URL is never refreshed for this one.
		{"another session's file", []SSOToken{{StartURL: url, File: "old-name.json", SSOTokenInfo: SSOTokenInfo{
			IssuedAt: expired.Add(-time.Hour), ExpiresAt: expired, RefreshableUntil: registration}}}, nil,
			SSOLogin{Found: true, Until: expired}},
		{"latest of several", []SSOToken{tok(now, now.Add(time.Hour), time.Time{}), tok(now, now.Add(30*time.Minute), time.Time{})}, nil,
			SSOLogin{Found: true, Live: true, Until: now.Add(time.Hour)}},
		{"no match", []SSOToken{{StartURL: "https://other/start", File: "x.json", SSOTokenInfo: SSOTokenInfo{ExpiresAt: now.Add(time.Hour)}}}, den(denUntil),
			SSOLogin{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FindSSOLogin(session, url, tc.tokens, tc.den, now)
			if got.Found != tc.want.Found || got.Live != tc.want.Live || !got.Until.Equal(tc.want.Until) {
				t.Errorf("FindSSOLogin = %+v, want %+v", got, tc.want)
			}
		})
	}
}
