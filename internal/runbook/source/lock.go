package source

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// LockName is the file beside den.yaml that records the commit each source
// is checked out at. Committed next to den.yaml, it makes everyone who shares
// the config run the same runbooks: `den runbook source sync` checks out what
// it records.
const LockName = "den.lock"

const lockHeader = `# den.lock — written by den runbook source update: the commit each runbook
# source is checked out at. Commit it beside den.yaml so that everyone sharing
# the config runs the same runbooks (den runbook source sync).
`

// Lock is den.lock's content.
type Lock struct {
	Sources map[string]Locked `yaml:"sources"`
}

// Locked is where one source is checked out.
type Locked struct {
	URL     string    `yaml:"url"`
	Ref     string    `yaml:"ref,omitempty"`
	Commit  string    `yaml:"commit"`
	Updated time.Time `yaml:"updated"`
}

// LockPath is the den.lock beside the den.yaml at cfgPath, beside the file a
// symlinked den.yaml points at, so it is committed with it; "" without one.
func LockPath(cfgPath string) string {
	if cfgPath == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(cfgPath); err == nil {
		cfgPath = real
	}
	return filepath.Join(filepath.Dir(cfgPath), LockName)
}

// ReadLock reads den.lock; a missing one is an empty lock.
func ReadLock(path string) (Lock, error) {
	lock := Lock{Sources: map[string]Locked{}}
	if path == "" {
		return lock, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return lock, nil
	}
	if err != nil {
		return lock, err
	}
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return Lock{Sources: map[string]Locked{}}, err
	}
	if lock.Sources == nil {
		lock.Sources = map[string]Locked{}
	}
	for name, l := range lock.Sources {
		if !ValidCommit(l.Commit) {
			return Lock{Sources: map[string]Locked{}}, fmt.Errorf("source %q: commit %q is not a commit id: fix den.lock, or delete it and run den runbook source update", name, l.Commit)
		}
	}
	return lock, nil
}

// WriteLock replaces den.lock in one step, so a den reading it at the same
// time sees the old content or the new, never half of it.
func WriteLock(path string, lock Lock) error {
	var buf bytes.Buffer
	buf.WriteString(lockHeader)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(lock); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return writeAtomic(path, buf.Bytes())
}

// writeAtomic writes data to path through a temporary file beside it.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}
