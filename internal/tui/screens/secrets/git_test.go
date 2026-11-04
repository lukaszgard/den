package secrets

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/secrets"
	"github.com/lukaszgard/den/internal/secrets/gitfile"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/tui/testkeys"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// gitItem is a SOPS-like secret in a file that may be in git. It counts its
// reads and the edits started on it.
type gitItem struct {
	fakeItem
	path  string
	spec  config.SecretGitSpec
	reads *int
	edits *int
}

func (g gitItem) GitFile() (string, config.SecretGitSpec) { return g.path, g.spec }
func (g gitItem) Read(context.Context) ([]secrets.Field, error) {
	*g.reads++
	return g.fields, nil
}
func (g gitItem) EditCmd() *exec.Cmd           { *g.edits++; return exec.Command("true") }
func (gitItem) EditResult(error) (bool, error) { return true, nil }
func (g gitItem) Location() string             { return g.path }
func (g gitItem) Details() []secrets.Detail    { return []secrets.Detail{{Label: "File", Value: g.path}} }
func newGitItem(name, path string) gitItem {
	return gitItem{fakeItem: fakeItem{name: name, status: secrets.Status{Ready: true},
		fields: []secrets.Field{{Key: "password", Value: "hunter2"}}}, path: path, reads: new(int), edits: new(int)}
}

// fakeGit stands for git: what Check says of each file before and after its
// repository was fetched, and what was asked of it.
type fakeGit struct {
	repos    map[string]string // file → working copy
	before   map[string]gitfile.FileState
	after    map[string]gitfile.FileState // once its repository was fetched
	fetchErr error
	ffErr    error

	fetched map[string]bool
	fetches []string // repositories fetched, in order
	checks  int
	ffs     int
}

func newFakeGit() *fakeGit {
	return &fakeGit{repos: map[string]string{}, before: map[string]gitfile.FileState{},
		after: map[string]gitfile.FileState{}, fetched: map[string]bool{}}
}

func (f *fakeGit) Repo(file string) string { return f.repos[file] }

func (f *fakeGit) Check(_ context.Context, repo string, files []gitfile.File) []gitfile.FileState {
	f.checks++
	out := make([]gitfile.FileState, len(files))
	for i, file := range files {
		st, ok := f.after[file.Path]
		if !ok || !f.fetched[repo] {
			st = f.before[file.Path]
		}
		st.File = file.Path
		out[i] = st
	}
	return out
}

func (f *fakeGit) Fetch(_ context.Context, st gitfile.FileState) error {
	f.fetches = append(f.fetches, st.Name)
	if f.fetchErr != nil {
		return f.fetchErr
	}
	f.fetched[f.repos[st.File]] = true
	return nil
}

func (f *fakeGit) FastForward(_ context.Context, st gitfile.FileState) (gitfile.Moved, error) {
	f.ffs++
	if f.ffErr != nil {
		return gitfile.Moved{}, f.ffErr
	}
	up := st
	up.State, up.Behind, up.BranchBehind = gitfile.UpToDate, 0, 0
	f.before[st.File], f.after[st.File] = up, up
	return gitfile.Moved{Name: st.Name, Branch: st.Branch, From: "a1b2c3d", To: "d4e5f6a", Commits: st.BranchBehind}, nil
}

// inRepo is the state of a file in the infra working copy on main.
func inRepo(s gitfile.State) gitfile.FileState {
	return gitfile.FileState{State: s, Repo: "/r/infra", Name: "infra", Branch: "main", RefBranch: "main",
		Upstream: "origin/main", Remote: "origin", RemoteRef: "refs/heads/main", UpstreamRef: "refs/remotes/origin/main",
		Head: "a1b2c3d4", UpstreamOID: "d4e5f6a7", FetchedAt: time.Now().Add(-2 * time.Hour)}
}

func outdatedState() gitfile.FileState {
	st := inRepo(gitfile.Outdated)
	st.Behind, st.BranchBehind = 2, 7
	st.Last = gitfile.Commit{Short: "a1b2c3d", Subject: "rotate stripe key", Author: "Anna", When: time.Now().Add(-3 * time.Hour)}
	return st
}

// gitPane is a pane over a secret in the infra repository (payments dev), one
// in another (billing), and one in no repository (local env), after its first
// tick on screen.
func gitPane(t *testing.T, g *fakeGit) (Model, gitItem) {
	t.Helper()
	pay := newGitItem("payments dev", "/r/infra/secrets/payments-dev.enc.yaml")
	billing := newGitItem("billing", "/r/billing/b.enc.yaml")
	local := newGitItem("local env", "/home/me/.config/den/local.enc.env")
	g.repos[pay.path], g.repos[billing.path] = "/r/infra", "/r/billing"
	if _, ok := g.before[pay.path]; !ok {
		g.before[pay.path] = inRepo(gitfile.UpToDate)
	}
	b := inRepo(gitfile.UpToDate)
	b.Repo, b.Name = "/r/billing", "billing"
	g.before[billing.path] = b
	m := New([]secrets.Item{pay, billing, local}, 100, 30).WithGit(g)
	m, _ = drive(t, m, tick(m))
	return m, pay
}

func tick(m Model) tea.Cmd {
	return func() tea.Msg { return msgs.TickMsg{Time: time.Now()} }
}

// drive carries cmd out as Bubble Tea would, handing what it returns back to
// the pane, and returns the flashes shown on the way.
func drive(t *testing.T, m Model, cmd tea.Cmd) (Model, []string) {
	t.Helper()
	var flashes []string
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case msgs.StatusFlashMsg:
			flashes = append(flashes, msg.Text)
		default:
			var next tea.Cmd
			m, next = m.Update(msg, true)
			queue = append(queue, next)
		}
	}
	return m, flashes
}

func press(t *testing.T, m Model, key string) (Model, []string) {
	t.Helper()
	m, cmd := m.Update(testkeys.Press(key), true)
	return drive(t, m, cmd)
}

func view(m Model) string { return ansi.Strip(m.View(100, 30, true)) }

func TestGitBadgeAndDetails(t *testing.T) {
	g := newFakeGit()
	g.before["/r/infra/secrets/payments-dev.enc.yaml"] = outdatedState()
	m, _ := gitPane(t, g)
	v := view(m)
	if l := lineWith(t, v, "payments dev"); !strings.Contains(l, "ready") || !strings.HasSuffix(l, "↓ outdated") {
		t.Errorf("readiness and freshness are apart: %q", l)
	}
	if l := lineWith(t, v, "local env"); strings.Contains(l, "outdated") || strings.Contains(l, "up to date") || strings.Contains(l, "not in git") {
		t.Errorf("a file in no repository has no badge: %q", l)
	}
	if l := lineWith(t, v, "billing"); !strings.HasSuffix(l, "✓ up to date") {
		t.Errorf("billing: %q", l)
	}
	for _, want := range []string{"infra · main → origin/main · fetched 2h ago", "2 newer commits · a1b2c3d rotate stripe key (Anna, 3h ago)", "f: fetch", "u: update"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if len(g.fetches) != 0 {
		t.Errorf("the first check fetched %v: den fetches only on f or u", g.fetches)
	}

	m, _ = press(t, m, "down")
	m, _ = press(t, m, "down")
	if v := view(m); strings.Contains(v, "f: fetch") || strings.Contains(v, "Git ") {
		t.Errorf("a secret in no repository offers no git keys or rows:\n%s", v)
	}
	if _, cmd := m.Update(testkeys.Press("f"), true); cmd != nil {
		t.Error("f on a secret in no repository must do nothing")
	}
}

func TestTicksNeverFetch(t *testing.T) {
	g := newFakeGit()
	m, _ := gitPane(t, g)
	for i := range 3 {
		var cmd tea.Cmd
		m, cmd = m.Update(msgs.TickMsg{Time: time.Now().Add(time.Duration(i) * time.Minute)}, false)
		m, _ = drive(t, m, cmd)
	}
	if g.checks != 2 || len(g.fetches) != 0 {
		t.Errorf("%d checks, fetches %v: the first tick checks each repository once, offline, and no tick fetches", g.checks, g.fetches)
	}
}

func TestFetchKeyFetchesOnlyTheSelectedRepository(t *testing.T) {
	g := newFakeGit()
	g.after["/r/infra/secrets/payments-dev.enc.yaml"] = outdatedState()
	m, _ := gitPane(t, g)
	m, flashes := press(t, m, "f")
	if strings.Join(g.fetches, ",") != "infra" {
		t.Errorf("fetched %v, want infra alone", g.fetches)
	}
	if len(flashes) != 2 || flashes[0] != "fetching infra…" || flashes[1] != "payments dev: origin/main has a newer version (2 commits)" {
		t.Errorf("flashes = %q", flashes)
	}
	if !strings.HasSuffix(lineWith(t, view(m), "payments dev"), "↓ outdated") {
		t.Error("the badge must follow the fetch")
	}
}

func TestFetchFailureLeavesTheSecretReady(t *testing.T) {
	g := newFakeGit()
	g.fetchErr = errors.New("git fetch: ssh: connect to host git.acme.com port 22: Operation timed out")
	m, _ := gitPane(t, g)
	m, flashes := press(t, m, "f")
	if len(flashes) != 2 || !strings.Contains(flashes[1], "Operation timed out") {
		t.Errorf("flashes = %q", flashes)
	}
	v := view(m)
	if l := lineWith(t, v, "payments dev"); !strings.Contains(l, "ready") || !strings.HasSuffix(l, "! check failed") {
		t.Errorf("row = %q", l)
	}
	if !strings.Contains(v, "Error        git fetch: ssh: connect to host") {
		t.Errorf("the details box must show git's line:\n%s", v)
	}
}

func TestUpdateAsksThenFastForwardsAndReadsAgain(t *testing.T) {
	g := newFakeGit()
	g.after["/r/infra/secrets/payments-dev.enc.yaml"] = outdatedState()
	m, pay := gitPane(t, g)
	m, _ = press(t, m, "enter")
	if *pay.reads != 1 {
		t.Fatalf("reads = %d", *pay.reads)
	}
	m, flashes := press(t, m, "u")
	if len(g.fetches) != 1 || g.ffs != 0 {
		t.Fatalf("u must fetch first and ask before updating: fetches %v, fast-forwards %d", g.fetches, g.ffs)
	}
	if strings.Join(flashes, "|") != "fetching infra…" {
		t.Errorf("flashes = %q", flashes)
	}
	v := view(m)
	for _, want := range []string{"Fast-forward infra (main) by 7 commits? This updates the whole branch, not only this file.", "y: update", "n: cancel"} {
		if !strings.Contains(strings.Join(strings.Fields(v), " "), want) {
			t.Errorf("the question lacks %q:\n%s", want, v)
		}
	}
	if !m.Busy() || !m.Asking() {
		t.Error("esc and tab must stay with the question")
	}

	m, flashes = press(t, m, "y")
	if g.ffs != 1 {
		t.Fatalf("y must fast-forward once, got %d", g.ffs)
	}
	if last := flashes[len(flashes)-1]; last != "infra: main a1b2c3d → d4e5f6a (7 commits)" {
		t.Errorf("flashes = %q", flashes)
	}
	if *pay.reads != 2 {
		t.Errorf("reads = %d, want the open file read again after the update", *pay.reads)
	}
	if v := view(m); strings.Contains(v, "newer version") || m.Asking() {
		t.Errorf("after the update:\n%s", v)
	}
}

func TestUpdateOfAnUpToDateFileOnlyFlashes(t *testing.T) {
	g := newFakeGit()
	m, _ := gitPane(t, g)
	m, flashes := press(t, m, "u")
	if m.Asking() || g.ffs != 0 || flashes[len(flashes)-1] != "payments dev is up to date" {
		t.Errorf("asking %v, %d fast-forwards, flashes %q", m.Asking(), g.ffs, flashes)
	}
}

func TestUpdateCancelled(t *testing.T) {
	for _, key := range []string{"n", "esc"} {
		g := newFakeGit()
		g.after["/r/infra/secrets/payments-dev.enc.yaml"] = outdatedState()
		m, _ := gitPane(t, g)
		m, _ = press(t, m, "u")
		m, flashes := press(t, m, key)
		if g.ffs != 0 || m.Asking() || strings.Join(flashes, "") != "payments dev not updated" {
			t.Errorf("%s: %d fast-forwards, asking %v, flashes %q", key, g.ffs, m.Asking(), flashes)
		}
	}
}

func TestUpdateRefused(t *testing.T) {
	diverged := outdatedState()
	diverged.State = gitfile.Diverged
	other := outdatedState()
	other.Branch = "feature/x"
	for name, st := range map[string]gitfile.FileState{"diverged": diverged, "other branch": other} {
		g := newFakeGit()
		g.after["/r/infra/secrets/payments-dev.enc.yaml"] = st
		m, _ := gitPane(t, g)
		m, flashes := press(t, m, "u")
		if m.Asking() || g.ffs != 0 || !strings.HasPrefix(flashes[len(flashes)-1], "payments dev not updated: "+st.Refusal()) {
			t.Errorf("%s: asking %v, flashes %q", name, m.Asking(), flashes)
		}
	}
}

func TestEditWarnsWhenOutdated(t *testing.T) {
	g := newFakeGit()
	g.before["/r/infra/secrets/payments-dev.enc.yaml"] = outdatedState()
	m, pay := gitPane(t, g)
	m, _ = press(t, m, "e")
	if *pay.edits != 0 || !m.Asking() {
		t.Fatal("e on an outdated file must ask first")
	}
	v := strings.Join(strings.Fields(view(m)), " ")
	for _, want := range []string{"origin/main has a newer version of this file.", "u: update first", "e: edit anyway"} {
		if !strings.Contains(v, want) {
			t.Errorf("the warning lacks %q:\n%s", want, v)
		}
	}
	m, _ = press(t, m, "e")
	if *pay.edits != 1 {
		t.Error("e again edits anyway")
	}

	m, _ = press(t, m, "e")
	press(t, m, "u")
	if len(g.fetches) != 1 {
		t.Errorf("u from the warning updates first: fetches %v", g.fetches)
	}
}

func TestEditIsCheckedAgain(t *testing.T) {
	g := newFakeGit()
	m, _ := gitPane(t, g)
	before := g.checks
	g.before["/r/infra/secrets/payments-dev.enc.yaml"] = inRepo(gitfile.NotCommitted)
	m, cmd := m.Update(editedMsg{name: "payments dev"}, true)
	m, _ = drive(t, m, cmd)
	if g.checks != before+2 || !strings.HasSuffix(lineWith(t, view(m), "payments dev"), "✎ not committed") {
		t.Errorf("checks %d → %d; a save must be checked again:\n%s", before, g.checks, view(m))
	}
}

func TestOpenFileBanner(t *testing.T) {
	g := newFakeGit()
	g.before["/r/infra/secrets/payments-dev.enc.yaml"] = outdatedState()
	m, _ := gitPane(t, g)
	m, _ = press(t, m, "enter")
	v := view(m)
	if l := lineWith(t, v, "newer version"); !strings.Contains(l, "↓ origin/main has a newer version of this file (2 commits)   u: update") {
		t.Errorf("banner = %q", l)
	}
	if !strings.Contains(v, "hunter2") && !strings.Contains(v, mask) {
		t.Errorf("the local values are still shown:\n%s", v)
	}
}

// f while a pass runs waits for it, then runs once.
func TestOneGitPassAtATime(t *testing.T) {
	g := newFakeGit()
	g.repos["/r/infra/a.enc.yaml"] = "/r/infra"
	g.before["/r/infra/a.enc.yaml"] = inRepo(gitfile.UpToDate)
	m := New([]secrets.Item{newGitItem("a", "/r/infra/a.enc.yaml")}, 100, 30).WithGit(g)
	m, first := m.Update(msgs.TickMsg{Time: time.Now()}, false)
	if first == nil {
		t.Fatal("the first tick must start a check")
	}
	m, cmd := m.Update(testkeys.Press("f"), true)
	m, flashes := drive(t, m, cmd)
	if len(g.fetches) != 0 || len(flashes) != 1 {
		t.Fatalf("f during a pass must wait: fetches %v, flashes %q", g.fetches, flashes)
	}
	drive(t, m, first)
	if len(g.fetches) != 1 {
		t.Errorf("the waiting fetch must run once the pass ends: %v", g.fetches)
	}
}

// A config reload builds a new pane: a pass of the old one must not land in it.
func TestGitPassOfAReplacedPaneIgnored(t *testing.T) {
	g := newFakeGit()
	old, _ := gitPane(t, g)
	_, cmd := old.recheckGit(localCheck)
	stale := cmd()
	g2 := newFakeGit()
	g2.before["/r/infra/secrets/payments-dev.enc.yaml"] = outdatedState()
	fresh, _ := gitPane(t, g2)
	fresh, _ = fresh.Update(stale, true)
	if !strings.HasSuffix(lineWith(t, view(fresh), "payments dev"), "↓ outdated") {
		t.Error("a pass of the replaced pane changed the new pane's states")
	}
}

func TestCheckFalseTurnsGitOff(t *testing.T) {
	g := newFakeGit()
	it := newGitItem("scratch", "/r/infra/scratch.enc.yaml")
	off := false
	it.spec.Check = &off
	g.repos[it.path] = "/r/infra"
	m := New([]secrets.Item{it}, 100, 30).WithGit(g)
	m, cmd := m.Update(msgs.TickMsg{Time: time.Now()}, false)
	m, _ = drive(t, m, cmd)
	if g.checks != 0 || strings.Contains(view(m), "f: fetch") {
		t.Errorf("check: false: %d checks", g.checks)
	}
}

func TestUntrackedFileOffersNoUpdate(t *testing.T) {
	g := newFakeGit()
	g.before["/r/infra/secrets/payments-dev.enc.yaml"] = gitfile.FileState{State: gitfile.NotTracked, Repo: "/r/infra", Name: "infra"}
	m, _ := gitPane(t, g)
	v := view(m)
	if !strings.HasSuffix(lineWith(t, v, "payments dev"), "not in git") || strings.Contains(v, "u: update") {
		t.Errorf("an untracked file:\n%s", v)
	}
}

func TestMissingFileShowsNoGit(t *testing.T) {
	g := newFakeGit()
	g.before["/r/infra/secrets/payments-dev.enc.yaml"] = gitfile.FileState{State: gitfile.Unknown, Repo: "/r/infra", Name: "infra"}
	m, _ := gitPane(t, g)
	if v := view(m); strings.Contains(v, "Git ") || strings.Contains(v, "u: update") || strings.Contains(v, "checking") {
		t.Errorf("a file that is not there:\n%s", v)
	}
}

// f pressed before the first check landed, on a file that turns out missing.
func TestFetchOfAMissingFileSaysSo(t *testing.T) {
	g := newFakeGit()
	it := newGitItem("gone", "/r/infra/gone.enc.yaml")
	it.status = secrets.Status{Reason: "file not found"}
	g.repos[it.path] = "/r/infra"
	g.before[it.path] = gitfile.FileState{State: gitfile.Unknown, Repo: "/r/infra", Name: "infra"}
	m := New([]secrets.Item{it}, 100, 30).WithGit(g)
	_, flashes := press(t, m, "f")
	if strings.Join(flashes, "|") != "fetching infra…|gone: file not found" {
		t.Errorf("flashes = %q", flashes)
	}
}

func TestFailedUpdateSaysWhy(t *testing.T) {
	g := newFakeGit()
	g.after["/r/infra/secrets/payments-dev.enc.yaml"] = outdatedState()
	g.ffErr = errors.New("git merge: local changes to other.txt would be overwritten; commit or stash them first")
	m, _ := gitPane(t, g)
	m, _ = press(t, m, "u")
	_, flashes := press(t, m, "y")
	if last := flashes[len(flashes)-1]; !strings.Contains(last, "payments dev not updated: git merge: local changes to other.txt") {
		t.Errorf("flashes = %q", flashes)
	}
}

func TestAgo(t *testing.T) {
	now := time.Now()
	for d, want := range map[time.Duration]string{10 * time.Second: "now", 5 * time.Minute: "5m ago", 3 * time.Hour: "3h ago", 72 * time.Hour: "3d ago"} {
		if got := ago(now.Add(-d), now); got != want {
			t.Errorf("ago(%s) = %q, want %q", d, got, want)
		}
	}
}
