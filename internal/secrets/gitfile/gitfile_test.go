package gitfile

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/leakcheck"
)

// git's global and system config are kept out of every test, so a
// developer's commit signing or URL rewrites do not reach them. It is set
// here rather than with t.Setenv so that the tests can run in parallel.
func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	leakcheck.Main(m)
}

const secret = "secrets/payments.enc.yaml"

// clone is a working copy in a temp dir. The tests run the real git: what
// den reads is git's own output, so a fake would only test the fake.
type clone struct {
	t   *testing.T
	dir string
}

// setup makes a bare "remote" with one commit holding a secret and an
// unrelated file, and two clones of it: a teammate's, who pushes, and mine,
// which den checks.
func setup(t *testing.T) (remote string, teammate, mine *clone) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Parallel()
	root := t.TempDir()
	remote = filepath.Join(root, "remote.git")
	run(t, root, "init", "-q", "--bare", "-b", "main", remote)
	teammate = &clone{t: t, dir: filepath.Join(root, "teammate")}
	run(t, root, "clone", "-q", remote, teammate.dir)
	teammate.write(secret, "password: one\n")
	teammate.write("other.txt", "a\n")
	teammate.commit("first")
	teammate.git("push", "-q", "origin", "main")
	mine = &clone{t: t, dir: filepath.Join(root, "infra")}
	run(t, root, "clone", "-q", remote, mine.dir)
	return remote, teammate, mine
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Anna", "-c", "user.email=anna@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (c *clone) git(args ...string) string { c.t.Helper(); return run(c.t, c.dir, args...) }

func (c *clone) write(rel, body string) {
	c.t.Helper()
	p := filepath.Join(c.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		c.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		c.t.Fatal(err)
	}
}

func (c *clone) commit(msg string) {
	c.t.Helper()
	c.git("add", "-A")
	c.git("commit", "-q", "-m", msg)
}

// push commits a change to rel and pushes it.
func (c *clone) push(rel, body, msg string) {
	c.t.Helper()
	c.write(rel, body)
	c.commit(msg)
	c.git("push", "-q", "origin", "HEAD")
}

func (c *clone) path(rel string) string { return filepath.Join(c.dir, filepath.FromSlash(rel)) }

// check is Check of one file of c.
func (c *clone) check(rel, branch string) FileState {
	c.t.Helper()
	return Git{}.Check(context.Background(), Find(c.path(rel)), []File{{Path: c.path(rel), Branch: branch}})[0]
}

// fetch fetches the upstream rel is compared with, then checks it again.
func (c *clone) fetch(rel, branch string) FileState {
	c.t.Helper()
	if err := (Git{}).Fetch(context.Background(), c.check(rel, branch)); err != nil {
		c.t.Fatal(err)
	}
	return c.check(rel, branch)
}

func TestFindWalksUpToTheWorkingCopy(t *testing.T) {
	_, _, mine := setup(t)
	if got := Find(mine.path(secret)); got != mine.dir {
		t.Errorf("Find = %q, want %q", got, mine.dir)
	}
	if got := Find(filepath.Join(t.TempDir(), "local.enc.env")); got != "" {
		t.Errorf("Find outside a repository = %q, want none", got)
	}
	// A worktree has a .git file, not a directory.
	wt := filepath.Join(t.TempDir(), "wt")
	mine.git("worktree", "add", "-q", "-b", "wt", wt)
	if got := Find(filepath.Join(wt, secret)); got != wt {
		t.Errorf("Find in a worktree = %q, want %q", got, wt)
	}
}

func TestOutdatedFileFastForwards(t *testing.T) {
	_, teammate, mine := setup(t)
	if st := mine.check(secret, ""); st.State != UpToDate || st.Upstream != "origin/main" || st.Branch != "main" || st.Name != "infra" {
		t.Fatalf("fresh clone: %+v", st)
	}
	teammate.push(secret, "password: two\n", "rotate the password")
	teammate.push("other.txt", "b\n", "unrelated")

	// Offline, den only knows the refs it has.
	if st := mine.check(secret, ""); st.State != UpToDate {
		t.Fatalf("before a fetch: %v, want up to date", st.State)
	}
	st := mine.fetch(secret, "")
	if st.State != Outdated || st.Behind != 1 || st.BranchBehind != 2 || st.Last.Subject != "rotate the password" || st.Last.Author != "Anna" {
		t.Fatalf("after the fetch: %+v", st)
	}
	if st.FetchedAt.IsZero() {
		t.Error("FetchedAt is the time of FETCH_HEAD, which the fetch wrote")
	}
	if why := st.Refusal(); why != "" {
		t.Fatalf("an outdated file on its branch must update, refused: %s", why)
	}
	moved, err := Git{}.FastForward(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Commits != 2 || moved.Branch != "main" || len(moved.From) != 7 || moved.From == moved.To {
		t.Errorf("moved = %+v", moved)
	}
	if !strings.HasPrefix(moved.String(), "infra: main "+moved.From+" → "+moved.To+" (2 commits)") {
		t.Errorf("String = %q", moved)
	}
	if body, _ := os.ReadFile(mine.path(secret)); string(body) != "password: two\n" {
		t.Errorf("the file reads %q after the update", body)
	}
	if st := mine.check(secret, ""); st.State != UpToDate {
		t.Errorf("after the update: %v", st.State)
	}
}

func TestUnrelatedUpstreamCommitLeavesTheFileUpToDate(t *testing.T) {
	_, teammate, mine := setup(t)
	teammate.push("other.txt", "b\n", "unrelated")
	if st := mine.fetch(secret, ""); st.State != UpToDate || st.BranchBehind != 1 {
		t.Errorf("state %v, %d behind: a busy repository is not an outdated secret", st.State, st.BranchBehind)
	}
}

func TestLocalChangesAreReminders(t *testing.T) {
	_, _, mine := setup(t)
	mine.write(secret, "password: mine\n")
	if st := mine.check(secret, ""); st.State != NotCommitted {
		t.Fatalf("edited: %v, want not committed", st.State)
	}
	mine.commit("my change")
	st := mine.check(secret, "")
	if st.State != NotPushed || st.Ahead != 1 {
		t.Fatalf("committed: %v (%d ahead), want not pushed", st.State, st.Ahead)
	}
	if st.Refusal() == "" {
		t.Error("only an outdated file is updated")
	}
	mine.git("push", "-q", "origin", "main")
	if st := mine.check(secret, ""); st.State != UpToDate {
		t.Errorf("pushed: %v", st.State)
	}
}

func TestDivergedFileIsRefused(t *testing.T) {
	_, teammate, mine := setup(t)
	teammate.push(secret, "password: theirs\n", "theirs")
	mine.write(secret, "password: mine\n")
	if st := mine.fetch(secret, ""); st.State != Diverged {
		t.Fatalf("edited here, changed upstream: %v, want diverged", st.State)
	}
	mine.commit("mine")
	st := mine.check(secret, "")
	if st.State != Diverged || st.Refusal() == "" {
		t.Fatalf("committed here, changed upstream: %v, refusal %q", st.State, st.Refusal())
	}
	head := mine.git("rev-parse", "HEAD")
	if _, err := (Git{}).FastForward(context.Background(), st); err == nil || !strings.Contains(err.Error(), "resolve it in git") {
		t.Errorf("err = %v, want the refusal", err)
	}
	if mine.git("rev-parse", "HEAD") != head {
		t.Error("a refused update moved HEAD")
	}
}

func TestDetachedHead(t *testing.T) {
	_, teammate, mine := setup(t)
	mine.git("checkout", "-q", "--detach")
	if st := mine.check(secret, ""); st.State != NoUpstream || !strings.Contains(st.Note, "detached") {
		t.Fatalf("detached without git.branch: %+v", st)
	}
	teammate.push(secret, "password: two\n", "rotate")
	st := mine.fetch(secret, "main")
	if st.State != Outdated || !strings.Contains(st.Refusal(), "detached") {
		t.Fatalf("detached, compared with main: %v, refusal %q", st.State, st.Refusal())
	}
}

func TestOperationInProgressIsRefused(t *testing.T) {
	_, teammate, mine := setup(t)
	mine.git("checkout", "-q", "-b", "side")
	mine.write("other.txt", "side\n")
	mine.commit("side")
	mine.git("checkout", "-q", "main")
	mine.write("other.txt", "main\n")
	mine.commit("main")
	cmd := exec.Command("git", "-c", "user.name=t", "-c", "user.email=t@example.com", "merge", "-q", "side")
	cmd.Dir = mine.dir
	if err := cmd.Run(); err == nil {
		t.Fatal("the merge was meant to conflict")
	}
	teammate.push(secret, "password: two\n", "rotate")
	st := mine.fetch(secret, "")
	if st.Busy != "merge" || !strings.Contains(st.Refusal(), "merge is in progress") {
		t.Errorf("busy %q, refusal %q", st.Busy, st.Refusal())
	}
}

// A file that is not there gets nothing from git: its probe says it is missing.
func TestMissingFileIsUnknown(t *testing.T) {
	_, _, mine := setup(t)
	if st := mine.check("secrets/gone/x.enc.yaml", ""); st.State != Unknown || st.Err != "" {
		t.Errorf("missing file: %+v", st)
	}
}

func TestUntrackedOrIgnoredFileIsNotInGit(t *testing.T) {
	_, _, mine := setup(t)
	mine.write("scratch.enc.yaml", "x: 1\n")
	mine.write(".gitignore", "*.local.yaml\n")
	mine.write("dev.local.yaml", "x: 1\n")
	for _, rel := range []string{"scratch.enc.yaml", "dev.local.yaml"} {
		if st := mine.check(rel, ""); st.State != NotTracked || st.State.String() != "not in git" {
			t.Errorf("%s: %v, want not in git", rel, st.State)
		}
	}
}

func TestBranchOtherThanTheCurrentOne(t *testing.T) {
	_, teammate, mine := setup(t)
	mine.git("checkout", "-q", "-b", "feature/x")
	teammate.push(secret, "password: two\n", "rotate")
	st := mine.fetch(secret, "main")
	if st.State != Outdated || st.Upstream != "origin/main" || st.RefBranch != "main" || st.Branch != "feature/x" {
		t.Fatalf("on feature/x, compared with main: %+v", st)
	}
	if why := st.Refusal(); !strings.Contains(why, "you are on feature/x; switch to main") {
		t.Errorf("refusal = %q", why)
	}
	if _, err := (Git{}).FastForward(context.Background(), st); err == nil {
		t.Error("an update on another branch than git.branch's must be refused")
	}

	// Without a local main, git.branch falls back on origin's.
	mine.git("branch", "-q", "-D", "main")
	if st := mine.check(secret, "main"); st.Upstream != "origin/main" || st.Remote != "origin" || st.State != Outdated {
		t.Errorf("no local main: %+v", st)
	}
}

func TestNoUpstream(t *testing.T) {
	_, teammate, mine := setup(t)
	mine.git("checkout", "-q", "-b", "local")
	if st := mine.check(secret, ""); st.State != NoUpstream || !strings.Contains(st.Note, "local tracks no remote branch") {
		t.Errorf("a branch without upstream: %+v", st)
	}
	if err := (Git{}).Fetch(context.Background(), mine.check(secret, "")); err == nil {
		t.Error("fetching with no upstream must say so")
	}

	// A git.branch origin has but this clone never fetched.
	teammate.git("push", "-q", "origin", "main:release")
	st := mine.check(secret, "release")
	if st.State != NoUpstream || !strings.Contains(st.Note, "origin/release has not been fetched") {
		t.Fatalf("never fetched: %+v", st)
	}
	if st := mine.fetch(secret, "release"); st.State != UpToDate {
		t.Errorf("after fetching it: %+v", st)
	}
}

func TestFetchFailureIsOneLine(t *testing.T) {
	_, _, mine := setup(t)
	mine.git("remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	err := Git{}.Fetch(context.Background(), mine.check(secret, ""))
	if err == nil || strings.Contains(err.Error(), "\n") || !strings.HasPrefix(err.Error(), "git fetch: ") {
		t.Fatalf("err = %v, want git's last line", err)
	}
	if st := mine.check(secret, ""); st.State != UpToDate {
		t.Errorf("a failed fetch changes nothing local: %v", st.State)
	}
}

func TestLocalChangesInTheWayStopTheUpdate(t *testing.T) {
	_, teammate, mine := setup(t)
	teammate.write("other.txt", "theirs\n")
	teammate.push(secret, "password: two\n", "both")
	mine.write("other.txt", "mine\n")
	st := mine.fetch(secret, "")
	if st.State != Outdated {
		t.Fatalf("state %v", st.State)
	}
	_, err := Git{}.FastForward(context.Background(), st)
	if err == nil || !strings.Contains(err.Error(), "local changes to other.txt would be overwritten") {
		t.Fatalf("err = %v, want the file in the way named", err)
	}
	if body, _ := os.ReadFile(mine.path("other.txt")); string(body) != "mine\n" {
		t.Error("the person's change was lost")
	}
}

func TestFastForwardRechecksFirst(t *testing.T) {
	_, teammate, mine := setup(t)
	teammate.push(secret, "password: two\n", "rotate")
	st := mine.fetch(secret, "")
	mine.write("other.txt", "local\n")
	mine.commit("moved on") // HEAD moved after the question was asked
	if _, err := (Git{}).FastForward(context.Background(), st); err == nil || !strings.Contains(err.Error(), "changed since it was checked") {
		t.Errorf("err = %v, want the stale state refused", err)
	}
}

func TestCheckWithoutGit(t *testing.T) {
	t.Parallel()
	st := Git{Bin: filepath.Join(t.TempDir(), "git")}.Check(context.Background(), t.TempDir(), []File{{Path: "/x/a.yaml"}})[0]
	if st.State != CheckFailed || !strings.HasPrefix(st.Err, "git not installed") {
		t.Errorf("state %+v", st)
	}
}

func TestFilesInOneRepositoryShareTheCheck(t *testing.T) {
	_, teammate, mine := setup(t)
	teammate.push("secrets/other.enc.yaml", "k: v\n", "another secret")
	teammate.push(secret, "password: two\n", "rotate")
	mine.git("pull", "-q", "--ff-only")
	teammate.push(secret, "password: three\n", "rotate again")
	if err := (Git{}).Fetch(context.Background(), mine.check(secret, "")); err != nil {
		t.Fatal(err)
	}
	mine.write("nope.yaml", "x: 1\n")
	got := Git{}.Check(context.Background(), mine.dir, []File{
		{Path: mine.path(secret)}, {Path: mine.path("secrets/other.enc.yaml")}, {Path: mine.path("nope.yaml")},
	})
	if got[0].State != Outdated || got[1].State != UpToDate || got[2].State != NotTracked {
		t.Errorf("states %v %v %v", got[0].State, got[1].State, got[2].State)
	}
}

func TestStateNames(t *testing.T) {
	for s, want := range map[State]string{Unknown: "", Outdated: "outdated", Diverged: "diverged", State(99): ""} {
		if s.String() != want {
			t.Errorf("%d = %q, want %q", s, s, want)
		}
	}
}

func TestStagedNewFileIsNotCommitted(t *testing.T) {
	_, _, mine := setup(t)
	mine.write("secrets/new.enc.yaml", "k: v\n")
	mine.git("add", "secrets/new.enc.yaml")
	if st := mine.check("secrets/new.enc.yaml", ""); st.State != NotCommitted || st.Path != "secrets/new.enc.yaml" {
		t.Errorf("staged: %+v", st)
	}
	mine.git("mv", secret, "secrets/renamed.enc.yaml")
	if st := mine.check("secrets/renamed.enc.yaml", ""); st.State != NotCommitted {
		t.Errorf("renamed: %v", st.State)
	}
}

// A branch that tracks a local branch needs no fetch: it is always current.
func TestLocalUpstream(t *testing.T) {
	_, _, mine := setup(t)
	mine.git("checkout", "-q", "-b", "topic", "--track", "main")
	st := mine.check(secret, "")
	if st.State != UpToDate || st.Upstream != "main" || st.Remote != "." {
		t.Fatalf("tracking main: %+v", st)
	}
	if err := (Git{}).Fetch(context.Background(), st); err != nil {
		t.Errorf("fetch of a local upstream: %v", err)
	}
	if (Git{}).Repo(mine.path(secret)) != mine.dir {
		t.Error("Repo is Find")
	}
}
