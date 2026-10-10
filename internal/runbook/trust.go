package runbook

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/runbook/source"
)

// Trust remembers which runbooks from sources the user has run, by the git
// tree of each one's folder. A runbook from a source asks before its first
// run, and again whenever an update changed its folder: what the user said
// yes to is that code, not whatever the repository holds next week.
//
// It is the user's own record, kept in their state directory, not in den.lock:
// someone else's review is not yours.
type Trust struct {
	path string
	mu   sync.Mutex
}

// Trusted is one runbook the user has run.
type Trusted struct {
	Tree   string    `json:"tree"`
	Commit string    `json:"commit"`
	At     time.Time `json:"at"`
	// Edited is set when the user changed a file of its checkout in place:
	// the tree no longer says what runs, so it asks again until run.
	Edited bool `json:"edited,omitempty"`
}

// NewTrust keeps trust in the file at path; nil, which remembers nothing, for
// an empty path.
func NewTrust(path string) *Trust {
	if path == "" {
		return nil
	}
	return &Trust{path: path}
}

// UserTrust is the trust file in the user's state directory, or nil when
// there is no home.
func UserTrust() *Trust {
	dir := platform.StateDir()
	if dir == "" {
		return nil
	}
	return NewTrust(filepath.Join(dir, "runbook-trust.json"))
}

// All is every runbook the user has run, by name. A missing or unreadable
// file trusts nothing, which only means asking again.
func (t *Trust) All() map[string]Trusted {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.read()
}

func (t *Trust) read() map[string]Trusted {
	all := map[string]Trusted{}
	if data, err := os.ReadFile(t.path); err == nil {
		_ = json.Unmarshal(data, &all)
	}
	return all
}

// Add remembers that the user ran name at tree.
func (t *Trust) Add(name, tree, commit string) error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	all := t.read()
	all[name] = Trusted{Tree: tree, Commit: commit, At: time.Now().UTC()}
	return t.write(all)
}

// MarkEdited remembers that the user edited name's checkout at commit: it
// asks before its next run, whatever it was trusted at before.
func (t *Trust) MarkEdited(name, commit string) error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	all := t.read()
	all[name] = Trusted{Tree: all[name].Tree, Commit: commit, At: time.Now().UTC(), Edited: true}
	return t.write(all)
}

// Forget drops name from the record: a runbook given the name later is
// trusted for nothing the user ran under it before.
func (t *Trust) Forget(name string) error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	all := t.read()
	if _, ok := all[name]; !ok {
		return nil
	}
	delete(all, name)
	return t.write(all)
}

// Has reports whether the user has run name, under any tree.
func (t *Trust) Has(name string) bool {
	_, ok := t.All()[name]
	return ok
}

// write replaces the trust file with all, atomically: a den killed halfway
// leaves the old file, never half a one that trusts nothing.
func (t *Trust) write(all map[string]Trusted) error {
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(t.path), ".runbook-trust.*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if err := errors.Join(werr, tmp.Close()); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), t.path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// applyTrust makes a source runbook the user has not run at this tree ask
// before it runs, saying why.
func applyTrust(rb *Runbook, all map[string]Trusted) {
	if rb.Remote == nil {
		return
	}
	t, ok := all[rb.Name]
	switch {
	case ok && t.Edited:
		rb.Remote.Note = "edited here: read it before it runs"
	case ok && t.Tree == rb.Remote.Tree:
		rb.Remote.Trusted = true
		return
	case ok:
		rb.Remote.Note = fmt.Sprintf("changed in %s since you ran it at %s", rb.Remote.Source, source.Short(t.Commit))
	default:
		rb.Remote.Note = fmt.Sprintf("new from %s: read it before its first run", rb.Remote.Source)
	}
	rb.Confirm = true
}

// trustStarted remembers a source runbook the user just started: it asked
// first, so the user said yes to this tree, and its next run need not ask.
func (r *Registry) trustStarted(rb Runbook) {
	if rb.Remote == nil || rb.Remote.Trusted {
		return
	}
	r.mu.RLock()
	trust, failed := r.trust, r.historyFail
	r.mu.RUnlock()
	if trust == nil {
		return
	}
	if err := trust.Add(rb.Name, rb.Remote.Tree, rb.Remote.Commit); err != nil {
		if failed != nil {
			failed(fmt.Errorf("runbook %q: not remembered as reviewed, it will ask again: %w", rb.Name, err))
		}
		return
	}
	r.Rescan()
}

// MarkEdited records that a file of the source runbook called name was edited
// in its checkout, so its next run asks first; the next Rescan applies it.
// A runbook of the user's own is theirs to edit: nothing to record.
func (r *Registry) MarkEdited(name string) error {
	run, ok := r.Lookup(name)
	if !ok {
		return nil
	}
	rb := run.Runbook()
	r.mu.RLock()
	trust := r.trust
	r.mu.RUnlock()
	if rb.Remote == nil || trust == nil {
		return nil
	}
	return trust.MarkEdited(name, rb.Remote.Commit)
}
