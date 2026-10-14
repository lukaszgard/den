package runbooks

import (
	"bytes"
	"cmp"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The Files tab lists a runbook's folder as `tree -l` prints it. enter shows
// the file under the cursor in the tab, rendered as About and Help are; e
// opens it in the editor, in a terminal tab of the view.

// filesLimit is how many entries the Files tab lists: a folder holding a
// node_modules would otherwise be read whole every time the tab refreshes.
const filesLimit = 1000

// filesEvery is how often the Files tab reads the folder again while it is on
// screen, so a file made in a terminal tab shows up.
const filesEvery = 2 * time.Second

// previewLimit is how much of a file the Files tab shows: glamour would take
// seconds over a log or a dump of megabytes.
const previewLimit = 256 << 10

// fileRow is one line of the tree.
type fileRow struct {
	prefix string // the connectors before the name: "│   ├── "
	name   string
	path   string
	link   string // where a symbolic link points, as it is written
	dir    bool
	exec   bool
	runs   int    // den's runs folded into the logs folder's row
	note   string // why a folder was not walked
}

// fileTree is a runbook's folder as the Files tab lists it: the folder itself
// first, then everything under it.
type fileTree struct {
	rows        []fileRow
	dirs, files int
	cut         bool // stopped at the limit
}

// readTree lists root as tree -l does: hidden entries left out, names in order
// whatever their case, linked folders followed unless they lead back into one
// being listed. den's own runs in logsDir fold into its row, as History lists
// them; whatever else a script keeps there is listed.
func readTree(root, logsDir string, limit int) fileTree {
	t := fileTree{rows: []fileRow{{name: ".", path: root, dir: true}}}
	above := map[string]bool{}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		above[real] = true
	}
	t.walk(root, "", logsDir, limit, above)
	return t
}

// walk lists dir's entries below the row of dir, and returns how many of den's
// runs it left out (in logsDir only).
func (t *fileTree) walk(dir, indent, logsDir string, limit int, above map[string]bool) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.rows = append(t.rows, fileRow{prefix: indent + "└── ", note: "[cannot be read]"})
		return 0
	}
	runs := 0
	entries = slices.DeleteFunc(entries, func(e fs.DirEntry) bool {
		if dir == logsDir && runbook.IsRunFile(e.Name()) {
			if filepath.Ext(e.Name()) == ".log" {
				runs++
			}
			return true
		}
		return strings.HasPrefix(e.Name(), ".")
	})
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		if c := cmp.Compare(strings.ToLower(a.Name()), strings.ToLower(b.Name())); c != 0 {
			return c
		}
		return cmp.Compare(a.Name(), b.Name())
	})
	for i, e := range entries {
		if t.dirs+t.files >= limit {
			t.cut = true
			return runs
		}
		branch, next := "├── ", "│   "
		if i == len(entries)-1 {
			branch, next = "└── ", "    "
		}
		path := filepath.Join(dir, e.Name())
		row := fileRow{prefix: indent + branch, name: e.Name(), path: path}
		if e.Type()&fs.ModeSymlink != 0 {
			row.link, _ = os.Readlink(path)
		}
		info, err := os.Stat(path) // through a link, to what it points at
		if err != nil || !info.IsDir() {
			row.exec = err == nil && info.Mode()&0o111 != 0
			t.files++
			t.rows = append(t.rows, row)
			continue
		}
		row.dir = true
		t.dirs++
		real, err := filepath.EvalSymlinks(path)
		if err == nil && above[real] {
			row.note = "[recursive, not followed]"
			t.rows = append(t.rows, row)
			continue
		}
		at := len(t.rows)
		t.rows = append(t.rows, row)
		above[real] = true
		t.rows[at].runs = t.walk(path, indent+next, logsDir, limit, above)
		delete(above, real)
		if t.cut {
			return runs
		}
	}
	return runs
}

// summary is tree's last line: how many folders and files it listed.
func (t fileTree) summary() string {
	s := fmt.Sprintf("%d %s, %d %s", t.dirs, plural(t.dirs, "directory", "directories"),
		t.files, plural(t.files, "file", "files"))
	if t.cut {
		s += fmt.Sprintf(" · cut at %d entries", filesLimit)
	}
	return s
}

// loadFiles reads the runbook's folder. The cursor stays on the file it was
// on, or where that file was when it is gone; at first, on the first entry
// under the folder.
func (v *View) loadFiles(rb runbook.Runbook) {
	on := ""
	if v.filesRead && v.filesCursor < len(v.files.rows) {
		on = v.files.rows[v.filesCursor].path
	} else {
		v.filesCursor = 1
	}
	v.files = readTree(rb.Dir, rb.LogsDir(), filesLimit)
	v.filesAt, v.filesRead = time.Now(), true
	for i, r := range v.files.rows {
		if on != "" && r.path == on {
			v.filesCursor = i
			break
		}
	}
	v.filesCursor = max(min(v.filesCursor, len(v.files.rows)-1), 0)
}

// filesListRows is how many rows of the Files tab the tree takes: all but the
// footer when it is taller than the tab.
func (v View) filesListRows(rows int) int {
	if len(v.files.rows)+2 <= rows {
		return len(v.files.rows)
	}
	return max(rows-1, 1)
}

// clampFiles keeps the cursor inside the tree and on screen.
func (v *View) clampFiles(rows int) {
	v.filesCursor = max(min(v.filesCursor, len(v.files.rows)-1), 0)
	list := v.filesListRows(rows)
	v.filesOffset = scrollOffset(v.filesOffset, v.filesCursor, list+2, len(v.files.rows))
}

// filesKey moves the cursor through the tree, shows the file it is on or
// opens it in the editor; with a file shown, it scrolls it.
func (v View) filesKey(msg tea.KeyPressMsg) (View, tea.Cmd) {
	if v.previewOpen {
		return v.previewKeyPress(msg)
	}
	k := msg.String()
	_, rows := v.bodySize()
	page := max(v.filesListRows(rows)-1, 1)
	switch k {
	case "up", "k":
		v.filesCursor--
	case "down", "j":
		v.filesCursor++
	// A page moves the window with the cursor: the cursor alone would only
	// cross the rows already on screen.
	case "pgup":
		v.filesCursor -= page
		v.filesOffset = max(v.filesOffset-page, 0)
	case "pgdown":
		v.filesCursor += page
		v.filesOffset += page
	case "home", "g":
		v.filesCursor = 0
	case "end", "G":
		v.filesCursor = len(v.files.rows) - 1
	case "enter":
		path, cmd := v.picked()
		if path == "" {
			return v, cmd
		}
		v.previewOpen, v.previewPath, v.previewKey = true, path, ""
		v.refreshPreview()
		v.preview.GotoTop()
		return v, nil
	case "e", "o":
		path, cmd := v.picked()
		if path == "" {
			return v, cmd
		}
		return v, v.editFile(path)
	}
	v.clampFiles(rows)
	return v, nil
}

// previewKeyPress scrolls the file shown, opens it in the editor, or goes
// back to the tree.
func (v View) previewKeyPress(msg tea.KeyPressMsg) (View, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		v.previewOpen = false
	case "e", "o":
		return v, v.editFile(v.previewPath)
	case "home", "g":
		v.preview.GotoTop()
	case "end", "G":
		v.preview.GotoBottom()
	default:
		var cmd tea.Cmd
		v.preview, cmd = v.preview.Update(msg)
		return v, cmd
	}
	return v, nil
}

// editFile opens path in the editor, in a terminal tab named after it.
func (v View) editFile(path string) tea.Cmd {
	name := v.name
	return func() tea.Msg { return msgs.OpenRunbookTerminalMsg{Name: name, File: path} }
}

// picked is the file under the cursor, or "" and what to say when the cursor
// is on something else. The logs folder's row goes to History, where den's
// runs are.
func (v View) picked() (string, tea.Cmd) {
	if v.filesCursor >= len(v.files.rows) {
		return "", nil
	}
	row := v.files.rows[v.filesCursor]
	switch {
	case row.runs > 0:
		if tabs := v.strip.Tabs(); tabs != nil {
			for p := range tabs.Fixed() {
				if tabs.FixedName(p) == tabHistory {
					tabs.Select(p)
				}
			}
		}
		return "", nil
	case row.name == ".":
		return "", flash("this is the runbook's folder — t opens a shell in it")
	case row.dir:
		return "", flash(row.name + "/ is a folder: pick a file in it")
	case row.path == "":
		return "", nil
	}
	if _, err := os.Stat(row.path); err != nil {
		return "", flash(row.name + " is gone")
	}
	return row.path, nil
}

// refreshPreview renders the file shown again when it or the width changed,
// so an edit saved in a terminal tab shows; it goes back to the tree when
// the file is gone.
func (v *View) refreshPreview() {
	if !v.previewOpen {
		return
	}
	info, err := os.Stat(v.previewPath)
	if err != nil {
		v.previewOpen = false
		return
	}
	cols, _ := v.bodySize()
	key := fmt.Sprint(cols, v.light, info.Size(), info.ModTime())
	if key == v.previewKey {
		return
	}
	v.previewKey = key
	v.preview.SetContent(components.RenderMarkdown(previewSource(v.previewPath, info.Size()), cols, !v.light))
}

// previewSource is the file at path as markdown for glamour, which renders
// About and Help too: a markdown file as it is, anything else as a code block
// coloured after the file's name. A binary file is only described.
func previewSource(path string, size int64) string {
	f, err := os.Open(path)
	if err != nil {
		return "It cannot be read: " + err.Error()
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, previewLimit))
	if err != nil {
		return "It cannot be read: " + err.Error()
	}
	if bytes.IndexByte(data[:min(len(data), 8<<10)], 0) >= 0 {
		return fmt.Sprintf("A binary file of %s: it is not shown here.", fileSize(size))
	}
	// Shown as it is written: escape sequences in a file from a source would
	// reach the terminal.
	text := runbook.CleanText(string(data))
	switch {
	case strings.TrimSpace(text) == "":
		return "_It is empty._"
	case !isMarkdown(path):
		text = codeBlock(text, codeLanguage(path))
	}
	if size > previewLimit {
		text += fmt.Sprintf("\n\n_Only its first %s are shown here, of %s._\n", fileSize(previewLimit), fileSize(size))
	}
	return text
}

func isMarkdown(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

// codeLanguage names the language of a code block for the file at path:
// chroma takes a file's name and colours it by its extension or, for a
// Makefile or a Dockerfile, the name itself; a script without either it
// recognises by its #! line.
func codeLanguage(path string) string {
	base := filepath.Base(path)
	if strings.ContainsAny(base, " \t`") {
		return strings.TrimPrefix(filepath.Ext(base), ".")
	}
	return base
}

// codeBlock fences text as code, with a fence longer than any run of
// backticks in it. Tabs become spaces: lipgloss measures a tab as one column.
func codeBlock(text, lang string) string {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	text = strings.ReplaceAll(text, "\t", "    ")
	return fence + lang + "\n" + strings.TrimRight(text, "\n") + "\n" + fence + "\n"
}

// fileSize is n bytes for people.
func fileSize(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

// previewBody is a file shown in the Files tab: its name above it, where it
// is and how far down below.
func (v View) previewBody(rows int) string {
	width := v.width - 4
	head := "── " + filepath.Base(v.previewPath)
	if info, err := os.Stat(v.previewPath); err == nil {
		head += " · " + fileSize(info.Size())
	}
	head += " "
	if n := lipgloss.Width(head); n < width {
		head += strings.Repeat("─", width-n)
	}
	foot := ""
	if v.preview.TotalLineCount() > v.preview.Height() {
		foot = fmt.Sprintf("scroll %3.0f%%", v.preview.ScrollPercent()*100)
	}
	// The path loses its start, not its file name, when it is too long.
	where := truncateLeft(tildePath(v.previewPath), max(width-lipgloss.Width(foot)-3, 1))
	gap := max(width-1-lipgloss.Width(where)-lipgloss.Width(foot), 1)
	return dim.Render(truncate(head, width)) + "\n" +
		fitLines(v.preview.View(), max(rows-2, 1)) + "\n" +
		dim.Render(" "+where) + strings.Repeat(" ", gap) + muted.Render(foot)
}

// filesBody is the Files tab: the tree, then its summary, or, when the tree
// is taller than the tab, a footer with the summary and how far down it is.
func (v View) filesBody(rows int) string {
	e, ok := lookup(v.reg, v.name)
	if !ok || e.isSeq() {
		return ""
	}
	if v.previewOpen {
		return v.previewBody(rows)
	}
	rb := e.runner.Runbook()
	summary := v.files.summary()
	if rb.Remote != nil {
		summary += " · from " + rb.Remote.Source + ": an edit here is lost when it updates"
	}
	width := v.width - 4
	list := v.filesListRows(rows)
	var lines []string
	for i := v.filesOffset; i < min(v.filesOffset+list, len(v.files.rows)); i++ {
		lines = append(lines, components.Zone(components.ZoneID("file", i), lipgloss.NewStyle().MaxWidth(width).Render(v.fileLine(i))))
	}
	if list == len(v.files.rows) {
		return strings.Join(append(lines, "", dim.Render(truncate(" "+summary, width))), "\n")
	}
	scroll := fmt.Sprintf("scroll %3.0f%%", v.filesPercent(list))
	left := truncate(" "+summary, max(width-lipgloss.Width(scroll)-2, 1))
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(scroll), 1)
	foot := dim.Render(left) + strings.Repeat(" ", gap) + muted.Render(scroll)
	return fitLines(strings.Join(lines, "\n"), list) + "\n" + foot
}

// filesPercent is how far down the tree the tab is scrolled, as a viewport
// reports it: 0 at the top, 100 with its last row on screen.
func (v View) filesPercent(list int) float64 {
	hidden := len(v.files.rows) - list
	if hidden <= 0 {
		return 100
	}
	return float64(min(v.filesOffset, hidden)) / float64(hidden) * 100
}

// fileLine is row i of the tree, with the cursor when it is on it.
func (v View) fileLine(i int) string {
	row := v.files.rows[i]
	cursor := "  "
	if i == v.filesCursor {
		cursor = accent.Render("▸ ")
	}
	name := row.name
	switch {
	case row.dir:
		if name != "." {
			name += "/"
		}
		name = lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render(name)
	case row.exec:
		name = lipgloss.NewStyle().Foreground(colorOK).Render(name)
	}
	line := cursor + dim.Render(row.prefix) + name
	if row.link != "" {
		line += dim.Render(" -> " + row.link)
	}
	if row.runs > 0 {
		line += "  " + dim.Render(fmt.Sprintf("%d %s · History", row.runs, plural(row.runs, "run", "runs")))
	}
	if row.note != "" {
		line += " " + dim.Render(row.note)
	}
	return line
}
