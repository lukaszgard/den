package runbooks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The fixed tabs of the runbook view, before its terminals.
const (
	tabLogs    = "Logs"
	tabHistory = "History"
	tabAbout   = "About"
	tabFiles   = "Files"
)

// FixedTabs are the tabs the view of the runbook or sequence called name has
// before its terminals. The root model makes the Tabs it owns with them.
func FixedTabs(reg *runbook.Registry, name string) []string {
	tabs := []string{tabLogs, tabHistory}
	e, ok := lookup(reg, name)
	if !ok || e.isSeq() || e.runner.Runbook().Settings.About {
		tabs = append(tabs, tabAbout)
	}
	// Only a runbook with a folder of its own: the others live in a shared
	// directory, or wherever den.yaml says they run.
	if ok && !e.isSeq() && e.runner.Runbook().Dir != "" {
		tabs = append(tabs, tabFiles)
	}
	return tabs
}

// View is the full-screen view of one runbook or sequence. It is built like
// the service detail view, and behaves like it: a header, then tabs — the
// logs of the current or last run, the runs kept on disk each with its own
// log, what the runbook is and what den hands it, the files in its folder —
// and the terminals opened in the runbook's folder, which the root model owns
// so they outlive the view.
type View struct {
	reg  *runbook.Registry
	ctx  context.Context
	name string

	strip components.TabStrip
	flow  flow
	mig   migration
	fork  forkQuestion
	del   deletion
	// resend asks, before an agent's tab restarts, whether to send it its
	// request again.
	resend resendQuestion
	// ask is the question E asks: what the agent should change.
	ask *newForm

	logs    components.LogView
	logsKey string // what the log view holds, so it is refilled only on change
	// saved caches the output of the run restored from disk, by its start.
	saved    []string
	savedFor time.Time

	// History lists the runs kept on disk with the selected one's log under
	// them, or, once opened, that log alone.
	history     []runbook.Entry
	historyFor  time.Time // when the last run finished as history was read
	historyFrom time.Time // when the last run started as history was read
	historyRead bool
	histCursor  int
	histLog     components.LogView
	histKey     string // what histLog holds, so it is refilled only on change
	histFor     string // the log histLog shows, to start a newly picked one at its end
	histOpen    bool

	about    viewport.Model
	aboutKey string // the width and source it was rendered for

	// Files lists the runbook's folder as a tree, the cursor on one entry.
	files       fileTree
	filesAt     time.Time // when the folder was last read
	filesRead   bool
	filesCursor int
	filesOffset int
	// The file enter shows in the tab, instead of the tree, until esc.
	preview     viewport.Model
	previewOpen bool
	previewPath string
	previewKey  string // the width, size and time of the file it was rendered for

	lastTab       string
	width, height int
	// light is set once the terminal reported a light background; the zero
	// value is the dark palette, which shows until it replies.
	light bool
}

// WithOutput sends every line of the runs the view starts to out, as the
// pane's WithOutput does.
func (v View) WithOutput(out func(name, line string)) View {
	v.flow.out = out
	return v
}

// NewView opens the view of the runbook or sequence called name over its
// tabs, for a screen of width×height.
func NewView(reg *runbook.Registry, ctx context.Context, name string, tabs *components.Tabs, width, height int) View {
	v := View{
		reg:     reg,
		ctx:     ctx,
		name:    name,
		flow:    flow{reg: reg, ctx: ctx},
		mig:     migration{reg: reg},
		fork:    forkQuestion{reg: reg},
		del:     deletion{reg: reg},
		logs:    components.NewLogView(width-4, 1),
		histLog: components.NewLogView(width-4, 1),
		about:   components.NewViewport(width-4, 1),
		preview: components.NewViewport(width-4, 1),
		width:   width,
		height:  height,
	}
	v.strip = components.NewTabStrip(tabs, func(tabID int) tea.Cmd {
		return func() tea.Msg { return msgs.OpenRunbookTerminalMsg{Name: name, TabID: tabID} }
	})
	v = v.refresh()
	v.fit()
	return v
}

// SetDark switches the view to the palette of a dark or a light background,
// which the terminal reports once the program runs.
func (v View) SetDark(isDark bool) View {
	v.light = !isDark
	v.flow.light = v.light
	return v.refresh()
}

// Dark reports whether the view draws for a dark background.
func (v View) Dark() bool { return !v.light }

// Name is the runbook or sequence on screen.
func (v View) Name() string { return v.name }

// OwnsBack reports that esc belongs to the view: a run's log is open full
// height in History, or a file in Files, and esc goes back to the runs or the
// tree before leaving.
func (v View) OwnsBack() bool {
	return (v.tab() == tabHistory && v.histOpen) || (v.tab() == tabFiles && v.previewOpen)
}

// CapturingKeys reports that the view needs every key: a terminal is typed
// into, a tab renamed, a close or a run confirmed, or a parameter answered.
// The root model must then not act on esc, h or q.
func (v View) CapturingKeys() bool {
	return v.strip.Capturing() || v.flow.active() || v.mig.active() || v.fork.active() || v.del.active() || v.ask != nil ||
		v.resend.active()
}

// AwaitingForm reports that a form is up, the parameters' or the request E
// asks, and needs every message.
func (v View) AwaitingForm() bool { return v.flow.awaitingForm() || v.ask != nil }

// StartTyping hands the keyboard to the terminal on screen, if one is.
func (v View) StartTyping() View {
	v.strip.StartTyping()
	return v
}

// StopTyping gives den the keyboard back, as when the terminal that had it
// closed.
func (v View) StopTyping() View {
	v.strip.Leave()
	return v
}

// RunningTerminals counts the terminals whose program still runs.
func (v View) RunningTerminals() int {
	if t := v.strip.Tabs(); t != nil {
		return t.Running()
	}
	return 0
}

// TerminalSize is the size a terminal opened in this view starts with.
func (v View) TerminalSize() (cols, rows int) { return v.bodySize() }

// chromeLines are the rows under the box: a blank line and the help line.
const chromeLines = 2

func (v View) boxInnerHeight() int { return max(v.height-chromeLines-2, 10) }

func (v View) bodySize() (cols, rows int) {
	cols, rows, _ = v.bodyArea(v.upperSection())
	return cols, rows
}

// bodyArea is bodySize for the already rendered sections above the tab bar,
// and the row of the screen the body starts on: under the box's top border,
// those sections and the tab bar.
func (v View) bodyArea(upperSection string) (cols, rows, top int) {
	upper := lipgloss.Height(lipgloss.NewStyle().Width(v.width - 2).Render(upperSection))
	return v.width - 4, max(v.boxInnerHeight()-upper-1, 1), 1 + upper + 1
}

// fit sizes the body's views, and the terminal on screen, to the box.
func (v *View) fit() {
	cols, rows := v.bodySize()
	v.logs.Resize(cols, rows)
	v.histLog.Resize(cols, v.histLogRows(rows))
	v.about.SetWidth(cols)
	v.about.SetHeight(rows)
	v.clampFiles(rows)
	v.preview.SetWidth(cols)
	v.preview.SetHeight(max(rows-2, 1))
	if term := v.strip.Current(); term != nil {
		term.Resize(cols, rows)
	}
}

// tab is the fixed tab on screen, or "" while a terminal is.
func (v View) tab() string {
	tabs := v.strip.Tabs()
	if tabs == nil {
		return tabLogs
	}
	return tabs.FixedName(tabs.Selected())
}

func (v View) Update(msg tea.Msg) (View, tea.Cmd) {
	if v.ask != nil {
		return v.updateAsk(msg)
	}
	if v.flow.awaitingForm() {
		var cmd tea.Cmd
		v.flow, cmd = v.flow.update(msg)
		return v, cmd
	}
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
	case msgs.TickMsg:
	case tea.KeyPressMsg:
		v, cmd = v.key(msg)
	// Pasted text goes to the terminal being typed into or the name being
	// edited. The pop-ups that ask something take keys, not text, and a form
	// has had the message above.
	case tea.PasteMsg:
		if !v.mig.active() && !v.fork.active() && !v.del.active() && !v.flow.active() && !v.resend.active() {
			_, cmd = v.strip.Paste(msg)
		}
	}
	v = v.refresh()
	v.fit()
	return v, cmd
}

func (v View) key(msg tea.KeyPressMsg) (View, tea.Cmd) {
	// tab in a pop-up does nothing, as in the list: the parameter form would
	// take it for "next", which on its last field starts the run.
	if msg.String() == "tab" && (v.flow.active() || v.mig.active() || v.fork.active() || v.del.active() || v.resend.active()) {
		return v, nil
	}
	if v.flow.active() {
		var cmd tea.Cmd
		v.flow, cmd = v.flow.update(msg)
		return v, cmd
	}
	if v.mig.active() {
		var cmd tea.Cmd
		v.mig, cmd = v.mig.key(msg)
		return v, cmd
	}
	if v.fork.active() {
		var cmd tea.Cmd
		v.fork, cmd = v.fork.key(msg)
		return v, cmd
	}
	if v.resend.active() {
		var cmd tea.Cmd
		v.resend, cmd = v.resend.key(msg)
		return v, cmd
	}
	if v.del.active() {
		var cmd tea.Cmd
		v.del, cmd = v.del.key(msg)
		return v, cmd
	}
	e, ok := lookup(v.reg, v.name)
	if !ok {
		return v, nil
	}
	k := msg.String()
	if k == "E" && !v.strip.Capturing() {
		return v.EditWithAgent("")
	}
	if e.isSeq() && (k == "t" || k == "e") && !v.strip.Capturing() {
		return v, flash("a sequence has no folder of its own: open one of its steps")
	}
	if handled, cmd := v.strip.Key(msg); handled {
		return v, cmd
	}

	switch k {
	case "r":
		var cmd tea.Cmd
		v.flow, cmd = v.flow.begin(e, v.width, v.height)
		return v, cmd
	case "x":
		if e.isRunning() {
			e.cancel()
			return v, flash(fmt.Sprintf("cancelling %s…", e.name()))
		}
		return v, nil
	case "m":
		var cmd tea.Cmd
		v.mig, cmd = v.mig.begin(e)
		return v, cmd
	case "D":
		var cmd tea.Cmd
		v.del, cmd = v.del.begin(e, v.strip.Tabs().Running())
		return v, cmd
	case "e":
		// On Files, e edits the file picked, in place even in a source's
		// checkout: that is what Files is for.
		if v.tab() == tabFiles {
			return v.filesKey(msg)
		}
		if rb := e.runner.Runbook(); rb.Remote != nil {
			v.fork = v.fork.begin(rb)
			return v, nil
		}
		name := v.name
		return v, func() tea.Msg { return msgs.OpenRunbookTerminalMsg{Name: name, Edit: true} }
	case "o":
		if tab := v.tab(); tab == tabLogs || tab == tabHistory {
			return v, v.openLog(e)
		}
		if v.tab() == tabFiles {
			return v.filesKey(msg)
		}
	case "y":
		line := "den run " + quoteArg(v.name)
		return v, components.Copy(line, "copied: "+line)
	}

	switch v.tab() {
	case tabLogs:
		return v, v.logs.Update(msg)
	case tabHistory:
		if v.histOpen {
			if k == "esc" || k == "backspace" {
				v.histOpen = false
				return v, nil
			}
			return v, v.histLog.Update(msg)
		}
		switch k {
		case "up", "k":
			v.histCursor = max(v.histCursor-1, 0)
		case "down", "j":
			v.histCursor = max(min(v.histCursor+1, len(v.history)-1), 0)
		case "enter":
			v.histOpen = len(v.history) > 0
		}
	case tabAbout:
		var cmd tea.Cmd
		v.about, cmd = v.about.Update(msg)
		return v, cmd
	case tabFiles:
		return v.filesKey(msg)
	}
	return v, nil
}

// openLog opens the log file of the run on screen in the editor, in a terminal
// tab of the view: the run Logs shows, or the one picked in History.
func (v View) openLog(e entry) tea.Cmd {
	st := e.state()
	path := ""
	switch {
	case v.tab() == tabHistory && v.histCursor < len(v.history):
		path = v.history[v.histCursor].LogPath()
	case v.tab() == tabLogs:
		path = st.LogPath
	}
	switch {
	case path == "" && st.Status == runbook.StatusNeverRun:
		return flash("nothing run yet — press r to run it")
	case path == "" && v.tab() == tabHistory:
		return flash("no run kept on disk yet")
	case path == "":
		return flash("this run is not kept on disk")
	case e.isSeq():
		return flash("a sequence has no terminals of its own: its log is " + tildePath(path))
	}
	if _, err := os.Stat(path); err != nil {
		return flash("its log is gone: " + tildePath(path))
	}
	name := v.name
	return func() tea.Msg {
		file, scratch, err := plainLog(path)
		if err != nil {
			return flash("its log could not be opened: " + err.Error())()
		}
		return msgs.OpenRunbookTerminalMsg{Name: name, File: file, Scratch: scratch}
	}
}

// plainLog is the log at path as the editor should open it. A run's log holds
// the colours of its script as escape codes, which an editor shows as ^[[32m
// between the words: when it has any, a copy without them, in a directory of
// its own (scratch) for the caller to remove. Without any, the log itself.
func plainLog(path string) (file, scratch string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	plain := ansi.Strip(string(data))
	if len(plain) == len(data) {
		return path, "", nil
	}
	scratch, err = os.MkdirTemp("", "den-log-")
	if err != nil {
		return "", "", err
	}
	file = filepath.Join(scratch, filepath.Base(path))
	if err := os.WriteFile(file, []byte(plain), 0o600); err != nil {
		_ = os.RemoveAll(scratch)
		return "", "", err
	}
	return file, scratch, nil
}

// refresh brings the tab on screen up to date with the registry and the disk,
// doing work only when something changed: it runs on every tick.
func (v View) refresh() View {
	e, ok := lookup(v.reg, v.name)
	if !ok {
		return v
	}
	tab := v.tab()
	entered := tab != v.lastTab
	v.lastTab = tab
	st := e.state()
	switch tab {
	case tabLogs:
		v.refreshLogs(st)
	case tabHistory:
		if entered {
			v.histOpen = false
		}
		// A run that started shows up at the top as it goes; one that
		// finished gets its final state.
		if entered || !v.historyRead || !st.StartedAt.Equal(v.historyFrom) ||
			(st.Status.Terminal() && !st.FinishedAt.Equal(v.historyFor)) {
			v.loadHistory(st)
		}
		v.refreshHistLog(st)
	case tabAbout:
		cols, _ := v.bodySize()
		if src := aboutSource(v.reg, e); entered || v.aboutKey != fmt.Sprint(cols, v.light, src) {
			v.aboutKey = fmt.Sprint(cols, v.light, src)
			v.about.SetContent(components.RenderMarkdown(src, cols, !v.light))
		}
	case tabFiles:
		if !e.isSeq() && (entered || time.Since(v.filesAt) >= filesEvery) {
			v.loadFiles(e.runner.Runbook())
		}
		v.refreshPreview()
	}
	return v
}

func (v *View) refreshLogs(st runbook.Run) {
	var (
		lines []string
		key   string
	)
	switch {
	case st.Saved != nil:
		lines = v.savedLogs(*st.Saved)
		head := fmt.Sprintf("── last run, %s · from %s ──", st.Saved.StartedAt.Local().Format("Mon 2 Jan 15:04"), orDash(st.Saved.Origin))
		lines = append([]string{head}, lines...)
		key = "saved " + st.Saved.StartedAt.String()
	case st.Status == runbook.StatusNeverRun:
		lines = []string{"Nothing yet — press r to run it."}
		key = "never"
	default:
		lines = st.Logs
		last := ""
		if len(lines) > 0 {
			last = lines[len(lines)-1]
		}
		key = fmt.Sprint("live ", st.StartedAt, len(lines), last, st.Status)
	}
	if key != v.logsKey {
		v.logsKey = key
		v.logs.SetContent(lines)
	}
}

// savedLogs reads a kept run's output, once.
func (v *View) savedLogs(e runbook.Entry) []string {
	if !e.StartedAt.Equal(v.savedFor) || v.saved == nil {
		lines, err := v.reg.History().Load(e)
		if err != nil {
			lines = []string{"(its output could not be read: " + err.Error() + ")"}
		}
		v.saved, v.savedFor = append([]string{}, lines...), e.StartedAt
	}
	return v.saved
}

// loadHistory reads the runs kept on disk. The cursor stays on the run it was
// on, or on the newest one, following a run that just started.
func (v *View) loadHistory(st runbook.Run) {
	var on time.Time
	if v.histCursor > 0 && v.histCursor < len(v.history) {
		on = v.history[v.histCursor].StartedAt
	}
	v.history, _ = v.reg.Runs(v.name)
	v.historyFor, v.historyFrom, v.historyRead = st.FinishedAt, st.StartedAt, true
	v.histCursor = min(v.histCursor, max(len(v.history)-1, 0))
	if on.IsZero() {
		v.histCursor = 0
		return
	}
	for i, h := range v.history {
		if h.StartedAt.Equal(on) {
			v.histCursor = i
			return
		}
	}
}

// refreshHistLog fills the log under the runs with the selected run's: the
// live output while it is this den's run in flight, its file otherwise, read
// again only when the file has grown.
func (v *View) refreshHistLog(st runbook.Run) {
	if v.histCursor >= len(v.history) {
		v.histOpen = false
		return
	}
	sel := v.history[v.histCursor]
	var (
		lines []string
		key   string
	)
	if sel.Running() && st.Status == runbook.StatusRunning && sel.LogPath() == st.LogPath {
		lines = st.Logs
		last := ""
		if len(lines) > 0 {
			last = lines[len(lines)-1]
		}
		key = fmt.Sprint("live ", st.StartedAt, len(lines), last)
	} else {
		key = sel.LogPath()
		if info, err := os.Stat(sel.LogPath()); err == nil {
			key += fmt.Sprint(" ", info.Size(), info.ModTime())
		}
		if key == v.histKey {
			return
		}
		var err error
		if lines, err = v.reg.History().Load(sel); err != nil {
			lines = []string{"(its output could not be read: " + err.Error() + ")"}
		}
	}
	if key == v.histKey {
		return
	}
	v.histKey = key
	v.histLog.SetContent(lines)
	if v.histFor != sel.LogPath() {
		v.histFor = sel.LogPath()
		v.histLog.GotoBottom()
	}
}

// --- rendering ---

func (v View) View() string {
	view, _ := v.Render()
	return view
}

// Render is View with the cursor of the terminal being typed into, from the
// top left of the view, or nil when there is none to show.
func (v View) Render() (view string, cursor *tea.Cursor) {
	upper := v.upperSection()
	cols, rows, top := v.bodyArea(upper)
	body := v.body(rows)
	if v.strip.Current() != nil {
		cursor = v.strip.Cursor(1, top, cols, rows) // 1: the box's left border
	}
	content := strings.Join([]string{upper, v.strip.Bar(v.width - 4), body}, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Width(v.width).
		Height(v.boxInnerHeight() + 2).
		Render(content)
	frame := lipgloss.JoinVertical(lipgloss.Left,
		box,
		"",
		// A line wider than the terminal would wrap and push the frame's top
		// off screen; the keys at its end are the ones that can be spared.
		lipgloss.NewStyle().MaxWidth(v.width).Render(v.helpLine()),
	)
	// A pop-up covers part of the screen, and the cursor of what is under it
	// would show through.
	switch {
	case v.flow.active():
		return components.Overlay(frame, v.flow.view(v.width, v.height), v.width, v.height), nil
	case v.mig.active():
		return components.Overlay(frame, v.mig.view(v.width), v.width, v.height), nil
	case v.fork.active():
		return components.Overlay(frame, v.fork.view(v.width), v.width, v.height), nil
	case v.del.active():
		return components.Overlay(frame, v.del.view(v.width), v.width, v.height), nil
	case v.resend.active():
		return components.Overlay(frame, v.resend.view(v.width), v.width, v.height), nil
	case v.ask != nil:
		v.ask.setSize(v.width, v.height)
		return components.Overlay(frame, v.ask.View(), v.width, v.height), nil
	}
	return frame, cursor
}

func (v View) body(rows int) string {
	if term := v.strip.Current(); term != nil {
		return fitLines(term.View(), rows)
	}
	switch v.tab() {
	case tabHistory:
		return fitLines(v.historyBody(rows), rows)
	case tabAbout:
		return fitLines(v.about.View(), rows)
	case tabFiles:
		return fitLines(v.filesBody(rows), rows)
	}
	return fitLines(v.logs.View(), rows)
}

func (v View) historyBody(rows int) string {
	e, ok := lookup(v.reg, v.name)
	switch {
	case v.reg.History() == nil:
		return dim.Render(" Run history is not kept in this session.")
	case len(v.history) == 0 && ok && !e.isSeq() && e.runner.Runbook().Settings.History == 0:
		return dim.Render(" This runbook keeps no runs: its history is 0.")
	case len(v.history) == 0:
		return dim.Render(" No runs kept yet.")
	}
	if v.histOpen {
		return v.histDivider() + "\n" + v.histLog.View()
	}
	list := v.histListRows(rows)
	// Runs that do not all fit get a row above and below saying how many
	// more there are: without them the list looked like all there was.
	more := len(v.history) > list
	visible := list + 2
	if more {
		visible = list
	}
	offset := scrollOffset(0, v.histCursor, visible, len(v.history))
	end := min(offset+max(visible-2, 1), len(v.history))
	var lines []string
	if offset > 0 {
		above := fmt.Sprintf("   ↑ %d more", offset)
		if end == len(v.history) {
			above += " · " + v.histKept(e, ok)
		}
		lines = append(lines, dim.Render(above))
	}
	for i := offset; i < end; i++ {
		h := v.history[i]
		run := h.Run()
		cursor := "  "
		if i == v.histCursor {
			cursor = accent.Render("▸ ")
		}
		params := make([]string, 0, len(h.Params))
		for k, val := range h.Params {
			params = append(params, k+"="+val)
		}
		sort.Strings(params)
		took := fmtDuration(run.Duration())
		if h.Interrupted() {
			took = "–" // it never got an end
		}
		line := fmt.Sprintf("%s%s %s %s  %s  %s  %s", cursor, statusDot(run.Status),
			lipgloss.NewStyle().Foreground(statusColor(run.Status)).Render(pad(runLabel(h), 11)),
			pad(took, 7),
			h.StartedAt.Local().Format("Mon 2 Jan 15:04"),
			muted.Render(pad(orDash(h.Origin), 8)),
			dim.Render(strings.Join(params, " ")))
		lines = append(lines, lipgloss.NewStyle().MaxWidth(v.width-4).Render(line))
	}
	if end < len(v.history) {
		lines = append(lines, dim.Render(fmt.Sprintf("   ↓ %d more · %s", len(v.history)-end, v.histKept(e, ok))))
	}
	if list == rows {
		return strings.Join(lines, "\n")
	}
	return fitLines(strings.Join(lines, "\n"), list) + "\n" + v.histDivider() + "\n" + v.histLog.View()
}

// histListRows is how many rows of History the runs take: as many as there
// are, up to two thirds of it, so the selected run's log has the rest; all of
// it when too short for both.
func (v View) histListRows(rows int) int {
	if rows < 8 {
		return rows
	}
	return min(len(v.history), max(3, rows*2/3))
}

// histKept is how many runs are kept, against how many the runbook keeps. A
// sequence has no history of its own, and a runbook whose history was just
// lowered keeps more until its next run prunes them.
func (v View) histKept(e entry, ok bool) string {
	n := len(v.history)
	if ok && !e.isSeq() {
		if keep := e.runner.Runbook().Settings.History; keep >= n {
			return fmt.Sprintf("%d of %d %s kept", n, keep, plural(keep, "run", "runs"))
		}
	}
	return fmt.Sprintf("%d %s kept", n, plural(n, "run", "runs"))
}

// histLogRows is how tall the log in History is, under its divider.
func (v View) histLogRows(rows int) int {
	if v.histOpen {
		return max(rows-1, 1)
	}
	return max(rows-v.histListRows(rows)-1, 1)
}

// histDivider names the run whose log is under it, and the file it is kept in.
func (v View) histDivider() string {
	h := v.history[v.histCursor]
	text := fmt.Sprintf("── %s · %s · from %s · ", h.StartedAt.Local().Format("Mon 2 Jan 15:04"),
		runLabel(h), orDash(h.Origin))
	width := v.width - 4
	// The path loses its start, not its file name, when it is too long.
	text += truncateLeft(tildePath(h.LogPath()), width-lipgloss.Width(text)-1) + " "
	if n := lipgloss.Width(text); n < width {
		text += strings.Repeat("─", width-n)
	}
	return dim.Render(truncate(text, width))
}

// truncateLeft shortens s to n runes by cutting its start.
func truncateLeft(s string, n int) string {
	r := []rune(s)
	switch {
	case len(r) <= n:
		return s
	case n < 2:
		return ""
	}
	return "…" + string(r[len(r)-n+1:])
}

// runLabel is how a kept run ended, in a word or two.
func runLabel(h runbook.Entry) string {
	if h.Interrupted() {
		return "interrupted"
	}
	return h.Run().Summary()
}

// upperSection is everything in the box above the tabs: what the runbook is,
// how its last run went, where it lives and what it runs.
func (v View) upperSection() string {
	e, ok := lookup(v.reg, v.name)
	if !ok {
		return " " + lipgloss.NewStyle().Bold(true).Render(v.name) + dim.Render("  — no longer defined")
	}
	st := e.state()
	badge := "runbook"
	if e.isSeq() {
		badge = "sequence"
	}
	head := fmt.Sprintf(" %s %s  %s %s",
		lipgloss.NewStyle().Foreground(lipgloss.Color("#1a1a1a")).Background(colorAccent).Padding(0, 1).Render(badge),
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFDF5")).Render(v.name),
		statusDot(st.Status),
		lipgloss.NewStyle().Bold(true).Foreground(statusColor(st.Status)).Render(statusLine(e, st)))
	if w := e.where(); w != "" && !e.isSeq() {
		head += "   " + muted.Render("env ") + w
	}

	code := lipgloss.NewStyle().Foreground(colorRunning).Background(lipgloss.Color("#1a1a1a")).Padding(0, 1)
	var where, facts []string
	if e.isSeq() {
		where = append(where, " "+code.Render(truncate(strings.Join(e.seq.Sequence().Steps, " → "), v.width-30)))
	} else {
		rb := e.runner.Runbook()
		first, _, _ := strings.Cut(strings.TrimSpace(rb.Command), "\n")
		where = append(where, " "+muted.Render(truncate(tildePath(rb.Home()), max(v.width/3, 20)))+"  "+code.Render(truncate(first, v.width/2)))
		facts = append(facts, e.kind())
		if rb.Timeout > 0 {
			facts = append(facts, "timeout "+rb.Timeout.String())
		}
		if rb.Retry != nil && rb.Retry.UntilSuccess {
			facts = append(facts, "retries every "+rb.Retry.Interval.String())
		}
		if n := len(rb.Params); n > 0 {
			facts = append(facts, fmt.Sprintf("%d %s", n, plural(n, "parameter", "parameters")))
		}
		facts = append(facts, keeps(rb.Settings.History))
	}
	if e.needsConfirm() {
		facts = append([]string{lipgloss.NewStyle().Foreground(colorWarn).Render("! asks before running")}, facts...)
	}
	if e.oldFormat() {
		// Its folder is the shared runbooks directory, which is also where t
		// opens: the reason to move it.
		where = append(where, " "+lipgloss.NewStyle().Foreground(colorWarn).Render("single script in "+truncate(tildePath(e.runner.Runbook().Home()), max(v.width-60, 12)))+
			dim.Render(" · ")+components.Hints(components.HelpKey{Key: "m", Text: "move it into its own folder"}))
	}
	where = append(where, " "+dim.Render(strings.Join(facts, " · "))+dim.Render(" · ")+
		components.Hints(components.HelpKey{Key: "y", Text: "copy den run"}))

	rule := dim.Render(strings.Repeat("─", max(v.width-4, 1)))
	return strings.Join(append(append([]string{head, rule}, where...), rule), "\n")
}

func keeps(n int) string {
	if n == 0 {
		return "keeps no runs"
	}
	return fmt.Sprintf("keeps %d %s", n, plural(n, "run", "runs"))
}

// statusLine is the header's account of the last run.
func statusLine(e entry, st runbook.Run) string {
	switch st.Status {
	case runbook.StatusNeverRun:
		return "NEVER RUN"
	case runbook.StatusRunning:
		return strings.ToUpper(result(e, st))
	}
	parts := []string{strings.ToUpper(st.Summary())}
	if d := fmtDuration(st.Duration()); d != "" {
		parts = append(parts, d)
	}
	if !st.FinishedAt.IsZero() {
		parts = append(parts, st.FinishedAt.Local().Format("Mon 15:04"))
	}
	return strings.Join(parts, " · ")
}

// helpLine lists the keys for what has the keyboard right now.
func (v View) helpLine() string {
	if v.flow.active() {
		return "  " + components.Hints(v.flow.help()...)
	}
	if v.mig.active() {
		return "  " + components.Hints(key("y", "move"), key("n", "keep"))
	}
	if v.fork.active() {
		return "  " + components.Hints(key("y", "copy"), key("n", "keep"))
	}
	if v.del.active() {
		return "  " + components.Hints(key("y", "delete"), key("n", "keep"))
	}
	if v.resend.active() {
		return "  " + components.Hints(resendKeys...)
	}
	if v.ask != nil && v.ask.asking {
		return "  " + components.Hints(key("y", "keep"), key("n", "discard"), key("esc", "back to it"))
	}
	if v.ask != nil {
		return "  " + components.Hints(key("enter", "start"), key("ctrl+j", "new line"), key("esc", "cancel"))
	}
	if line, ok := v.strip.ModeHelp(components.HelpKey{Key: "esc", Text: "back"}); ok {
		return line
	}
	e, _ := lookup(v.reg, v.name)
	keys := []components.HelpKey{{Key: "h", Text: "help"}, {Key: "r", Text: "run"}}
	files := v.tab() == tabFiles // where e edits the file picked
	if e.runner != nil || e.seq != nil {
		if e.isRunning() {
			keys = append(keys, components.HelpKey{Key: "x", Text: "cancel"})
		}
		switch {
		case e.isSeq():
		case files:
			keys = append(keys, key("t", "shell here"))
		case e.runner.Runbook().Remote != nil:
			keys = append(keys, key("t", "shell here"), key("e", "copy to edit"))
		default:
			keys = append(keys, key("t", "shell here"), key("e", "edit"))
		}
		if !e.isSeq() && !files {
			keys = append(keys, key("E", "edit with "+editHint(v.reg, e.runner.Runbook())))
		}
		if e.oldFormat() {
			keys = append(keys, key("m", "move into a folder"))
		}
		keys = append(keys, key("D", "delete"))
	}
	switch v.tab() {
	case tabLogs:
		keys = append(keys, key("o", "log in editor"))
	case tabHistory:
		if v.histOpen {
			keys = append(keys, key("↑↓", "scroll"), key("o", "log in editor"), key("←→", "tabs"), key("esc", "runs"))
			return "  " + components.Hints(keys...)
		}
		keys = append(keys, key("↑↓", "select"), key("enter", "whole log"), key("o", "log in editor"))
	case tabFiles:
		if v.previewOpen {
			keys = append(keys, key("↑↓", "scroll"), key("e", "edit in editor"), key("←→", "tabs"), key("esc", "files"))
			return "  " + components.Hints(keys...)
		}
		keys = append(keys, key("↑↓", "select"), key("enter", "view"), key("e", "edit in editor"))
	}
	keys = append(keys, key("←→", "tabs"), key("esc", "back"))
	return "  " + components.Hints(keys...)
}

// Shortcuts lists the keys of the view for the shortcuts pop-up.
func (v View) Shortcuts() []components.ShortcutGroup {
	e, _ := lookup(v.reg, v.name)
	run := []components.Shortcut{
		{Keys: "r", Text: "Run it (parameters and confirmation first)"},
		{Keys: "x", Text: "Cancel the run (on Logs, History, About, Files)"},
		{Keys: "y", Text: "Copy the den run command for it"},
		{Keys: "o", Text: "Open the run's log in the editor (Logs, History)"},
	}
	if e.seq == nil {
		run = append(run,
			components.Shortcut{Keys: "t", Text: "New terminal: a shell in its folder, with its env"},
			components.Shortcut{Keys: "e", Text: "Open its script in the editor, in a tab (on Files: the file picked)"},
			components.Shortcut{Keys: "E", Text: "Ask its agent to change it: " + editHint(v.reg, e.runner.Runbook()) + " (agent: in runbook.yaml or runbooks.defaults)"},
			components.Shortcut{Keys: "m", Text: "Move a one-script runbook into its own folder"})
	}
	run = append(run, components.Shortcut{Keys: "D", Text: "Delete it for good, with its runs and state (asks first)"})
	return []components.ShortcutGroup{
		{Title: "Runbook", Keys: run},
		{Title: "Tabs", Keys: []components.Shortcut{
			{Keys: "← →", Text: "Previous / next tab (or tab, shift+tab)"},
			{Keys: "1-9", Text: "Jump to a terminal and type into it"},
			{Keys: "enter", Text: "Type into a terminal; on History, a run's whole log"},
			{Keys: "n", Text: "Rename the terminal's tab"},
			{Keys: "x", Text: "Close the terminal (asks while it runs)"},
			{Keys: "esc ctrl+t", Text: "Give den the keys back (esc twice in an editor)"},
		}},
		{Title: "View", Keys: []components.Shortcut{
			{Keys: "↑ ↓", Text: "Scroll the logs, About or a file shown; pick a run or a file"},
			{Keys: "enter", Text: "On Files: show the file picked; esc goes back to the tree"},
			{Keys: "esc", Text: "Back to the list (from a run's whole log or a file: to the runs or files)"},
		}},
	}
}

// aboutSource is the About tab as markdown: the runbook's README.md when its
// folder has one, then what den sets for it, its parameters and settings.
func aboutSource(reg *runbook.Registry, e entry) string {
	var b strings.Builder
	if e.isSeq() {
		seq := e.seq.Sequence()
		if seq.Description != "" {
			b.WriteString(seq.Description + "\n\n")
		}
		b.WriteString("## Steps\n\nRun in order; the first that fails stops the sequence.\n\n")
		for i, step := range seq.Steps {
			fmt.Fprintf(&b, "%d. %s\n", i+1, step)
		}
		return b.String()
	}

	rb := e.runner.Runbook()
	readme := ""
	if rb.Dir != "" {
		if data, err := os.ReadFile(filepath.Join(rb.Dir, "README.md")); err == nil {
			// A README is shown as it is written: escape sequences in one
			// from a source would reach the terminal.
			readme = strings.TrimSpace(runbook.CleanText(string(data)))
		}
	}
	switch {
	case readme != "":
		b.WriteString(readme + "\n\n")
	case rb.Description != "":
		b.WriteString(rb.Description + "\n\n")
	}

	fmt.Fprintf(&b, "## Where\n\n- Defined in `%s`\n- Runs in `%s`\n", tildePath(rb.Origin()), tildePath(rb.Cwd))
	fmt.Fprintf(&b, "- Runs `%s`\n\n", strings.ReplaceAll(strings.TrimSpace(rb.Command), "\n", " ⏎ "))

	vars := reg.ContextVars(rb)
	names := make([]string, 0, len(vars))
	for k := range vars {
		names = append(names, k)
	}
	slices.Sort(names)
	b.WriteString("## What den sets\n\nFor a run, and for a terminal opened with t. `DEN_PORT_*` are the connections up right now.\n\n")
	b.WriteString("| Variable | Value |\n|---|---|\n")
	for _, k := range names {
		fmt.Fprintf(&b, "| `%s` | `%s` |\n", k, vars[k])
	}

	if len(rb.Params) > 0 {
		b.WriteString("\n## Parameters\n\nAsked for before every run; they reach the script as `DEN_PARAM_<NAME>`.\n\n")
		b.WriteString("| Name | Default | Notes |\n|---|---|---|\n")
		for _, p := range rb.Params {
			var notes []string
			if p.Required {
				notes = append(notes, "required")
			}
			if p.Secret {
				notes = append(notes, "secret")
			}
			if len(p.Options) > 0 {
				notes = append(notes, "one of "+strings.Join(p.Options, ", "))
			}
			if p.Description != "" {
				notes = append(notes, p.Description)
			}
			def := p.Default
			if p.Secret && def != "" {
				def = "••••••"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", p.Name, orDash(def), strings.Join(notes, "; "))
		}
	}

	s := rb.Settings
	fmt.Fprintf(&b, "\n## Settings\n\n- History: %s\n- Shell (t): `%s`\n- Editor (e, o): `%s`\n",
		keeps(s.History), orDefault(s.Shell, "$SHELL"), orDefault(s.Editor, "$VISUAL, $EDITOR or vi"))
	return b.String()
}

// fitLines returns view as exactly rows lines, so a body can neither grow the
// box nor leave it short.
func fitLines(view string, rows int) string {
	lines := strings.Split(view, "\n")
	if len(lines) > rows {
		lines = lines[:rows]
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// tildePath shortens a path in the home directory to ~/….
func tildePath(p string) string { return platform.TildePath(p) }

// quoteArg quotes a runbook name for a shell command line when it needs it.
func quoteArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"$`\\|&;<>()*?[]#~!{}") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func orDash(s string) string { return orDefault(s, "–") }

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
