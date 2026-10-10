// Package update finds, downloads, verifies and installs new den releases. It
// holds no UI: the Updates pane drives it one step at a time and renders the
// progress.
//
// Releases come from one of three places. With an S3 bucket configured (a
// mirror you host), the `aws` CLI reads latest.json and the archives the release
// workflow publishes there. Otherwise the default reads the release feed on
// github.com: latest.json and the release's files by their URLs, which no API
// rate limit covers (the install scripts read the same). The GitHub releases API
// is used with GITHUB_TOKEN (what a private repository needs), for a GitHub
// Enterprise server, and when a release has no latest.json.
package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultGitHubAPI is the releases endpoint used when the feed cannot answer
// (a release without latest.json, a private repository) and with a token.
const DefaultGitHubAPI = "https://api.github.com/repos/lukaszgard/den/releases/latest"

// DefaultRepo is the owner/repo whose release feed is read by default.
const DefaultRepo = "lukaszgard/den"

// DefaultGitHubWeb is the site the release feed and the release files live on.
const DefaultGitHubWeb = "https://github.com"

// ChecksumsFile is the name GoReleaser gives the checksum list of a release.
const ChecksumsFile = "checksums.txt"

// ErrNoChecksums means the release publishes no checksums.txt. Releases made
// before den verified downloads do not have one, so callers may choose to go
// on without verification — but must say so.
var ErrNoChecksums = errors.New("release has no " + ChecksumsFile)

// Source says where releases are looked up and downloaded from.
type Source struct {
	// S3 is used when S3Bucket is set.
	S3Bucket   string
	S3Prefix   string // e.g. "den/", no leading slash
	AWSProfile string
	AWSRegion  string

	// GitHubAPI is the releases/latest URL; DefaultGitHubAPI when empty. A
	// GitHub Enterprise server has its own. On api.github.com it also names the
	// repository whose release feed is read (a fork's).
	GitHubAPI string
	// Token authenticates GitHub requests (GITHUB_TOKEN); may be empty. A token
	// selects the releases API: it has the higher limit and reads private repos.
	Token string
	// GitHubWeb is the site of the release feed and of the release files;
	// DefaultGitHubWeb when empty. Tests point it at a local server.
	GitHubWeb string

	// HTTPClient is used for GitHub requests; a client with timeouts when nil.
	HTTPClient *http.Client
}

// UsesS3 reports whether the source reads from S3 rather than GitHub.
func (s Source) UsesS3() bool { return s.S3Bucket != "" }

// Release is the latest published release for this platform.
type Release struct {
	Version string // tag, e.g. v0.0.41
	URL     string // release page (GitHub) or archive location (S3)
	Notes   string // release notes, markdown

	Archive      string // archive file name for this platform
	ArchiveURL   string // GitHub asset URL (API or release feed); empty for S3
	ChecksumsURL string // GitHub URL of checksums.txt; empty when absent or S3

	// feed marks a release built from the release feed, whose files are named
	// by URL without asking GitHub which exist: a 404 on checksums.txt is
	// ErrNoChecksums there.
	feed bool
}

// ArchiveName is the GoReleaser archive name for version on goos/goarch.
// GoReleaser strips the tag's leading "v" from archive names.
func ArchiveName(version, goos, goarch string) string {
	ver := strings.TrimPrefix(version, "v")
	if goos == "windows" {
		return fmt.Sprintf("den_%s_%s_%s.zip", ver, goos, goarch)
	}
	return fmt.Sprintf("den_%s_%s_%s.tar.gz", ver, goos, goarch)
}

// SameVersion reports whether two version strings name the same release,
// ignoring a leading "v" on either.
func SameVersion(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// Latest returns the latest release for goos/goarch.
func (s Source) Latest(ctx context.Context, goos, goarch string) (Release, error) {
	if s.UsesS3() {
		return s.latestS3(ctx, goos, goarch)
	}
	if repo, ok := s.feedRepo(); ok {
		rel, err := s.latestFeed(ctx, repo, goos, goarch)
		if !isNotFound(err) {
			return rel, err
		}
		// No latest.json: a release cut before it was published, a fork's
		// workflow without that step, a private repository answered anonymously,
		// or the seconds before the workflow uploads it. The API knows better.
	}
	return s.latestGitHub(ctx, goos, goarch)
}

// Location names where Latest reads: the S3 URL, the release feed or the API.
func (s Source) Location() string {
	if s.UsesS3() {
		return s.s3URL("")
	}
	if repo, ok := s.feedRepo(); ok {
		return s.feedURL(repo)
	}
	api := s.GitHubAPI
	if api == "" {
		api = DefaultGitHubAPI
	}
	if s.Token != "" {
		api += " (GITHUB_TOKEN)"
	}
	return api
}

// feedRepo returns the owner/repo whose release feed this source reads, or
// false when it reads the API: a token needs the API (the feed is public, and a
// private repo is only answerable with one), and any GitHubAPI that is not
// api.github.com is a server whose URL layout is not ours to guess.
func (s Source) feedRepo() (string, bool) {
	if s.Token != "" {
		return "", false
	}
	if s.GitHubAPI == "" || s.GitHubAPI == DefaultGitHubAPI {
		return DefaultRepo, true
	}
	u, err := url.Parse(s.GitHubAPI)
	if err != nil || u.Scheme != "https" || u.Hostname() != "api.github.com" {
		return "", false
	}
	// /repos/<owner>/<repo>/releases/latest
	p := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(p) != 5 || p[0] != "repos" || p[1] == "" || p[2] == "" || p[3] != "releases" || p[4] != "latest" {
		return "", false
	}
	return p[1] + "/" + p[2], true
}

func (s Source) web() string {
	if s.GitHubWeb == "" {
		return DefaultGitHubWeb
	}
	return strings.TrimRight(s.GitHubWeb, "/")
}

func (s Source) feedURL(repo string) string {
	return s.web() + "/" + repo + "/releases/latest/download/latest.json"
}

// latestFeed reads latest.json from the release feed — the file the release
// workflow publishes and the install scripts read — and names the release's
// files by URL, so no API call is made. A 404 is returned as a statusError for
// Latest to fall back on.
func (s Source) latestFeed(ctx context.Context, repo, goos, goarch string) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.feedURL(repo), nil)
	if err != nil {
		return Release{}, err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Release{}, err
		}
		return Release{}, fmt.Errorf("could not reach github.com — den needs HTTPS access to github.com; behind a proxy set HTTPS_PROXY: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if err := s.checkStatus(resp); err != nil {
		return Release{}, err
	}
	var meta struct {
		Version string `json:"version"`
		Notes   string `json:"notes"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&meta); err != nil {
		return Release{}, fmt.Errorf("parse latest.json: %w", err)
	}
	if meta.Version == "" {
		return Release{}, fmt.Errorf("latest.json has no version")
	}
	archive := ArchiveName(meta.Version, goos, goarch)
	base := s.web() + "/" + repo + "/releases/"
	return Release{
		Version:      meta.Version,
		URL:          base + "tag/" + meta.Version,
		Notes:        meta.Notes,
		Archive:      archive,
		ArchiveURL:   base + "download/" + meta.Version + "/" + archive,
		ChecksumsURL: base + "download/" + meta.Version + "/" + ChecksumsFile,
		feed:         true,
	}, nil
}

func (s Source) latestS3(ctx context.Context, goos, goarch string) (Release, error) {
	out, err := s.aws(ctx, "s3", "cp", s.s3URL("latest.json"), "-")
	if err != nil {
		return Release{}, err
	}
	var meta struct {
		Version string `json:"version"`
		Notes   string `json:"notes"`
	}
	if err := json.Unmarshal(out, &meta); err != nil {
		return Release{}, fmt.Errorf("parse latest.json: %w", err)
	}
	if meta.Version == "" {
		return Release{}, fmt.Errorf("latest.json has no version")
	}
	archive := ArchiveName(meta.Version, goos, goarch)
	return Release{
		Version: meta.Version,
		URL:     s.s3URL(meta.Version + "/" + archive),
		Notes:   meta.Notes,
		Archive: archive,
	}, nil
}

func (s Source) latestGitHub(ctx context.Context, goos, goarch string) (Release, error) {
	api := s.GitHubAPI
	if api == "" {
		api = DefaultGitHubAPI
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return Release{}, err
	}
	s.authorize(req)
	resp, err := s.client().Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if err := s.checkStatus(resp); err != nil {
		return Release{}, err
	}
	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Body    string `json:"body"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"url"` // API URL — fetched with Accept: application/octet-stream
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&release); err != nil {
		return Release{}, fmt.Errorf("parse release: %w", err)
	}
	if release.TagName == "" {
		return Release{}, fmt.Errorf("no release tag returned by API")
	}
	rel := Release{
		Version: release.TagName,
		URL:     release.HTMLURL,
		Notes:   release.Body,
		Archive: ArchiveName(release.TagName, goos, goarch),
	}
	for _, a := range release.Assets {
		switch a.Name {
		case rel.Archive:
			rel.ArchiveURL = a.URL
		case ChecksumsFile:
			rel.ChecksumsURL = a.URL
		}
	}
	return rel, nil
}

// Download writes the release archive to path. When progress is non-nil it
// holds the bytes written so far; total, when non-nil, receives the size if
// known. Both are safe to read from another goroutine while this runs.
func (s Source) Download(ctx context.Context, rel Release, path string, progress, total *atomic.Int64) error {
	if s.UsesS3() {
		key := s.S3Prefix + rel.Version + "/" + rel.Archive
		if total != nil {
			if n := s.s3Size(ctx, key); n > 0 {
				total.Store(n)
			}
		}
		_, err := s.aws(ctx, "s3", "cp", "s3://"+s.S3Bucket+"/"+key, path, "--no-progress")
		return err
	}
	if rel.ArchiveURL == "" {
		return fmt.Errorf("release %s has no %s asset", rel.Version, rel.Archive)
	}
	return s.downloadGitHub(ctx, rel.ArchiveURL, path, progress, total)
}

// Checksums returns the release's SHA-256 checksums by file name, or
// ErrNoChecksums when it publishes none.
func (s Source) Checksums(ctx context.Context, rel Release) (map[string]string, error) {
	var data []byte
	if s.UsesS3() {
		key := s.S3Prefix + rel.Version + "/" + ChecksumsFile
		if s.s3Size(ctx, key) <= 0 {
			return nil, ErrNoChecksums
		}
		out, err := s.aws(ctx, "s3", "cp", "s3://"+s.S3Bucket+"/"+key, "-")
		if err != nil {
			return nil, err
		}
		data = out
	} else {
		if rel.ChecksumsURL == "" {
			return nil, ErrNoChecksums
		}
		var buf bytes.Buffer
		if err := s.fetchGitHub(ctx, rel.ChecksumsURL, &buf, nil, nil); err != nil {
			if rel.feed && isNotFound(err) {
				return nil, ErrNoChecksums
			}
			return nil, fmt.Errorf("download %s: %w", ChecksumsFile, err)
		}
		data = buf.Bytes()
	}
	return ParseChecksums(data)
}

// ParseChecksums parses GoReleaser's "<sha256>  <file>" lines.
func ParseChecksums(data []byte) (map[string]string, error) {
	sums := map[string]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 || len(fields[0]) != sha256.Size*2 {
			return nil, fmt.Errorf("malformed %s line %q", ChecksumsFile, line)
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return nil, fmt.Errorf("malformed %s line %q", ChecksumsFile, line)
		}
		sums[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	if len(sums) == 0 {
		return nil, fmt.Errorf("empty %s", ChecksumsFile)
	}
	return sums, nil
}

// Verify checks the file at path against the checksum listed for name.
func Verify(path, name string, sums map[string]string) error {
	want, ok := sums[name]
	if !ok {
		return fmt.Errorf("%s does not list %s", ChecksumsFile, name)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", name, got, want)
	}
	return nil
}

// ── S3 ───────────────────────────────────────────────────────────────────────

func (s Source) s3URL(key string) string {
	return fmt.Sprintf("s3://%s/%s%s", s.S3Bucket, s.S3Prefix, key)
}

// s3Size returns an object's size, or 0 when it is missing or unreadable.
func (s Source) s3Size(ctx context.Context, key string) int64 {
	out, err := s.aws(ctx, "s3api", "head-object", "--bucket", s.S3Bucket, "--key", key,
		"--query", "ContentLength", "--output", "text")
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// aws runs the AWS CLI as the configured profile and returns its stdout.
func (s Source) aws(ctx context.Context, args ...string) ([]byte, error) {
	if s.AWSProfile != "" {
		args = append(args, "--profile", s.AWSProfile)
	}
	if s.AWSRegion != "" {
		args = append(args, "--region", s.AWSRegion)
	}
	cmd := exec.CommandContext(ctx, "aws", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if authErr := awsAuthError(msg, s.AWSProfile); authErr != nil {
			return nil, authErr
		}
		if msg != "" {
			return nil, fmt.Errorf("aws %s: %s", args[0], msg)
		}
		return nil, fmt.Errorf("aws %s: %w", args[0], err)
	}
	return out, nil
}

// awsAuthError turns the AWS CLI's credential failures into an actionable
// error naming the login command, or returns nil for any other failure.
func awsAuthError(stderr, profile string) error {
	for _, p := range []string{
		"NoCredentialsError",
		"Unable to locate credentials",
		"ExpiredTokenException",
		"Token has expired",
		"SSOTokenProviderFailure",
		"Error loading SSO Token",
		"AuthFailure",
		"InvalidClientTokenId",
	} {
		if strings.Contains(stderr, p) {
			login := "aws sso login"
			if profile != "" {
				login += " --profile " + profile
			}
			return fmt.Errorf("not logged in to AWS — use the ☁ AWS pane to log in, or run:\n  %s", login)
		}
	}
	return nil
}

// ── GitHub ──────────────────────────────────────────────────────────────────────

func (s Source) client() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
		CheckRedirect: s.keepAuthOnSameSite,
	}
}

// keepAuthOnSameSite re-applies the asset headers on redirect. Go drops the
// Authorization header on cross-host redirects; GitHub Enterprise serves private assets
// through a redirect to another host on the same site, which still needs it.
// A redirect to any other site — a pre-signed storage URL — gets no token.
func (s Source) keepAuthOnSameSite(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("too many redirects")
	}
	req.Header.Set("Accept", "application/octet-stream")
	if s.Token != "" && sameSite(via[0].URL, req.URL) {
		req.Header.Set("Authorization", "Bearer "+s.Token) //nolint:gosec // G119: only to the same site, the point of this function
	}
	return nil
}

// sameSite reports whether b's host is a's host or shares its parent domain
// (media.example.com for api.example.com).
func sameSite(a, b *url.URL) bool {
	ha, hb := a.Hostname(), b.Hostname()
	if ha == hb {
		return true
	}
	parent := ha
	if i := strings.IndexByte(ha, '.'); i >= 0 && strings.Count(ha, ".") >= 2 {
		parent = ha[i+1:]
	}
	return strings.HasSuffix(hb, "."+parent)
}

func (s Source) authorize(req *http.Request) {
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
}

// statusError is a non-200 answer. It keeps the code so callers can tell a 404
// (the file is not there) from a failure.
type statusError struct {
	code int
	msg  string
}

func (e *statusError) Error() string { return e.msg }

// isNotFound reports whether err is an HTTP 404 answer.
func isNotFound(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.code == http.StatusNotFound
}

// requested names what a response answered for, without a query: a redirect
// to pre-signed storage carries credentials in it.
func requested(resp *http.Response) string {
	if resp.Request == nil || resp.Request.URL == nil {
		return "GitHub"
	}
	return resp.Request.URL.Host + resp.Request.URL.Path
}

func (s Source) checkStatus(resp *http.Response) error {
	code := resp.StatusCode
	var msg string
	switch {
	case code == http.StatusOK:
		return nil
	case (code == http.StatusUnauthorized || code == http.StatusNotFound) && s.Token == "":
		// GitHub answers 404 rather than 401 for a private repo without a token.
		msg = fmt.Sprintf("HTTP %d — no such release; for a private repository set GITHUB_TOKEN to a token that can read it", code)
	case code == http.StatusUnauthorized:
		msg = "HTTP 401 — GITHUB_TOKEN was rejected: it is invalid, expired or revoked"
	case code == http.StatusNotFound:
		// With a token, GitHub answers 404 both for a repository the token cannot
		// read and for one with no published release: a token that is valid but
		// not granted this repository looks exactly like a missing one.
		msg = fmt.Sprintf("HTTP 404 from %s — GITHUB_TOKEN is valid but GitHub shows it no such release. "+
			"The token must be able to read this repository: a fine-grained token needs this repository "+
			"selected (under its owner) and Contents: read, a classic token the repo scope, authorized "+
			"for SSO when the organization enforces it. A published release must exist too", requested(resp))
	case (code == http.StatusForbidden || code == http.StatusTooManyRequests) && s.Token == "":
		msg = fmt.Sprintf("HTTP %d — GitHub's rate limit was reached; GITHUB_TOKEN raises it, or leave it unset and den reads the release feed", code)
	case code == http.StatusForbidden || code == http.StatusTooManyRequests:
		msg = fmt.Sprintf("HTTP %d — GitHub's rate limit was reached or GITHUB_TOKEN may not read this repository; unset it and den reads the release feed", code)
	default:
		msg = fmt.Sprintf("HTTP %d", code)
	}
	return &statusError{code: code, msg: msg}
}

func (s Source) downloadGitHub(ctx context.Context, assetURL, path string, progress, total *atomic.Int64) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := s.fetchGitHub(ctx, assetURL, f, progress, total); err != nil {
		f.Close() //nolint:errcheck
		return err
	}
	return f.Close()
}

// fetchGitHub streams a release asset into w.
func (s Source) fetchGitHub(ctx context.Context, assetURL string, w io.Writer, progress, total *atomic.Int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/octet-stream")
	s.authorize(req)
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if err := s.checkStatus(resp); err != nil {
		return err
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		return fmt.Errorf("server returned an HTML page instead of the file — check GITHUB_TOKEN")
	}
	if total != nil && resp.ContentLength > 0 {
		total.Store(resp.ContentLength)
	}
	var r io.Reader = resp.Body
	if progress != nil {
		r = &countingReader{r: r, n: progress}
	}
	_, err = io.Copy(w, r)
	return err
}

type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}
