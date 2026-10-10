package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const expiredReason = "SSO login expired — log in from the AWS pane"

// awsHome writes an AWS config with an SSO profile, a static-key profile and
// a legacy SSO profile, and returns the config's path. There is no login yet.
func awsHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfg := `[sso-session corp]
sso_start_url = https://corp.awsapps.com/start
sso_region = eu-central-1

[profile sso-prof]
sso_session = corp
sso_account_id = 1
sso_role_name = dev

[profile static]
region = eu-central-1

[profile legacy]
sso_start_url = https://legacy.awsapps.com/start
`
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sso", "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "config")
}

// login writes the cached token of start URL url, expiring at exp. mod is the
// file's mod time: two logins written within the file system's clock tick
// would otherwise look like the same file.
func login(t *testing.T, awsConfig, url string, exp, mod time.Time) {
	t.Helper()
	file := filepath.Join(filepath.Dir(awsConfig), "sso", "cache", filepath.Base(filepath.Dir(url))+".json")
	tok := fmt.Sprintf(`{"startUrl":%q,"accessToken":"x","expiresAt":%q}`, url, exp.UTC().Format(time.RFC3339))
	if err := os.WriteFile(file, []byte(tok), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, mod, mod); err != nil {
		t.Fatal(err)
	}
}

const corpURL, legacyURL = "https://corp.awsapps.com/start", "https://legacy.awsapps.com/start"

func TestAWSLoginsCheck(t *testing.T) {
	now := time.Now()
	valid := awsHome(t)
	login(t, valid, corpURL, now.Add(time.Hour), now)
	expired := awsHome(t)
	login(t, expired, corpURL, now.Add(-time.Minute), now)

	cases := []struct {
		name, profile, awsConfig, want string
	}{
		{"no profile", "", valid, ""},
		{"no aws config", "sso-prof", "", ""},
		{"unknown profile", "typo", valid, "unknown AWS profile typo"},
		{"sso valid", "sso-prof", valid, ""},
		{"sso expired", "sso-prof", expired, expiredReason},
		{"static keys not checked", "static", expired, ""},
		{"legacy sso without token", "legacy", valid, expiredReason},
		{"unreadable aws config is not an error", "anything", filepath.Join(t.TempDir(), "none"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewAWSLogins(tc.awsConfig).Check(tc.profile, now); got != tc.want {
				t.Errorf("Check = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAWSLoginsNilChecksNothing(t *testing.T) {
	var a *AWSLogins
	if got := a.Check("sso-prof", time.Now()); got != "" {
		t.Errorf("Check = %q, want nothing", got)
	}
	if got := a.Stamp(time.Now()); got != "" {
		t.Errorf("Stamp = %q, want empty", got)
	}
}

// The point of sharing one value: a hundred items asking costs one read of
// the AWS config and the token cache, and a login that happens later is seen
// without anyone telling it.
func TestAWSLoginsReadsFilesOncePerChange(t *testing.T) {
	now := time.Now()
	home := awsHome(t)
	a := NewAWSLogins(home)

	for range 100 {
		if got := a.Check("sso-prof", now); got != expiredReason {
			t.Fatalf("before login: Check = %q", got)
		}
	}
	if a.loads != 1 {
		t.Fatalf("files read %d times for 100 checks, want 1", a.loads)
	}

	login(t, home, corpURL, now.Add(time.Hour), now)
	for range 100 {
		if got := a.Check("sso-prof", now); got != "" {
			t.Fatalf("after login: Check = %q", got)
		}
	}
	if a.loads != 2 {
		t.Fatalf("files read %d times, want once more after the login", a.loads)
	}

	// A token is valid until its expiry, which no file change announces.
	if got := a.Check("sso-prof", now.Add(2*time.Hour)); got != expiredReason {
		t.Errorf("past the expiry: Check = %q", got)
	}
	if a.loads != 2 {
		t.Errorf("an expiry must not need the files read again (%d reads)", a.loads)
	}
}

func TestAWSLoginsSeesConfigEdits(t *testing.T) {
	now := time.Now()
	home := awsHome(t)
	a := NewAWSLogins(home)
	if got := a.Check("added", now); got != "unknown AWS profile added" {
		t.Fatalf("Check = %q", got)
	}
	f, err := os.OpenFile(home, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n[profile added]\nregion = eu-central-1\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if got := a.Check("added", now); got != "" {
		t.Errorf("after the edit: Check = %q", got)
	}
}

func TestAWSLoginsStamp(t *testing.T) {
	now := time.Now()
	home := awsHome(t)
	a := NewAWSLogins(home)

	before := a.Stamp(now)
	if before == "" {
		t.Fatal("a readable AWS config must have a stamp")
	}
	if again := a.Stamp(now.Add(time.Minute)); again != before {
		t.Error("the stamp changed although nothing did")
	}

	login(t, home, corpURL, now.Add(time.Hour), now)
	loggedIn := a.Stamp(now)
	if loggedIn == before {
		t.Error("a login must change the stamp")
	}
	if again := a.Stamp(now.Add(30 * time.Minute)); again != loggedIn {
		t.Error("the stamp changed while the login was still valid")
	}
	if a.Stamp(now.Add(2*time.Hour)) == loggedIn {
		t.Error("the login running out must change the stamp")
	}

	// A second login of the same length as the first: only the file's time differs.
	login(t, home, corpURL, now.Add(time.Hour), now.Add(time.Second))
	if a.Stamp(now) == loggedIn {
		t.Error("a new login must change the stamp")
	}

	login(t, home, legacyURL, now.Add(time.Hour), now)
	if got := a.Check("legacy", now); got != "" {
		t.Errorf("legacy profile after its login: Check = %q", got)
	}
}
