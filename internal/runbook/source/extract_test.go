package source

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type entry struct {
	name, body, link string
	dir              bool
	mode             int64
}

func tarOf(t *testing.T, entries ...entry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: e.mode}
		switch {
		case e.dir:
			h.Typeflag, h.Mode = tar.TypeDir, 0o755
		case e.link != "":
			h.Typeflag, h.Linkname, h.Mode = tar.TypeSymlink, e.link, 0o777
		default:
			h.Typeflag, h.Size = tar.TypeReg, int64(len(e.body))
			if h.Mode == 0 {
				h.Mode = 0o644
			}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestExtractRefusesEscapes(t *testing.T) {
	cases := map[string][]entry{
		"a parent path":            {{name: "../evil", body: "x"}},
		"an absolute path":         {{name: "/tmp/evil", body: "x"}},
		"a link climbing out":      {{name: "a/l", link: "../../etc/passwd"}},
		"an absolute link":         {{name: "l", link: "/etc/passwd"}},
		"a file under a link":      {{name: "d", dir: true}, {name: "l", link: "d"}, {name: "l/f", body: "x"}},
		"an entry of another kind": {{name: "h"}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			buf := tarOf(t, entries...)
			if name == "an entry of another kind" {
				// A hard link: git archive never writes one.
				buf = hardLinkTar(t)
			}
			dir := filepath.Join(t.TempDir(), "tree")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := extract(buf, dir); err == nil {
				t.Fatal("extract accepted it")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil")); err == nil {
				t.Error("a file landed outside the tree")
			}
		})
	}
}

func hardLinkTar(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "h", Typeflag: tar.TypeLink, Linkname: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestExtractKeepsModesAndInsideLinks(t *testing.T) {
	dir := t.TempDir()
	buf := tarOf(t,
		entry{name: "pax_global_header"}, // replaced below by a real global header
		entry{name: "a/", dir: true},
		entry{name: "a/run.sh", body: "#!/bin/sh\n", mode: 0o775},
		entry{name: "a/notes.txt", body: "hi", mode: 0o664},
		entry{name: "b/f", body: "shared"},
		entry{name: "a/shared", link: "../b/f"},
	)
	if err := extract(withGlobalHeader(t, buf), dir); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, "a", "run.sh")); err != nil || info.Mode().Perm() != 0o750 {
		t.Errorf("run.sh mode = %v, %v: want 0750", info, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "a", "notes.txt")); err != nil || info.Mode().Perm() != 0o640 {
		t.Errorf("notes.txt mode = %v, %v: want 0640", info, err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "a", "shared")); err != nil || string(data) != "shared" {
		t.Errorf("a link inside the tree: %q, %v", data, err)
	}
}

// withGlobalHeader swaps the placeholder first entry for the pax global
// header git archive starts with.
func withGlobalHeader(t *testing.T, rest *bytes.Buffer) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": "c0ffee"}}); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(rest)
	first := true
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		if first {
			first = false
			continue
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			body, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}
