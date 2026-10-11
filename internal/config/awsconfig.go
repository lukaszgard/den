package config

import (
	"bufio"
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
	SessionName string `json:"sessionName"`
	StartURL    string `json:"startUrl"`
	ExpiresAt   string `json:"expiresAt"`
	AccessToken string `json:"accessToken"`
}

// ssoTimeLayouts lists the formats used by the AWS CLI for token expiry.
var ssoTimeLayouts = []string{
	"2006-01-02T15:04:05UTC",
	"2006-01-02T15:04:05Z",
	time.RFC3339,
	"2006-01-02T15:04:05+0000",
}

// SSOTokenInfo holds the expiry and issue time of an SSO access token.
// IssuedAt is derived from the cache file's modification time, which reflects
// when aws sso login last ran.
type SSOTokenInfo struct {
	ExpiresAt time.Time
	IssuedAt  time.Time
}

// SSOToken is one cached SSO login: whose it is and when it runs out. The
// access token itself is never kept.
type SSOToken struct {
	SessionName string
	StartURL    string
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
		fpath := filepath.Join(ssoCacheDir, e.Name())
		data, err := os.ReadFile(fpath)
		if err != nil {
			continue
		}
		var tok ssoTokenFile
		if err := json.Unmarshal(data, &tok); err != nil {
			continue
		}
		if tok.AccessToken == "" {
			continue
		}
		for _, layout := range ssoTimeLayouts {
			t, err := time.Parse(layout, tok.ExpiresAt)
			if err != nil {
				continue
			}
			info := SSOTokenInfo{ExpiresAt: t}
			if fi, err := os.Stat(fpath); err == nil {
				info.IssuedAt = fi.ModTime()
			}
			out = append(out, SSOToken{SessionName: tok.SessionName, StartURL: tok.StartURL, SSOTokenInfo: info})
			break
		}
	}
	return out
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
