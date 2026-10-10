package runbook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lukaszgard/den/internal/platform"
)

// Where a run was started from, as History records it.
const (
	OriginTUI = "tui"
	OriginCLI = "den run"
	OriginMCP = "mcp"
)

// History keeps runs on disk, so a restart, a re-run or a run started from
// another den process (den run, den mcp) does not erase what happened.
//
// Every run is two files, numbered in the order the runs began: log-01.log
// with that run's output, redacted as it was shown, and log-01.json describing
// the run. A runbook in a folder of the user's keeps them in logs/ in that
// folder, beside its script. den keeps the others' under root, in a folder per
// name: den.yaml's runbooks and sequences have no folder, and a runbook from a
// source has one that every update replaces. The .log is written as the run
// goes, so it holds the whole run however long, and what a den that died had
// printed. Files are private to the user: output can carry anything a script
// printed.
type History struct {
	root string
}

// NewHistory keeps runs under root, and in the folders of the runbooks that
// have one. An empty root keeps nothing.
func NewHistory(root string) *History {
	if root == "" {
		return nil
	}
	return &History{root: root}
}

// Entry describes one finished run.
type Entry struct {
	Runbook    string            `json:"runbook"`
	Status     string            `json:"status"`
	ExitCode   int               `json:"exit_code"`
	StartedAt  time.Time         `json:"started_at"`
	FinishedAt time.Time         `json:"finished_at"`
	Attempts   int               `json:"attempts,omitempty"`
	Origin     string            `json:"origin,omitempty"`
	Params     map[string]string `json:"params,omitempty"` // secret ones are never written
	Error      string            `json:"error,omitempty"`
	// PID is the den process writing the run, while it runs.
	PID int `json:"pid,omitempty"`

	log         string // the .log beside the .json
	interrupted bool   // "running", but its den is gone
}

// RunStatus is the recorded status, StatusFailed for one den does not know or
// for a run its den never finished.
func (e Entry) RunStatus() RunStatus {
	if e.Status == StatusRunning.String() && !e.interrupted {
		return StatusRunning
	}
	for s := StatusSucceeded; s <= StatusTimedOut; s++ {
		if s.String() == e.Status {
			return s
		}
	}
	return StatusFailed
}

// Running reports whether the run is still going, in this den or another.
func (e Entry) Running() bool { return e.RunStatus() == StatusRunning }

// Interrupted reports a run whose den stopped before it was over: den was
// killed or crashed, so the run never got its final status.
func (e Entry) Interrupted() bool { return e.interrupted }

// LogPath is the file holding the run's output.
func (e Entry) LogPath() string { return e.log }

// Run is the entry as a Run, without its output.
func (e Entry) Run() Run {
	return Run{
		Status:     e.RunStatus(),
		ExitCode:   e.ExitCode,
		StartedAt:  e.StartedAt,
		FinishedAt: e.FinishedAt,
		Attempt:    max(e.Attempts, 1),
		LogPath:    e.log,
		Saved:      &e,
	}
}

const (
	logExt  = ".log"
	metaExt = ".json"
	runStem = "log-"
)

// LogsFolder is the folder in a runbook's own folder holding its runs.
const LogsFolder = "logs"

// runName is the files of run n, without their extension: log-01, log-02, …,
// log-100 after log-99.
func runName(n int) string { return fmt.Sprintf("%s%02d", runStem, n) }

// runNumber is the number in the name of a run's .log or .json; false for
// any other file, which den leaves alone: a runbook's logs/ may hold its
// script's own.
func runNumber(path string) (int, bool) {
	name := filepath.Base(path)
	ext := filepath.Ext(name)
	if ext != logExt && ext != metaExt {
		return 0, false
	}
	digits, ok := strings.CutPrefix(strings.TrimSuffix(name, ext), runStem)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	return n, err == nil && n > 0
}

// IsRunFile reports whether path is one of den's own files of a run, a
// log-NN.log or log-NN.json, rather than something a script keeps beside them.
func IsRunFile(path string) bool {
	_, ok := runNumber(path)
	return ok
}

// Recording is one run being kept as it goes: its .log grows a line at a
// time, and its .json says the run is going until Finish.
type Recording struct {
	name string
	keep int
	dir  string // the folder of the runbook's runs
	base string // the run's files, without their extension

	mu  sync.Mutex
	f   *os.File // nil once finished
	err error    // the first write that failed
}

// Begin starts keeping a run of name that began at e.StartedAt, in logs, the
// runbook's own LogsDir, or under den's root when logs is "". keep 0, or a
// nil History, keeps nothing and returns a nil Recording, which is safe to
// use.
func (h *History) Begin(name, logs string, keep int, e Entry) (*Recording, error) {
	if h == nil || keep <= 0 {
		return nil, nil
	}
	dir := h.dir(name, logs)
	if err := makeRunsDir(dir, logs != ""); err != nil {
		return nil, err
	}
	f, base, err := createLog(dir)
	if err != nil {
		return nil, err
	}
	e.Status = StatusRunning.String()
	e.PID = os.Getpid()
	// The .json is written last: an entry is listed only once its log is there.
	if err := writeMeta(base, name, e); err != nil {
		_ = f.Close()
		_ = os.Remove(base + logExt)
		return nil, err
	}
	return &Recording{name: name, keep: keep, dir: dir, base: base, f: f}, nil
}

// makeRunsDir makes the folder runs are kept in. One made in a runbook's own
// folder ignores everything in it for git: that folder may be in a
// repository, even one shared as a source, and what a run printed is not for
// sharing. A logs/ already there is the user's, and is left as it is.
func makeRunsDir(dir string, own bool) error {
	if !own {
		return os.MkdirAll(dir, 0o700)
	}
	err := os.Mkdir(dir, 0o700)
	switch {
	case errors.Is(err, fs.ErrExist):
		return nil
	case err != nil:
		return err
	}
	ignore := "# den keeps this runbook's runs here: what they printed is not for git.\n*\n"
	return os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(ignore), 0o600)
}

// createLog creates the .log of a new run in dir, numbered one past the
// highest there. O_EXCL settles a race with another den starting a run in
// the same folder: whichever comes second takes the next number.
func createLog(dir string) (*os.File, string, error) {
	for n := lastRun(dir) + 1; ; n++ {
		base := filepath.Join(dir, runName(n))
		f, err := os.OpenFile(base+logExt, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
		if !errors.Is(err, fs.ErrExist) {
			return f, base, err
		}
	}
}

// lastRun is the highest run number in dir, 0 when there is none.
func lastRun(dir string) int {
	files, _ := filepath.Glob(filepath.Join(dir, runStem+"*"))
	last := 0
	for _, f := range files {
		if n, ok := runNumber(f); ok {
			last = max(last, n)
		}
	}
	return last
}

// Tally is how often a runbook or sequence has run: Runs since its runs were
// first kept, Failed among the runs it still keeps.
type Tally struct {
	Runs, Failed int
}

// Tally counts the runs of name kept in logs as Begin keeps them; false when
// none were ever kept.
func (h *History) Tally(name, logs string) (Tally, bool) {
	if h == nil {
		return Tally{}, false
	}
	return tally(h.dir(name, logs))
}

// tally counts the runs in dir. A run's number is one past the highest there,
// and pruning keeps the newest, so that number is how many runs there have
// been, pruned ones included; what pruning took is no longer there to say
// whether it failed.
func tally(dir string) (Tally, bool) {
	t := Tally{Runs: lastRun(dir)}
	if t.Runs == 0 {
		return Tally{}, false
	}
	entries, _ := list(dir)
	for _, e := range entries {
		if e.RunStatus().failed() {
			t.Failed++
		}
	}
	return t, true
}

// Tally counts the runs kept beside this one, once Finish has pruned them;
// false when nothing is kept.
func (rec *Recording) Tally() (Tally, bool) {
	if rec == nil {
		return Tally{}, false
	}
	return tally(rec.dir)
}

// settle is t once a run that ended in status is over: what the runs kept
// beside it say, when they are counted, or else t with that run's failure.
func (t Tally) settle(kept Tally, counted bool, status RunStatus) Tally {
	if counted {
		return t.merge(kept)
	}
	if status.failed() {
		t.Failed++
	}
	return t
}

// merge is t with what the runs kept on disk say. Runs never goes down: the
// runs a migration moves lose the numbers of those pruned before.
func (t Tally) merge(kept Tally) Tally {
	return Tally{Runs: max(t.Runs, kept.Runs), Failed: kept.Failed}
}

// failed reports a run that did not do its job: it failed, timed out, or its
// den died under it. A cancelled run was stopped on purpose.
func (s RunStatus) failed() bool { return s == StatusFailed || s == StatusTimedOut }

// Number is the run's number among the runs kept beside it; 0 when nothing
// is kept.
func (rec *Recording) Number() int {
	if rec == nil {
		return 0
	}
	n, _ := runNumber(rec.base + logExt)
	return n
}

// Line appends one line of output. A write that fails is reported by Finish;
// the run goes on either way.
func (rec *Recording) Line(s string) {
	if rec == nil {
		return
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.f == nil || rec.err != nil {
		return
	}
	if _, err := rec.f.WriteString(s + "\n"); err != nil {
		rec.err = err
	}
}

// Path is the file the run's output goes to; "" when nothing is kept.
func (rec *Recording) Path() string {
	if rec == nil {
		return ""
	}
	return rec.base + logExt
}

// Finish gives the run its final state e and prunes all but the newest keep
// runs. It reports the first write that failed, too.
func (rec *Recording) Finish(e Entry) error {
	if rec == nil {
		return nil
	}
	rec.mu.Lock()
	f, werr := rec.f, rec.err
	// A reader still draining a stray grandchild's output writes nothing more.
	rec.f = nil
	rec.mu.Unlock()
	if f == nil {
		return nil
	}
	cerr := f.Close()
	e.PID = 0
	if err := writeMeta(rec.base, rec.name, e); err != nil {
		return err
	}
	prune(rec.dir, rec.keep)
	return errors.Join(werr, cerr)
}

// writeMeta writes a run's .json. It replaces the file in one step: a den
// listing the runs meanwhile reads the old state or the new, never half of one.
func writeMeta(base, name string, e Entry) error {
	e.Runbook = name
	meta, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	tmp := base + metaExt + ".tmp"
	if err := os.WriteFile(tmp, append(meta, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, base+metaExt); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Save records a finished run of name whose output is already known, in
// logs as Begin does, and prunes all but the newest keep runs. keep 0
// records nothing.
func (h *History) Save(name, logs string, keep int, e Entry, lines []string) error {
	rec, err := h.Begin(name, logs, keep, e)
	if rec == nil {
		return err
	}
	for _, l := range lines {
		rec.Line(l)
	}
	return rec.Finish(e)
}

// List returns the recorded runs of name, kept in logs as Begin keeps them,
// newest first.
func (h *History) List(name, logs string) ([]Entry, error) {
	if h == nil {
		return nil, nil
	}
	return list(h.dir(name, logs))
}

// list returns the runs kept in dir, newest first.
func list(dir string) ([]Entry, error) {
	matches, err := filepath.Glob(filepath.Join(dir, runStem+"*"+metaExt))
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(matches))
	for _, m := range matches {
		if _, ok := runNumber(m); !ok {
			continue
		}
		data, err := os.ReadFile(m)
		if err != nil {
			continue // pruned by another den meanwhile
		}
		var e Entry
		if json.Unmarshal(data, &e) != nil {
			continue
		}
		e.log = strings.TrimSuffix(m, metaExt) + logExt
		if e.Status == StatusRunning.String() && !platform.ProcessAlive(e.PID) {
			e.interrupted = true
			e.ExitCode = -1
			if e.Error == "" {
				e.Error = "den stopped before the run was over"
			}
		}
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].StartedAt.After(entries[j].StartedAt) })
	return entries, nil
}

// Latest returns the newest recorded run of name that is over. A run still
// going is another den's: it is not this one's last run.
func (h *History) Latest(name, logs string) (Entry, bool) {
	entries, _ := h.List(name, logs)
	for _, e := range entries {
		if !e.Running() {
			return e, true
		}
	}
	return Entry{}, false
}

// Adopt moves the runs den kept for name into logs, the folder of its own a
// runbook has just been given, numbered after any already there in the order
// they ran. A run still going, in another den, stays where it is.
func (h *History) Adopt(name, logs string) error {
	if h == nil || logs == "" {
		return nil
	}
	from := h.dir(name, "")
	entries, err := list(from)
	if err != nil || len(entries) == 0 {
		return err
	}
	if err := makeRunsDir(logs, true); err != nil {
		return err
	}
	slices.Reverse(entries) // oldest first: the numbers keep their order
	for _, e := range entries {
		if e.Running() {
			continue
		}
		f, base, err := createLog(logs)
		if err != nil {
			return err
		}
		_ = f.Close()
		// The .log first, as Begin writes them: a run is listed only once
		// its .json is there.
		if err := moveFile(e.log, base+logExt); err != nil {
			_ = os.Remove(base + logExt)
			return err
		}
		if err := moveFile(strings.TrimSuffix(e.log, logExt)+metaExt, base+metaExt); err != nil {
			_ = moveFile(base+logExt, e.log)
			return err
		}
	}
	_ = os.Remove(from) // only once empty: a run still going keeps it
	return nil
}

// moveFile moves a run's file, copying it when from and to are on different
// file systems, as the state directory and a runbook's folder can be.
func moveFile(from, to string) error {
	if os.Rename(from, to) == nil {
		return nil
	}
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.WriteFile(to, data, 0o600); err != nil {
		return err
	}
	return os.Remove(from)
}

// Load reads the output of a recorded run.
func (h *History) Load(e Entry) ([]string, error) {
	if e.log == "" {
		return nil, errors.New("run has no recorded output")
	}
	data, err := os.ReadFile(e.log)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

// prune removes all but the newest keep runs in dir that are over: a run
// still going, in another den maybe, is not one to delete from under it.
func prune(dir string, keep int) {
	entries, _ := list(dir)
	newest := 0 // the newest kept run's number
	kept := 0
	for _, e := range entries {
		if e.Running() {
			continue
		}
		if kept < keep {
			if kept == 0 {
				newest, _ = runNumber(e.log)
			}
			kept++
			continue
		}
		// Another den process may have pruned it already.
		_ = os.Remove(strings.TrimSuffix(e.log, logExt) + metaExt)
		_ = os.Remove(e.log)
	}
	if newest == 0 {
		return
	}
	// A den that died between a run's two files left a .log with no .json:
	// debris once it is older than a run kept. A newer one may be a run that
	// is starting this very moment.
	logs, _ := filepath.Glob(filepath.Join(dir, runStem+"*"+logExt))
	for _, l := range logs {
		n, ok := runNumber(l)
		if !ok || n >= newest {
			continue
		}
		if _, err := os.Stat(strings.TrimSuffix(l, logExt) + metaExt); errors.Is(err, fs.ErrNotExist) {
			_ = os.Remove(l)
		}
	}
}

// Forget deletes the runs den kept for name in its state directory, so that a
// runbook given the name later starts with none. Runs in a runbook's own
// logs/ go with its folder.
func (h *History) Forget(name string) error {
	if h == nil {
		return nil
	}
	return os.RemoveAll(h.dir(name, ""))
}

// dir is the folder of name's runs: logs when the runbook has a folder of its
// own, else one under root, readable, and unique even when two names differ
// only in characters a file name cannot hold.
func (h *History) dir(name, logs string) string {
	if logs != "" {
		return logs
	}
	return filepath.Join(h.root, folderName(name))
}

// folderName is a runbook's folder in a directory den keeps per runbook:
// readable, and unique even when two names differ only in characters a file
// name cannot hold.
func folderName(name string) string {
	sum := sha256.Sum256([]byte(name))
	return slug(name) + "-" + hex.EncodeToString(sum[:4])
}

func slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 48 {
		s = strings.TrimRight(s[:48], "-")
	}
	if s == "" {
		s = "runbook"
	}
	return s
}

// UserHistory is the history kept in the runbooks' folders and the user's
// state directory, shared by the TUI, den run and den mcp; nil when there is
// no state directory.
func UserHistory() *History {
	dir := platform.StateDir()
	if dir == "" {
		return nil
	}
	return NewHistory(filepath.Join(dir, "runbooks"))
}
