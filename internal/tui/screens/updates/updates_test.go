package updates

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/tui/applog"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/tui/testkeys"
	"github.com/lukaszgard/den/internal/update"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// A restart closes every connection, so these pin when the prompt appears,
// which keys answer it, and that it says what will close.

// installed returns a pane that has just installed v9.9.9.
func installed(t *testing.T, active func() []string) Model {
	t.Helper()
	if !update.CanReexec {
		t.Skip("restart is not offered on this platform")
	}
	m := New("1.0.0", nil, applog.New(), active, 100, 30)
	m, _ = m.Update(msgs.UpdateCheckResultMsg{Latest: "v9.9.9"}, false)
	m, _ = m.Update(msgs.UpdateInstallResultMsg{}, false)
	return m
}

func TestInstallOpensRestartPrompt(t *testing.T) {
	m := installed(t, nil)
	if !m.ShowingRestartPrompt() {
		t.Fatal("a successful install should open the restart prompt")
	}
	view := ansi.Strip(m.View(100, 30, true))
	for _, want := range []string{"Installed v9.9.9", "Restart den now?", "No connections are open.", "y: restart now  n: later"} {
		if !strings.Contains(view, want) {
			t.Errorf("prompt misses %q:\n%s", want, view)
		}
	}
}

func TestFailedInstallHasNoRestartPrompt(t *testing.T) {
	m := New("1.0.0", nil, applog.New(), nil, 100, 30)
	m, _ = m.Update(msgs.UpdateInstallResultMsg{Err: errors.New("replace binary: denied")}, false)
	if m.ShowingRestartPrompt() {
		t.Fatal("a failed install must not offer a restart")
	}
	m, _ = m.Update(testkeys.Press("r"), true)
	if m.ShowingRestartPrompt() {
		t.Fatal("r must not offer a restart before an install succeeded")
	}
}

func TestRestartPromptListsWhatCloses(t *testing.T) {
	active := []string{"rds  orders  :5432"}
	m := installed(t, func() []string { return active })
	view := ansi.Strip(m.View(100, 30, true))
	if !strings.Contains(view, "Restarting closes 1 connection:") || !strings.Contains(view, "rds  orders  :5432") {
		t.Fatalf("prompt should list the open connection:\n%s", view)
	}
	// The list is read at render time, not copied when the install ended.
	active = append(active, "vpn  corp")
	view = ansi.Strip(m.View(100, 30, true))
	if !strings.Contains(view, "Restarting closes 2 connections:") || !strings.Contains(view, "vpn  corp") {
		t.Fatalf("prompt should follow connections opened since the install:\n%s", view)
	}
}

func TestRestartPromptKeys(t *testing.T) {
	m := installed(t, nil)

	// Unfocused, the pane ignores keys: the y belongs to whatever has focus.
	m, cmd := m.Update(testkeys.Press("y"), false)
	if cmd != nil || !m.ShowingRestartPrompt() {
		t.Fatal("an unfocused pane must not answer the prompt")
	}
	// enter is too easy to press by reflex for something that drops connections.
	m, cmd = m.Update(testkeys.Press("enter"), true)
	if cmd != nil || !m.ShowingRestartPrompt() {
		t.Fatal("enter must not restart")
	}

	for _, later := range []string{"n", "esc"} {
		m, cmd = m.Update(testkeys.Press(later), true)
		if cmd != nil || m.ShowingRestartPrompt() {
			t.Fatalf("%s should close the prompt without restarting", later)
		}
		if view := ansi.Strip(m.View(100, 30, true)); !strings.Contains(view, "Press r to restart now.") {
			t.Fatalf("after %s the pane should say how to restart later:\n%s", later, view)
		}
		m, _ = m.Update(testkeys.Press("r"), true)
		if !m.ShowingRestartPrompt() {
			t.Fatal("r should reopen the prompt")
		}
	}

	m, cmd = m.Update(testkeys.Press("y"), true)
	if m.ShowingRestartPrompt() || cmd == nil {
		t.Fatal("y should close the prompt and ask for a restart")
	}
	got, ok := cmd().(msgs.UpdateRestartMsg)
	if !ok || got.Version != "v9.9.9" {
		t.Fatalf("y produced %#v, want UpdateRestartMsg{v9.9.9}", got)
	}
}

// A release's notes are its CHANGELOG section, several times the height of a
// terminal. Printed into the pane they made the frame taller than the screen,
// and Bubble Tea then drops the top of it: the header, the menu and the start
// of the notes. These pin where the notes go instead, and that no state of the
// pane is taller than the rows it is given.

func longNotes() string {
	var b strings.Builder
	b.WriteString("What this release is about.\n\n### Section\n\n")
	for i := range 200 {
		fmt.Fprintf(&b, "- note line %d\n", i)
	}
	return b.String()
}

func rows(s string) int { return strings.Count(s, "\n") + 1 }

// available returns a pane that has found v9.9.9 with long notes.
func available(height int) Model {
	m := New("1.0.0", nil, applog.New(), nil, 100, height)
	m, _ = m.Update(msgs.UpdateCheckResultMsg{Latest: "v9.9.9", Notes: longNotes()}, false)
	return m
}

func TestChangelogDoc(t *testing.T) {
	installed := "# Changelog\n\n## [1.0.0] - 2026-01-01\n\nold\n"

	doc := changelogDoc(installed, "v9.9.9", "new things")
	head, old := strings.Index(doc, "## v9.9.9 — new, not installed yet"), strings.Index(doc, "## [1.0.0]")
	if !strings.HasPrefix(doc, "# Changelog\n") || head < 0 || old < head ||
		strings.Index(doc, "new things") < head || strings.Index(doc, "new things") > old {
		t.Fatalf("the new release's notes should come first, under their own heading:\n%s", doc)
	}
	if strings.Count(doc, "# Changelog") != 1 {
		t.Fatalf("the title should appear once:\n%s", doc)
	}

	// A build from a checkout that already has the section must not show it twice.
	if got := changelogDoc(installed, "v1.0.0", "old"); got != installed {
		t.Fatalf("a changelog that has the release should be left alone:\n%s", got)
	}
	if got := changelogDoc(installed, "v9.9.9", ""); got != installed {
		t.Fatalf("without notes there is nothing to add:\n%s", got)
	}
}

func TestNotesOpenInTheChangelogBox(t *testing.T) {
	m := available(30)
	view := ansi.Strip(m.View(100, 30, true))
	if strings.Contains(view, "note line") {
		t.Fatalf("the pane should not print the notes:\n%s", view)
	}
	for _, want := range []string{"v9.9.9 available", "What's new: press s", "s: what's new"} {
		if !strings.Contains(view, want) {
			t.Errorf("pane misses %q:\n%s", want, view)
		}
	}

	m, _ = m.Update(testkeys.Press("s"), true)
	if !m.ShowingChangelog() {
		t.Fatal("s should open the changelog")
	}
	view = ansi.Strip(m.View(100, 30, true))
	for _, want := range []string{"Changelog", "v9.9.9 — new, not installed yet", "What this release is about."} {
		if !strings.Contains(view, want) {
			t.Errorf("changelog misses %q:\n%s", want, view)
		}
	}
}

func TestChangelogHintWithoutNotes(t *testing.T) {
	m := New("1.0.0", nil, applog.New(), nil, 100, 30)
	m, _ = m.Update(msgs.UpdateCheckResultMsg{Latest: "v9.9.9"}, false)
	view := ansi.Strip(m.View(100, 30, true))
	if strings.Contains(view, "What's new") || !strings.Contains(view, "s: changelog") {
		t.Fatalf("a release without notes has nothing new to point at:\n%s", view)
	}
}

func TestViewFitsThePane(t *testing.T) {
	many := make([]string, 40)
	for i := range many {
		many[i] = fmt.Sprintf("rds  db-%d  :54%02d", i, i)
	}
	longErr := errors.New(strings.Repeat("step failed: ", 30) + "first line\nsecond line\nthird line")

	cases := []struct {
		name  string
		build func(t *testing.T, height int) Model
		want  string // still on screen however little room there is
	}{
		{"update available", func(_ *testing.T, h int) Model { return available(h) }, "s: what's new"},
		{"restart prompt", func(t *testing.T, h int) Model {
			return installed(t, func() []string { return many }).SetSize(100, h)
		}, "y: restart now  n: later"},
		{"check failed", func(_ *testing.T, h int) Model {
			m := New("1.0.0", nil, applog.New(), nil, 100, h)
			m, _ = m.Update(msgs.UpdateCheckResultMsg{Err: longErr}, false)
			return m
		}, "c: check"},
		{"install failed", func(_ *testing.T, h int) Model {
			m := available(h)
			for i := range 30 {
				m, _ = m.Update(msgs.UpdateInstallProgressMsg{Step: fmt.Sprintf("step %d", i)}, false)
			}
			m, _ = m.Update(msgs.UpdateInstallResultMsg{Err: longErr}, false)
			return m
		}, "i: install"},
		{"help", func(_ *testing.T, h int) Model {
			m, _ := available(h).Update(testkeys.Press("h"), true)
			return m
		}, "↑ Updates"},
		{"changelog", func(_ *testing.T, h int) Model {
			m, _ := available(h).Update(testkeys.Press("s"), true)
			return m
		}, "esc/s: close"},
	}
	for _, tc := range cases {
		for _, height := range []int{12, 21, 40} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, height), func(t *testing.T) {
				view := ansi.Strip(tc.build(t, height).View(100, height, true))
				if got := rows(view); got > height {
					t.Errorf("view is %d rows in a %d-row pane:\n%s", got, height, view)
				}
				if !strings.Contains(view, tc.want) {
					t.Errorf("view misses %q:\n%s", tc.want, view)
				}
			})
		}
	}
}

// For S3 the release URL is the archive itself, and curl cannot fetch s3://.
func TestManualDownloadCommandForS3(t *testing.T) {
	cfg := &config.UpdatesConfig{S3Bucket: "bucket", S3Prefix: "den/", AWSProfile: "team", AWSRegion: "eu-central-1"}
	archive := "s3://bucket/den/v9.9.9/den_9.9.9_linux_amd64.tar.gz"
	m := New("1.0.0", cfg, applog.New(), nil, 160, 40)
	m, _ = m.Update(msgs.UpdateCheckResultMsg{Latest: "v9.9.9", URL: archive}, false)
	view := ansi.Strip(m.View(160, 40, true))
	// The flags go on a continuation line: on one line the command is wider
	// than the pane of a 120-column terminal and its end is cut off.
	for _, want := range []string{"aws s3 cp " + archive + " . \\", "  --profile team --region eu-central-1"} {
		if !strings.Contains(view, want) {
			t.Errorf("pane misses %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "curl") || strings.Contains(view, "Release page") {
		t.Errorf("an S3 source has no release page and nothing curl can fetch:\n%s", view)
	}

	// Without a profile or a region there is nothing to continue.
	m = New("1.0.0", &config.UpdatesConfig{S3Bucket: "bucket"}, applog.New(), nil, 160, 40)
	m, _ = m.Update(msgs.UpdateCheckResultMsg{Latest: "v9.9.9", URL: archive}, false)
	if view := ansi.Strip(m.View(160, 40, true)); !strings.Contains(view, "aws s3 cp "+archive+" .") || strings.Contains(view, "\\") {
		t.Errorf("a bare command should stay on one line:\n%s", view)
	}

	// The GitHub Enterprise source keeps its download line.
	m = New("1.0.0", nil, applog.New(), nil, 160, 40)
	m, _ = m.Update(msgs.UpdateCheckResultMsg{Latest: "v9.9.9", URL: "https://ghe.example/org/den/releases/tag/v9.9.9"}, false)
	if view := ansi.Strip(m.View(160, 40, true)); !strings.Contains(view, "curl -LO https://ghe.example/org/den/releases/archive/") ||
		!strings.Contains(view, "Release page: https://ghe.example/org/den/releases/tag/v9.9.9") {
		t.Errorf("the GitHub Enterprise instructions changed:\n%s", view)
	}
}

// The tab's progress bar follows the install: a percentage for the download
// while its size is known, indeterminate for the rest, and nothing otherwise.
func TestProgressFollowsTheInstall(t *testing.T) {
	m := New("1.0.0", nil, applog.New(), nil, 100, 30)
	if m.Progress() != nil {
		t.Fatal("an idle pane has no progress bar")
	}
	m.state = stateInstalling
	if bar := m.Progress(); bar == nil || bar.State != tea.ProgressBarIndeterminate {
		t.Fatalf("an install with no size yet should be indeterminate, got %+v", bar)
	}
	m, _ = m.Update(msgs.UpdateDownloadProgressMsg{Downloaded: 25, Total: 100}, false)
	if bar := m.Progress(); bar == nil || bar.State != tea.ProgressBarDefault || bar.Value != 25 {
		t.Fatalf("a quarter downloaded should be 25%%, got %+v", bar)
	}
	m, _ = m.Update(msgs.UpdateDownloadProgressMsg{Downloaded: 100, Total: 100}, false)
	if bar := m.Progress(); bar == nil || bar.State != tea.ProgressBarIndeterminate {
		t.Fatalf("a finished download leaves the extracting and installing, got %+v", bar)
	}
	m, _ = m.Update(msgs.UpdateInstallResultMsg{}, false)
	if m.Progress() != nil {
		t.Fatal("the bar should clear when the install ends")
	}
}
