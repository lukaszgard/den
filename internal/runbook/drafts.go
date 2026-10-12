package runbook

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lukaszgard/den/internal/platform"
)

// What a request typed for an agent is about: a runbook to write (the
// Runbooks pane's n), or one to change (E).
const (
	DraftNew  = "new"
	DraftEdit = "edit"
)

// Where a request is: still being written, or sent, its runbook created or
// its change started. A sent one is a saved request: kept so an agent that
// failed can be given it again.
const (
	StateDraft   = "draft"
	StateCreated = "created"
)

// draftVersion is the format of a draft file.
const draftVersion = 1

// Draft is a request typed for an agent, with what goes with it, as one file
// of the Drafts store.
type Draft struct {
	Version int    `json:"version"`
	ID      string `json:"id"`    // time and random; never the name, which may be empty or change
	Kind    string `json:"kind"`  // DraftNew or DraftEdit
	State   string `json:"state"` // StateDraft or StateCreated
	// Name is the new runbook's name as typed.
	Name string `json:"name,omitempty"`
	// Runbook is the runbook to change (DraftEdit), or, once sent, the one
	// the request created or changed.
	Runbook string `json:"runbook,omitempty"`
	// Folder is that runbook's folder: a saved request whose folder is gone
	// was deleted outside den. Its name is no proof: a runbook.yaml an agent
	// broke is not listed, and that is when the request is wanted.
	Folder  string    `json:"folder,omitempty"`
	Agent   string    `json:"agent,omitempty"`
	Request string    `json:"request"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// Drafts keeps requests typed for an agent, one file each, in a folder of the
// user's state directory: what was typed survives an esc, a quit or a crash,
// and an agent that failed can be given its request again. A request may hold
// a secret, so the folder and its files are the user's alone, and nothing
// reads them for den mcp or --json.
type Drafts struct {
	dir string
	mu  sync.Mutex
	now func() time.Time
}

// NewDrafts keeps drafts in dir; nil, which keeps nothing, for an empty dir.
func NewDrafts(dir string) *Drafts {
	if dir == "" {
		return nil
	}
	return &Drafts{dir: dir, now: time.Now}
}

// UserDrafts is the drafts folder in the user's state directory, or nil when
// there is no home.
func UserDrafts() *Drafts {
	dir := platform.StateDir()
	if dir == "" {
		return nil
	}
	return NewDrafts(filepath.Join(dir, "runbook-drafts"))
}

// Dir is the folder the drafts are kept in.
func (d *Drafts) Dir() string {
	if d == nil {
		return ""
	}
	return d.dir
}

// draftIDRe is what an ID may hold: it names a file in the folder, and must
// never reach outside it.
var draftIDRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z-]*$`)

// errNoDrafts is Save's answer without a folder to keep drafts in.
var errNoDrafts = errors.New("drafts are not kept: den has no state directory")

// Save writes dr, giving a new one its ID and every one its times. A saved
// request replaces any older one of the same runbook and kind: restarting an
// agent offers the last thing asked of it.
func (d *Drafts) Save(dr Draft) (Draft, error) {
	if d == nil {
		return dr, errNoDrafts
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now().UTC()
	if dr.ID == "" {
		id, err := newDraftID(now)
		if err != nil {
			return dr, err
		}
		dr.ID = id
	} else if !draftIDRe.MatchString(dr.ID) {
		return dr, fmt.Errorf("draft ID %q is not one den made", dr.ID)
	}
	if dr.State == "" {
		dr.State = StateDraft
	}
	if dr.Created.IsZero() {
		dr.Created = now
	}
	dr.Version, dr.Updated = draftVersion, now
	if err := d.write(dr); err != nil {
		return dr, err
	}
	if dr.State == StateCreated {
		for _, old := range d.read() {
			if old.ID != dr.ID && old.State == StateCreated && old.Runbook == dr.Runbook && old.Kind == dr.Kind {
				_ = os.Remove(d.path(old.ID))
			}
		}
	}
	return dr, nil
}

// newDraftID is when the draft was made, then random bytes: two made in the
// same second differ, and the files list in the order they were made.
func newDraftID(now time.Time) (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return now.Format("20060102T150405") + "-" + hex.EncodeToString(b), nil
}

// Get reads the draft with the given ID.
func (d *Drafts) Get(id string) (Draft, bool) {
	if d == nil || !draftIDRe.MatchString(id) {
		return Draft{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	dr, err := readDraft(d.path(id))
	return dr, err == nil
}

// List is every draft still being written, the one saved last first. Drafts
// untouched for more than days days are deleted on the way; 0 keeps them
// all. A file that does not read as a draft is left alone and not listed.
func (d *Drafts) List(days int) []Draft {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	cutoff := time.Time{}
	if days > 0 {
		cutoff = d.now().AddDate(0, 0, -days)
	}
	var out []Draft
	for _, dr := range d.read() {
		if dr.State != StateDraft {
			continue
		}
		if !cutoff.IsZero() && dr.Updated.Before(cutoff) {
			_ = os.Remove(d.path(dr.ID))
			continue
		}
		out = append(out, dr)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// Request is the newest saved request of the runbook called name, of kind
// (DraftNew, DraftEdit, or "" for either).
func (d *Drafts) Request(name, kind string) (Draft, bool) {
	if d == nil {
		return Draft{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var best Draft
	found := false
	for _, dr := range d.read() {
		if dr.State != StateCreated || dr.Runbook != name || (kind != "" && dr.Kind != kind) {
			continue
		}
		if !found || dr.Updated.After(best.Updated) {
			best, found = dr, true
		}
	}
	return best, found
}

// Delete removes the draft with the given ID; one that is gone already is no
// error.
func (d *Drafts) Delete(id string) error {
	if d == nil {
		return nil
	}
	if !draftIDRe.MatchString(id) {
		return fmt.Errorf("draft ID %q is not one den made", id)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := os.Remove(d.path(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Forget removes everything kept for the runbook called name: its saved
// requests, and drafts of a change to it. A runbook given the name later was
// asked none of it.
func (d *Drafts) Forget(name string) error {
	return d.removeIf(func(dr Draft) bool { return dr.Runbook == name })
}

// dropOrphans removes the saved requests whose runbook's folder is gone.
func (d *Drafts) dropOrphans() error {
	return d.removeIf(func(dr Draft) bool {
		if dr.State != StateCreated || dr.Folder == "" {
			return false
		}
		_, err := os.Stat(dr.Folder)
		return errors.Is(err, fs.ErrNotExist)
	})
}

func (d *Drafts) removeIf(drop func(Draft) bool) error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var errs []error
	for _, dr := range d.read() {
		if drop(dr) {
			if err := os.Remove(d.path(dr.ID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (d *Drafts) path(id string) string { return filepath.Join(d.dir, id+".json") }

// read is every draft in the folder, unsorted. Called with d.mu held.
func (d *Drafts) read() []Draft {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil
	}
	var out []Draft
	for _, e := range entries {
		name := e.Name()
		id, ok := strings.CutSuffix(name, ".json")
		if !ok || e.IsDir() || !draftIDRe.MatchString(id) {
			continue
		}
		dr, err := readDraft(filepath.Join(d.dir, name))
		if err != nil || dr.ID != id {
			continue
		}
		out = append(out, dr)
	}
	return out
}

func readDraft(path string) (Draft, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Draft{}, err
	}
	var dr Draft
	if err := json.Unmarshal(data, &dr); err != nil {
		return Draft{}, err
	}
	return dr, nil
}

// write replaces dr's file atomically: a den killed halfway leaves the draft
// as it was saved last, never half of it.
func (d *Drafts) write(dr Draft) error {
	data, err := json.MarshalIndent(dr, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d.dir, ".draft.*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if err := errors.Join(werr, tmp.Close()); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), d.path(dr.ID)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// SetDrafts keeps requests typed for an agent in d from now on, and drops the
// saved requests of runbooks whose folder went while den was not running.
// Without a call nothing is kept, which is how tests stay out of the user's
// state directory.
func (r *Registry) SetDrafts(d *Drafts) {
	r.mu.Lock()
	r.drafts = d
	failed := r.historyFail
	r.mu.Unlock()
	if err := d.dropOrphans(); err != nil && failed != nil {
		failed(fmt.Errorf("drafts: %w", err))
	}
}

// KeepsDrafts reports whether there is a store to keep drafts in.
func (r *Registry) KeepsDrafts() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.drafts != nil
}

// KeepRequest is runbooks.drafts.keep_request: until-deleted, until-exit or
// never.
func (r *Registry) KeepRequest() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg.KeepRequest()
}

// Drafts is every draft still being written, the newest first; those older
// than runbooks.drafts.days are deleted on the way.
func (r *Registry) Drafts() []Draft {
	r.mu.RLock()
	d, days := r.drafts, r.cfg.DraftDays()
	r.mu.RUnlock()
	return d.List(days)
}

// SaveDraft writes dr; see Drafts.Save.
func (r *Registry) SaveDraft(dr Draft) (Draft, error) {
	return r.draftStore().Save(dr)
}

// DeleteDraft removes the draft or saved request with the given ID.
func (r *Registry) DeleteDraft(id string) error {
	return r.draftStore().Delete(id)
}

// SavedRequest is the newest saved request of the runbook called name, of
// kind (DraftNew, DraftEdit, or "" for either).
func (r *Registry) SavedRequest(name, kind string) (Draft, bool) {
	return r.draftStore().Request(name, kind)
}

func (r *Registry) draftStore() *Drafts {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.drafts
}
