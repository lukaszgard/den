package runbook

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/lukaszgard/den/internal/config"
)

// Migration is what moving a header script into a folder of its own does.
type Migration struct {
	Script string // the script as it is now
	Dir    string // the folder it moves into
	Run    string // the script's name in the folder: run plus its extension
}

// Files are the files the folder will hold.
func (m Migration) Files() []string { return []string{ManifestName, m.Run, "README.md"} }

// PlanMigration says where the header script at path would move: a folder
// named after it, next to it, holding run<ext>.
func PlanMigration(path string) Migration {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return Migration{
		Script: path,
		Dir:    filepath.Join(filepath.Dir(path), strings.TrimSuffix(base, ext)),
		Run:    "run" + ext,
	}
}

// MigrateScript moves the header script at path into a folder of its own, the
// shape every runbook has now:
//
//   - runbook.yaml with what its den: directives said;
//   - run<ext>, the script itself without those directives, mode kept;
//   - README.md from the comment block at its top.
//
// The script is removed only once all three are written; a failure removes the
// folder again, so nothing is left half moved.
func MigrateScript(path string) (Migration, error) {
	m := PlanMigration(path)
	spec, ok, _ := parseFile(path)
	if !ok {
		return m, fmt.Errorf("%s has no den:name header", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return m, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if _, err := os.Stat(m.Dir); err == nil {
		return m, fmt.Errorf("%s already exists", m.Dir)
	}

	manifest, err := migratedManifest(spec, m, filepath.Dir(path))
	if err != nil {
		return m, err
	}
	script, readme := splitHeader(data)
	if readme == "" {
		readme = spec.Description
	}
	files := []struct {
		name string
		body []byte
		mode os.FileMode
	}{
		{ManifestName, manifest, 0o600},
		{m.Run, script, info.Mode().Perm()},
		{"README.md", []byte(strings.TrimSpace("# "+spec.Name+"\n\n"+strings.TrimSpace(readme)) + "\n"), 0o600},
	}

	if err := os.Mkdir(m.Dir, 0o750); err != nil {
		return m, err
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(m.Dir, f.name), f.body, f.mode); err != nil {
			_ = os.RemoveAll(m.Dir)
			return m, err
		}
	}
	if err := os.Remove(path); err != nil {
		// Both would load, and the folder would win: undo instead.
		_ = os.RemoveAll(m.Dir)
		return m, fmt.Errorf("remove %s: %w", path, err)
	}
	return m, nil
}

// migratedManifest is the runbook.yaml for spec moved by m. A cwd the header
// named relative to the script's directory is rewritten relative to the
// folder; without one the runbook now runs inside its folder.
func migratedManifest(spec config.RunbookSpec, m Migration, oldDir string) ([]byte, error) {
	spec.Command = "./" + m.Run
	if spec.Cwd != "" {
		// The command runs in cwd, so it has to name the script from there.
		abs := resolvePath(spec.Cwd, oldDir)
		run := filepath.Join(m.Dir, m.Run)
		spec.Cwd, spec.Command = abs, shellQuote(run)
		if rel, err := filepath.Rel(m.Dir, abs); err == nil {
			spec.Cwd = rel
		}
		if rel, err := filepath.Rel(abs, run); err == nil {
			if !strings.HasPrefix(rel, "..") {
				rel = "./" + rel
			}
			spec.Command = shellQuote(rel)
		}
	}
	spec.Source, spec.Dir = "", ""

	var b bytes.Buffer
	fmt.Fprintf(&b, "# Moved from %s by den on %s.\n", filepath.Base(m.Script), time.Now().Format("2006-01-02"))
	b.WriteString("# The same keys as an entry of runbooks.items in den.yaml: see docs/runbooks.\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(spec); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// splitHeader returns the script without its den: directives, and the comment
// block at its top — the lines after the shebang that are comments but not
// directives — as markdown for a README. A run of indented lines in it (a list
// of variables, say) becomes a code block, which keeps it aligned.
func splitHeader(data []byte) (script []byte, readme string) {
	var (
		out      bytes.Buffer
		doc      []string
		inHeader = true
	)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 0; scanner.Scan(); n++ {
		line := scanner.Text()
		if n < headerScanLines && headerRe.MatchString(line) {
			continue
		}
		out.WriteString(line + "\n")
		if !inHeader || (n == 0 && strings.HasPrefix(line, "#!")) {
			continue
		}
		text, isComment := commentText(line)
		if !isComment {
			inHeader = false
			continue
		}
		if strings.HasPrefix(text, " ") {
			if len(doc) > 0 && doc[len(doc)-1] != "" && !strings.HasPrefix(doc[len(doc)-1], "    ") {
				doc = append(doc, "")
			}
			text = "   " + text
		}
		doc = append(doc, text)
	}
	return out.Bytes(), strings.TrimSpace(strings.Join(doc, "\n"))
}

// commentText is a comment line's text without its marker and one space.
func commentText(line string) (string, bool) {
	for _, marker := range []string{"//", "#"} {
		if rest, ok := strings.CutPrefix(strings.TrimLeft(line, " \t"), marker); ok {
			return strings.TrimPrefix(rest, " "), true
		}
	}
	return "", false
}

// Migrate moves the header script behind the runbook called name into a folder
// of its own and rescans. The runbook keeps its name, and with it its runner,
// its history and its terminals.
func (r *Registry) Migrate(name string) (Migration, error) {
	run, ok := r.Lookup(name)
	if !ok {
		return Migration{}, fmt.Errorf("no runbook named %q", name)
	}
	rb := run.Runbook()
	switch {
	case rb.Dir != "":
		return Migration{}, fmt.Errorf("%s already has its folder: %s", name, rb.Dir)
	case rb.Source == "":
		return Migration{}, fmt.Errorf("%s is defined in den.yaml: move it into a folder by hand, or keep it there", name)
	case run.IsRunning():
		return Migration{}, fmt.Errorf("%s is running: migrate it once it has finished", name)
	}
	m, err := MigrateScript(rb.Source)
	if err != nil {
		return m, err
	}
	// Its runs move with it, into the folder's logs/. The move itself is done
	// either way: runs left behind are only reported.
	r.mu.RLock()
	h, failed := r.history, r.historyFail
	r.mu.RUnlock()
	if err := h.Adopt(name, Runbook{Dir: m.Dir}.LogsDir()); err != nil && failed != nil {
		failed(fmt.Errorf("runbook %q: not every run moved into %s: %w", name, m.Dir, err))
	}
	r.Rescan()
	run.restore(h)
	return m, nil
}

// HeaderScripts lists the runbooks still defined by a header script.
func (r *Registry) HeaderScripts() []Runbook {
	var out []Runbook
	for _, run := range r.Runbooks() {
		if rb := run.Runbook(); rb.Source != "" && rb.Dir == "" {
			out = append(out, rb)
		}
	}
	return out
}
