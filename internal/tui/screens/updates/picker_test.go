package updates

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/tui/applog"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/tui/testkeys"
	"github.com/lukaszgard/den/internal/update"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The version picker installs any published release, older ones included. A
// slip here downgrades someone without saying so, installs a yanked release,
// or drops the skip list a downgrade relies on.

func pickerList() []update.Release {
	rel := func(v string, day int) update.Release {
		return update.Release{Version: v, Date: time.Date(2026, 10, day, 10, 0, 0, 0, time.UTC),
			Archive: update.ArchiveName(v, runtime.GOOS, runtime.GOARCH), ArchiveURL: "https://example.invalid/" + v, Notes: "notes of " + v}
	}
	rc := rel("v1.8.0-rc.1", 20)
	rc.Prerelease = true
	yanked := rel("v1.7.0", 10)
	yanked.Yanked = "SSM loop"
	noBuild := rel("v1.6.0", 1)
	noBuild.ArchiveURL = ""
	return []update.Release{rc, rel("v1.7.2", 16), rel("v1.7.1", 15), yanked, noBuild}
}

// pickerPane runs 1.7.2 and has checked against pickerList.
func pickerPane(t *testing.T, cfg *config.UpdatesConfig) Model {
	t.Helper()
	m := New("1.7.2", cfg, applog.New(), nil, 120, 30).WithPrefs(filepath.Join(t.TempDir(), "updates.json"))
	m.newSource = func(*config.UpdatesConfig) update.Source { return update.Source{} }
	m, _ = m.Update(msgs.UpdateCheckResultMsg{Latest: "v1.7.2", Releases: pickerList()}, false)
	return m
}

func press(m Model, keys ...string) Model {
	for _, k := range keys {
		m, _ = m.Update(testkeys.Press(k), true)
	}
	return m
}

func screen(m Model) string { return ansi.Strip(m.View(120, 30, true)) }

func rowOf(t *testing.T, view, version string) string {
	t.Helper()
	for line := range strings.SplitSeq(view, "\n") {
		if strings.Contains(line, version+" ") {
			return line
		}
	}
	t.Fatalf("no row for %s:\n%s", version, view)
	return ""
}

func TestPickerListsEveryRelease(t *testing.T) {
	m := pickerPane(t, nil)
	if view := screen(m); !strings.Contains(view, "v: versions") {
		t.Fatalf("the pane should offer the picker:\n%s", view)
	}
	m = press(m, "v")
	if !m.ShowingPicker() {
		t.Fatal("v should open the picker")
	}
	view := screen(m)
	for version, want := range map[string]string{
		"v1.7.2": "current",
		"v1.7.1": "↓ downgrade",
		"v1.7.0": "⚠ yanked: SSM loop",
		"v1.6.0": fmt.Sprintf("no %s/%s build", runtime.GOOS, runtime.GOARCH),
	} {
		if row := rowOf(t, view, version); !strings.Contains(row, want) {
			t.Errorf("row %q misses %q", row, want)
		}
	}
	if !strings.Contains(rowOf(t, view, "v1.7.2"), "▸") || !strings.Contains(view, "2026-10-16") {
		t.Errorf("the cursor should start on the running version, with dates:\n%s", view)
	}
	// Release candidates are the prerelease channel's, until p shows them.
	if strings.Contains(view, "v1.8.0-rc.1") {
		t.Fatalf("the stable channel hides release candidates:\n%s", view)
	}
	view = screen(press(m, "p"))
	if row := rowOf(t, view, "v1.8.0-rc.1"); !strings.Contains(row, "↑ upgrade") || !strings.Contains(row, "rc") {
		t.Errorf("rc row = %q", row)
	}
	if view := screen(press(pickerPane(t, &config.UpdatesConfig{Channel: "prerelease"}), "v")); !strings.Contains(view, "v1.8.0-rc.1") {
		t.Errorf("the prerelease channel shows release candidates at once:\n%s", view)
	}

	m = press(m, "esc")
	if m.ShowingPicker() {
		t.Fatal("esc should close the picker")
	}
}

func TestPickerDowngradeAsksAndSkipsTheVersionLeft(t *testing.T) {
	m := press(pickerPane(t, nil), "v", "down", "enter")
	view := screen(m)
	for _, want := range []string{"Install v1.7.1?", "↓ downgrade from v1.7.2", "older than the version picker (v1.8.0)",
		"newer than v1.7.1 are ignored or rejected", "☑ skip v1.7.2 in update checks", "y: install  n: cancel  x: skip v1.7.2"} {
		if !strings.Contains(view, want) {
			t.Errorf("confirm misses %q:\n%s", want, view)
		}
	}
	// x is the skip box; enter is not an answer.
	m = press(m, "x", "enter")
	if view := screen(m); !strings.Contains(view, "☐ skip v1.7.2") || m.state == stateInstalling {
		t.Fatalf("x should clear the box and enter install nothing:\n%s", view)
	}
	m = press(m, "x", "n")
	if !m.ShowingPicker() || strings.Contains(screen(m), "Install v1.7.1?") {
		t.Fatal("n should go back to the list")
	}

	m = press(m, "enter")
	m, cmd := m.Update(testkeys.Press("y"), true)
	if cmd == nil || m.state != stateInstalling || m.ShowingPicker() {
		t.Fatalf("y should start installing and close the picker, state %v", m.state)
	}
	if view := screen(m); !strings.Contains(view, "Installing v1.7.1") {
		t.Fatalf("the install should name the chosen version:\n%s", view)
	}
	started, ok := cmd().(tea.BatchMsg)
	if !ok || len(started) == 0 {
		t.Fatalf("y produced %T", cmd())
	}
	if msg, ok := started[0]().(msgs.UpdateInstallStartedMsg); !ok || msg.Version != "v1.7.1" {
		t.Fatalf("started = %#v, want v1.7.1", msg)
	}

	// The version left is skipped only once the install worked.
	if prefs, _ := update.LoadPrefs(m.prefsPath); prefs.Skips("v1.7.2") {
		t.Fatal("v1.7.2 skipped before the install finished")
	}
	m, _ = m.Update(msgs.UpdateInstallResultMsg{Version: "v1.7.1"}, false)
	if prefs, err := update.LoadPrefs(m.prefsPath); err != nil || !prefs.Skips("v1.7.2") {
		t.Fatalf("after the downgrade v1.7.2 should be skipped: %+v, %v", prefs, err)
	}
	if view := screen(m); !strings.Contains(view, "Installed v1.7.1") {
		t.Errorf("the result should name the installed version:\n%s", view)
	}
}

func TestFailedDowngradeSkipsNothing(t *testing.T) {
	m := press(pickerPane(t, nil), "v", "down", "enter", "y")
	m, _ = m.Update(msgs.UpdateInstallResultMsg{Version: "v1.7.1", Err: errors.New("download: boom")}, false)
	if prefs, _ := update.LoadPrefs(m.prefsPath); prefs.Skips("v1.7.2") {
		t.Fatal("a failed downgrade must not skip the version still running")
	}
}

func TestPickerConfirmWarnsOfYankedAndPin(t *testing.T) {
	m := press(pickerPane(t, &config.UpdatesConfig{Pin: "v1.7.1"}), "v", "down", "down", "enter")
	view := screen(m)
	for _, want := range []string{"Install v1.7.0?", "⚠ yanked: SSM loop", "updates.pin holds den on v1.7.1"} {
		if !strings.Contains(view, want) {
			t.Errorf("confirm misses %q:\n%s", want, view)
		}
	}
	// An upgrade has no skip box.
	m = press(pickerPane(t, nil), "v", "p", "up", "enter")
	if view := screen(m); !strings.Contains(view, "↑ upgrade from v1.7.2") || strings.Contains(view, "skip v1.7.2") ||
		strings.Contains(view, "older than the version picker") {
		t.Errorf("an upgrade asks plainly:\n%s", view)
	}
}

func TestPickerRefusesWhatItCannotInstall(t *testing.T) {
	m := press(pickerPane(t, nil), "v", "enter")
	if view := screen(m); m.picker.confirm || !strings.Contains(view, "v1.7.2 is the version you run") {
		t.Fatalf("enter on the running version installs nothing:\n%s", view)
	}
	m = press(m, "down", "down", "down", "enter")
	if view := screen(m); m.picker.confirm || !strings.Contains(view, "v1.6.0 has no "+runtime.GOOS+"/"+runtime.GOARCH+" build") {
		t.Fatalf("a release without this platform's archive cannot be installed:\n%s", view)
	}
}

func TestPickerSkipToggleIsSavedAndRechecks(t *testing.T) {
	m := press(pickerPane(t, nil), "v", "down", "x")
	if prefs, _ := update.LoadPrefs(m.prefsPath); !prefs.Skips("v1.7.1") {
		t.Fatal("x should save the skip at once")
	}
	if row := rowOf(t, screen(m), "v1.7.1"); !strings.Contains(row, "skipped") {
		t.Errorf("row = %q, want it marked skipped", row)
	}
	m = press(m, "x")
	if prefs, _ := update.LoadPrefs(m.prefsPath); prefs.Skips("v1.7.1") {
		t.Fatal("a second x should take it off")
	}
	// Closing after an edit checks again under the new list.
	m, cmd := m.Update(testkeys.Press("esc"), true)
	if cmd == nil || m.state != stateChecking {
		t.Fatal("closing after a skip edit should check again")
	}
	if _, cmd := press(pickerPane(t, nil), "v").Update(testkeys.Press("esc"), true); cmd != nil {
		t.Fatal("closing without an edit checks nothing")
	}
}

func TestPickerNotes(t *testing.T) {
	m := press(pickerPane(t, nil), "v", "down", "s")
	if view := screen(m); !strings.Contains(view, "Release notes") || !strings.Contains(view, "notes of v1.7.1") {
		t.Fatalf("s should open the notes of the row:\n%s", view)
	}
	m = press(m, "esc")
	if !m.ShowingPicker() || strings.Contains(screen(m), "notes of v1.7.1") {
		t.Fatal("esc should close the notes, back to the list")
	}
}

// Without releases.json the check keeps no list: the picker asks for one, says
// so while it loads, and offers a retry when it fails.
func TestPickerLoadsTheList(t *testing.T) {
	m := New("1.7.2", nil, applog.New(), nil, 120, 30)
	m, _ = m.Update(msgs.UpdateCheckResultMsg{Latest: "v1.7.2"}, false)
	m, cmd := m.Update(testkeys.Press("v"), true)
	if cmd == nil || !strings.Contains(screen(m), "Loading releases…") {
		t.Fatalf("v should load the list:\n%s", screen(m))
	}
	m, _ = m.Update(msgs.UpdateReleasesMsg{Err: errors.New("HTTP 403: rate limit")}, false)
	view := screen(m)
	if !strings.Contains(view, "Could not list the releases") || !strings.Contains(view, "rate limit") || !strings.Contains(view, "r: retry") {
		t.Fatalf("a failed load should say why and offer a retry:\n%s", view)
	}
	if m, cmd = m.Update(testkeys.Press("r"), true); cmd == nil || !m.picker.loading {
		t.Fatal("r should load again")
	}
	m, _ = m.Update(msgs.UpdateReleasesMsg{Releases: pickerList()}, false)
	if view := screen(m); !strings.Contains(view, "v1.7.1") {
		t.Fatalf("the loaded list should show:\n%s", view)
	}
	// The list is kept: opening again loads nothing.
	m = press(m, "esc")
	if _, cmd := m.Update(testkeys.Press("v"), true); cmd != nil {
		t.Fatal("a second v should reuse the list")
	}
	// A config reload may change the source, so the list goes.
	if _, cmd := m.SetUpdatesConfig(nil).Update(testkeys.Press("v"), true); cmd == nil {
		t.Fatal("after a config reload v should load again")
	}
}

func TestStatusSaysPinAndYanked(t *testing.T) {
	list := pickerList()
	m := New("1.7.0", &config.UpdatesConfig{Pin: "v1.7.1"}, applog.New(), nil, 120, 30)
	m, _ = m.Update(msgs.UpdateCheckResultMsg{Latest: "v1.7.1", Releases: list, Why: "updates.pin holds den on v1.7.1"}, false)
	view := screen(m)
	for _, want := range []string{"pinned to v1.7.1 (updates.pin)", "⚠ v1.7.0 is yanked: SSM loop", "↑ v1.7.1 available", "updates.pin holds den on v1.7.1"} {
		if !strings.Contains(view, want) {
			t.Errorf("status misses %q:\n%s", want, view)
		}
	}

	// A check that goes down from a yanked release says it once.
	m = New("1.7.0", nil, applog.New(), nil, 120, 30)
	m, _ = m.Update(msgs.UpdateCheckResultMsg{Latest: "v1.6.0", Releases: list, Why: "v1.7.0 is yanked: SSM loop"}, false)
	view = screen(m)
	if strings.Count(view, "is yanked") != 1 || !strings.Contains(view, "↓ v1.6.0 offered — older than this den") {
		t.Errorf("the way off a yanked release:\n%s", view)
	}
}

// recorder answers every request with reply and keeps the URLs asked.
type recorder struct {
	mu    sync.Mutex
	asked []string
	reply func(*http.Request) int
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.asked = append(r.asked, req.URL.String())
	r.mu.Unlock()
	code := r.reply(req)
	body := ""
	if code == http.StatusOK {
		body = `{"version":"v1.7.3","notes":"n"}`
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: req}, nil
}

// The startup check runs on every start: with no releases.json published (all
// releases before the picker) it must read latest.json as before, never the
// rate-limited API.
func TestStartupCheckWithoutIndexNeverAsksTheAPI(t *testing.T) {
	rec := &recorder{reply: func(req *http.Request) int {
		if strings.HasSuffix(req.URL.Path, "/latest.json") {
			return http.StatusOK
		}
		return http.StatusNotFound
	}}
	m := New("1.7.2", nil, applog.New(), nil, 120, 30)
	m.newSource = func(cfg *config.UpdatesConfig) update.Source {
		src := update.NewSource(cfg)
		src.Token = ""
		src.HTTPClient = &http.Client{Transport: rec}
		return src
	}
	msg, ok := m.Check()().(msgs.UpdateCheckResultMsg)
	if !ok || msg.Err != nil || msg.Latest != "v1.7.3" || msg.Releases != nil {
		t.Fatalf("check = %#v", msg)
	}
	for _, u := range rec.asked {
		if strings.Contains(u, "api.github.com") {
			t.Errorf("the startup check asked the API: %v", rec.asked)
		}
	}
	if len(rec.asked) != 2 {
		t.Errorf("asked %v, want releases.json then latest.json", rec.asked)
	}
}
