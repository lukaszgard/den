// Package source checks runbook sources — git repositories of runbook
// folders, named in den.yaml's runbooks.sources — out of git.
//
// Each source has a bare mirror of its repository, fetched only when the user
// asks (den runbook source update or sync), and one checked-out tree per
// commit: a plain copy made by git archive, without .git, that never changes
// once it exists. Moving a source to a new commit makes a new tree beside the
// old one, so a runbook running from the old tree keeps its files, and a scan
// in the middle of an update sees one tree or the other, never half of each.
// Which commit is current is den.lock's to say, not this package's.
package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
)

// Manifest is the file that makes a folder a runbook.
const Manifest = "runbook.yaml"

// The kinds of Change.
const (
	Added   = "added"
	Changed = "changed"
	Removed = "removed"
)

// updateTimeout bounds one update or sync: a fetch that has not finished in
// five minutes is stuck on the network, not slow.
const updateTimeout = 5 * time.Minute

// Store keeps the mirrors and the checked-out trees of runbook sources.
type Store struct {
	root   string
	gitBin string
}

// NewStore keeps sources under root; nil for an empty root.
func NewStore(root string) *Store {
	if root == "" {
		return nil
	}
	return &Store{root: root, gitBin: "git"}
}

// UserStore is the store in the user's data directory
// (~/.local/share/den/sources), or nil when there is no home.
func UserStore() *Store {
	dir := platform.DataDir()
	if dir == "" {
		return nil
	}
	return NewStore(filepath.Join(dir, "sources"))
}

// Root is the folder the store keeps everything in.
func (s *Store) Root() string { return s.root }

// Index is what den knows of one commit without reading its files: the git
// tree of each runbook folder in it. A tree id changes exactly when something
// in the folder does, which is what tells den a runbook needs reviewing again.
type Index struct {
	Root string `json:"root"`
	// Runbooks maps every folder holding a runbook.yaml, by its path in the
	// repository ("" for the root), to its git tree.
	Runbooks map[string]string `json:"runbooks"`
}

// Update is what one update did.
type Update struct {
	Source   string
	Ref      string
	Previous string // the commit before; "" when the source was new here
	Commit   string
	Changes  []Change
}

// Moved reports whether the update checked out another commit.
func (u Update) Moved() bool { return u.Commit != u.Previous }

// Lines describe the update as people read it: where the source moved, then
// one line per runbook folder it added (+), changed (~, with git's stat) or
// removed (-).
func (u Update) Lines() []string {
	ref := u.Ref
	if ref == "" {
		ref = "default branch"
	}
	var out []string
	switch {
	case u.Previous == "":
		out = append(out, fmt.Sprintf("%s  checked out %s  (%s)", u.Source, Short(u.Commit), ref))
	case !u.Moved():
		out = append(out, fmt.Sprintf("%s  up to date at %s  (%s)", u.Source, Short(u.Commit), ref))
	default:
		out = append(out, fmt.Sprintf("%s  %s → %s  (%s)", u.Source, Short(u.Previous), Short(u.Commit), ref))
	}
	for _, c := range u.Changes {
		folder := c.Folder
		if folder == "" {
			folder = u.Source
		}
		mark, detail := "~", c.Stat
		switch c.Kind {
		case Added:
			mark, detail = "+", "new"
		case Removed:
			mark, detail = "-", "removed"
		}
		out = append(out, strings.TrimRight(fmt.Sprintf("  %s %-24s %s", mark, folder, detail), " "))
	}
	return out
}

// AsksAgain reports whether the update added or changed a runbook, which
// then asks before its next run.
func (u Update) AsksAgain() bool {
	for _, c := range u.Changes {
		if c.Kind != Removed {
			return true
		}
	}
	return false
}

// Change is a runbook folder an update added, changed or removed.
type Change struct {
	Folder string // its path below the source's path; "" for a runbook at it
	Kind   string // Added, Changed or Removed
	Stat   string // git's --shortstat for a changed one
}

// fullCommit is a whole commit id, SHA-1 or SHA-256: what den.lock may hold
// and what den asks git about.
var fullCommit = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// ValidCommit reports whether c is a whole commit id. den.lock is shared, so
// a commit read from it is checked before it names a folder or reaches git,
// where "--output=…" would be an option.
func ValidCommit(c string) bool { return fullCommit.MatchString(c) }

// Short is a commit id as people read it.
func Short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

// dir is the folder of src: its name and a hash of its repository, so that
// pointing a source at another repository never reuses the first one's.
func (s *Store) dir(src config.RunbookSource) string {
	id := src.URL
	if repo, err := config.ParseRepo(src.URL); err == nil {
		id = repo.ID
	}
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(s.root, src.Name+"-"+hex.EncodeToString(sum[:4]))
}

func (s *Store) mirror(src config.RunbookSource) string { return filepath.Join(s.dir(src), "repo.git") }

func (s *Store) trees(src config.RunbookSource) string { return filepath.Join(s.dir(src), "trees") }

// TreeDir is where the tree of commit is, or would be, checked out.
func (s *Store) TreeDir(src config.RunbookSource, commit string) string {
	return filepath.Join(s.trees(src), commit)
}

// Tree is the checked-out tree of commit, or "" when it is not checked out.
func (s *Store) Tree(src config.RunbookSource, commit string) string {
	if !ValidCommit(commit) {
		return ""
	}
	dir := s.TreeDir(src, commit)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	return dir
}

// Index is the index of a checked-out commit.
func (s *Store) Index(src config.RunbookSource, commit string) (Index, bool) {
	if !ValidCommit(commit) {
		return Index{}, false
	}
	data, err := os.ReadFile(s.TreeDir(src, commit) + ".json")
	if err != nil {
		return Index{}, false
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return Index{}, false
	}
	return idx, true
}

// SaveIndex records the index of commit beside its tree.
func (s *Store) SaveIndex(src config.RunbookSource, commit string, idx Index) error {
	if err := os.MkdirAll(s.trees(src), 0o750); err != nil {
		return err
	}
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	return writeAtomic(s.TreeDir(src, commit)+".json", data)
}

// Update fetches src and checks out the newest commit of its ref. previous
// is the commit checked out before, from den.lock: the changes are counted
// from it, and its tree is kept for a run still in flight. Older trees go.
func (s *Store) Update(ctx context.Context, src config.RunbookSource, previous string) (Update, error) {
	if !ValidCommit(previous) {
		previous = ""
	}
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	if err := s.fetch(ctx, src); err != nil {
		return Update{}, err
	}
	commit, err := s.resolve(ctx, src)
	if err != nil {
		return Update{}, err
	}
	if err := s.checkout(ctx, src, commit); err != nil {
		return Update{}, err
	}
	u := Update{Source: src.Name, Ref: src.Ref, Previous: previous, Commit: commit}
	if u.Moved() {
		u.Changes = s.changes(ctx, src, previous, commit)
	}
	s.prune(src, commit, previous)
	return u, nil
}

// Sync checks out commit, fetching only when the mirror lacks it: what a
// den.lock committed by someone else asks for.
func (s *Store) Sync(ctx context.Context, src config.RunbookSource, commit string) error {
	if !ValidCommit(commit) {
		return fmt.Errorf("source %s: %q is not a commit id", src.Name, commit)
	}
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	if s.Tree(src, commit) != "" {
		return nil
	}
	if !s.has(ctx, src, commit) {
		if err := s.fetch(ctx, src); err != nil {
			return err
		}
		if !s.has(ctx, src, commit) {
			return fmt.Errorf("source %s: commit %s is no longer in the repository: den runbook source update %s", src.Name, Short(commit), src.Name)
		}
	}
	return s.checkout(ctx, src, commit)
}

// Remove forgets src: its mirror and every tree.
func (s *Store) Remove(src config.RunbookSource) error {
	return os.RemoveAll(s.dir(src))
}

func (s *Store) fetch(ctx context.Context, src config.RunbookSource) error {
	repo, err := config.ParseRepo(src.URL)
	if err != nil {
		return fmt.Errorf("source %s: %w", src.Name, err)
	}
	mirror := s.mirror(src)
	if _, err := os.Stat(filepath.Join(mirror, "HEAD")); err != nil {
		if err := os.MkdirAll(mirror, 0o750); err != nil {
			return err
		}
		if _, err := s.git(ctx, src.Name, "-C", mirror, "init", "--quiet", "--bare"); err != nil {
			return err
		}
	}
	// Every branch and tag, so that any ref den.yaml names resolves without
	// another trip, and the remote's HEAD for a source without a ref.
	_, err = s.git(ctx, src.Name, "-C", mirror, "fetch", "--quiet", "--prune", "--no-tags", "--force", "--", repo.Clone,
		"+HEAD:refs/den/head", "+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*")
	return err
}

var commitID = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// resolve finds the commit src's ref names: a tag first, then a branch, then
// a commit id.
func (s *Store) resolve(ctx context.Context, src config.RunbookSource) (string, error) {
	candidates := []string{"refs/den/head"}
	if src.Ref != "" {
		candidates = []string{"refs/tags/" + src.Ref, "refs/heads/" + src.Ref}
		if commitID.MatchString(src.Ref) {
			candidates = append(candidates, src.Ref)
		}
	}
	for _, c := range candidates {
		if out, err := s.git(ctx, src.Name, "-C", s.mirror(src), "rev-parse", "--verify", "--quiet", c+"^{commit}"); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}
	id := src.URL
	if repo, err := config.ParseRepo(src.URL); err == nil {
		id = repo.ID
	}
	return "", fmt.Errorf("source %s: ref %q is not a tag, branch or commit of %s", src.Name, src.Ref, id)
}

func (s *Store) has(ctx context.Context, src config.RunbookSource, commit string) bool {
	_, err := s.git(ctx, src.Name, "-C", s.mirror(src), "cat-file", "-e", commit+"^{commit}")
	return err == nil
}

// checkout makes the tree of commit, unless it exists: extracted into a
// temporary folder and renamed into place, its index written first, so a tree
// that exists is complete.
func (s *Store) checkout(ctx context.Context, src config.RunbookSource, commit string) error {
	if s.Tree(src, commit) != "" {
		if _, ok := s.Index(src, commit); ok {
			return nil
		}
	}
	if err := os.MkdirAll(s.trees(src), 0o750); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(s.trees(src), ".tmp-"+Short(commit)+"-")
	if err != nil {
		return err
	}
	// Gone after the rename; this clears what a failure left.
	defer func() { _ = os.RemoveAll(tmp) }()
	extractInto := func(r io.Reader) error { return extract(r, tmp) }
	if err := s.gitStream(ctx, src.Name, extractInto, "-C", s.mirror(src), "archive", "--format=tar", commit); err != nil {
		return err
	}
	idx, err := s.index(ctx, src, commit)
	if err != nil {
		return err
	}
	if err := s.SaveIndex(src, commit, idx); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.TreeDir(src, commit)); err != nil {
		if s.Tree(src, commit) != "" {
			return nil // another den checked it out meanwhile
		}
		return err
	}
	return nil
}

// index asks git for the tree of every runbook folder in commit.
func (s *Store) index(ctx context.Context, src config.RunbookSource, commit string) (Index, error) {
	mirror := s.mirror(src)
	root, err := s.git(ctx, src.Name, "-C", mirror, "rev-parse", commit+"^{tree}")
	if err != nil {
		return Index{}, err
	}
	out, err := s.git(ctx, src.Name, "-C", mirror, "ls-tree", "-r", "-t", "-z", "--full-tree", commit)
	if err != nil {
		return Index{}, err
	}
	trees := map[string]string{"": strings.TrimSpace(string(root))}
	var folders []string
	for rec := range strings.SplitSeq(string(out), "\x00") {
		meta, name, ok := strings.Cut(rec, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			continue
		}
		switch fields[1] {
		case "tree":
			trees[name] = fields[2]
		case "blob":
			if path.Base(name) == Manifest {
				folders = append(folders, folderOf(name))
			}
		}
	}
	idx := Index{Root: trees[""], Runbooks: make(map[string]string, len(folders))}
	for _, f := range folders {
		idx.Runbooks[f] = trees[f]
	}
	return idx, nil
}

// folderOf is the folder a repository path is in, "" for the root.
func folderOf(name string) string {
	if dir := path.Dir(name); dir != "." {
		return dir
	}
	return ""
}

// RepoPath is a source's path as the repository writes it: slashes, cleaned,
// "" for the root.
func RepoPath(p string) string {
	p = path.Clean(filepath.ToSlash(p))
	if p == "." || p == "/" {
		return ""
	}
	return strings.Trim(p, "/")
}

// Folders are the runbook folders of idx that src lists: those directly in
// its path, and the path itself when it holds a runbook.yaml. Keys are the
// folders' paths below src's path ("" for the path itself); values their
// trees.
func Folders(src config.RunbookSource, idx Index) map[string]string {
	base := RepoPath(src.Path)
	out := map[string]string{}
	for folder, tree := range idx.Runbooks {
		switch {
		case folder == base:
			out[""] = tree
		case folderOf(folder) == base:
			out[path.Base(folder)] = tree
		}
	}
	return out
}

// changes lists the runbook folders of src that differ between two commits.
// An old commit den cannot read any more (a force-push removed it) counts as
// empty: everything is new.
func (s *Store) changes(ctx context.Context, src config.RunbookSource, previous, commit string) []Change {
	newIdx, _ := s.Index(src, commit)
	var oldIdx Index
	if previous != "" {
		if idx, ok := s.Index(src, previous); ok {
			oldIdx = idx
		} else if idx, err := s.index(ctx, src, previous); err == nil {
			oldIdx = idx
		}
	}
	before, after := Folders(src, oldIdx), Folders(src, newIdx)
	names := make([]string, 0, len(before)+len(after))
	for n := range before {
		names = append(names, n)
	}
	for n := range after {
		if _, dup := before[n]; !dup {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	var out []Change
	for _, n := range names {
		was, inOld := before[n]
		is, inNew := after[n]
		switch {
		case !inOld:
			out = append(out, Change{Folder: n, Kind: Added})
		case !inNew:
			out = append(out, Change{Folder: n, Kind: Removed})
		case was != is:
			out = append(out, Change{Folder: n, Kind: Changed, Stat: s.stat(ctx, src, previous, commit, n)})
		}
	}
	return out
}

// stat is git's one-line summary of how a folder changed.
func (s *Store) stat(ctx context.Context, src config.RunbookSource, previous, commit, folder string) string {
	args := []string{"-C", s.mirror(src), "diff", "--shortstat", previous, commit}
	if p := path.Join(RepoPath(src.Path), folder); p != "" && p != "." {
		args = append(args, "--", p)
	}
	out, err := s.git(ctx, src.Name, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// prune removes the trees of every commit but keep, and temporary folders a
// crash left behind an hour ago or more.
func (s *Store) prune(src config.RunbookSource, keep ...string) {
	entries, err := os.ReadDir(s.trees(src))
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		full := filepath.Join(s.trees(src), name)
		if strings.HasPrefix(name, ".tmp-") {
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
				_ = os.RemoveAll(full)
			}
			continue
		}
		if slices.Contains(keep, strings.TrimSuffix(name, ".json")) {
			continue
		}
		_ = os.RemoveAll(full)
	}
}
