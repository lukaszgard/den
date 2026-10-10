package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// The self-updater replaces the running binary, so a regression here either
// bricks an install or skips the check that the download is what was released.
// These tests drive the GitHub path against an httptest server and the S3 path
// against a fake `aws` on PATH.

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// githubServer serves a latest release whose assets are the given files.
func githubServer(t *testing.T, tag string, assets map[string][]byte, wantToken string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wantToken != "" && r.Header.Get("Authorization") != "Bearer "+wantToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/releases/latest" {
			type asset struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			}
			var list []asset
			for name := range assets {
				list = append(list, asset{Name: name, URL: srv.URL + "/assets/" + name})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": tag, "html_url": srv.URL + "/releases/tag/" + tag, "body": "notes", "assets": list,
			})
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/assets/")
		data, ok := assets[name]
		if !ok || r.Header.Get("Accept") != "application/octet-stream" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestArchiveNameAndSameVersion(t *testing.T) {
	if got := ArchiveName("v1.2.3", "darwin", "arm64"); got != "den_1.2.3_darwin_arm64.tar.gz" {
		t.Errorf("darwin archive = %q", got)
	}
	if got := ArchiveName("1.2.3", "windows", "amd64"); got != "den_1.2.3_windows_amd64.zip" {
		t.Errorf("windows archive = %q", got)
	}
	if !SameVersion("v1.2.3", "1.2.3") || SameVersion("v1.2.3", "v1.2.4") {
		t.Error("SameVersion must ignore a leading v and nothing else")
	}
}

func TestGitHubDownloadVerifiedEndToEnd(t *testing.T) {
	archive := ArchiveName("v1.0.0", runtime.GOOS, runtime.GOARCH)
	data := tarGz(t, map[string]string{"README.md": "readme", "den": "#!/bin/sh\necho new\n"})
	sums := fmt.Sprintf("%s  %s\n%s  other.tar.gz\n", sha(data), archive, strings.Repeat("0", 64))
	srv := githubServer(t, "v1.0.0", map[string][]byte{archive: data, ChecksumsFile: []byte(sums)}, "tok")
	src := Source{GitHubAPI: srv.URL + "/releases/latest", Token: "tok"}
	ctx := context.Background()

	rel, err := src.Latest(ctx, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "v1.0.0" || rel.Notes != "notes" || rel.ArchiveURL == "" || rel.ChecksumsURL == "" {
		t.Fatalf("release = %+v", rel)
	}

	dir := t.TempDir()
	archivePath := filepath.Join(dir, "download")
	var progress, total atomic.Int64
	if err := src.Download(ctx, rel, archivePath, &progress, &total); err != nil {
		t.Fatal(err)
	}
	if progress.Load() != int64(len(data)) {
		t.Errorf("progress = %d, want %d", progress.Load(), len(data))
	}

	got, err := src.Checksums(ctx, rel)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(archivePath, rel.Archive, got); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	target := filepath.Join(dir, "den")
	writeFile(t, target, []byte("old"))
	bin, err := ExtractBinary(archivePath, "linux", target)
	if err != nil {
		t.Fatal(err)
	}
	if err := Replace(bin, target); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "#!/bin/sh\necho new\n" {
		t.Fatalf("target = %q", b)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("replaced binary is not executable: %v", fi.Mode())
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Fatalf("temp binary left behind: %v", err)
	}
}

func TestVerifyRejectsTamperedArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	writeFile(t, path, []byte("tampered"))
	err := Verify(path, "a.tar.gz", map[string]string{"a.tar.gz": sha([]byte("original"))})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
	if err := Verify(path, "missing.tar.gz", map[string]string{"a.tar.gz": sha([]byte("x"))}); err == nil {
		t.Fatal("an archive the checksums do not list must not verify")
	}
}

func TestReleaseWithoutChecksums(t *testing.T) {
	archive := ArchiveName("v1.0.0", runtime.GOOS, runtime.GOARCH)
	srv := githubServer(t, "v1.0.0", map[string][]byte{archive: []byte("x")}, "")
	src := Source{GitHubAPI: srv.URL + "/releases/latest"}
	rel, err := src.Latest(context.Background(), runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Checksums(context.Background(), rel); !errors.Is(err, ErrNoChecksums) {
		t.Fatalf("err = %v, want ErrNoChecksums", err)
	}
}

func TestParseChecksums(t *testing.T) {
	good := strings.Repeat("a", 64)
	sums, err := ParseChecksums([]byte(good + "  den.tar.gz\n\n" + strings.Repeat("B", 64) + " *den.zip\n"))
	if err != nil {
		t.Fatal(err)
	}
	if sums["den.tar.gz"] != good || sums["den.zip"] != strings.Repeat("b", 64) {
		t.Fatalf("sums = %v", sums)
	}
	for _, bad := range []string{"", "nothex  den.tar.gz", good + "  a  b", strings.Repeat("z", 64) + "  den"} {
		if _, err := ParseChecksums([]byte(bad)); err == nil {
			t.Errorf("ParseChecksums(%q) accepted malformed input", bad)
		}
	}
}

func TestGitHubWithoutTokenExplainsWhatToSet(t *testing.T) {
	srv := githubServer(t, "v1.0.0", nil, "required")
	_, err := Source{GitHubAPI: srv.URL + "/releases/latest"}.Latest(context.Background(), "linux", "amd64")
	if err == nil || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Fatalf("err = %v, want a hint to set GITHUB_TOKEN", err)
	}
}

func TestExtractBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "den")

	t.Run("zip for windows", func(t *testing.T) {
		path := filepath.Join(dir, "a.zip")
		writeFile(t, path, zipArchive(t, map[string]string{"den.exe": "MZ"}))
		bin, err := ExtractBinary(path, "windows", target)
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(bin); string(b) != "MZ" {
			t.Fatalf("extracted %q", b)
		}
	})
	t.Run("archive without the binary", func(t *testing.T) {
		path := filepath.Join(dir, "b.tar.gz")
		writeFile(t, path, tarGz(t, map[string]string{"README.md": "x"}))
		if _, err := ExtractBinary(path, "linux", target); err == nil {
			t.Fatal("expected an error for an archive without den")
		}
	})
	t.Run("oversized entry", func(t *testing.T) {
		prev := maxBinarySize
		maxBinarySize = 4
		t.Cleanup(func() { maxBinarySize = prev })
		own := t.TempDir()
		path := filepath.Join(own, "c.tar.gz")
		writeFile(t, path, tarGz(t, map[string]string{"den": "0123456789"}))
		if _, err := ExtractBinary(path, "linux", filepath.Join(own, "den")); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("err = %v, want the size cap to trip", err)
		}
		left, _ := filepath.Glob(filepath.Join(own, "den-new-*"))
		if len(left) != 0 {
			t.Fatalf("temp files left behind: %v", left)
		}
	})
}

func TestSameSite(t *testing.T) {
	u := func(s string) *url.URL {
		p, err := url.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		from, to string
		want     bool
	}{
		{"https://git.corp.example/api", "https://git.corp.example/x", true},
		{"https://api.corp.example/a", "https://media.corp.example/b", true},
		{"https://api.github.com/a", "https://objects.githubusercontent.com/b", false},
		{"https://git.corp.example/a", "https://bucket.s3.amazonaws.com/b", false},
	}
	for _, c := range cases {
		if got := sameSite(u(c.from), u(c.to)); got != c.want {
			t.Errorf("sameSite(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// With nothing configured the check asks the public GitHub API, and sends no
// credentials: a public repository needs none.
func TestDefaultSourceIsPublicGitHub(t *testing.T) {
	var asked *http.Request
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		asked = r
		body := `{"tag_name":"v1.2.3","html_url":"https://github.com/o/den/releases/tag/v1.2.3","body":"notes","assets":[]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	rel, err := Source{HTTPClient: client}.Latest(context.Background(), "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if asked.URL.String() != DefaultGitHubAPI || !strings.HasPrefix(DefaultGitHubAPI, "https://api.github.com/repos/") {
		t.Errorf("asked %s, want the default %s on api.github.com", asked.URL, DefaultGitHubAPI)
	}
	if asked.Header.Get("Authorization") != "" {
		t.Error("no token is set, so no Authorization header may be sent")
	}
	if rel.Version != "v1.2.3" {
		t.Errorf("release = %+v", rel)
	}
}
