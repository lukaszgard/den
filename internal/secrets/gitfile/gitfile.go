// Package gitfile tells whether a file kept in a git working copy is up to
// date with its upstream, and brings it up to date. It is what the Secrets
// pane knows of a SOPS file a team keeps in git: a teammate rotates a value
// and pushes, and the old value would otherwise look ready until a login
// fails.
//
// The comparison is per file, not per repository: a busy repository is
// nearly always behind, so a secret is outdated only when its own blob on
// the upstream differs from the one checked out.
//
// Check is offline: it reads the refs the repository already has, so a fetch
// by the shell or an IDE counts as much as one by den. Fetch is the only
// network call, and FastForward the only write, which never does more than a
// fast-forward of the branch checked out.
package gitfile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/gitcli"
	"github.com/lukaszgard/den/internal/prereq"
)

// FetchTimeout bounds one fetch: one that has not finished in 30 seconds is
// stuck on the network (a VPN down, a firewall), not slow.
const FetchTimeout = 30 * time.Second

// Find returns the folder of the working copy file is in — the nearest one up
// from it that holds a .git, a directory or (for a worktree or a submodule) a
// file — or "" when there is none. It starts no process, so it can run for
// every secret while the pane is built.
func Find(file string) string {
	dir, err := filepath.Abs(filepath.Dir(file))
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// State is how a file stands against its upstream. The order is from least
// to most worth a look.
type State int

const (
	Unknown      State = iota // not checked yet, or the file is not there
	NotTracked                // inside a working copy, but untracked or ignored
	CheckFailed               // git failed: FileState.Err says why
	NoUpstream                // nothing to compare with: FileState.Note says why
	UpToDate                  // the same blob as the upstream's
	NotPushed                 // local commits change it and the upstream lacks them
	NotCommitted              // changed in the working tree or the index
	Outdated                  // the upstream has a newer version
	Diverged                  // changed both here and on the upstream
)

var stateNames = [...]string{"", "not in git", "check failed", "no upstream", "up to date", "not pushed", "not committed", "outdated", "diverged"}

func (s State) String() string {
	if s < 0 || int(s) >= len(stateNames) {
		return ""
	}
	return stateNames[s]
}

// File is one file to check, and the branch whose upstream it is compared
// with: Branch (git.branch in den.yaml), or the branch checked out when
// Branch is empty.
type File struct {
	Path   string
	Branch string
}

// Commit is the summary of one commit.
type Commit struct {
	Short   string
	Subject string
	Author  string
	When    time.Time
}

// FileState is what Check found out about one file.
type FileState struct {
	State State
	Err   string // why the check failed, in one line
	Note  string // why there is no upstream

	File      string // the path checked, as given
	Repo      string // the working copy's top folder, as git names it
	Name      string // the working copy's folder name, for messages
	Path      string // the file in the repository, slash-separated
	Branch    string // the branch checked out; "" when HEAD is detached
	RefBranch string // the branch whose upstream is compared with
	Busy      string // an operation in progress (a merge, a rebase); "" when none

	Upstream    string // the upstream's short name, such as origin/main
	UpstreamRef string // its full name, refs/remotes/origin/main
	Remote      string // what Fetch fetches from; "." for a local branch
	RemoteRef   string // the branch on the remote, refs/heads/main

	Head        string // the commit checked out
	UpstreamOID string // the upstream's commit when checked

	Behind int    // upstream commits that change the file
	Ahead  int    // local commits that change the file
	Last   Commit // the newest upstream commit that changes the file, when Behind > 0

	BranchBehind int // upstream commits of any file: what a fast-forward brings
	BranchAhead  int // local commits of any file: none, or no fast-forward

	FetchedAt time.Time // when the repository last fetched; zero when never
}

// Refusal says why FastForward will not update the file, or "" when it can.
// Only an outdated file is updated, only on the branch it is compared on,
// and only by a fast-forward.
func (s FileState) Refusal() string {
	switch {
	case s.State == Diverged:
		return fmt.Sprintf("it changed both here and on %s; an encrypted file cannot be merged, so resolve it in git", s.Upstream)
	case s.State != Outdated:
		return "it is " + s.State.String()
	case s.Branch == "":
		return fmt.Sprintf("HEAD is detached in %s; check out %s to update", s.Name, s.RefBranch)
	case s.Busy != "":
		return fmt.Sprintf("a %s is in progress in %s; finish it first", s.Busy, s.Name)
	case s.RefBranch != s.Branch:
		return fmt.Sprintf("you are on %s; switch to %s to update", s.Branch, s.RefBranch)
	case s.BranchAhead > 0:
		return fmt.Sprintf("%s has %s that %s lacks, so it cannot fast-forward; pull in a terminal", s.Branch, commits(s.BranchAhead), s.Upstream)
	}
	return ""
}

// Moved is what FastForward did.
type Moved struct {
	Name, Branch string
	From, To     string // short commit ids
	Commits      int
}

func (m Moved) String() string {
	return fmt.Sprintf("%s: %s %s → %s (%s)", m.Name, m.Branch, m.From, m.To, commits(m.Commits))
}

func commits(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return strconv.Itoa(n) + " commits"
}

// Git checks files with the git CLI. The zero value runs git from PATH.
type Git struct {
	Bin string
}

// Repo is Find, so a pane can be handed a fake.
func (Git) Repo(file string) string { return Find(file) }

func (g Git) runner() gitcli.Runner {
	return gitcli.Runner{Bin: g.Bin, Env: []string{
		// A check in the background must not take the index lock that a
		// git command the person runs at the same moment needs.
		"GIT_OPTIONAL_LOCKS=0",
		// File names are names, never patterns.
		"GIT_LITERAL_PATHSPECS=1",
	}}
}

func (g Git) bin() string {
	if g.Bin == "" {
		return "git"
	}
	return g.Bin
}

// repoInfo is what files in one working copy share.
type repoInfo struct {
	top     string
	fetched time.Time
	busy    string
}

// inProgress are the files git keeps while an operation waits on the person,
// and the name of that operation.
var inProgress = []struct{ path, name string }{
	{"MERGE_HEAD", "merge"},
	{"rebase-merge", "rebase"},
	{"rebase-apply", "rebase"},
	{"CHERRY_PICK_HEAD", "cherry-pick"},
	{"REVERT_HEAD", "revert"},
	{"BISECT_LOG", "bisect"},
}

func (g Git) repoInfo(ctx context.Context, repo string) (repoInfo, error) {
	args := []string{"-C", repo, "rev-parse", "--show-toplevel", "--git-path", "FETCH_HEAD"}
	for _, p := range inProgress {
		args = append(args, "--git-path", p.path)
	}
	out, err := g.runner().Run(ctx, args...)
	if err != nil {
		return repoInfo{}, err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 2+len(inProgress) {
		return repoInfo{}, fmt.Errorf("git rev-parse: unexpected output %q", out)
	}
	abs := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(repo, p)
	}
	info := repoInfo{top: lines[0]}
	if fi, err := os.Stat(abs(lines[1])); err == nil {
		info.fetched = fi.ModTime()
	}
	for i, p := range inProgress {
		if _, err := os.Stat(abs(lines[2+i])); err == nil {
			info.busy = p.name
			break
		}
	}
	return info, nil
}

// Check finds out how each of files, all in the working copy repo (as Find
// returned it), stands against its upstream. It is offline, and files that
// share a branch share the work: one status, one ls-tree per commit compared.
func (g Git) Check(ctx context.Context, repo string, files []File) []FileState {
	out := make([]FileState, len(files))
	for i, f := range files {
		out[i] = FileState{File: f.Path}
	}
	fail := func(err error) []FileState {
		for i := range out {
			out[i].State, out[i].Err = CheckFailed, err.Error()
		}
		return out
	}
	if _, err := exec.LookPath(g.bin()); err != nil {
		return fail(errors.New("git not installed — " + prereq.InstallHint("git")))
	}
	info, err := g.repoInfo(ctx, repo)
	if err != nil {
		return fail(err)
	}
	var tracked []int
	for i, f := range files {
		st := &out[i]
		st.Repo, st.Name, st.FetchedAt, st.Busy = info.top, filepath.Base(info.top), info.fetched, info.busy
		if _, err := os.Stat(f.Path); errors.Is(err, fs.ErrNotExist) {
			continue // git has nothing to say of a file that is not there; the probe does
		}
		rel, err := g.trackedPath(ctx, f.Path)
		switch {
		case err != nil:
			st.State, st.Err = CheckFailed, err.Error()
		case rel == "":
			st.State = NotTracked
		default:
			st.Path = rel
			tracked = append(tracked, i)
		}
	}
	if len(tracked) == 0 {
		return out
	}

	paths := make([]string, len(tracked))
	for j, i := range tracked {
		paths[j] = out[i].Path
	}
	head, branch, dirty, err := g.status(ctx, info.top, paths)
	if err != nil {
		for _, i := range tracked {
			out[i].State, out[i].Err = CheckFailed, err.Error()
		}
		return out
	}
	// Files compared with the same branch share the comparison. A branch
	// named by git.branch is a group of its own: it falls back on origin.
	type group struct {
		ref   string
		named bool
	}
	byRef := map[group][]int{}
	var groups []group
	for _, i := range tracked {
		out[i].Head, out[i].Branch = head, branch
		k := group{ref: files[i].Branch, named: files[i].Branch != ""}
		if !k.named {
			k.ref = branch
		}
		out[i].RefBranch = k.ref
		if _, seen := byRef[k]; !seen {
			groups = append(groups, k)
		}
		byRef[k] = append(byRef[k], i)
	}
	for _, k := range groups {
		g.compare(ctx, info.top, k.ref, k.named, byRef[k], out, dirty)
	}
	return out
}

// trackedPath is file's path in its repository, or "" when git does not track it.
func (g Git) trackedPath(ctx context.Context, file string) (string, error) {
	out, err := g.runner().Run(ctx, "-C", filepath.Dir(file), "ls-files", "-z", "--full-name", "--", filepath.Base(file))
	if err != nil {
		return "", err
	}
	rel, _, _ := strings.Cut(string(out), "\x00")
	return rel, nil
}

// status reads the commit and branch checked out, and which of paths differ
// from it in the index or the working tree.
func (g Git) status(ctx context.Context, top string, paths []string) (head, branch string, dirty map[string]bool, err error) {
	args := append([]string{"-C", top, "status", "--porcelain=v2", "--branch", "--no-ahead-behind", "--untracked-files=no", "-z", "--"}, paths...)
	out, err := g.runner().Run(ctx, args...)
	if err != nil {
		return "", "", nil, err
	}
	dirty = map[string]bool{}
	recs := strings.Split(string(out), "\x00")
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		switch {
		case strings.HasPrefix(rec, "# branch.oid "):
			if oid := strings.TrimPrefix(rec, "# branch.oid "); oid != "(initial)" {
				head = oid
			}
		case strings.HasPrefix(rec, "# branch.head "):
			if b := strings.TrimPrefix(rec, "# branch.head "); b != "(detached)" {
				branch = b
			}
		case strings.HasPrefix(rec, "1 "):
			dirty[field(rec, 8)] = true
		case strings.HasPrefix(rec, "2 "):
			dirty[field(rec, 9)] = true
			i++ // the name it had, in a record of its own
		case strings.HasPrefix(rec, "u "):
			dirty[field(rec, 10)] = true
		}
	}
	return head, branch, dirty, nil
}

// field is the n-th space-separated field of rec, and the rest of it: a path
// comes last and may hold spaces.
func field(rec string, n int) string {
	parts := strings.SplitN(rec, " ", n+1)
	if len(parts) <= n {
		return ""
	}
	return parts[n]
}

// compare works out the state of files idx, which are compared with the
// upstream of branch ref; named when git.branch names it.
func (g Git) compare(ctx context.Context, top, ref string, named bool, idx []int, out []FileState, dirty map[string]bool) {
	set := func(s State, note string) {
		for _, i := range idx {
			out[i].State, out[i].Note = s, note
		}
	}
	if ref == "" {
		set(NoUpstream, "HEAD is detached; set git.branch to compare with a branch")
		return
	}
	upRef, remote, remoteRef, err := g.upstreamOf(ctx, top, ref, named)
	if err != nil {
		g.failAll(idx, out, err)
		return
	}
	if upRef == "" {
		set(NoUpstream, ref+" tracks no remote branch")
		return
	}
	short := shortRef(upRef)
	for _, i := range idx {
		out[i].Upstream, out[i].UpstreamRef, out[i].Remote, out[i].RemoteRef = short, upRef, remote, remoteRef
	}
	upOID := g.revParse(ctx, top, upRef+"^{commit}")
	if upOID == "" {
		set(NoUpstream, short+" has not been fetched")
		return
	}
	head := out[idx[0]].Head
	base := ""
	if head != "" {
		base = g.mergeBase(ctx, top, head, upOID)
	}
	branchAhead, branchBehind, err := g.leftRight(ctx, top, head, upOID)
	if err != nil {
		g.failAll(idx, out, err)
		return
	}
	paths := make([]string, len(idx))
	for j, i := range idx {
		paths[j] = out[i].Path
	}
	headBlobs, err1 := g.blobs(ctx, top, head, paths)
	upBlobs, err2 := g.blobs(ctx, top, upOID, paths)
	baseBlobs, err3 := g.blobs(ctx, top, base, paths)
	if err := errors.Join(err1, err2, err3); err != nil {
		g.failAll(idx, out, err)
		return
	}
	for _, i := range idx {
		st := &out[i]
		st.UpstreamOID, st.BranchAhead, st.BranchBehind = upOID, branchAhead, branchBehind
		h, u, b := headBlobs[st.Path], upBlobs[st.Path], baseBlobs[st.Path]
		// Without a merge base both sides count as changed.
		upstreamChanged := h != u && u != b
		localChanged := h != u && h != b
		switch {
		case upstreamChanged && (localChanged || dirty[st.Path]):
			st.State = Diverged
		case upstreamChanged:
			st.State = Outdated
		case dirty[st.Path]:
			st.State = NotCommitted
		case localChanged:
			st.State = NotPushed
		default:
			st.State = UpToDate
		}
		var err error
		if upstreamChanged {
			st.Behind, st.Last, err = g.newer(ctx, top, head, upOID, st.Path)
		}
		if err == nil && localChanged {
			st.Ahead, err = g.count(ctx, top, upOID, head, st.Path)
		}
		if err != nil {
			st.State, st.Err = CheckFailed, err.Error()
		}
	}
}

func (g Git) failAll(idx []int, out []FileState, err error) {
	for _, i := range idx {
		out[i].State, out[i].Err = CheckFailed, err.Error()
	}
}

// upstreamOf is the branch ref is compared with: its tracking branch or,
// for a git.branch with none (not even checked out here), origin's branch of
// that name. upRef is "" when there is none.
func (g Git) upstreamOf(ctx context.Context, top, ref string, named bool) (upRef, remote, remoteRef string, err error) {
	out, err := g.runner().Run(ctx, "-C", top, "for-each-ref", "--format=%(upstream)%00%(upstream:remotename)%00%(upstream:remoteref)", "refs/heads/"+ref)
	if err != nil {
		return "", "", "", err
	}
	line, _, _ := strings.Cut(string(out), "\n")
	parts := strings.Split(line, "\x00")
	switch {
	case len(parts) == 3 && parts[0] != "":
		return parts[0], parts[1], parts[2], nil
	case named:
		return "refs/remotes/origin/" + ref, "origin", "refs/heads/" + ref, nil
	}
	return "", "", "", nil
}

// shortRef is the name git shows for a branch: origin/main, or main.
func shortRef(ref string) string {
	for _, p := range []string{"refs/remotes/", "refs/heads/"} {
		if s, ok := strings.CutPrefix(ref, p); ok {
			return s
		}
	}
	return ref
}

// revParse is the object rev names, or "" when there is none.
func (g Git) revParse(ctx context.Context, top string, rev ...string) string {
	out, err := g.runner().Run(ctx, append([]string{"-C", top, "rev-parse", "--verify", "--quiet"}, rev...)...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// mergeBase is the newest commit a and b share, or "" when they share none.
func (g Git) mergeBase(ctx context.Context, top, a, b string) string {
	out, err := g.runner().Run(ctx, "-C", top, "merge-base", a, b)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// leftRight counts the commits only head has and only up has.
func (g Git) leftRight(ctx context.Context, top, head, up string) (ahead, behind int, err error) {
	if head == "" {
		n, err := g.count(ctx, top, "", up, "")
		return 0, n, err
	}
	out, err := g.runner().Run(ctx, "-C", top, "rev-list", "--left-right", "--count", head+"..."+up)
	if err != nil {
		return 0, 0, err
	}
	if _, err := fmt.Sscan(string(out), &ahead, &behind); err != nil {
		return 0, 0, fmt.Errorf("git rev-list: unexpected output %q", out)
	}
	return ahead, behind, nil
}

// count is the number of commits in from..to that change path ("" for any).
func (g Git) count(ctx context.Context, top, from, to, path string) (int, error) {
	rng := to
	if from != "" {
		rng = from + ".." + to
	}
	args := []string{"-C", top, "rev-list", "--count", rng}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := g.runner().Run(ctx, args...)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// newer counts the upstream commits that change path and returns the newest.
func (g Git) newer(ctx context.Context, top, head, up, path string) (int, Commit, error) {
	rng := up
	if head != "" {
		rng = head + ".." + up
	}
	out, err := g.runner().Run(ctx, "-C", top, "log", "--format=%h%x00%s%x00%an%x00%ct", rng, "--", path)
	if err != nil {
		return 0, Commit{}, err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		// Changed by a merge alone, which log leaves out without -m.
		return 0, Commit{}, nil
	}
	var c Commit
	if p := strings.Split(lines[0], "\x00"); len(p) == 4 {
		c.Short, c.Subject, c.Author = p[0], p[1], p[2]
		if sec, err := strconv.ParseInt(p[3], 10, 64); err == nil {
			c.When = time.Unix(sec, 0)
		}
	}
	return len(lines), c, nil
}

// blobs maps each of paths to its blob in commit; a path the commit lacks
// is left out, and so is everything when commit is "".
func (g Git) blobs(ctx context.Context, top, commit string, paths []string) (map[string]string, error) {
	got := map[string]string{}
	if commit == "" {
		return got, nil
	}
	out, err := g.runner().Run(ctx, append([]string{"-C", top, "ls-tree", "-z", "--full-name", commit, "--"}, paths...)...)
	if err != nil {
		return nil, err
	}
	for rec := range bytes.SplitSeq(out, []byte{0}) {
		// <mode> SP <type> SP <object> TAB <path>
		meta, path, ok := strings.Cut(string(rec), "\t")
		if f := strings.Fields(meta); ok && len(f) == 3 && f[1] == "blob" {
			got[path] = f[2]
		}
	}
	return got, nil
}

// Fetch brings the upstream st is compared with up to date, and only that
// branch: no tags, no submodules. It never asks for a password (gitcli), so a
// key that needs a passphrase fails with git's message instead.
func (g Git) Fetch(ctx context.Context, st FileState) error {
	switch st.Remote {
	case "":
		return errors.New("no upstream to fetch")
	case ".":
		return nil // a local branch is always current
	}
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	refspec := st.RemoteRef
	if strings.HasPrefix(st.UpstreamRef, "refs/remotes/") {
		refspec = "+" + st.RemoteRef + ":" + st.UpstreamRef
	}
	_, err := g.runner().Run(ctx, "-C", st.Repo, "fetch", "--quiet", "--no-tags", "--recurse-submodules=no", "--", st.Remote, refspec)
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("git fetch: no answer from %s in %s", st.Remote, FetchTimeout)
	}
	return err
}

// FastForward moves the branch checked out to the upstream commit st was
// checked against, if st is still how things stand and Refusal has nothing
// against it. It runs git merge --ff-only, never pull, so the person's
// pull.rebase does not apply, and with --no-autostash, so their changes stay
// where they are: git refuses when the update would overwrite one.
func (g Git) FastForward(ctx context.Context, st FileState) (Moved, error) {
	if why := st.Refusal(); why != "" {
		return Moved{}, errors.New(why)
	}
	now := g.Check(ctx, st.Repo, []File{{Path: st.File, Branch: st.RefBranch}})[0]
	switch {
	case now.State == CheckFailed:
		return Moved{}, errors.New(now.Err)
	case now.Head != st.Head || now.Branch != st.Branch || now.UpstreamOID != st.UpstreamOID:
		return Moved{}, fmt.Errorf("%s changed since it was checked; check again", st.Name)
	}
	if why := now.Refusal(); why != "" {
		return Moved{}, errors.New(why)
	}
	cmd := g.runner().Command(ctx, "-C", st.Repo, "merge", "--ff-only", "--no-autostash", "--quiet", st.UpstreamOID)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Moved{}, errors.New("git merge: " + mergeError(stderr.String(), err))
	}
	return Moved{Name: st.Name, Branch: st.Branch, From: abbrev(st.Head), To: abbrev(st.UpstreamOID), Commits: now.BranchBehind}, nil
}

// mergeError is why git merge stopped, in one line. Its last line is only
// "Aborting" when local changes are in the way, so those are named instead.
func mergeError(stderr string, err error) string {
	var files []string
	overwritten := false
	for line := range strings.SplitSeq(stderr, "\n") {
		switch {
		case strings.Contains(line, "would be overwritten"):
			overwritten = true
		case overwritten && strings.HasPrefix(line, "\t"):
			files = append(files, strings.TrimSpace(line))
		}
	}
	if overwritten {
		return "local changes to " + strings.Join(files, ", ") + " would be overwritten; commit or stash them first"
	}
	if msg := gitcli.LastLine(stderr); msg != "" {
		return msg
	}
	return err.Error()
}

func abbrev(oid string) string {
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}
