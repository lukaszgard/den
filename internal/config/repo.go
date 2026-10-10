package config

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Repo is a git repository named by runbooks.sources.
type Repo struct {
	// Clone is what git fetches: the URL as written, or https:// for the
	// host/owner/repo and owner/repo shorthands.
	Clone string
	// ID names the repository whatever form its URL takes: host/owner/repo
	// in lower case, without .git. allow_sources patterns match it, and
	// den's checkouts are kept under it.
	ID string
}

var (
	// scpRepo is git's scp-like form, user@host:owner/repo.git.
	scpRepo = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+@)?([A-Za-z0-9.-]+):(.*)$`)
	// shortRepo is host/owner/repo or owner/repo, without a scheme.
	shortRepo = regexp.MustCompile(`^[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)+$`)
)

// errRepoCredentials is returned for a URL carrying a token or password:
// den.yaml is meant to be committed, and every URL den shows would show it.
var errRepoCredentials = errors.New("url holds a user, token or password: leave it out and let git's credential helper or your SSH key supply it")

// ParseRepo reads a repository URL as written in den.yaml: https://, ssh://,
// git:// and file:// URLs, git's user@host:owner/repo form, an absolute path,
// host/owner/repo, or owner/repo on GitHub. Errors never repeat the URL, in
// case it carries a credential.
func ParseRepo(raw string) (Repo, error) {
	s := strings.TrimSpace(raw)
	switch {
	case s == "":
		return Repo{}, errors.New("url is empty")
	case strings.HasPrefix(s, "-") || strings.ContainsAny(s, " \t\r\n"):
		// A leading dash would reach git as an option.
		return Repo{}, errors.New("url is not a git repository URL")
	}
	if strings.Contains(s, "://") {
		return parseRepoURL(s)
	}
	if filepath.IsAbs(s) || strings.HasPrefix(s, "/") {
		p := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(s)), ".git")
		return Repo{Clone: s, ID: "file" + p}, nil
	}
	if m := scpRepo.FindStringSubmatch(s); m != nil {
		p := repoPath(m[2])
		if p == "" {
			return Repo{}, errors.New("url names no repository: give host:owner/repo")
		}
		return Repo{Clone: s, ID: strings.ToLower(m[1]) + "/" + p}, nil
	}
	if shortRepo.MatchString(s) {
		parts := strings.Split(strings.TrimSuffix(s, ".git"), "/")
		switch {
		case len(parts) == 2 && !strings.Contains(parts[0], "."):
			s = "github.com/" + s
		case len(parts) < 3 || !strings.Contains(parts[0], "."):
			return Repo{}, errors.New("url is not a git repository URL: give host/owner/repo or a full URL")
		}
		return Repo{Clone: "https://" + s, ID: strings.ToLower(strings.TrimSuffix(s, ".git"))}, nil
	}
	return Repo{}, errors.New("url is not a git repository URL: give host/owner/repo or a full URL")
}

func parseRepoURL(s string) (Repo, error) {
	u, err := url.Parse(s)
	if err != nil {
		return Repo{}, errors.New("url is not a git repository URL")
	}
	switch u.Scheme {
	case "https", "http":
		if u.User != nil {
			return Repo{}, errRepoCredentials
		}
	case "ssh", "git":
		if _, has := u.User.Password(); has {
			return Repo{}, errRepoCredentials
		}
	case "file":
		p := strings.TrimSuffix(path.Clean(u.Path), ".git")
		if p == "/" || p == "." {
			return Repo{}, errors.New("url names no repository")
		}
		return Repo{Clone: s, ID: "file" + p}, nil
	default:
		return Repo{}, fmt.Errorf("url is not a git repository URL: %s:// is not https, ssh, git or file", u.Scheme)
	}
	host, p := strings.ToLower(u.Hostname()), repoPath(u.Path)
	if host == "" || p == "" {
		return Repo{}, errors.New("url names no repository: give host/owner/repo")
	}
	return Repo{Clone: s, ID: host + "/" + p}, nil
}

// repoPath is the owner/repo part of a URL's path, as an ID holds it.
func repoPath(p string) string {
	p = strings.TrimSuffix(strings.Trim(p, "/"), ".git")
	return strings.ToLower(strings.Trim(p, "/"))
}

// Allowed reports whether one of patterns, as runbooks.allow_sources writes
// them ("ghe.acme.com/ops/*"), matches the repository. No patterns allow
// every repository.
func (r Repo) Allowed(patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if ok, _ := path.Match(strings.ToLower(strings.TrimSpace(p)), r.ID); ok {
			return true
		}
	}
	return false
}

// SourceNameFromURL suggests a source name for a repository URL: its last
// path segment, lower case, with anything but letters and digits as dashes.
func SourceNameFromURL(raw string) string {
	s := strings.TrimRight(strings.TrimSpace(raw), "/")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.ToLower(strings.TrimSuffix(s, ".git"))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	if name := strings.Trim(b.String(), "-"); name != "" {
		return name
	}
	return "runbooks"
}
