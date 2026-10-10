package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
)

// testRepo is a real git repository in a temp dir: den's sources are fetched
// by the real git, so the tests are too.
type testRepo struct {
	t   *testing.T
	dir string
}

// newRepo makes an empty repository on branch main. git's global and system
// config are kept out, so a developer's commit signing or URL rewrites do not
// reach the tests, or den's own git calls in them.
func newRepo(t *testing.T) *testRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	r := &testRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	return r
}

func (r *testRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *testRepo) write(rel, body string, mode os.FileMode) {
	r.t.Helper()
	path := filepath.Join(r.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		r.t.Fatal(err)
	}
}

func (r *testRepo) remove(rel string) {
	r.t.Helper()
	if err := os.RemoveAll(filepath.Join(r.dir, filepath.FromSlash(rel))); err != nil {
		r.t.Fatal(err)
	}
}

// commit records everything and returns the commit's id.
func (r *testRepo) commit(msg string) string {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

// runbook writes a runbook folder at rel.
func (r *testRepo) runbook(rel, script string) {
	r.write(rel+"/runbook.yaml", "command: ./run.sh\n", 0o644)
	r.write(rel+"/run.sh", "#!/bin/sh\n"+script+"\n", 0o755)
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestUpdateDefaultBranch(t *testing.T) {
	repo := newRepo(t)
	repo.runbook("rb/a", "echo a")
	repo.write("rb/a/README.md", "# A\n", 0o644)
	head := repo.commit("one")

	store := NewStore(t.TempDir())
	src := config.RunbookSource{Name: "ops", URL: repo.dir, Path: "rb"}
	u, err := store.Update(testCtx(t), src, "")
	if err != nil {
		t.Fatal(err)
	}
	if u.Commit != head || u.Previous != "" || !u.Moved() {
		t.Fatalf("update = %+v, want commit %s from nothing", u, head)
	}
	tree := store.Tree(src, head)
	if tree == "" {
		t.Fatal("the commit is not checked out")
	}
	info, err := os.Stat(filepath.Join(tree, "rb", "a", "run.sh"))
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("run.sh = %v, %v: want an executable file", info, err)
	}
	if _, err := os.Stat(filepath.Join(tree, ".git")); err == nil {
		t.Error("a checked-out tree has no .git: nobody should commit or pull in it")
	}
	idx, ok := store.Index(src, head)
	if !ok || idx.Runbooks["rb/a"] == "" || idx.Root == "" {
		t.Fatalf("index = %+v, %v: want the tree of rb/a", idx, ok)
	}
	if want := []Change{{Folder: "a", Kind: Added}}; !slices.Equal(u.Changes, want) {
		t.Errorf("changes = %+v, want %+v", u.Changes, want)
	}
}

func TestUpdateTagBranchAndCommit(t *testing.T) {
	repo := newRepo(t)
	repo.runbook("a", "echo 1")
	first := repo.commit("one")
	repo.git("tag", "v1")
	repo.git("branch", "stable")
	repo.runbook("a", "echo 2")
	second := repo.commit("two")

	cases := map[string]string{"": second, "main": second, "v1": first, "stable": first, first: first, first[:7]: first}
	for ref, want := range cases {
		store := NewStore(t.TempDir())
		u, err := store.Update(testCtx(t), config.RunbookSource{Name: "ops", URL: repo.dir, Ref: ref}, "")
		if err != nil {
			t.Fatalf("ref %q: %v", ref, err)
		}
		if u.Commit != want {
			t.Errorf("ref %q checked out %s, want %s", ref, u.Commit, want)
		}
	}
}

func TestUpdateUnknownRef(t *testing.T) {
	repo := newRepo(t)
	repo.runbook("a", "echo 1")
	repo.commit("one")
	_, err := NewStore(t.TempDir()).Update(testCtx(t), config.RunbookSource{Name: "ops", URL: repo.dir, Ref: "nope"}, "")
	if err == nil || !strings.Contains(err.Error(), `ref "nope" is not a tag, branch or commit`) {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdateReportsChanges(t *testing.T) {
	repo := newRepo(t)
	repo.runbook("a", "echo a")
	repo.runbook("c", "echo c")
	repo.write("lib/common.sh", "#!/bin/sh\n", 0o755)
	first := repo.commit("one")
	store := NewStore(t.TempDir())
	src := config.RunbookSource{Name: "ops", URL: repo.dir}
	if _, err := store.Update(testCtx(t), src, ""); err != nil {
		t.Fatal(err)
	}

	repo.runbook("a", "echo a, changed")
	repo.runbook("b", "echo b")
	repo.remove("c")
	repo.write("lib/common.sh", "#!/bin/sh\necho helper\n", 0o755)
	second := repo.commit("two")

	u, err := store.Update(testCtx(t), src, first)
	if err != nil {
		t.Fatal(err)
	}
	if u.Previous != first || u.Commit != second {
		t.Fatalf("update = %+v", u)
	}
	var got []string
	for _, c := range u.Changes {
		got = append(got, c.Kind+" "+c.Folder)
	}
	if want := []string{"changed a", "added b", "removed c"}; !slices.Equal(got, want) {
		t.Fatalf("changes = %v, want %v (lib holds no runbook)", got, want)
	}
	if !strings.Contains(u.Changes[0].Stat, "1 file changed") {
		t.Errorf("stat of a = %q, want git's shortstat", u.Changes[0].Stat)
	}

	again, err := store.Update(testCtx(t), src, second)
	if err != nil {
		t.Fatal(err)
	}
	if again.Moved() || len(again.Changes) != 0 {
		t.Errorf("an update with nothing new = %+v, want no move and no changes", again)
	}
}

func TestUpdatePrunesOldTrees(t *testing.T) {
	repo := newRepo(t)
	store := NewStore(t.TempDir())
	src := config.RunbookSource{Name: "ops", URL: repo.dir}
	var commits []string
	previous := ""
	for i := range 3 {
		repo.runbook("a", strings.Repeat("echo x;", i+1))
		commits = append(commits, repo.commit("change"))
		u, err := store.Update(testCtx(t), src, previous)
		if err != nil {
			t.Fatal(err)
		}
		previous = u.Commit
	}
	if store.Tree(src, commits[0]) != "" {
		t.Error("the oldest tree is still there")
	}
	if _, ok := store.Index(src, commits[0]); ok {
		t.Error("the oldest tree's index is still there")
	}
	for _, c := range commits[1:] {
		if store.Tree(src, c) == "" {
			t.Errorf("tree %s is gone: the current one and the one before it stay, for runs still in flight", Short(c))
		}
	}
}

func TestUpdateBadURL(t *testing.T) {
	newRepo(t) // for the git config isolation
	src := config.RunbookSource{Name: "ops", URL: filepath.Join(t.TempDir(), "missing")}
	_, err := NewStore(t.TempDir()).Update(testCtx(t), src, "")
	if err == nil || !strings.Contains(err.Error(), "source ops: git fetch") {
		t.Fatalf("err = %v, want one naming the source and the git step", err)
	}
	if strings.Count(err.Error(), "\n") > 0 {
		t.Errorf("err = %q, want one line", err)
	}
}

func TestSyncChecksOutALockedCommit(t *testing.T) {
	repo := newRepo(t)
	repo.runbook("a", "echo 1")
	first := repo.commit("one")
	repo.runbook("a", "echo 2")
	repo.commit("two")

	store := NewStore(t.TempDir())
	src := config.RunbookSource{Name: "ops", URL: repo.dir}
	if err := store.Sync(testCtx(t), src, first); err != nil {
		t.Fatal(err)
	}
	tree := store.Tree(src, first)
	if tree == "" {
		t.Fatal("the locked commit is not checked out")
	}
	if data, _ := os.ReadFile(filepath.Join(tree, "a", "run.sh")); !strings.Contains(string(data), "echo 1") {
		t.Errorf("run.sh = %q, want the locked commit's", data)
	}
	if err := store.Sync(testCtx(t), src, strings.Repeat("0", 40)); err == nil {
		t.Error("a commit the repository does not have should fail")
	}
}

func TestRemoveForgetsTheSource(t *testing.T) {
	repo := newRepo(t)
	repo.runbook("a", "echo 1")
	head := repo.commit("one")
	store := NewStore(t.TempDir())
	src := config.RunbookSource{Name: "ops", URL: repo.dir}
	if _, err := store.Update(testCtx(t), src, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(src); err != nil {
		t.Fatal(err)
	}
	if store.Tree(src, head) != "" {
		t.Error("the tree survived Remove")
	}
}

func TestStoreKeysASourceByItsRepository(t *testing.T) {
	store := NewStore(t.TempDir())
	a := store.TreeDir(config.RunbookSource{Name: "ops", URL: "acme/one"}, "c1")
	b := store.TreeDir(config.RunbookSource{Name: "ops", URL: "acme/two"}, "c1")
	if a == b {
		t.Error("pointing a source at another repository must not reuse the first one's checkouts")
	}
}

func TestGitEnvNeverPrompts(t *testing.T) {
	env := gitEnv([]string{"PATH=/bin", "GIT_TERMINAL_PROMPT=1"})
	for _, want := range []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "SSH_ASKPASS_REQUIRE=never", "GIT_SSH_COMMAND=ssh -o BatchMode=yes", "PATH=/bin"} {
		if !slices.Contains(env, want) {
			t.Errorf("env %v lacks %s", env, want)
		}
	}
	if slices.Contains(env, "GIT_TERMINAL_PROMPT=1") {
		t.Error("the inherited GIT_TERMINAL_PROMPT=1 survived")
	}
	own := gitEnv([]string{"GIT_SSH_COMMAND=ssh -i ~/.ssh/work"})
	if !slices.Contains(own, "GIT_SSH_COMMAND=ssh -i ~/.ssh/work") || slices.Contains(own, "GIT_SSH_COMMAND=ssh -o BatchMode=yes") {
		t.Errorf("env %v: a GIT_SSH_COMMAND of the user's own is kept", own)
	}
}

func TestStoreRefusesWhatIsNotACommitID(t *testing.T) {
	store := NewStore(t.TempDir())
	src := config.RunbookSource{Name: "ops", URL: "acme/ops"}
	if store.Tree(src, "../../etc") != "" {
		t.Error("Tree followed a path out of the store")
	}
	if _, ok := store.Index(src, "../x"); ok {
		t.Error("Index read a file out of the store")
	}
	if err := store.Sync(context.Background(), src, "--output=/tmp/pwned"); err == nil || !strings.Contains(err.Error(), "not a commit id") {
		t.Errorf("Sync = %v", err)
	}
}
