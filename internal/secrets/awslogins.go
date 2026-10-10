package secrets

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lukaszgard/den/internal/config"
)

// AWSLogins answers whether an AWS profile has a login that can be used. One
// value is shared by every item of a config: it reads the AWS config and the
// SSO token cache once, and again only when those files change, so probing a
// hundred items costs one parse rather than a hundred.
//
// A nil *AWSLogins checks nothing.
type AWSLogins struct {
	path string // the AWS config file; the SSO cache sits beside it

	mu       sync.Mutex
	loaded   bool
	sig      string // what the files looked like when they were read
	readable bool
	profiles map[string]ssoLogin
	tokens   []config.SSOToken
	loads    int // how often the files were read
}

// ssoLogin is the SSO login a profile needs. The zero value is a profile
// without one: static keys and credential_process are left to the provider.
type ssoLogin struct{ session, startURL string }

// NewAWSLogins reads logins from awsConfig (~/.aws/config by default) and the
// SSO cache beside it. With an empty path nothing is checked.
func NewAWSLogins(awsConfig string) *AWSLogins {
	return &AWSLogins{path: awsConfig}
}

// Check returns why the named AWS profile cannot work, or "" when it looks
// usable: it must exist in the AWS config and, for an SSO profile, have an
// unexpired cached login. Nothing is checked for an empty name, and an
// unreadable AWS config is not reported: the SDK or CLI may still find
// credentials elsewhere (AWS_CONFIG_FILE, environment variables).
func (a *AWSLogins) Check(profile string, now time.Time) string {
	if a == nil || a.path == "" || profile == "" {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refresh()
	if !a.readable {
		return ""
	}
	login, known := a.profiles[profile]
	if !known {
		return "unknown AWS profile " + profile
	}
	if login == (ssoLogin{}) {
		return ""
	}
	for _, tok := range a.tokens {
		if tok.Matches(login.session, login.startURL) {
			if tok.ExpiresAt.After(now) {
				return ""
			}
			break
		}
	}
	return "SSO login expired — log in from the AWS pane"
}

// Stamp changes whenever an answer of Check may have: a login or logout, an
// edit of the AWS config, a token running out. Comparing two stamps costs the
// same however many items there are, so a caller can ask often and probe its
// items only when the stamp moved.
func (a *AWSLogins) Stamp(now time.Time) string {
	if a == nil || a.path == "" {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refresh()
	b := make([]byte, 0, len(a.sig)+len(a.tokens))
	b = append(b, a.sig...)
	for _, tok := range a.tokens {
		if tok.ExpiresAt.After(now) {
			b = append(b, '+')
		} else {
			b = append(b, '-')
		}
	}
	return string(b)
}

// refresh reads the files again if they changed since the last read. Only
// what Check needs is kept: which login each profile uses and when each
// cached login runs out.
func (a *AWSLogins) refresh() {
	sig := a.signature()
	if a.loaded && string(sig) == a.sig {
		return
	}
	a.loaded, a.sig = true, string(sig)
	a.loads++
	a.profiles, a.tokens = nil, config.ListSSOTokens(config.SSOCacheDir(a.path))

	all, err := config.LoadAWSProfiles(a.path)
	a.readable = err == nil
	if err != nil {
		return
	}
	sessions := map[string]string{} // sso-session → start URL
	for _, p := range all {
		if p.Kind == "sso-session" {
			sessions[p.Name] = p.SSOStartURL
		}
	}
	a.profiles = make(map[string]ssoLogin, len(all))
	for _, p := range all {
		if p.Kind == "sso-session" {
			continue
		}
		// A legacy profile carries sso_start_url itself, without an
		// sso-session section.
		login := ssoLogin{session: p.SSOSession, startURL: p.SSOStartURL}
		if p.SSOSession != "" {
			login.startURL = sessions[p.SSOSession]
		}
		a.profiles[p.Name] = login
	}
}

// signature describes the AWS config and the cached logins without opening
// them: size and mod time of each. `aws sso login` rewrites the token file,
// so a login shows up here.
func (a *AWSLogins) signature() []byte {
	b := make([]byte, 0, 256)
	add := func(name string, fi os.FileInfo) {
		b = append(b, name...)
		b = append(b, ':')
		b = strconv.AppendInt(b, fi.Size(), 36)
		b = append(b, ':')
		b = strconv.AppendInt(b, fi.ModTime().UnixNano(), 36)
		b = append(b, ';')
	}
	if fi, err := os.Stat(a.path); err == nil {
		add("", fi)
	}
	b = append(b, '|')
	entries, _ := os.ReadDir(config.SSOCacheDir(a.path))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if fi, err := e.Info(); err == nil {
			add(e.Name(), fi)
		}
	}
	return b
}
