package runbooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// layout makes the files and folders of paths under root: a path ending in /
// is a folder.
func layout(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if strings.HasSuffix(p, "/") {
			if err := os.MkdirAll(full, 0o750); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// plainTree is the tree as tree prints it, one row a line.
func plainTree(tr fileTree) string {
	lines := make([]string, len(tr.rows))
	for i, r := range tr.rows {
		line := r.prefix + r.name
		if r.dir && r.name != "." {
			line += "/"
		}
		if r.link != "" {
			line += " -> " + r.link
		}
		if r.runs > 0 {
			line += " (runs)"
		}
		if r.note != "" {
			line += " " + r.note
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func TestReadTreeDrawsLikeTree(t *testing.T) {
	root := t.TempDir()
	layout(t, root, "run.sh", "README.md", "sql/b.sql", "sql/A.sql", "sql/deep/c.sql", "zz/", ".env", ".git/config", "sql/.hidden")
	tr := readTree(root, "", filesLimit)
	want := strings.Join([]string{
		".",
		"├── README.md",
		"├── run.sh",
		"├── sql/",
		"│   ├── A.sql",
		"│   ├── b.sql",
		"│   └── deep/",
		"│       └── c.sql",
		"└── zz/",
	}, "\n")
	if got := plainTree(tr); got != want {
		t.Fatalf("tree:\n%s\nwant:\n%s", got, want)
	}
	if tr.dirs != 3 || tr.files != 5 || tr.summary() != "3 directories, 5 files" {
		t.Errorf("dirs %d, files %d, summary %q", tr.dirs, tr.files, tr.summary())
	}
}

// Linked folders are followed, as tree -l does, unless they lead back into a
// folder being listed.
func TestReadTreeFollowsLinksButNotLoops(t *testing.T) {
	root := t.TempDir()
	shared := t.TempDir()
	layout(t, shared, "lib.sh")
	layout(t, root, "sub/x.txt")
	for link, to := range map[string]string{"shared": shared, "sub/up": "..", "run": "sub/x.txt"} {
		if err := os.Symlink(to, filepath.Join(root, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
	}
	got := plainTree(readTree(root, "", filesLimit))
	for _, want := range []string{
		"├── run -> sub/x.txt",
		"├── shared/ -> " + shared,
		"│   └── lib.sh",
		"    ├── up/ -> .. [recursive, not followed]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("tree lacks %q:\n%s", want, got)
		}
	}
}

// den's own runs fold into the logs folder's row; what a script keeps there
// is listed. Outside logsDir, run-like names are ordinary files.
func TestReadTreeFoldsDensRuns(t *testing.T) {
	root := t.TempDir()
	layout(t, root, "logs/log-01.log", "logs/log-01.json", "logs/log-02.log", "logs/log-02.json", "logs/.gitignore",
		"logs/app.log", "other/log-01.log")
	tr := readTree(root, filepath.Join(root, "logs"), filesLimit)
	want := strings.Join([]string{
		".",
		"├── logs/ (runs)",
		"│   └── app.log",
		"└── other/",
		"    └── log-01.log",
	}, "\n")
	if got := plainTree(tr); got != want {
		t.Fatalf("tree:\n%s\nwant:\n%s", got, want)
	}
	if tr.rows[1].runs != 2 {
		t.Errorf("runs = %d, want 2 (one per .log)", tr.rows[1].runs)
	}
}

func TestReadTreeStopsAtTheLimit(t *testing.T) {
	root := t.TempDir()
	layout(t, root, "a/1", "a/2", "b", "c", "d")
	tr := readTree(root, "", 3)
	if !tr.cut || tr.dirs+tr.files != 3 || len(tr.rows) != 4 {
		t.Fatalf("cut %v, %d dirs, %d files, %d rows: want 3 entries and the cut", tr.cut, tr.dirs, tr.files, len(tr.rows))
	}
	if !strings.Contains(tr.summary(), "cut at") {
		t.Errorf("summary = %q, want the cut said", tr.summary())
	}
}

func TestFilesPercentFollowsTheWindow(t *testing.T) {
	v := View{files: fileTree{rows: make([]fileRow, 30)}}
	for offset, want := range map[int]float64{0: 0, 10: 50, 20: 100, 25: 100} {
		v.filesOffset = offset
		if got := v.filesPercent(10); got != want {
			t.Errorf("offset %d: %v%%, want %v%%", offset, got, want)
		}
	}
}
