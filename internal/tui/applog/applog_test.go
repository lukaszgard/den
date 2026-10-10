package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Configure runs again on every config reload: the same file stays open and
// is appended to once, a file no longer named is closed, a new one opened.
func TestConfigureAgainKeepsOrSwapsTheLogFile(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "a.log"), filepath.Join(dir, "b.log")
	l := New()
	t.Cleanup(l.Close)

	if err := l.Configure("info", first); err != nil {
		t.Fatal(err)
	}
	out := l.fileOut
	if err := l.Configure("debug", first); err != nil {
		t.Fatal(err)
	}
	if l.fileOut != out {
		t.Fatal("a reload naming the same file opened it again")
	}
	l.Debug("one")
	if err := l.Configure("info", second); err != nil {
		t.Fatal(err)
	}
	l.Info("two")
	if err := l.Configure("", ""); err != nil {
		t.Fatal(err)
	}
	if l.fileOut != nil {
		t.Fatal("a reload without logs.file_location left the file open")
	}
	l.Info("three")

	for path, want := range map[string]string{first: "one", second: "two"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(data); strings.Count(got, "\n") != 1 || !strings.Contains(got, want) {
			t.Errorf("%s = %q, want one line with %q", filepath.Base(path), got, want)
		}
	}
}

// A run printing more than the panel keeps must not push den's own lines
// out: the two are kept apart, and put back in the order they came.
func TestRunOutputKeepsDensOwnLines(t *testing.T) {
	l := New()
	l.Warn("before")
	for i := range 2 * maxLines {
		l.Output("deploy", fmt.Sprintf("line %d", i))
	}
	l.Info("after")

	own := plain(l.Lines(false))
	if len(own) != 2 || !strings.Contains(own[0], "before") || !strings.Contains(own[1], "after") {
		t.Fatalf("den's own lines = %q, want before and after", own)
	}
	all := plain(l.Lines(true))
	if len(all) != maxLines+2 {
		t.Fatalf("got %d lines, want %d", len(all), maxLines+2)
	}
	if !strings.Contains(all[0], "[WARN] before") ||
		!strings.Contains(all[1], fmt.Sprintf("[deploy] line %d", maxLines)) ||
		!strings.Contains(all[len(all)-1], "[INFO] after") {
		t.Errorf("lines out of order: first %q, %q, last %q", all[0], all[1], all[len(all)-1])
	}
}

// The panel refreshes when Version changes; the length stops changing once
// the buffers are full.
func TestVersionChangesPastTheCap(t *testing.T) {
	l := New()
	for range maxLines {
		l.Info("x")
	}
	v := l.Version()
	l.Info("y")
	if l.Version() == v {
		t.Fatal("Version did not change once the buffer was full")
	}
	v = l.Version()
	l.Debug("below the level")
	if l.Version() != v {
		t.Fatal("a line below the level changed Version")
	}
}

// Run output keeps its colours in the panel, loses anything else that acts
// on the terminal, and reaches the file as plain words.
func TestRunOutputColoursAndFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "den.log")
	l := New()
	t.Cleanup(l.Close)
	if err := l.Configure("info", path); err != nil {
		t.Fatal(err)
	}
	l.Output("deploy", "\x1b[32mok\x1b[0m done\x1b]0;title\x07")

	got := l.Lines(true)[0]
	if !strings.Contains(got, "\x1b[32mok\x1b[0m done") {
		t.Errorf("panel line %q lost the script's colour", got)
	}
	if strings.Contains(got, "\x1b]") || strings.Contains(got, "title") {
		t.Errorf("panel line %q kept an OSC", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(data); !strings.Contains(s, "[RUN deploy] ok done\n") || strings.Contains(s, "\x1b") {
		t.Errorf("file = %q, want the run's words without escape codes", s)
	}
}

// Run output counts as info: a quieter level leaves it out.
func TestRunOutputFollowsTheLevel(t *testing.T) {
	l := New()
	if err := l.Configure("warn", ""); err != nil {
		t.Fatal(err)
	}
	l.Output("deploy", "ok")
	if n := len(l.Lines(true)); n != 0 {
		t.Errorf("got %d lines at level warn, want none", n)
	}
}

// den's own lines carry the level's colour on their tag.
func TestLevelsAreColoured(t *testing.T) {
	l := New()
	l.Warn("w")
	l.Error("e")
	lines := l.Lines(false)
	for i, line := range lines {
		if !strings.Contains(line, "\x1b[") {
			t.Errorf("line %d %q has no colour", i, line)
		}
	}
	if lines[0] == strings.Replace(lines[1], "ERROR", "WARN", 1) {
		t.Error("WARN and ERROR look the same")
	}
}

func plain(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Strip(l)
	}
	return out
}
