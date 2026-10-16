package update

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The picker and the startup check read the list of releases from
// releases.json. These pin where each source reads it, what falls back to
// what when it is missing, and that the startup check never turns into an API
// call per start.

const (
	indexURL  = "https://github.com/lukaszgard/den/releases/latest/download/releases.json"
	listURL   = "https://api.github.com/repos/lukaszgard/den/releases?per_page=100"
	indexJSON = `{"schema": 1, "releases": [
		{"version": "v1.7.0", "date": "2026-10-10T10:00:00Z", "prerelease": false, "yanked": "SSM reconnect loop — use v1.7.1", "notes": "old"},
		{"version": "v1.8.0-rc.1", "date": "2026-10-20T10:00:00Z", "prerelease": true, "notes": "rc"},
		{"version": "nightly", "date": "2026-10-20T10:00:00Z"},
		{"version": "v1.7.2", "date": "2026-10-16T11:28:55Z", "prerelease": false, "notes": "fix"}]}`
	listJSON = `[
		{"tag_name": "v1.8.0-rc.1", "html_url": "https://github.com/lukaszgard/den/releases/tag/v1.8.0-rc.1", "prerelease": true,
		 "published_at": "2026-10-20T10:00:00Z", "assets": [{"name": "den_1.8.0-rc.1_linux_amd64.tar.gz", "url": "https://api.github.com/a/1"}]},
		{"tag_name": "v1.9.0", "draft": true, "assets": []},
		{"tag_name": "v1.7.2", "html_url": "https://github.com/lukaszgard/den/releases/tag/v1.7.2", "body": "fix",
		 "published_at": "2026-10-16T11:28:55Z", "assets": [
			{"name": "den_1.7.2_linux_amd64.tar.gz", "url": "https://api.github.com/a/2"},
			{"name": "checksums.txt", "url": "https://api.github.com/a/3"}]},
		{"tag_name": "v1.7.0", "published_at": "2026-10-10T10:00:00Z", "assets": [{"name": "den_1.7.0_darwin_arm64.tar.gz", "url": "https://api.github.com/a/4"}]}]`
)

func versions(rels []Release) string {
	var vs []string
	for _, r := range rels {
		vs = append(vs, r.Version)
	}
	return strings.Join(vs, " ")
}

func TestIndexFromReleaseFeed(t *testing.T) {
	r := &routed{reply: func(*http.Request) (int, string) { return http.StatusOK, indexJSON }}
	got, err := Source{HTTPClient: r.client()}.Index(context.Background(), "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if asked := r.urls(); len(asked) != 1 || asked[0] != indexURL {
		t.Fatalf("asked %v, want only %s", asked, indexURL)
	}
	// Newest first, whatever the file's order; a tag that is not a version is not listed.
	if v := versions(got); v != "v1.8.0-rc.1 v1.7.2 v1.7.0" {
		t.Fatalf("versions = %s", v)
	}
	rc, fix, old := got[0], got[1], got[2]
	if !rc.Prerelease || fix.Prerelease || old.Yanked != "SSM reconnect loop — use v1.7.1" || fix.Yanked != "" {
		t.Errorf("flags lost: %+v", got)
	}
	if !fix.Date.Equal(time.Date(2026, 10, 16, 11, 28, 55, 0, time.UTC)) || fix.Notes != "fix" {
		t.Errorf("v1.7.2 = %+v", fix)
	}
	if want := "https://github.com/lukaszgard/den/releases/download/v1.7.2/den_1.7.2_linux_amd64.tar.gz"; fix.ArchiveURL != want || !fix.feed {
		t.Errorf("archive URL = %s, want %s", fix.ArchiveURL, want)
	}
}

func TestIndexRejectsWhatIsNotOne(t *testing.T) {
	for _, body := range []string{`not json`, `{"releases": []}`, `[]`} {
		r := &routed{reply: func(*http.Request) (int, string) { return http.StatusOK, body }}
		if _, err := (Source{HTTPClient: r.client()}).Index(context.Background(), "linux", "amd64"); err == nil || errors.Is(err, ErrNoIndex) {
			t.Errorf("index %q: err = %v, want a parse error", body, err)
		}
	}
}

// Every release before the index existed has none: the startup check reads
// latest.json then, as it always did, and asks the API nothing.
func TestCheckWithoutIndexReadsLatestJSONOnly(t *testing.T) {
	r := &routed{reply: func(req *http.Request) (int, string) {
		if strings.HasSuffix(req.URL.Path, "/latest.json") {
			return http.StatusOK, feedJSON
		}
		return http.StatusNotFound, ""
	}}
	src := Source{HTTPClient: r.client()}
	offer, index, err := src.Check(context.Background(), "linux", "amd64", "1.2.0", Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if offer.Version != "v1.2.3" || index != nil {
		t.Fatalf("offer = %s, index = %v; want v1.2.3 and no index", offer.Version, index)
	}
	if asked := r.urls(); len(asked) != 2 || asked[0] != indexURL || asked[1] != feedURL {
		t.Fatalf("asked %v, want the index then latest.json, and never the API", asked)
	}

	// A skip applies to latest.json too.
	offer, _, err = src.Check(context.Background(), "linux", "amd64", "1.2.0", Policy{Skipped: []string{"v1.2.3"}})
	if err != nil || offer.Version != "1.2.0" {
		t.Fatalf("offer = %s, %v; want the current version", offer.Version, err)
	}
	// A pin is named on the feed without a listing.
	offer, _, err = src.Check(context.Background(), "linux", "amd64", "1.2.3", Policy{Pin: "1.1.0"})
	if err != nil || offer.Version != "v1.1.0" ||
		offer.ArchiveURL != "https://github.com/lukaszgard/den/releases/download/v1.1.0/den_1.1.0_linux_amd64.tar.gz" {
		t.Fatalf("offer = %+v, %v; want v1.1.0 named on the feed", offer, err)
	}
}

func TestCheckWithIndex(t *testing.T) {
	r := &routed{reply: func(*http.Request) (int, string) { return http.StatusOK, indexJSON }}
	src := Source{HTTPClient: r.client()}
	offer, index, err := src.Check(context.Background(), "linux", "amd64", "1.7.0", Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if offer.Version != "v1.7.2" || len(index) != 3 {
		t.Fatalf("offer = %s, index = %v", offer.Version, versions(index))
	}
	if asked := r.urls(); len(asked) != 1 {
		t.Fatalf("asked %v, want the index alone", asked)
	}
	if _, _, err := src.Check(context.Background(), "linux", "amd64", "1.7.0", Policy{Pin: "v1.6.0"}); err == nil {
		t.Fatal("a pin the index does not list must fail the check")
	}
}

// Someone choosing a version gets one unauthenticated API call when there is
// no index yet.
func TestReleasesFallBackToTheAPIList(t *testing.T) {
	r := &routed{reply: func(req *http.Request) (int, string) {
		if req.URL.Host == "api.github.com" {
			return http.StatusOK, listJSON
		}
		return http.StatusNotFound, ""
	}}
	got, err := Source{HTTPClient: r.client()}.Releases(context.Background(), "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if asked := r.urls(); len(asked) != 2 || asked[0] != indexURL || asked[1] != listURL {
		t.Fatalf("asked %v, want the index, then %s", asked, listURL)
	}
	for _, req := range r.asked {
		if req.Header.Get("Authorization") != "" {
			t.Errorf("%s: no token is set, so none may be sent", req.URL)
		}
	}
	// The draft is not listed.
	if v := versions(got); v != "v1.8.0-rc.1 v1.7.2 v1.7.0" {
		t.Fatalf("versions = %s", v)
	}
	src := Source{}
	if !got[0].Prerelease || got[1].ArchiveURL != "https://api.github.com/a/2" || got[1].ChecksumsURL != "https://api.github.com/a/3" {
		t.Errorf("assets lost: %+v", got[:2])
	}
	// A release without this platform's archive is listed but cannot be installed.
	if src.CanInstall(got[2]) || !src.CanInstall(got[1]) {
		t.Errorf("CanInstall: v1.7.0 has no linux/amd64 archive, v1.7.2 has one")
	}
}

// With a token the list is the API's, and a releases.json among the assets
// adds the yanked marks.
func TestTokenListMergesYankedFromTheIndexAsset(t *testing.T) {
	withIndex := strings.Replace(listJSON, `{"name": "checksums.txt", "url": "https://api.github.com/a/3"}`,
		`{"name": "checksums.txt", "url": "https://api.github.com/a/3"}, {"name": "releases.json", "url": "https://api.github.com/a/idx"}`, 1)
	r := &routed{reply: func(req *http.Request) (int, string) {
		if req.URL.Path == "/a/idx" {
			return http.StatusOK, indexJSON
		}
		return http.StatusOK, withIndex
	}}
	got, err := Source{HTTPClient: r.client(), Token: "tok"}.Index(context.Background(), "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if asked := r.urls(); len(asked) != 2 || asked[0] != listURL || asked[1] != "https://api.github.com/a/idx" {
		t.Fatalf("asked %v", asked)
	}
	for _, req := range r.asked {
		if req.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("%s: the token must be sent", req.URL)
		}
	}
	if got[2].Version != "v1.7.0" || got[2].Yanked != "SSM reconnect loop — use v1.7.1" || got[1].Yanked != "" {
		t.Fatalf("yanked marks = %+v", got)
	}
}

func TestEnterpriseListsNextToItsLatestURL(t *testing.T) {
	r := &routed{reply: func(*http.Request) (int, string) { return http.StatusOK, "[]" }}
	api := "https://ghe.example.com/api/v3/repos/o/r/releases/latest"
	if _, err := (Source{HTTPClient: r.client(), GitHubAPI: api}).Releases(context.Background(), "linux", "amd64"); err != nil {
		t.Fatal(err)
	}
	if asked := r.urls(); len(asked) != 1 || asked[0] != "https://ghe.example.com/api/v3/repos/o/r/releases?per_page=100" {
		t.Fatalf("asked %v", asked)
	}
}

func TestIndexFailureIsNotFallenBackOn(t *testing.T) {
	r := &routed{reply: func(*http.Request) (int, string) { return http.StatusInternalServerError, "" }}
	_, _, err := Source{HTTPClient: r.client()}.Check(context.Background(), "linux", "amd64", "1.0.0", Policy{})
	if err == nil || errors.Is(err, ErrNoIndex) || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v, want the index's HTTP 500", err)
	}
	if asked := r.urls(); len(asked) != 1 {
		t.Errorf("asked %v: a failing index must not be retried elsewhere", asked)
	}
}

func TestNamed(t *testing.T) {
	if rel, ok := (Source{S3Bucket: "b", S3Prefix: "den/"}).Named("v1.0.0", "linux", "amd64"); !ok || rel.URL != "s3://b/den/v1.0.0/den_1.0.0_linux_amd64.tar.gz" {
		t.Errorf("S3 Named = %+v, %v", rel, ok)
	}
	if _, ok := (Source{Token: "t"}).Named("v1.0.0", "linux", "amd64"); ok {
		t.Error("the API cannot name a release without listing it")
	}
}

// The CLI's install: download, verify, extract and replace, each step heard;
// a tampered archive stops it before the binary is touched.
func TestInstall(t *testing.T) {
	archive := ArchiveName("v1.0.0", runtime.GOOS, runtime.GOARCH)
	data := tarGz(t, map[string]string{"den": "new"})
	sums := fmt.Sprintf("%s  %s\n", sha(data), archive)
	srv := feedServer(t, DefaultRepo, "v1.0.0", map[string][]byte{archive: data, ChecksumsFile: []byte(sums)})
	src := Source{GitHubWeb: srv.URL}
	rel, _ := src.Named("v1.0.0", runtime.GOOS, runtime.GOARCH)
	exe := filepath.Join(t.TempDir(), "den")
	writeFile(t, exe, []byte("old"))

	var steps []string
	if err := src.Install(context.Background(), rel, exe, "linux", func(s string) { steps = append(steps, s) }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("exe = %q", b)
	}
	if len(steps) != 4 || !strings.HasPrefix(steps[1], "Verified SHA-256") || !strings.HasPrefix(steps[3], "Replaced ") {
		t.Fatalf("steps = %q", steps)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), "den-*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}

	tampered := feedServer(t, DefaultRepo, "v1.0.0", map[string][]byte{archive: []byte("evil"), ChecksumsFile: []byte(sums)})
	src = Source{GitHubWeb: tampered.URL}
	rel, _ = src.Named("v1.0.0", runtime.GOOS, runtime.GOARCH)
	err := src.Install(context.Background(), rel, exe, "linux", nil)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("a failed install changed the binary: %q", b)
	}

	// Without checksums.txt it installs, and says so.
	bare := feedServer(t, DefaultRepo, "v1.0.0", map[string][]byte{archive: data})
	src = Source{GitHubWeb: bare.URL}
	rel, _ = src.Named("v1.0.0", runtime.GOOS, runtime.GOARCH)
	steps = nil
	if err := src.Install(context.Background(), rel, exe, "linux", func(s string) { steps = append(steps, s) }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(steps, "\n"), "download not verified") {
		t.Fatalf("steps = %q, want the missing checksums said", steps)
	}

	if err := (Source{}).Install(context.Background(), Release{Version: "v1.0.0", Archive: archive}, exe, "linux", nil); err == nil {
		t.Fatal("a release without an archive URL must not install")
	}
}
