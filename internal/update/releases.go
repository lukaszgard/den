package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// IndexFile lists every release: the release workflow builds it from the
// GitHub releases and .github/yanked.json and uploads it to the latest stable
// release, so it reads at releases/latest/download/releases.json like
// latest.json, with no API call.
const IndexFile = "releases.json"

// ErrNoIndex means the source publishes no releases.json: a mirror that has
// not copied it, every release made before den listed its releases, or the
// seconds between a release and the upload of its index.
var ErrNoIndex = errors.New("no " + IndexFile + " published")

// index is the layout of releases.json.
type index struct {
	Schema   int `json:"schema"`
	Releases []struct {
		Version    string `json:"version"`
		Date       string `json:"date"`
		Prerelease bool   `json:"prerelease"`
		Yanked     string `json:"yanked"`
		Notes      string `json:"notes"`
	} `json:"releases"`
}

// parseIndex reads releases.json and builds each release's files with named.
func parseIndex(data []byte, named func(version, notes string) Release) ([]Release, error) {
	var idx index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse %s: %w", IndexFile, err)
	}
	if idx.Schema < 1 {
		return nil, fmt.Errorf("parse %s: not a release index (no schema)", IndexFile)
	}
	var rels []Release
	for _, e := range idx.Releases {
		if !IsVersion(e.Version) {
			continue
		}
		rel := named(e.Version, e.Notes)
		rel.Date = parseDate(e.Date)
		rel.Prerelease = e.Prerelease || IsPrerelease(e.Version)
		rel.Yanked = e.Yanked
		rels = append(rels, rel)
	}
	sortNewestFirst(rels)
	return rels, nil
}

func parseDate(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func sortNewestFirst(rels []Release) {
	slices.SortStableFunc(rels, func(a, b Release) int { return Compare(b.Version, a.Version) })
}

// Index returns the releases the source lists, newest first, from its
// releases.json, or ErrNoIndex when it has none. A source read through the
// releases API (a token, GitHub Enterprise) lists them from the API, which
// always answers.
//
// The startup check uses Index rather than Releases: without an index it falls
// back to latest.json, one request like before, instead of an API call on
// every start.
func (s Source) Index(ctx context.Context, goos, goarch string) ([]Release, error) {
	if s.UsesS3() {
		out, err := s.aws(ctx, "s3", "cp", s.s3URL(IndexFile), "-")
		if err != nil {
			// 403 too: S3 answers it for a missing object to a caller that may
			// not list the bucket. A caller that may read nothing hears it again
			// from latest.json.
			if se, ok := errors.AsType[*statusError](err); ok && (se.code == 404 || se.code == 403) {
				return nil, fmt.Errorf("%w at %s", ErrNoIndex, s.s3URL(IndexFile))
			}
			return nil, err
		}
		return parseIndex(out, func(v, notes string) Release { return s.s3Release(v, notes, goos, goarch) })
	}
	if repo, ok := s.feedRepo(); ok {
		u := s.web() + "/" + repo + "/releases/latest/download/" + IndexFile
		data, err := s.getFeed(ctx, u)
		if isNotFound(err) {
			return nil, fmt.Errorf("%w at %s", ErrNoIndex, u)
		}
		if err != nil {
			return nil, err
		}
		return parseIndex(data, func(v, notes string) Release { return s.feedRelease(repo, v, notes, goos, goarch) })
	}
	return s.listGitHub(ctx, goos, goarch)
}

// Releases returns every release the source has, newest first, for someone
// choosing one: the index when there is one, and otherwise one unauthenticated
// call to the releases API of the feed's repository. A mirror without an
// index has no API to ask.
func (s Source) Releases(ctx context.Context, goos, goarch string) ([]Release, error) {
	rels, err := s.Index(ctx, goos, goarch)
	if !errors.Is(err, ErrNoIndex) {
		return rels, err
	}
	if s.UsesS3() {
		return nil, fmt.Errorf("%w — copy %s from a GitHub release into the mirror next to latest.json", err, IndexFile)
	}
	return s.listGitHub(ctx, goos, goarch)
}

// Named builds the release called version without asking which exist: the
// release feed and an S3 mirror name a release's files by its version. It is
// false for the releases API, whose files are only known from a listing.
func (s Source) Named(version, goos, goarch string) (Release, bool) {
	if s.UsesS3() {
		return s.s3Release(version, "", goos, goarch), true
	}
	if repo, ok := s.feedRepo(); ok {
		return s.feedRelease(repo, version, "", goos, goarch), true
	}
	return Release{}, false
}

// CanInstall reports whether rel has an archive this source can download: a
// release the API lists without this platform's archive is shown, not
// installed.
func (s Source) CanInstall(rel Release) bool { return s.UsesS3() || rel.ArchiveURL != "" }

// CheckResult is what an update check found.
type CheckResult struct {
	// Offer is the release to install; the running version when there is
	// nothing to install, which SameVersion then reports.
	Offer Release
	// Index is the source's releases.json, newest first; nil when it has none.
	Index []Release
	// Why says why Offer is not simply the newest release; often empty.
	Why string
}

// Check is the update check: the release to offer under p. Without an index
// the check reads latest.json as it always has, plus the pinned release by
// name, so it never costs an API call per start.
func (s Source) Check(ctx context.Context, goos, goarch, current string, p Policy) (CheckResult, error) {
	rels, err := s.Index(ctx, goos, goarch)
	index := rels
	if errors.Is(err, ErrNoIndex) {
		index = nil
		var latest Release
		if latest, err = s.Latest(ctx, goos, goarch); err == nil {
			rels = []Release{latest}
			if p.Pin != "" && !SameVersion(p.Pin, latest.Version) {
				if pinned, ok := s.Named(Canonical(p.Pin), goos, goarch); ok {
					rels = append(rels, pinned)
				}
			}
		}
	}
	if err != nil {
		return CheckResult{}, err
	}
	offer, err := Offer(rels, current, p)
	if err != nil {
		return CheckResult{}, err
	}
	return CheckResult{Offer: offer, Index: index, Why: Why(rels, current, offer, p)}, nil
}

// listURL is the releases API listing next to the releases/latest URL.
func (s Source) listURL() string {
	api := s.GitHubAPI
	if api == "" {
		api = DefaultGitHubAPI
	}
	return strings.TrimSuffix(api, "/latest") + "?per_page=100"
}

// ghRelease is one release as the GitHub releases API returns it.
type ghRelease struct {
	TagName     string `json:"tag_name"`
	HTMLURL     string `json:"html_url"`
	Body        string `json:"body"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"url"` // API URL — fetched with Accept: application/octet-stream
	} `json:"assets"`
}

// release maps r to a Release with this platform's files, which come from its
// assets: the archive URL stays empty when it has none for goos/goarch.
func (r ghRelease) release(goos, goarch string) Release {
	rel := Release{
		Version:    r.TagName,
		URL:        r.HTMLURL,
		Notes:      r.Body,
		Archive:    ArchiveName(r.TagName, goos, goarch),
		Date:       parseDate(r.PublishedAt),
		Prerelease: r.Prerelease || IsPrerelease(r.TagName),
	}
	for _, a := range r.Assets {
		switch a.Name {
		case rel.Archive:
			rel.ArchiveURL = a.URL
		case ChecksumsFile:
			rel.ChecksumsURL = a.URL
		}
	}
	return rel
}

// listGitHub lists the newest 100 releases through the API. The yanked marks
// come from the releases.json one of them carries, which is fetched only when
// there is one.
func (s Source) listGitHub(ctx context.Context, goos, goarch string) ([]Release, error) {
	var buf bytes.Buffer
	if err := s.getAPI(ctx, s.listURL(), &buf); err != nil {
		return nil, err
	}
	var list []ghRelease
	if err := json.Unmarshal(buf.Bytes(), &list); err != nil {
		return nil, fmt.Errorf("parse releases: %w", err)
	}
	var rels []Release
	indexURL := ""
	for _, r := range list {
		if r.Draft || !IsVersion(r.TagName) {
			continue
		}
		rels = append(rels, r.release(goos, goarch))
		for _, a := range r.Assets {
			if a.Name == IndexFile && indexURL == "" {
				indexURL = a.URL
			}
		}
	}
	sortNewestFirst(rels)
	if indexURL == "" {
		return rels, nil
	}
	var data bytes.Buffer
	if err := s.fetchGitHub(ctx, indexURL, &data, nil, nil); err != nil {
		return nil, fmt.Errorf("download %s: %w", IndexFile, err)
	}
	indexed, err := parseIndex(data.Bytes(), func(v, _ string) Release { return Release{Version: v} })
	if err != nil {
		return nil, err
	}
	for i := range rels {
		for _, e := range indexed {
			if SameVersion(e.Version, rels[i].Version) {
				rels[i].Yanked = e.Yanked
			}
		}
	}
	return rels, nil
}

// getAPI reads one releases API answer into w.
func (s Source) getAPI(ctx context.Context, apiURL string, w *bytes.Buffer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return err
	}
	s.authorize(req)
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if err := s.checkStatus(resp); err != nil {
		return err
	}
	_, err = w.ReadFrom(io.LimitReader(resp.Body, 10<<20))
	return err
}
