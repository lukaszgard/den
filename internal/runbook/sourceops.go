package runbook

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook/source"
)

// SourceState is one runbook source as the CLI and the TUI show it.
type SourceState struct {
	Spec       config.RunbookSource
	Repo       string         // host/owner/repo
	Locked     *source.Locked // its den.lock entry; nil before its first update
	CheckedOut bool           // its tree is on this machine
	Problem    string         // what is wrong or off, with the command that fixes it
	Folders    []SourceFolder // every runbook folder in its tree, shown or not
}

// SourceFolder is one runbook folder of a source.
type SourceFolder struct {
	Folder      string // what include and exclude call it
	Name        string // the name it is listed under
	Description string
	Selected    bool // include and exclude let it show
	Listed      bool // and it loaded: den run knows it
}

var errNoStore = errors.New("den keeps no runbook sources on this machine: it has no home directory")

func (r *Registry) store() *source.Store {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sources
}

// SourceStore is where sources are checked out; nil when den reads none.
func (r *Registry) SourceStore() *source.Store { return r.store() }

func (r *Registry) lockPath() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return source.LockPath(r.cfgPath)
}

func (r *Registry) source(name string) (config.RunbookSource, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.cfg.Sources {
		if s.Name == name {
			return s, nil
		}
	}
	return config.RunbookSource{}, fmt.Errorf("no runbook source named %q (den runbook source list)", name)
}

// Sources describes every source den.yaml names.
func (r *Registry) Sources() []SourceState {
	r.mu.RLock()
	cfg, store, cfgPath := r.cfg, r.sources, r.cfgPath
	r.mu.RUnlock()
	lock, lockErr := source.ReadLock(source.LockPath(cfgPath))
	out := make([]SourceState, 0, len(cfg.Sources))
	for _, src := range cfg.Sources {
		st := SourceState{Spec: src}
		switch {
		case store == nil:
			st.Problem = errNoStore.Error()
		case lockErr != nil:
			st.Problem = "den.lock: " + lockErr.Error()
		default:
			c := checkoutOf(src, store, lock)
			st.Repo, st.Locked, st.CheckedOut, st.Problem = c.repo.ID, c.locked, c.usable, c.problem
			if c.usable {
				st.Folders = r.sourceFolders(src, c)
			}
		}
		out = append(out, st)
	}
	return out
}

func (r *Registry) sourceFolders(src config.RunbookSource, c checkout) []SourceFolder {
	base := filepath.Join(c.tree, filepath.FromSlash(source.RepoPath(src.Path)))
	folders := source.Folders(src, c.index)
	var out []SourceFolder
	for _, rel := range slices.Sorted(maps.Keys(folders)) {
		if strings.HasPrefix(rel, ".") {
			continue
		}
		label := folderLabel(src, rel)
		f := SourceFolder{Folder: label, Name: qualifiedName(src, rel, label, false), Selected: Selected(src, label)}
		if spec, named, err := loadManifestNamed(filepath.Join(base, filepath.FromSlash(rel))); err == nil {
			f.Name = qualifiedName(src, rel, spec.Name, named)
			f.Description = CleanText(spec.Description)
		}
		if run, ok := r.Lookup(f.Name); ok {
			rb := run.Runbook()
			f.Listed = rb.Remote != nil && rb.Remote.Source == src.Name
		}
		out = append(out, f)
	}
	return out
}

// editConfig changes den.yaml through change and reloads what it says.
func (r *Registry) editConfig(change func(*config.ConfigEdit) error) error {
	r.mu.RLock()
	path := r.cfgPath
	r.mu.RUnlock()
	if path == "" {
		return errors.New("den has no den.yaml to change")
	}
	e, err := config.EditConfig(path)
	if err != nil {
		return err
	}
	if err := change(e); err != nil {
		return err
	}
	cfg, err := e.Save()
	if err != nil {
		return err
	}
	r.Reload(cfg, path)
	return nil
}

// AddSource writes src into den.yaml (named after its URL when it has no
// name), and with fetch checks it out at once. A source that cannot be
// fetched stays in den.yaml, to be updated later; the error says so.
func (r *Registry) AddSource(ctx context.Context, src config.RunbookSource, fetch bool) (source.Update, error) {
	if src.Name == "" {
		src.Name = config.SourceNameFromURL(src.URL)
	}
	if _, err := r.source(src.Name); err == nil {
		return source.Update{}, fmt.Errorf("a source named %q exists already", src.Name)
	}
	repo, err := config.ParseRepo(src.URL)
	if err != nil {
		return source.Update{}, err
	}
	r.mu.RLock()
	allowed := r.cfg.AllowSources
	r.mu.RUnlock()
	if !repo.Allowed(allowed) {
		return source.Update{}, fmt.Errorf("%s is not in runbooks.allow_sources (%s)", repo.ID, strings.Join(allowed, ", "))
	}
	if err := r.editConfig(func(e *config.ConfigEdit) error { return e.AddSource(src) }); err != nil {
		return source.Update{}, err
	}
	if !fetch {
		return source.Update{Source: src.Name, Ref: src.Ref}, nil
	}
	u, err := r.UpdateSource(ctx, src.Name)
	if err != nil {
		return u, fmt.Errorf("%w — %s stays in den.yaml; try again with den runbook source update %s", err, src.Name, src.Name)
	}
	return u, nil
}

// UpdateSource fetches the source called name, checks out the newest
// commit of its ref and pins it in den.lock.
func (r *Registry) UpdateSource(ctx context.Context, name string) (source.Update, error) {
	src, err := r.source(name)
	if err != nil {
		return source.Update{}, err
	}
	store, lockPath := r.store(), r.lockPath()
	if store == nil {
		return source.Update{}, errNoStore
	}
	if lockPath == "" {
		return source.Update{}, errors.New("den has no den.yaml to keep den.lock beside")
	}
	lock, err := source.ReadLock(lockPath)
	if err != nil {
		return source.Update{}, err
	}
	previous := ""
	if l, ok := lock.Sources[name]; ok && sameRepo(l.URL, src.URL) {
		previous = l.Commit
	}
	u, err := store.Update(ctx, src, previous)
	if err != nil {
		return u, err
	}
	if err := pin(lockPath, src, u.Commit); err != nil {
		return u, err
	}
	r.Rescan()
	return u, nil
}

// pin records in den.lock that src is at commit. The lock is read again
// first: a fetch takes long enough for another den to pin another source.
// An entry that did not change is left alone, so den.lock only changes in
// git when a source moved.
func pin(lockPath string, src config.RunbookSource, commit string) error {
	lock, err := source.ReadLock(lockPath)
	if err != nil {
		return err
	}
	if l, ok := lock.Sources[src.Name]; ok && l.URL == src.URL && l.Ref == src.Ref && l.Commit == commit {
		return nil
	}
	lock.Sources[src.Name] = source.Locked{URL: src.URL, Ref: src.Ref, Commit: commit, Updated: time.Now().UTC()}
	return source.WriteLock(lockPath, lock)
}

func sameRepo(a, b string) bool {
	ra, errA := config.ParseRepo(a)
	rb, errB := config.ParseRepo(b)
	return errA == nil && errB == nil && ra.ID == rb.ID
}

// SyncSources checks out what den.lock pins, fetching only what is missing;
// a source den.lock does not know yet is updated.
func (r *Registry) SyncSources(ctx context.Context) error {
	store, lockPath := r.store(), r.lockPath()
	if store == nil {
		return errNoStore
	}
	lock, err := source.ReadLock(lockPath)
	if err != nil {
		return err
	}
	r.mu.RLock()
	sources := slices.Clone(r.cfg.Sources)
	r.mu.RUnlock()
	var errs []error
	for _, src := range sources {
		if l, ok := lock.Sources[src.Name]; ok && sameRepo(l.URL, src.URL) {
			errs = append(errs, store.Sync(ctx, src, l.Commit))
			continue
		}
		_, err := r.UpdateSource(ctx, src.Name)
		errs = append(errs, err)
	}
	r.Rescan()
	return errors.Join(errs...)
}

// RemoveSource takes the source called name out of den.yaml and den.lock and
// deletes its checkout, unless one of its runbooks is running. What den kept
// for each of its runbooks goes too, as Delete does for one.
func (r *Registry) RemoveSource(name string) error {
	src, err := r.source(name)
	if err != nil {
		return err
	}
	for _, run := range r.Runbooks() {
		if rb := run.Runbook(); rb.Remote != nil && rb.Remote.Source == name && run.IsRunning() {
			return fmt.Errorf("%s is running: remove its source once it has finished", rb.Name)
		}
	}
	// Every runbook of its tree, installed or not: one uninstalled earlier
	// may have run before.
	var names []string
	for _, st := range r.Sources() {
		if st.Spec.Name == name {
			for _, f := range st.Folders {
				names = append(names, f.Name)
			}
		}
	}
	if err := r.editConfig(func(e *config.ConfigEdit) error { return e.RemoveSource(name) }); err != nil {
		return err
	}
	var errs []error
	for _, n := range names {
		errs = append(errs, r.forget(n))
	}
	if lockPath := r.lockPath(); lockPath != "" {
		lock, err := source.ReadLock(lockPath)
		if _, ok := lock.Sources[name]; err == nil && ok {
			delete(lock.Sources, name)
			if len(lock.Sources) == 0 {
				errs = append(errs, os.Remove(lockPath))
			} else {
				errs = append(errs, source.WriteLock(lockPath, lock))
			}
		}
	}
	if store := r.store(); store != nil {
		errs = append(errs, store.Remove(src))
	}
	r.Rescan()
	return errors.Join(errs...)
}

// SelectSourceRunbook installs (on) or uninstalls one runbook of a source by
// editing its include and exclude in den.yaml. which is its folder, its name,
// or its name without the source's prefix.
func (r *Registry) SelectSourceRunbook(sourceName, which string, on bool) error {
	src, err := r.source(sourceName)
	if err != nil {
		return err
	}
	folder := ""
	for _, st := range r.Sources() {
		if st.Spec.Name != sourceName {
			continue
		}
		if !st.CheckedOut {
			return fmt.Errorf("source %s: %s", sourceName, st.Problem)
		}
		for _, f := range st.Folders {
			if f.Folder == which || f.Name == which || f.Name == sourceName+"/"+which {
				folder = f.Folder
			}
		}
	}
	if folder == "" {
		return fmt.Errorf("no runbook %q in source %s (den runbook browse %s)", which, sourceName, sourceName)
	}
	include, exclude, err := SelectFolder(src, folder, on)
	if err != nil {
		return err
	}
	if slices.Equal(include, src.Include) && slices.Equal(exclude, src.Exclude) {
		return nil
	}
	return r.editConfig(func(e *config.ConfigEdit) error {
		if !slices.Equal(include, src.Include) {
			if err := e.SetSourceList(sourceName, "include", include); err != nil {
				return err
			}
		}
		if !slices.Equal(exclude, src.Exclude) {
			return e.SetSourceList(sourceName, "exclude", exclude)
		}
		return nil
	})
}

var envLine = regexp.MustCompile(`(?m)^env:.*$`)

// ForkDir is the folder Fork copies the source runbook called name into, to
// say where it goes before it is copied; "" when it cannot be.
func (r *Registry) ForkDir(name string) string {
	run, ok := r.Lookup(name)
	root := r.firstDir()
	if !ok || run.Runbook().Remote == nil || root == "" {
		return ""
	}
	return filepath.Join(root, forkFolder(run.Runbook().Remote))
}

// forkFolder is what the copy of a source runbook is called: its folder in
// the source, or the source's name for one at its root.
func forkFolder(rm *Remote) string {
	if rm.Folder != "" {
		return rm.Folder
	}
	return rm.Source
}

// Fork copies the folder of the source runbook called name into root, or
// the first of runbooks.dirs, as a runbook of your own that updates no longer
// touch, and returns the name it is listed under. Its env: is rewritten to
// the environment env_map gave it, since your environments are what it is
// read against from now on.
func (r *Registry) Fork(name, root string) (string, error) {
	run, ok := r.Lookup(name)
	if !ok {
		return "", fmt.Errorf("no runbook named %q (den run --list)", name)
	}
	rb := run.Runbook()
	if rb.Remote == nil {
		return "", fmt.Errorf("%s is yours already: edit it where it is", name)
	}
	if root == "" {
		if root = r.firstDir(); root == "" {
			return "", errors.New("den.yaml has no runbooks.dirs to copy it into")
		}
	}
	folder := forkFolder(rb.Remote)
	dest := filepath.Join(root, folder)
	if _, err := os.Lstat(dest); err == nil {
		return "", fmt.Errorf("%s exists already", dest)
	}
	spec, named, err := loadManifestNamed(rb.Dir)
	if err != nil {
		return "", err
	}
	local := folder
	if named {
		local = spec.Name
	}
	if _, taken := r.Lookup(local); taken {
		return "", fmt.Errorf("a runbook named %q exists already", local)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", err
	}
	if err := copyTemplate(rb.Dir, dest); err != nil {
		return "", err
	}
	if rb.Env != "" {
		manifest := filepath.Join(dest, ManifestName)
		data, err := os.ReadFile(manifest)
		if err != nil {
			return "", err
		}
		data = envLine.ReplaceAll(data, []byte("env: "+rb.Env))
		if err := os.WriteFile(manifest, data, 0o600); err != nil {
			return "", err
		}
	}
	r.Rescan()
	return local, nil
}
