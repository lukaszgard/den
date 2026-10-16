package config

import (
	"bufio"
	"crypto/sha1" //nolint:gosec // the AWS CLI names its token files with it
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// KeyVal is a single key=value pair from an AWS config section.
type KeyVal struct {
	Key, Value string
}

// AWSProfile represents a single [profile ...] or [sso-session ...] entry
// from ~/.aws/config.
type AWSProfile struct {
	Name        string // display name (without "profile " prefix)
	SSOSession  string // value of sso_session key, empty if not set
	Region      string // value of region key
	Kind        string // "profile" or "sso-session"
	SSOStartURL string // sso_start_url (for sso-session sections)
	SSORegion   string // sso_region (for sso-session sections)
	// Bare marks a [name] section without the "profile " prefix. The AWS CLI
	// and SDKs ignore those in the config file (only the credentials file
	// names profiles that way), so it cannot be used as a profile.
	Bare    bool
	RawKeys []KeyVal // all key=value pairs in order (for detail view)
}

// LoadAWSProfiles parses an AWS config file in INI format and returns all
// profiles and SSO sessions. Results are sorted: sso-sessions first, then
// profiles alphabetically.
func LoadAWSProfiles(cfgPath string) ([]AWSProfile, error) {
	f, err := os.Open(cfgPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var profiles []AWSProfile
	var current *AWSProfile

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip blank lines and comments
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		// Section header
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if current != nil {
				profiles = append(profiles, *current)
			}
			section := line[1 : len(line)-1]
			p := AWSProfile{}
			switch {
			case section == "default":
				p.Kind = "profile"
				p.Name = "default"
			case strings.HasPrefix(section, "profile "):
				p.Kind = "profile"
				p.Name = strings.TrimSpace(strings.TrimPrefix(section, "profile "))
			case strings.HasPrefix(section, "sso-session "):
				p.Kind = "sso-session"
				p.Name = strings.TrimSpace(strings.TrimPrefix(section, "sso-session "))
			default:
				p.Kind = "profile"
				p.Name = section
				p.Bare = true
			}
			current = &p
			continue
		}

		// Key=value line
		if current != nil {
			if idx := strings.IndexByte(line, '='); idx > 0 {
				k := strings.TrimSpace(line[:idx])
				v := strings.TrimSpace(line[idx+1:])
				current.RawKeys = append(current.RawKeys, KeyVal{Key: k, Value: v})
				switch k {
				case "sso_session":
					current.SSOSession = v
				case "region":
					current.Region = v
				case "sso_start_url":
					current.SSOStartURL = v
				case "sso_region":
					current.SSORegion = v
				}
			}
		}
	}

	if current != nil {
		profiles = append(profiles, *current)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Sort: sso-sessions first, then profiles alphabetically within each group
	sort.SliceStable(profiles, func(i, j int) bool {
		if profiles[i].Kind != profiles[j].Kind {
			return profiles[i].Kind == "sso-session"
		}
		return profiles[i].Name < profiles[j].Name
	})

	return profiles, nil
}

type ssoTokenFile struct {
	SessionName           string `json:"sessionName"`
	StartURL              string `json:"startUrl"`
	ExpiresAt             string `json:"expiresAt"`
	AccessToken           string `json:"accessToken"`
	RefreshToken          string `json:"refreshToken"`
	RegistrationExpiresAt string `json:"registrationExpiresAt"`
}

// ssoTimeLayouts lists the formats used by the AWS CLI for token expiry.
var ssoTimeLayouts = []string{
	"2006-01-02T15:04:05UTC",
	"2006-01-02T15:04:05Z",
	time.RFC3339,
	"2006-01-02T15:04:05+0000",
}

// parseSSOTime reads a time the AWS CLI wrote in any of ssoTimeLayouts.
func parseSSOTime(s string) (time.Time, bool) {
	for _, layout := range ssoTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// SSOTokenInfo holds the expiry and issue time of an SSO access token.
// IssuedAt is derived from the cache file's modification time, which reflects
// when aws sso login last ran, or when the token was last renewed.
type SSOTokenInfo struct {
	ExpiresAt time.Time
	IssuedAt  time.Time
	// RefreshableUntil is when the client registration that swaps the refresh
	// token for a new access token runs out: zero when the file has no refresh
	// token, or a registration expiry den cannot read.
	RefreshableUntil time.Time
}

// SSOToken is one cached SSO login: whose it is and when it runs out. The
// access and refresh tokens themselves are never kept.
type SSOToken struct {
	SessionName string
	StartURL    string
	File        string // the cache file's name; SSOTokenPath says whose it is
	SSOTokenInfo
}

// Matches reports whether the token is the login of the given session or
// start URL (AWS CLI v2 stores startUrl, not sessionName).
func (t SSOToken) Matches(sessionName, startURL string) bool {
	return (sessionName != "" && t.SessionName == sessionName) ||
		(startURL != "" && t.StartURL == startURL)
}

// ListSSOTokens reads every login in ssoCacheDir, in file name order, with
// the file's mod time as IssuedAt. Files that are not a login (a client
// registration, den's own cache) or whose expiry cannot be read are skipped.
func ListSSOTokens(ssoCacheDir string) []SSOToken {
	entries, err := os.ReadDir(ssoCacheDir)
	if err != nil {
		return nil
	}
	var out []SSOToken
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if tok, ok := LoadSSOToken(filepath.Join(ssoCacheDir, e.Name())); ok {
			out = append(out, tok)
		}
	}
	return out
}

// LoadSSOToken reads the login in one cache file, with the file's mod time as
// IssuedAt. It reports false for a file that is not a login or whose expiry
// cannot be read.
func LoadSSOToken(path string) (SSOToken, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SSOToken{}, false
	}
	var tok ssoTokenFile
	if err := json.Unmarshal(data, &tok); err != nil || tok.AccessToken == "" {
		return SSOToken{}, false
	}
	expires, ok := parseSSOTime(tok.ExpiresAt)
	if !ok {
		return SSOToken{}, false
	}
	info := SSOTokenInfo{ExpiresAt: expires}
	if tok.RefreshToken != "" {
		info.RefreshableUntil, _ = parseSSOTime(tok.RegistrationExpiresAt)
	}
	if fi, err := os.Stat(path); err == nil {
		info.IssuedAt = fi.ModTime()
	}
	return SSOToken{SessionName: tok.SessionName, StartURL: tok.StartURL, File: filepath.Base(path), SSOTokenInfo: info}, true
}

// SSOTokenPath is the file the AWS CLI keeps an sso-session's login in, named
// after the session's SHA-1. It is the only file the SDK refreshes for the
// session: another file for the same start URL (a renamed session's) is not.
func SSOTokenPath(cacheDir, session string) string {
	sum := sha1.Sum([]byte(session)) //nolint:gosec // the CLI's file name, not a security use
	return filepath.Join(cacheDir, hex.EncodeToString(sum[:])+".json")
}

// SSOLogin is one session's login as the caches describe it.
type SSOLogin struct {
	Found bool      // the AWS CLI cache holds a login for the session
	Live  bool      // the CLI and SDKs can still use it, refreshing it if needed
	Until time.Time // when it runs out; zero when only IAM Identity Center knows
}

// FindSSOLogin decides whether the login of session (or of startURL, for a
// legacy profile) is alive. The access token's expiry is not its end: with an
// sso-session the token lasts an hour, and the CLI and SDKs swap in a new one
// from the refresh token, without a prompt, until the IAM Identity Center
// session ends. No file records that end, so a login that can be refreshed
// counts, unless den's own record of the session (den writes it at a login,
// with the role credentials' expiry) has run out and the file was not written
// since: the AWS pane then says the login ended, and a renewal or a new login
// rewrites the file.
//
// Until is den's record while it runs, what the AWS pane shows; otherwise the
// latest access token's expiry. For a login that is not live, it is the latest
// expiry known, so the caller can say when it ended.
func FindSSOLogin(session, startURL string, tokens []SSOToken, den *DenCache, now time.Time) SSOLogin {
	var entry DenCacheEntry
	hasEntry := false
	if den != nil && session != "" {
		entry, hasEntry = den.Sessions[session]
	}
	denRunning := hasEntry && entry.ExpiresAt.After(now)
	own := ""
	if session != "" {
		own = filepath.Base(SSOTokenPath("", session))
	}

	var login SSOLogin
	var latest time.Time // the latest access token's expiry
	for _, tok := range tokens {
		if !tok.Matches(session, startURL) {
			continue
		}
		login.Found = true
		if tok.ExpiresAt.After(latest) {
			latest = tok.ExpiresAt
		}
		refreshable := tok.File == own && tok.RefreshableUntil.After(now) &&
			(!hasEntry || denRunning || tok.IssuedAt.After(entry.ExpiresAt))
		if tok.ExpiresAt.After(now) || refreshable {
			login.Live = true
		}
	}
	switch {
	case !login.Found:
	case !login.Live:
		login.Until = latest
		if hasEntry && !denRunning && entry.ExpiresAt.After(latest) {
			login.Until = entry.ExpiresAt
		}
	case denRunning:
		login.Until = entry.ExpiresAt
	case latest.After(now):
		login.Until = latest
	}
	return login
}

// ReadSSOToken scans ssoCacheDir for the token file matching either
// sessionName or startURL (AWS CLI v2 stores startUrl, not sessionName).
// When several match (the cache keeps the files of a renamed session), the one
// that runs out last wins, so a stale file never hides a fresh login.
// Returns token info including the file mod time as IssuedAt, or nil if not found.
func ReadSSOToken(sessionName, startURL, ssoCacheDir string) *SSOTokenInfo {
	var best *SSOTokenInfo
	for _, tok := range ListSSOTokens(ssoCacheDir) {
		if tok.Matches(sessionName, startURL) && (best == nil || tok.ExpiresAt.After(best.ExpiresAt)) {
			info := tok.SSOTokenInfo
			best = &info
		}
	}
	return best
}

// SSOCacheDir returns the SSO token cache directory relative to the AWS
// config file path (e.g. ~/.aws/config → ~/.aws/sso/cache).
func SSOCacheDir(awsConfigPath string) string {
	return filepath.Join(filepath.Dir(awsConfigPath), "sso", "cache")
}

// DenCacheEntry holds den's own record of an SSO login event.
type DenCacheEntry struct {
	IssuedAt  time.Time `json:"issuedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// DenCache is the structure of ~/.aws/sso/cache/den.json.
type DenCache struct {
	Sessions map[string]DenCacheEntry `json:"sessions"`
}

const denCacheFile = "den.json"

// ReadDenCache reads ~/.aws/sso/cache/den.json. Returns an empty cache if the
// file does not exist.
func ReadDenCache(cacheDir string) *DenCache {
	data, err := os.ReadFile(filepath.Join(cacheDir, denCacheFile))
	if err != nil {
		return &DenCache{Sessions: map[string]DenCacheEntry{}}
	}
	var c DenCache
	if err := json.Unmarshal(data, &c); err != nil {
		return &DenCache{Sessions: map[string]DenCacheEntry{}}
	}
	if c.Sessions == nil {
		c.Sessions = map[string]DenCacheEntry{}
	}
	return &c
}

// WriteDenCache upserts a session entry in ~/.aws/sso/cache/den.json.
func WriteDenCache(cacheDir, sessionName string, issuedAt, expiresAt time.Time) error {
	c := ReadDenCache(cacheDir)
	c.Sessions[sessionName] = DenCacheEntry{IssuedAt: issuedAt, ExpiresAt: expiresAt}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cacheDir, denCacheFile), data, 0600)
}

type exportCredentialsOutput struct {
	Expiration string `json:"Expiration"`
}

// ExportCredentials runs "aws configure export-credentials --profile <profile>
// --format process" and returns the Expiration time from the output.
func ExportCredentials(profile string) (*time.Time, error) {
	out, err := exec.Command("aws", "configure", "export-credentials", //nolint:gosec
		"--profile", profile, "--format", "process").Output()
	if err != nil {
		return nil, err
	}
	var result exportCredentialsOutput
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339, result.Expiration)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// CredentialProfileNames lists the profile names in an AWS credentials file.
// Only the [section] lines are read; keys and secrets are never looked at.
func CredentialProfileNames(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var names []string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			// [profile x] is the config file's spelling; the CLI is lenient about
			// it here, and so is the check that a profile exists.
			names = append(names, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line[1:len(line)-1]), "profile ")))
		}
	}
	return names, nil
}
