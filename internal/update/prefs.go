package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/lukaszgard/den/internal/platform"
)

// Prefs are one person's choices about updates on this machine, kept out of
// den.yaml because they are not the team's: the versions never to offer again.
type Prefs struct {
	Skipped []string `json:"skipped,omitempty"`
}

// PrefsFile is where Prefs live: updates.json in den's state directory, or ""
// when there is none (no home directory).
func PrefsFile() string {
	dir := platform.StateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "updates.json")
}

// LoadPrefs reads the prefs at path. A file that is not there, or no path,
// is no prefs; one that does not parse is an error, so a skip list is never
// dropped without a word.
func LoadPrefs(path string) (Prefs, error) {
	var p Prefs
	if path == "" {
		return p, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return Prefs{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return p, nil
}

// SavePrefs writes p to path through a temp file, so a crash leaves the old
// file or the new one, never half of one.
func SavePrefs(path string, p Prefs) error {
	if path == "" {
		return errors.New("no state directory to keep update preferences in")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".updates-*.json")
	if err != nil {
		return err
	}
	_, err = tmp.Write(append(data, '\n'))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name()) //nolint:errcheck
		return fmt.Errorf("save %s: %w", path, err)
	}
	return nil
}

// Skips reports whether version is skipped.
func (p Prefs) Skips(version string) bool {
	return Policy{Skipped: p.Skipped}.Skips(version)
}

// Skip adds version to the skip list, newest first; false when it was there.
func (p *Prefs) Skip(version string) bool {
	if p.Skips(version) {
		return false
	}
	p.Skipped = append(p.Skipped, Canonical(version))
	slices.SortStableFunc(p.Skipped, func(a, b string) int { return Compare(b, a) })
	return true
}

// Unskip takes version off the skip list; false when it was not on it.
func (p *Prefs) Unskip(version string) bool {
	n := len(p.Skipped)
	p.Skipped = slices.DeleteFunc(p.Skipped, func(s string) bool { return SameVersion(s, version) })
	return len(p.Skipped) != n
}
