package runbook

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lukaszgard/den/internal/config"
)

// Validation is what den makes of a runbook folder: what validate_runbook
// tells an agent that has just written one.
type Validation struct {
	OK       bool     `json:"ok"`
	Folder   string   `json:"folder"`
	Name     string   `json:"name,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Command  string   `json:"command,omitempty"`
	Cwd      string   `json:"cwd,omitempty"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

// ResolveFolder turns what an agent named — a folder name, taken in the first
// runbooks directory, or a path — into a folder inside one of the runbooks
// directories. Anything outside them is refused: validating must not become a
// way to read the rest of the disk.
func (r *Registry) ResolveFolder(folder string) (string, error) {
	dirs := r.runbooksDirs()
	if len(dirs) == 0 {
		return "", errors.New("den.yaml has no runbooks.dirs: there is nowhere for a runbook folder to be")
	}
	folder = strings.TrimSpace(folder)
	if folder == "" {
		return "", errors.New("name the runbook's folder")
	}
	path := canonical(resolvePath(folder, dirs[0]))
	if directlyIn(path, dirs) {
		return path, nil
	}
	return "", fmt.Errorf("%s is not a folder directly in a runbooks directory (%s)", path, strings.Join(dirs, ", "))
}

// runbooksDirs is runbooks.dirs, absolute and with their symlinks resolved.
func (r *Registry) runbooksDirs() []string {
	r.mu.RLock()
	cfg, base := r.cfg, configDir(r.cfgPath)
	r.mu.RUnlock()
	dirs := make([]string, len(cfg.Dirs))
	for i, d := range cfg.Dirs {
		dirs[i] = canonical(resolvePath(d, base))
	}
	return dirs
}

// directlyIn reports whether path is an entry of one of dirs itself, not the
// directory, and not something deeper in it.
func directlyIn(path string, dirs []string) bool {
	for _, dir := range dirs {
		if rel, err := filepath.Rel(dir, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !strings.Contains(rel, string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// canonical is path made absolute with its symlinks resolved, as far as they
// exist, so a link cannot lead a folder out of a runbooks directory.
func canonical(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		return filepath.Join(dir, filepath.Base(path))
	}
	return path
}

// ValidateFolder loads the runbook folder dir the way a scan does and reports
// everything that would keep it from loading or running, and what is worth
// fixing besides.
func (r *Registry) ValidateFolder(dir string) Validation {
	v := Validation{Folder: dir, Errors: []string{}, Warnings: []string{}}
	fail := func(format string, args ...any) { v.Errors = append(v.Errors, fmt.Sprintf(format, args...)) }

	if _, err := os.Stat(filepath.Join(dir, ManifestName)); err != nil {
		fail("no %s: den does not see this folder as a runbook", ManifestName)
		return v
	}
	spec, err := loadManifest(dir)
	if err != nil {
		fail("%s: %v", ManifestName, err)
		return v
	}
	r.mu.RLock()
	envs, defaults, agents := r.envs, r.cfg.Defaults, r.cfg.Agents
	r.mu.RUnlock()
	if err := config.ValidateRunbook(spec, envs); err != nil {
		fail("%v", err)
	}
	if err := config.CheckRunbookAgent(fmt.Sprintf("runbook %q", spec.Name), spec.Agent, agents); err != nil {
		v.Warnings = append(v.Warnings, err.Error()+": E cannot start it")
	}
	rb, err := fromSpec(spec, defaults)
	if err != nil {
		fail("%v", err)
		return v
	}
	v.Name, v.Kind, v.Command, v.Cwd = rb.Name, rb.Kind, rb.Command, rb.Cwd

	first := strings.Fields(strings.TrimSpace(rb.Command))
	if script := entryScript(rb); script != "" {
		if info, err := os.Stat(script); err == nil && strings.HasPrefix(first[0], "./") && info.Mode().Perm()&0o111 == 0 {
			fail("%s is not executable: chmod +x it, or run it through its interpreter in command", filepath.Base(script))
		}
	} else if len(first) > 0 && strings.HasPrefix(first[0], "./") {
		fail("command runs %s, which is not in %s", first[0], rb.Cwd)
	}

	if other, ok := r.Lookup(rb.Name); ok && canonical(other.Runbook().Home()) != canonical(dir) {
		fail("the name %q is taken by %s: choose another", rb.Name, other.Runbook().Origin())
	}
	if _, ok := r.LookupSequence(rb.Name); ok {
		fail("the name %q is taken by a sequence: choose another", rb.Name)
	}

	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		v.Warnings = append(v.Warnings, "no README.md: say what it does, when to run it and how to read its result")
	}
	if rb.Description == "" {
		v.Warnings = append(v.Warnings, "no description: the Runbooks panel shows it next to the name")
	}
	if rb.Timeout == 0 {
		v.Warnings = append(v.Warnings, "no timeout: a hung run is only stopped by hand")
	}
	if rb.Env == "" && len(envs) > 0 {
		v.Warnings = append(v.Warnings, "no env: it runs with whatever AWS profile the user's shell has")
	}
	v.OK = len(v.Errors) == 0
	return v
}
