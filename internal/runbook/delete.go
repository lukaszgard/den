package runbook

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
)

// DeleteKind is what deleting a runbook removes, which follows from where it
// is defined.
type DeleteKind string

const (
	DeleteFolder   DeleteKind = "folder"   // a folder with a runbook.yaml in runbooks.dirs
	DeleteScript   DeleteKind = "script"   // a script with a # den: header
	DeleteItem     DeleteKind = "den.yaml" // an entry of runbooks.items
	DeleteSequence DeleteKind = "sequence" // an entry of runbooks.sequences
	DeleteSource   DeleteKind = "source"   // a runbook of runbooks.sources
)

// Deletion is everything deleting one runbook or sequence does, worked out
// before anything goes, so that the user sees it first. Nothing deleted can
// be brought back by den.
type Deletion struct {
	Name string
	Kind DeleteKind

	// Folder is the runbook's folder, deleted with all it holds.
	Folder string
	Files  int   // files in Folder
	Bytes  int64 // and their size
	// OwnRepo is set when Folder holds a git repository of its own.
	OwnRepo bool
	// Script is the header script that is the runbook.
	Script string
	// Repo is the git work tree Folder or Script sits in: whatever was
	// committed there can be checked out again. "" when there is none.
	Repo string
	// Source and SourceFolder say which runbook of which source a runbook
	// from one is; it is uninstalled, as its checkout is shared.
	Source       string
	SourceFolder string

	Runs       int    // runs kept, wherever it keeps them
	History    string // den's folder of its runs in the state directory, if any
	State      string // its DEN_RUNBOOK_STATE folder, if it has one yet
	StateFiles int
	StateBytes int64
	Trusted    bool // the user has run it as a source runbook: den remembers the review

	UsedBy   []string // sequences with a step of its name
	Shadowed []string // the runbooks of the same name den skipped: the first takes its place
	Template bool     // Folder is runbooks.template
	Steps    []string // a sequence's steps, which stay
	Cwd      string   // where it runs, when that stays
	Entry    string   // the script it runs, when that stays
}

// PlanDelete works out what deleting the runbook or sequence called name
// would do, without doing any of it. It refuses one that is running, here or
// in another den, and a runbook a running sequence is going through.
func (r *Registry) PlanDelete(name string) (Deletion, error) {
	d := Deletion{Name: name}
	r.mu.RLock()
	cfg, base, cfgPath := r.cfg, configDir(r.cfgPath), r.cfgPath
	h, trust, stateRoot, warnings := r.history, r.trust, r.stateRoot, r.warnings
	r.mu.RUnlock()

	logs := ""
	if sr, ok := r.LookupSequence(name); ok {
		if sr.IsRunning() {
			return d, fmt.Errorf("%s is running: delete it once it has finished", name)
		}
		d.Kind, d.Steps = DeleteSequence, sr.Sequence().Steps
	} else if run, ok := r.Lookup(name); ok {
		if run.IsRunning() {
			return d, fmt.Errorf("%s is running: delete it once it has finished", name)
		}
		for _, sr := range r.Sequences() {
			if sr.IsRunning() && slices.Contains(sr.Sequence().Steps, name) {
				return d, fmt.Errorf("sequence %s is running, and %s is one of its steps: delete it once it has finished", sr.Sequence().Name, name)
			}
		}
		rb := run.Runbook()
		logs = rb.LogsDir()
		switch {
		case rb.Remote != nil:
			d.Kind, d.Source, d.SourceFolder = DeleteSource, rb.Remote.Source, rb.Remote.Folder
		case rb.Dir != "":
			d.Kind = DeleteFolder
			if err := r.planFolder(&d, rb, cfg.Template, base, cfgPath); err != nil {
				return d, err
			}
		case rb.Source != "":
			d.Kind = DeleteScript
			if err := r.planScript(&d, rb); err != nil {
				return d, err
			}
		default:
			d.Kind, d.Cwd = DeleteItem, rb.Cwd
		}
		for _, seq := range cfg.Sequences {
			if slices.Contains(seq.Steps, name) {
				d.UsedBy = append(d.UsedBy, seq.Name)
			}
		}
		for _, w := range warnings {
			if w.shadows == name {
				d.Shadowed = append(d.Shadowed, w.Path)
			}
		}
	} else if i := slices.IndexFunc(cfg.Sequences, func(s config.SequenceSpec) bool { return s.Name == name }); i >= 0 {
		// One den skipped, a step of it gone: still den.yaml's to delete.
		d.Kind, d.Steps = DeleteSequence, cfg.Sequences[i].Steps
	} else if slices.ContainsFunc(cfg.Items, func(s config.RunbookSpec) bool { return s.Name == name }) {
		d.Kind = DeleteItem
	} else {
		return d, fmt.Errorf("no runbook or sequence named %q", name)
	}

	// Runs are read from where it keeps them now, and from the state
	// directory, where a runbook kept them before it had a folder.
	places := []string{logs}
	if logs != "" {
		places = append(places, "")
	}
	for _, logs := range places {
		runs, _ := h.List(name, logs)
		for _, e := range runs {
			if e.Running() {
				return d, fmt.Errorf("%s is running in another den (pid %d): delete it once it has finished", name, e.PID)
			}
		}
		d.Runs += len(runs)
	}
	if h != nil && exists(h.dir(name, "")) {
		d.History = h.dir(name, "")
	}
	if stateRoot != "" {
		if dir := filepath.Join(stateRoot, folderName(name)); exists(dir) {
			d.State = dir
			d.StateFiles, d.StateBytes = usage(dir)
		}
	}
	d.Trusted = trust.Has(name)
	return d, nil
}

// planFolder checks that the folder of rb is one den may delete: an entry of
// a runbooks directory, as a scan found it, that holds neither den.yaml nor
// a runbooks directory.
func (r *Registry) planFolder(d *Deletion, rb Runbook, template, base, cfgPath string) error {
	loc := filepath.Join(canonical(filepath.Dir(rb.Dir)), filepath.Base(rb.Dir))
	dirs := r.runbooksDirs()
	if !directlyIn(loc, dirs) {
		return fmt.Errorf("%s is not directly in a runbooks directory (%s): delete it by hand", loc, strings.Join(dirs, ", "))
	}
	info, err := os.Lstat(loc)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		// A scan lists no link, so it became one since: whatever it points
		// at is not what the user was shown.
		return fmt.Errorf("%s is no longer a folder: delete it by hand", loc)
	}
	// Shown and deleted as den lists it: the same folder, without the
	// resolved links of its parents (/tmp is /private/tmp on macOS).
	d.Folder = rb.Dir
	d.Repo = gitWorkTree(filepath.Dir(rb.Dir))
	for _, dir := range dirs {
		if within(loc, dir) {
			return fmt.Errorf("%s holds the runbooks directory %s: delete it by hand", loc, dir)
		}
	}
	if cfgPath != "" && within(loc, canonical(cfgPath)) {
		return fmt.Errorf("%s holds den.yaml: delete it by hand", loc)
	}
	d.Files, d.Bytes = usage(loc)
	d.OwnRepo = exists(filepath.Join(loc, ".git"))
	d.Template = template != "" && canonical(resolvePath(template, base)) == loc
	if rb.Cwd != "" && !within(loc, canonical(rb.Cwd)) {
		d.Cwd = rb.Cwd
	}
	if s := entryScript(rb); s != "" && !within(loc, canonical(s)) {
		d.Entry = s
	}
	return nil
}

// planScript checks that a header script is a file of a runbooks directory.
func (r *Registry) planScript(d *Deletion, rb Runbook) error {
	loc := filepath.Join(canonical(filepath.Dir(rb.Source)), filepath.Base(rb.Source))
	if dirs := r.runbooksDirs(); !directlyIn(loc, dirs) {
		return fmt.Errorf("%s is not directly in a runbooks directory (%s): delete it by hand", loc, strings.Join(dirs, ", "))
	}
	d.Script = rb.Source
	d.Repo = gitWorkTree(filepath.Dir(rb.Source))
	return nil
}

// Delete deletes the runbook or sequence called name for good, as PlanDelete
// describes it, and forgets what den kept under its name: its runs, its
// DEN_RUNBOOK_STATE folder, the review of a source runbook. A runbook given
// the name later starts with none of it.
//
// The definition goes first: den.yaml is validated before it is written, and
// a folder is renamed out of the way before it is deleted, so a failure there
// leaves everything as it was. What fails after that is reported, with the
// runbook gone all the same.
func (r *Registry) Delete(name string) (Deletion, error) {
	d, err := r.PlanDelete(name)
	if err != nil {
		return d, err
	}
	unretire, err := r.retire(d)
	if err != nil {
		return d, err
	}
	var gone bool
	var leftover error
	switch d.Kind {
	case DeleteItem:
		err = r.editConfig(func(e *config.ConfigEdit) error { return e.RemoveRunbook(name) })
	case DeleteSequence:
		err = r.editConfig(func(e *config.ConfigEdit) error { return e.RemoveSequence(name) })
	case DeleteSource:
		err = r.SelectSourceRunbook(d.Source, d.SourceFolder, false)
	case DeleteScript:
		err = os.Remove(d.Script)
	case DeleteFolder:
		if gone, err = removeFolder(d); gone {
			leftover, err = err, nil
		}
	}
	if err != nil {
		unretire()
		return d, err
	}
	r.drop(d)
	forgot := r.forget(name)
	r.Rescan()
	if err := errors.Join(leftover, forgot); err != nil {
		return d, fmt.Errorf("%s is deleted, but not all of it: %w", name, err)
	}
	return d, nil
}

// retire claims the runner of d, so nothing starts it while it goes, and
// returns what gives it back. A definition den skipped has no runner.
func (r *Registry) retire(d Deletion) (func(), error) {
	if d.Kind == DeleteSequence {
		if sr, ok := r.LookupSequence(d.Name); ok {
			return sr.unretire, sr.retire()
		}
	} else if run, ok := r.Lookup(d.Name); ok {
		return run.unretire, run.retire()
	}
	return func() {}, nil
}

// drop takes the retired runner of d out of the registry. A rescan would keep
// it for a runbook of the same name den skipped until now, which then could
// never run.
func (r *Registry) drop(d Deletion) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d.Kind == DeleteSequence {
		if sr, ok := r.seqByName[d.Name]; ok {
			delete(r.seqByName, d.Name)
			r.sequences = slices.DeleteFunc(r.sequences, func(s *SequenceRunner) bool { return s == sr })
			r.generation++
		}
		return
	}
	if run, ok := r.byName[d.Name]; ok {
		delete(r.byName, d.Name)
		r.runners = slices.DeleteFunc(r.runners, func(x *Runner) bool { return x == run })
		r.generation++
	}
}

// forget deletes what den keeps for name outside its definition: its runs in
// the state directory, its DEN_RUNBOOK_STATE folder and the record of the
// user having run it. All three go by name, so a runbook given it later would
// otherwise inherit them.
func (r *Registry) forget(name string) error {
	r.mu.RLock()
	h, trust, root := r.history, r.trust, r.stateRoot
	r.mu.RUnlock()
	errs := []error{h.Forget(name), trust.Forget(name)}
	if root != "" {
		errs = append(errs, os.RemoveAll(filepath.Join(root, folderName(name))))
	}
	return errors.Join(errs...)
}

// removeFolder deletes the folder of d. It is first renamed to a hidden name
// beside it, which a scan skips: the runbook is gone at once, and when the
// rename fails — a file in use on Windows — nothing is. gone says whether
// the runbook went; an error with it is a deletion that stopped halfway,
// naming what is left.
func removeFolder(d Deletion) (gone bool, err error) {
	parent, base := filepath.Split(d.Folder)
	hidden := ""
	for i := 0; i < 100 && hidden == ""; i++ {
		candidate := filepath.Join(parent, "."+base+".deleting")
		if i > 0 {
			candidate += fmt.Sprintf("-%d", i)
		}
		if _, err := os.Lstat(candidate); errors.Is(err, fs.ErrNotExist) {
			hidden = candidate
		}
	}
	if hidden == "" {
		return false, fmt.Errorf("%s: no free name to move it out of the way", d.Folder)
	}
	if err := os.Rename(d.Folder, hidden); err != nil {
		return false, fmt.Errorf("nothing deleted: %w", err)
	}
	if err := os.RemoveAll(hidden); err != nil {
		return true, fmt.Errorf("what is left of its folder is in %s: %w", hidden, err)
	}
	return true, nil
}

// Gone lists what Delete removes, a line each.
func (d Deletion) Gone() []string {
	var out []string
	switch d.Kind {
	case DeleteFolder:
		out = append(out, fmt.Sprintf("%s/ — %s", platform.TildePath(d.Folder), filesSize(d.Files, d.Bytes)))
	case DeleteScript:
		out = append(out, platform.TildePath(d.Script))
	case DeleteItem:
		out = append(out, "its entry in den.yaml (runbooks.items)")
	case DeleteSequence:
		out = append(out, "its entry in den.yaml (runbooks.sequences)")
	case DeleteSource:
		out = append(out, fmt.Sprintf("it from %s: uninstalled in den.yaml", d.Source))
	}
	switch {
	case d.Runs == 1:
		out = append(out, "1 run, with its log")
	case d.Runs > 1:
		out = append(out, fmt.Sprintf("%d runs, with their logs", d.Runs))
	}
	switch {
	case d.State != "" && d.StateFiles == 0:
		out = append(out, fmt.Sprintf("its state folder %s/, empty", platform.TildePath(d.State)))
	case d.State != "":
		out = append(out, fmt.Sprintf("its state folder %s/ — %s", platform.TildePath(d.State), filesSize(d.StateFiles, d.StateBytes)))
	}
	if d.Trusted {
		out = append(out, "the record of your review: installed again, it asks before it runs")
	}
	return out
}

// Kept lists what Delete leaves alone, a line each.
func (d Deletion) Kept() []string {
	var out []string
	switch d.Kind {
	case DeleteFolder:
		if d.Entry != "" {
			out = append(out, fmt.Sprintf("%s, the script it runs", platform.TildePath(d.Entry)))
		}
		if d.Cwd != "" {
			out = append(out, fmt.Sprintf("%s/, where it runs", platform.TildePath(d.Cwd)))
		}
	case DeleteItem:
		if d.Cwd != "" {
			out = append(out, fmt.Sprintf("%s/, where it runs, and whatever its command runs", platform.TildePath(d.Cwd)))
		}
	case DeleteSequence:
		out = append(out, "its steps: "+strings.Join(d.Steps, ", "))
	case DeleteSource:
		out = append(out, fmt.Sprintf("the checkout of %s, which its other runbooks share: den runbook source remove %s deletes it", d.Source, d.Source))
	}
	return out
}

// Notes are what else to know before saying yes.
func (d Deletion) Notes() []string {
	var out []string
	for _, seq := range d.UsedBy {
		out = append(out, fmt.Sprintf("sequence %s has it as a step: it stops loading until you change it", seq))
	}
	if len(d.Shadowed) > 0 {
		out = append(out, fmt.Sprintf("%s also defines %s: it takes its place", platform.TildePath(d.Shadowed[0]), d.Name))
	}
	if d.Template {
		out = append(out, "it is runbooks.template: new runbooks cannot be made until that changes")
	}
	if d.OwnRepo {
		out = append(out, "it holds a git repository of its own, which goes with it")
	}
	switch {
	case d.Kind == DeleteSource:
	case d.Kind == DeleteItem || d.Kind == DeleteSequence:
		out = append(out, "den.yaml keeps everything else as written, comments included")
	case d.Repo != "":
		out = append(out, fmt.Sprintf("it is in the git work tree %s: what was committed can be checked out again", platform.TildePath(d.Repo)))
	default:
		out = append(out, "nothing can bring it back")
	}
	return out
}

// within reports whether path is dir or inside it.
func within(dir, path string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// usage counts the files below dir and their size, without following links.
func usage(dir string) (files int, size int64) {
	_ = filepath.WalkDir(dir, func(_ string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		files++
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			size += info.Size()
		}
		return nil
	})
	return files, size
}

// gitWorkTree is the top of the git work tree dir is in, or "".
func gitWorkTree(dir string) string {
	for {
		if exists(filepath.Join(dir, ".git")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func filesSize(files int, size int64) string {
	return plural(files, "file") + ", " + byteSize(size)
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func byteSize(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
}
