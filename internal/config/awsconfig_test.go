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
	writeCache(t, dir, "b.json", `{"sessionName":"corp","accessToken":"y","expiresAt":"2031-01-02T03:04:05UTC"}`)
	// What else lives in the cache: a client registration, den's own record,
	// a file that is not JSON, a login whose expiry cannot be read.
	writeCache(t, dir, "client.json", `{"clientId":"c","clientSecret":"s","expiresAt":"2030-01-02T03:04:05Z"}`)
	writeCache(t, dir, "den.json", `{"sessions":{}}`)
	writeCache(t, dir, "broken.json", `{`)
	writeCache(t, dir, "undated.json", `{"startUrl":"https://corp/start","accessToken":"z","expiresAt":"soon"}`)
	writeCache(t, dir, "notes.txt", `{"startUrl":"https://corp/start","accessToken":"x","expiresAt":"2030-01-02T03:04:05Z"}`)

	got := ListSSOTokens(dir)
	if len(got) != 2 {
		t.Fatalf("tokens = %+v, want the two logins", got)
	}
	if got[0].StartURL != "https://corp/start" || got[0].ExpiresAt.Year() != 2030 || got[0].IssuedAt.IsZero() {
		t.Errorf("first token = %+v", got[0])
	}
	if got[1].SessionName != "corp" || got[1].ExpiresAt.Year() != 2031 {
		t.Errorf("second token = %+v", got[1])
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
