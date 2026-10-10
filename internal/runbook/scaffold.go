package runbook

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/lukaszgard/den/internal/platform"
)

// The built-in template of a new runbook folder: a runbook.yaml naming every
// key it may set, a script printing what den hands it, and a README the About
// tab shows.
const (
	scaffoldManifest = `name: %s
description: ""
command: ./run.sh          # runs inside this folder, through sh -c
# env: eu-dev              # an entry of environments: in den.yaml, for AWS_PROFILE and friends
# timeout: 5m
# confirm: true            # show the command and ask before running
# params:
#   - name: target
#     description: What to act on
#     options: [test, prod]
#     required: true
# history: 20              # finished runs kept on disk
# shell: bash              # what t opens in this folder
# editor: nvim             # what e opens run.sh with
`
	scaffoldScript = `#!/usr/bin/env bash
# What den hands a runbook: the connection context of its env:, the local
# ports of live connections, and the parameters it was given.
set -euo pipefail

echo "runbook: ${DEN_RUNBOOK}"
echo "profile: ${AWS_PROFILE:-none} · region: ${AWS_REGION:-none}"
env | grep -E '^DEN_(PORT|PARAM)_' | sort || true
`
	scaffoldReadme = `# %s

What this runbook does, when to run it, and what to check afterwards.
`
)

// nameLine is the name: key of a runbook.yaml, as a template carries it.
var nameLine = regexp.MustCompile(`(?m)^name:.*$`)

// Scaffold creates a runbook folder for name in root and returns its path:
// a copy of the template folder when one is given, else the built-in
// template. The folder is named after the runbook, and its runbook.yaml names
// it, so the folder can be renamed later without renaming the runbook.
func Scaffold(root, name, template string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("a runbook needs a name")
	}
	if root == "" {
		return "", errors.New("no runbooks directory: add one to runbooks.dirs in den.yaml")
	}
	dir := filepath.Join(root, slug(name))
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("%s already exists", dir)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", err
	}
	if template != "" {
		if err := copyTemplate(template, dir); err != nil {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("template %s: %w", template, err)
		}
		return dir, nameManifest(dir, name)
	}
	if err := os.Mkdir(dir, 0o750); err != nil {
		return "", err
	}
	files := []struct {
		name, body string
		mode       os.FileMode
	}{
		{ManifestName, fmt.Sprintf(scaffoldManifest, strconv.Quote(name)), 0o600},
		{"run.sh", scaffoldScript, 0o700},
		{"README.md", fmt.Sprintf(scaffoldReadme, name), 0o600},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.body), f.mode); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// copyTemplate copies the folder from into to, which must not exist yet,
// keeping each file's mode so scripts stay executable. Both sides go through
// os.Root, so a symlink in the template cannot lead the copy outside it.
func copyTemplate(from, to string) error {
	src, err := os.OpenRoot(from)
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.Mkdir(to, 0o750); err != nil {
		return err
	}
	dst, err := os.OpenRoot(to)
	if err != nil {
		return err
	}
	defer dst.Close()
	return fs.WalkDir(src.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == "." {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == LogsFolder {
				return fs.SkipDir // a template that is a runbook too keeps its runs to itself
			}
			return dst.MkdirAll(path, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return nil // a link or a device has no place in a template
		}
		data, err := src.ReadFile(path)
		if err != nil {
			return err
		}
		return dst.WriteFile(path, data, info.Mode().Perm())
	})
}

// nameManifest sets the name: of the runbook.yaml in dir, writing one with
// only a name when the template had none.
func nameManifest(dir, name string) error {
	path := filepath.Join(dir, ManifestName)
	line := "name: " + strconv.Quote(name)
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return os.WriteFile(path, []byte(line+"\n"), 0o600)
	case err != nil:
		return err
	}
	text := string(data)
	if nameLine.MatchString(text) {
		text = nameLine.ReplaceAllLiteralString(text, line)
	} else {
		text = line + "\n" + text
	}
	return os.WriteFile(path, []byte(text), 0o600)
}

// NewRunbook scaffolds a runbook folder for name in root, or in the first of
// runbooks.dirs when root is empty, from runbooks.template, and rescans so it
// is listed at once.
func (r *Registry) NewRunbook(name, root string) (string, error) {
	if err := r.NewRunbookConflict(name, root); err != nil {
		return "", err
	}
	r.mu.RLock()
	cfg, base := r.cfg, configDir(r.cfgPath)
	r.mu.RUnlock()
	if root == "" {
		root = r.firstDir()
	}
	dir, err := Scaffold(root, name, resolvePath(cfg.Template, base))
	if err != nil {
		return "", err
	}
	r.Rescan()
	return dir, nil
}

// NewRunbookConflict says why name cannot go to a new runbook in root, the
// first of runbooks.dirs when empty, or nil when it can: no name, no folder
// to put it in, a runbook or a sequence of that name, or a folder that the
// runbook's would be. Names are compared ignoring case: den run tells
// "Deploy" from "deploy", a person picking one from the list does not.
func (r *Registry) NewRunbookConflict(name, root string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("a runbook needs a name")
	}
	r.mu.RLock()
	for taken := range r.byName {
		if strings.EqualFold(taken, name) {
			r.mu.RUnlock()
			return fmt.Errorf("a runbook named %q exists already", taken)
		}
	}
	for taken := range r.seqByName {
		if strings.EqualFold(taken, name) {
			r.mu.RUnlock()
			return fmt.Errorf("a sequence named %q exists already", taken)
		}
	}
	r.mu.RUnlock()
	if root == "" {
		root = r.firstDir()
	}
	if root == "" {
		return errors.New("no runbooks directory: add one to runbooks.dirs in den.yaml")
	}
	// A folder that did not load, or a name another one's folder takes:
	// "Rotate keys!" goes into rotate-keys too.
	if dir := filepath.Join(root, slug(name)); exists(dir) {
		return fmt.Errorf("%s%c exists already", platform.TildePath(dir), filepath.Separator)
	}
	return nil
}

// NewRunbookDir is the folder NewRunbook makes for name in the first of
// runbooks.dirs, to say where it goes before it is made: that directory
// itself for an empty name, "" when den.yaml lists none.
func (r *Registry) NewRunbookDir(name string) string {
	root, name := r.firstDir(), strings.TrimSpace(name)
	if root == "" || name == "" {
		return root
	}
	return filepath.Join(root, slug(name))
}

// firstDir is the first of runbooks.dirs, where a new runbook and the copy of
// a source's go; "" when den.yaml lists none.
func (r *Registry) firstDir() string {
	r.mu.RLock()
	dirs, base := r.cfg.Dirs, configDir(r.cfgPath)
	r.mu.RUnlock()
	if len(dirs) == 0 {
		return ""
	}
	return resolvePath(dirs[0], base)
}
