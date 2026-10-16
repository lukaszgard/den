package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/update"
)

// `den update` replaces the binary it runs from, older versions included.
// These drive it against a releases API served by httptest, as a GitHub
// Enterprise server would answer, with the binary to replace a temp file.

func denArchive(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "den", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releasesServer serves v0.0.1–v0.0.3 and v0.0.4-rc.1, each with this
// platform's archive and checksums.txt, and a releases.json that yanks
// v0.0.3. tamper replaces one release's archive after its checksum was taken.
func releasesServer(t *testing.T, tamper string) string {
	t.Helper()
	assets := map[string][]byte{}
	type asset struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	type release struct {
		Tag        string  `json:"tag_name"`
		Prerelease bool    `json:"prerelease"`
		Published  string  `json:"published_at"`
		Assets     []asset `json:"assets"`
	}
	var listJSON []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/den/releases" {
			_, _ = w.Write(listJSON)
			return
		}
		data, ok := assets[strings.TrimPrefix(r.URL.Path, "/assets/")]
		if !ok || r.Header.Get("Accept") != "application/octet-stream" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)

	var list []release
	for i, v := range []string{"v0.0.4-rc.1", "v0.0.3", "v0.0.2", "v0.0.1"} {
		archive := update.ArchiveName(v, runtime.GOOS, runtime.GOARCH)
		data := denArchive(t, "den "+v)
		sum := sha256.Sum256(data)
		if v == tamper {
			data = denArchive(t, "evil")
		}
		assets[v+"/"+archive] = data
		assets[v+"/checksums.txt"] = fmt.Appendf(nil, "%s  %s\n", hex.EncodeToString(sum[:]), archive)
		rel := release{Tag: v, Prerelease: strings.Contains(v, "-"), Published: fmt.Sprintf("2026-10-%02dT10:00:00Z", 20-i),
			Assets: []asset{{archive, srv.URL + "/assets/" + v + "/" + archive}, {"checksums.txt", srv.URL + "/assets/" + v + "/checksums.txt"}}}
		if v == "v0.0.3" {
			assets["releases.json"] = []byte(`{"schema": 1, "releases": [{"version": "v0.0.3", "yanked": "eats tunnels"}]}`)
			rel.Assets = append(rel.Assets, asset{"releases.json", srv.URL + "/assets/releases.json"})
		}
		list = append(list, rel)
	}
	var err error
	if listJSON, err = json.Marshal(list); err != nil {
		t.Fatal(err)
	}
	return srv.URL
}

// updateEnv points den update at the server, runs it as version, keeps its
// skip list in a temp state directory and gives it a binary to replace.
func updateEnv(t *testing.T, as, tamper string) (cfgPath, exe string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath = filepath.Join(dir, "den.yaml")
	cfg := "version: \"1\"\nupdates:\n  github_api: " + releasesServer(t, tamper) + "/repos/o/den/releases/latest\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	t.Setenv("GITHUB_TOKEN", "")
	exe = filepath.Join(dir, "bin", "den")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("den "+as), 0o755); err != nil { //nolint:gosec // a stand-in binary
		t.Fatal(err)
	}
	prevVersion, prevExe := version, denExecutable
	version, denExecutable = as, func() (string, error) { return exe, nil }
	t.Cleanup(func() { version, denExecutable = prevVersion, prevExe })
	return cfgPath, exe
}

func installed(t *testing.T, exe string) string {
	t.Helper()
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpdateCheckSkipsTheYankedRelease(t *testing.T) {
	cfg, _ := updateEnv(t, "0.0.1", "")
	out, err := runDen(t, "update", "--json", "-c", cfg)
	if err != nil {
		t.Fatal(err)
	}
	var r updateReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if r.Offered != "v0.0.2" || r.Direction != "upgrade" || r.Why != "v0.0.3 is yanked: eats tunnels" || r.Channel != "stable" {
		t.Fatalf("report = %+v", r)
	}
	out, err = runDen(t, "update", "-c", cfg)
	if err != nil || !strings.Contains(out, "den v0.0.2 is available (you run 0.0.1)") || !strings.Contains(out, "v0.0.3 is yanked: eats tunnels") {
		t.Fatalf("den update = %q, %v", out, err)
	}
}

func TestUpdateList(t *testing.T) {
	cfg, _ := updateEnv(t, "0.0.2", "")
	out, err := runDen(t, "update", "list", "-c", cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"VERSION", "v0.0.3   2026-10-19  upgrade    yanked: eats tunnels", "v0.0.2   2026-10-18  current", "v0.0.1   2026-10-17  downgrade"} {
		if !strings.Contains(out, want) {
			t.Errorf("list misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "rc.1") {
		t.Errorf("release candidates are listed with --all only:\n%s", out)
	}
	if out, _ := runDen(t, "update", "list", "--all", "-c", cfg); !strings.Contains(out, "v0.0.4-rc.1") || !strings.Contains(out, "rc") {
		t.Errorf("--all should list the rc:\n%s", out)
	}
	out, err = runDen(t, "update", "list", "--json", "-c", cfg)
	if err != nil {
		t.Fatal(err)
	}
	var entries []releaseEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil || len(entries) != 3 || entries[0].Yanked == "" || !entries[2].Installable {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
}

func TestUpdateInstallDowngradeSkipsTheVersionLeft(t *testing.T) {
	cfg, exe := updateEnv(t, "0.0.2", "")
	if _, err := runDen(t, "update", "install", "v0.0.1", "-c", cfg); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("outside a terminal without --yes = %v, want a refusal naming --yes", err)
	}
	if got := installed(t, exe); got != "den 0.0.2" {
		t.Fatalf("a refused install changed the binary: %q", got)
	}

	out, err := runDen(t, "update", "install", "v0.0.1", "--yes", "--skip-current", "-c", cfg)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"a downgrade from 0.0.2", "older than the version picker", "✓ Verified SHA-256 against checksums.txt",
		"Installed v0.0.1", "v0.0.2 is skipped in update checks"} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}
	if got := installed(t, exe); got != "den v0.0.1" {
		t.Fatalf("binary = %q, want v0.0.1's", got)
	}
	prefs, err := update.LoadPrefs(update.PrefsFile())
	if err != nil || !prefs.Skips("v0.0.2") {
		t.Fatalf("v0.0.2 should be skipped: %+v, %v", prefs, err)
	}

	// Running v0.0.1 now, with v0.0.2 skipped and v0.0.3 yanked: nothing is offered.
	version = "0.0.1"
	out, err = runDen(t, "update", "install", "-c", cfg)
	if err != nil || !strings.Contains(out, "nothing to install") {
		t.Fatalf("den update install = %q, %v; want nothing to install", out, err)
	}
	if out, _ := runDen(t, "update", "unskip", "0.0.2"); !strings.Contains(out, "v0.0.2 is no longer skipped") {
		t.Fatalf("unskip = %q", out)
	}
	if out, _ := runDen(t, "update", "-c", cfg); !strings.Contains(out, "den v0.0.2 is available") {
		t.Fatalf("after unskip v0.0.2 should be offered again:\n%s", out)
	}
}

func TestUpdateInstallRefusesATamperedArchive(t *testing.T) {
	cfg, exe := updateEnv(t, "0.0.1", "v0.0.2")
	_, err := runDen(t, "update", "install", "v0.0.2", "--yes", "-c", cfg)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
	if got := installed(t, exe); got != "den 0.0.1" {
		t.Fatalf("a tampered archive changed the binary: %q", got)
	}
}

func TestUpdateInstallArguments(t *testing.T) {
	cfg, exe := updateEnv(t, "0.0.1", "")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"v9.9.9"}, "no release v9.9.9"},
		{[]string{"latest"}, "not a version"},
		{[]string{"v0.0.2", "--yes", "--skip-current"}, "--skip-current skips the version a downgrade leaves"},
	} {
		_, err := runDen(t, append(append([]string{"update", "install"}, c.args...), "-c", cfg)...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("install %v: err = %v, want %q", c.args, err, c.want)
		}
	}
	if out, err := runDen(t, "update", "install", "0.0.1", "-c", cfg); err != nil || !strings.Contains(out, "is the version you run") {
		t.Errorf("installing the running version = %q, %v", out, err)
	}
	if got := installed(t, exe); got != "den 0.0.1" {
		t.Fatalf("binary changed: %q", got)
	}
}

func TestUpdateSkip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if out, err := runDen(t, "update", "skip", "1.7.2"); err != nil || out != "v1.7.2 is skipped: update checks will not offer it\n" {
		t.Fatalf("skip = %q, %v", out, err)
	}
	if out, _ := runDen(t, "update", "skip", "v1.7.2"); out != "v1.7.2 is already skipped\n" {
		t.Fatalf("second skip = %q", out)
	}
	if _, err := runDen(t, "update", "skip", "soon"); err == nil {
		t.Fatal("a skip that is not a version must fail")
	}
	if out, _ := runDen(t, "update", "unskip", "v1.0.0"); out != "v1.0.0 was not skipped\n" {
		t.Fatalf("unskip = %q", out)
	}
}

// A den.yaml the running release rejects must not keep it from installing the
// one that accepts it.
func TestUpdateReadsTheUpdatesBlockOfABrokenConfig(t *testing.T) {
	cfg, _ := updateEnv(t, "0.0.1", "")
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	broken := string(data) + "tunnels:\n  - name: no command\n"
	if err := os.WriteFile(cfg, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runDen(t, "update", "-c", cfg)
	if err != nil || !strings.Contains(out, "has errors") || !strings.Contains(out, "den v0.0.2 is available") {
		t.Fatalf("den update with a broken den.yaml = %q, %v", out, err)
	}
}
