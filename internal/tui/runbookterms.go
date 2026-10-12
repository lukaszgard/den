package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaszgard/den/internal/agent"
	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/termsession"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/tui/screens/runbooks"
)

// The runbook view is the runbook's counterpart of the detail view: Enter on
// a runbook opens it full screen, with its logs, history and about, and with
// terminals that open in the runbook's folder. The root model owns those
// terminals, by runbook name, so they outlive the view as a service's do.

// rescanEvery is how often the runbook folders are read again while the
// Runbooks pane or a runbook view is on screen, with runbooks.rescan on.
const rescanEvery = 5 * time.Second

// programStarter runs a terminal's program on a terminal of cols×rows. Tests
// replace it.
type programStarter func(cmd termsession.Command, cols, rows int) (liveTerminal, error)

func startProgram(cmd termsession.Command, cols, rows int) (liveTerminal, error) {
	s, err := termsession.Start(cmd, cols, rows)
	if err != nil {
		return nil, err
	}
	return components.NewTermView(s), nil
}

// runbooksRescannedMsg reports that a rescan of the runbook folders finished.
type runbooksRescannedMsg struct{}

// rescanRunbooks reads the runbook folders again off the UI goroutine.
func (m *AppModel) rescanRunbooks() tea.Cmd {
	m.lastRescan = time.Now()
	reg := m.runbookReg
	return func() tea.Msg {
		reg.Rescan()
		return runbooksRescannedMsg{}
	}
}

// rescanDue reports whether the runbook folders are on screen and due to be
// read again.
func (m *AppModel) rescanDue(now time.Time) bool {
	if !m.runbookReg.RescanOn() || now.Sub(m.lastRescan) < rescanEvery {
		return false
	}
	return m.showRunbook || (!m.showDetail && m.currentPane() == paneRunbooks)
}

// runningTerminals counts the running terminals of each runbook for the list.
func runningTerminals(tabs map[string]*components.Tabs) func(string) int {
	return func(name string) int {
		if t := tabs[name]; t != nil {
			return t.Running()
		}
		return 0
	}
}

// runbookTabsFor returns name's terminal tabs, creating them on first use and
// keeping their fixed tabs in step with its settings.
func (m *AppModel) runbookTabsFor(name string) *components.Tabs {
	fixed := runbooks.FixedTabs(m.runbookReg, name)
	tabs := m.runbookTabs[name]
	switch {
	case tabs == nil:
		tabs = components.NewTabs(fixed...)
		tabs.SetKeyDisambiguation(m.keyDisambiguation)
		m.runbookTabs[name] = tabs
	case tabs.Fixed() != len(fixed) || tabs.FixedName(tabs.Fixed()-1) != fixed[len(fixed)-1]:
		tabs.SetFixed(fixed...)
	}
	return tabs
}

// openRunbook shows the runbook view of msg.Name, and its script in the
// editor when asked to.
func (m *AppModel) openRunbook(msg msgs.OpenRunbookMsg) []tea.Cmd {
	if _, ok := m.runbookReg.Lookup(msg.Name); !ok {
		if _, ok := m.runbookReg.LookupSequence(msg.Name); !ok {
			return nil
		}
	}
	m.runbookView = runbooks.NewView(m.runbookReg, m.ctx, msg.Name, m.runbookTabsFor(msg.Name), m.width, detailHeight(m.height)).
		WithOutput(m.logger.Output).SetDark(!m.light)
	m.showRunbook = true
	if msg.AgentEdit {
		var cmd tea.Cmd
		m.runbookView, cmd = m.runbookView.EditWithAgent(msg.Draft)
		return []tea.Cmd{cmd}
	}
	if msg.Agent != "" {
		return m.openAgentTerminal(msg.Name, msg.Agent, msg.AgentName, 0, false)
	}
	if msg.Edit {
		return m.openRunbookTerminal(msgs.OpenRunbookTerminalMsg{Name: msg.Name, Edit: true})
	}
	return nil
}

// openRunbookTerminal starts a shell in the runbook's folder, or its script or
// a run's log in the editor, with the variables den sets for it. Forking runs
// off the UI goroutine; the result comes back as a terminalStartedMsg.
func (m *AppModel) openRunbookTerminal(msg msgs.OpenRunbookTerminalMsg) []tea.Cmd {
	r, ok := m.runbookReg.Lookup(msg.Name)
	if !ok {
		return nil
	}
	if msg.AgentEdit {
		return m.openAgentTerminal(msg.Name, msg.Request, "", 0, true)
	}
	if agentName, ok := m.agentTabs[msg.Name][msg.TabID]; ok && msg.TabID != 0 {
		edit := m.agentEdits[msg.Name][msg.TabID]
		request := ""
		if saved, ok := m.runbookReg.SavedRequest(msg.Name, draftKind(edit)); ok {
			// The view asks; its answer comes back as this message, answered.
			if !msg.Answered && m.showRunbook && m.runbookView.Name() == msg.Name {
				m.runbookView = m.runbookView.AskResend(msg.TabID, saved.Request)
				return nil
			}
			if msg.Resend {
				request = saved.Request
			}
		}
		return m.openAgentTerminal(msg.Name, request, agentName, msg.TabID, edit)
	}
	// The restart of a failed editor opens its file again, not a shell.
	if e, ok := m.editorTabs[msg.Name][msg.TabID]; ok && msg.TabID != 0 {
		msg.File, msg.Edit = e.file, e.edit
	}
	rb := r.Runbook()
	var (
		cmd  termsession.Command
		name string
		edit *sourceEdit
		tab  *editorTab
	)
	if msg.File != "" || msg.Edit {
		// Noted now: by the time the editor starts, the user may have moved.
		tab = &editorTab{file: msg.File, edit: msg.Edit, scratch: msg.Scratch}
		if tabs := m.runbookTabs[msg.Name]; tabs != nil {
			tab.from = tabs.FixedName(tabs.Selected())
		}
	}
	editor := rb.Settings.Editor
	if editor == "" {
		editor = platform.UserEditor()
	}
	switch {
	case msg.File != "":
		// Named after the file, a run's log after the run (log-07), not the
		// editor: next to the script's tab, "nvim" twice would not say which
		// is which.
		cmd, name = editorCommand(editor, msg.File), strings.TrimSuffix(filepath.Base(msg.File), ".log")
		edit = startSourceEdit(msg.Name, rb, msg.File)
	case msg.Edit:
		cmd, name = editorCommand(editor, m.runbookReg.EditTarget(rb)), programName(editor)
	default:
		cmd.Path, name = platform.UserShell(), programName(platform.UserShell())
		if rb.Settings.Shell != "" {
			cmd, name = commandLine(rb.Settings.Shell), programName(rb.Settings.Shell)
		}
	}
	cmd.Env = m.runbookReg.ContextEnv(rb)
	cmd.Dir = rb.Home()

	cols, rows := m.runbookView.TerminalSize()
	start := m.startProgram
	return []tea.Cmd{func() tea.Msg {
		term, err := start(cmd, cols, rows)
		return terminalStartedMsg{runbook: msg.Name, tabID: msg.TabID, name: name, term: term, err: err, edit: edit, editor: tab}
	}}
}

// editorTab is a tab den opened an editor in: the file it edits (with edit,
// the runbook's script), so that restarting a failed editor opens it again,
// and the fixed tab it was opened from, by name, which gets the screen back
// when the editor closes. scratch is a directory made for the editor, that
// goes with it.
type editorTab struct {
	file    string
	edit    bool
	from    string
	scratch string
}

// editorClosed closes the tab of an editor that exited cleanly: an editor's
// last screen is blank, and its restart would only open the file again. The
// tab it was opened from comes back, if the editor was still on screen. A
// failed editor keeps its tab, with what it said.
func (m *AppModel) editorClosed(term liveTerminal) {
	if term.ExitErr() != nil {
		return
	}
	for name, tabs := range m.runbookTabs {
		for n := 1; n <= tabs.Len(); n++ {
			p := tabs.Position(n)
			if tabs.Term(p) != components.Terminal(term) {
				continue
			}
			e, ok := m.editorTabs[name][tabs.ID(p)]
			if !ok {
				return
			}
			delete(m.editorTabs[name], tabs.ID(p))
			if e.scratch != "" {
				_ = os.RemoveAll(e.scratch)
			}
			onScreen := tabs.Selected() == p
			tabs.Close(p)
			if onScreen {
				tabs.SelectFixed(e.from)
				if m.showRunbook && m.runbookView.Name() == name {
					m.runbookView = m.runbookView.StopTyping()
				}
			}
			return
		}
	}
}

// sourceEdit is a file of a source runbook's checkout open in the editor, as
// it was when the editor started.
type sourceEdit struct {
	runbook, source, path string
	size                  int64
	mod                   time.Time
}

// startSourceEdit notes how file is before the editor opens it, when it is in
// the checkout of a source runbook; nil for anything else, the user's own
// runbooks and a source runbook's logs, which are not in its checkout.
func startSourceEdit(name string, rb runbook.Runbook, file string) *sourceEdit {
	if rb.Remote == nil || rb.Dir == "" {
		return nil
	}
	if rel, err := filepath.Rel(rb.Dir, file); err != nil || !filepath.IsLocal(rel) {
		return nil
	}
	edit := &sourceEdit{runbook: name, source: rb.Remote.Source, path: file}
	if info, err := os.Stat(file); err == nil {
		edit.size, edit.mod = info.Size(), info.ModTime()
	}
	return edit
}

// sourceEditClosed is called when term's program ended. When term was an
// editor on a source runbook's file and the file changed, the runbook asks
// before its next run: the returned command says so. nil otherwise.
func (m *AppModel) sourceEditClosed(term liveTerminal) tea.Cmd {
	edit, ok := m.sourceEdits[term]
	if !ok {
		return nil
	}
	delete(m.sourceEdits, term)
	info, err := os.Stat(edit.path)
	if err == nil && info.Size() == edit.size && info.ModTime().Equal(edit.mod) {
		return nil
	}
	if err := m.runbookReg.MarkEdited(edit.runbook); err != nil {
		text := fmt.Sprintf("%s was edited but den could not note it: %v", edit.runbook, err)
		return func() tea.Msg { return warnFlash(text) }
	}
	text := fmt.Sprintf("%s was edited: it asks before its next run, and the edit is lost when %s updates", edit.runbook, edit.source)
	return func() tea.Msg { return warnFlash(text) }
}

// openAgentTerminal starts an agent in a terminal tab of the runbook's view,
// in its folder, to write it from request: den's runbook guide, and its MCP
// server for an agent that takes one, go with it (see internal/agent).
// agentName is the runbooks.agents entry to start, or a kind such as claude;
// "" is runbooks.agent's choice. tabID restarts an exited agent tab. edit
// changes a runbook that exists instead of writing a new one, and then "" is
// the runbook's own agent setting before runbooks.agent's; the setting may be
// a command line, which Pick reads.
func (m *AppModel) openAgentTerminal(name, request, agentName string, tabID int, edit bool) []tea.Cmd {
	r, ok := m.runbookReg.Lookup(name)
	if !ok {
		return nil
	}
	if agentName == "" && edit {
		agentName = r.Runbook().Settings.Agent
	}
	if agentName == "" {
		agentName = m.runbookReg.Agent()
	}
	ag, err := agent.Pick(m.runbookReg.Agents(), agentName)
	if err != nil {
		text := err.Error()
		return []tea.Cmd{func() tea.Msg { return warnFlash(text) }}
	}
	den, err := os.Executable()
	if err != nil {
		text := "den cannot find itself to hand the agent its MCP server: " + err.Error()
		return []tea.Cmd{func() tea.Msg { return warnFlash(text) }}
	}
	cfgPath := m.configPath
	if abs, err := filepath.Abs(cfgPath); err == nil && cfgPath != "" {
		cfgPath = abs
	}
	home := r.Runbook().Home()
	cmd, err := agent.Command(ag, agent.Options{
		Den: den, Config: cfgPath, Dir: home, Guide: m.runbookReg.Guide(home), Request: request, Edit: edit,
	})
	if err != nil {
		text := err.Error()
		return []tea.Cmd{func() tea.Msg { return warnFlash(text) }}
	}
	cols, rows := m.runbookView.TerminalSize()
	start := m.startProgram
	return []tea.Cmd{func() tea.Msg {
		term, err := start(cmd, cols, rows)
		if err == nil {
			term = &keepsEsc{liveTerminal: term}
		}
		return terminalStartedMsg{runbook: name, tabID: tabID, name: ag.Label(), term: term, err: err, agent: ag.Name, agentEdit: edit}
	}}
}

// keepsEsc is an agent's terminal: esc is the agent's own key (Claude Code
// interrupts with it), so a single esc never gives den the keys back there,
// whatever the terminal's modes say; a second right after it, or ctrl+t, does.
// It also notes that den closed it: a closed program reports a clean exit,
// which keep_request: until-exit must not take for the agent being done.
type keepsEsc struct {
	liveTerminal
	closed atomic.Bool
}

func (k *keepsEsc) FullScreen() bool { return k.Alive() }

func (k *keepsEsc) Close() {
	k.closed.Store(true)
	k.liveTerminal.Close()
}

// exitedByItself reports a program that ended cleanly without den closing it.
func (k *keepsEsc) exitedByItself() bool {
	return !k.Alive() && k.ExitErr() == nil && !k.closed.Load()
}

// agentStarted drops the saved request of an agent's runbook once the agent
// is up, with runbooks.drafts.keep_request: never.
func (m *AppModel) agentStarted(name string, edit bool) {
	if m.runbookReg.KeepRequest() != config.KeepNever {
		return
	}
	if err := m.runbookReg.DropRequest(name, draftKind(edit)); err != nil {
		m.logger.Warn("runbook drafts: " + err.Error())
	}
}

// agentExited drops the saved request of the runbook whose agent term ran,
// with runbooks.drafts.keep_request: until-exit, when the agent ended cleanly
// by itself. A failed agent, or one den stopped, keeps it for a restart.
func (m *AppModel) agentExited(term liveTerminal) {
	agentTerm, ok := term.(*keepsEsc)
	if !ok || !agentTerm.exitedByItself() || m.runbookReg.KeepRequest() != config.KeepUntilExit {
		return
	}
	for name, tabs := range m.runbookTabs {
		for n := 1; n <= tabs.Len(); n++ {
			p := tabs.Position(n)
			if tabs.Term(p) != components.Terminal(term) {
				continue
			}
			if err := m.runbookReg.DropRequest(name, draftKind(m.agentEdits[name][tabs.ID(p)])); err != nil {
				m.logger.Warn("runbook drafts: " + err.Error())
			}
			return
		}
	}
}

// draftKind is the kind of request an agent was started with: a change to a
// runbook, or a new one.
func draftKind(edit bool) string {
	if edit {
		return runbook.DraftEdit
	}
	return runbook.DraftNew
}

// runbookTerminalStarted puts a started program into its runbook's tab and
// gives it the keyboard. One with nowhere to go — its runbook gone, its tab
// closed, the tabs full — is stopped again.
func (m *AppModel) runbookTerminalStarted(msg terminalStartedMsg) []tea.Cmd {
	tabs := m.runbookTabs[msg.runbook]
	placed := false
	id := msg.tabID
	if _, known := m.runbookReg.Lookup(msg.runbook); known && tabs != nil {
		if msg.tabID != 0 {
			placed = tabs.Replace(msg.tabID, msg.term)
		} else {
			var p int
			p, placed = tabs.Add(msg.name, msg.term)
			id = tabs.ID(p)
		}
	}
	if placed && msg.edit != nil {
		m.sourceEdits[msg.term] = *msg.edit
	}
	// A restart keeps the tab's entry, and with it where it came from.
	if placed && msg.editor != nil && msg.tabID == 0 {
		if m.editorTabs[msg.runbook] == nil {
			m.editorTabs[msg.runbook] = map[int]editorTab{}
		}
		m.editorTabs[msg.runbook][id] = *msg.editor
	}
	if placed && msg.agent != "" {
		m.agentStarted(msg.runbook, msg.agentEdit)
		if m.agentTabs[msg.runbook] == nil {
			m.agentTabs[msg.runbook] = map[int]string{}
		}
		m.agentTabs[msg.runbook][id] = msg.agent
		if m.agentEdits[msg.runbook] == nil {
			m.agentEdits[msg.runbook] = map[int]bool{}
		}
		m.agentEdits[msg.runbook][id] = msg.agentEdit
	}
	if !placed {
		msg.term.Close()
		return nil
	}
	m.logger.Debug(fmt.Sprintf("terminal: %s started for runbook %s", msg.name, msg.runbook))
	if m.showRunbook && m.runbookView.Name() == msg.runbook && tabs.Current() == components.Terminal(msg.term) {
		m.runbookView = m.runbookView.StartTyping()
		m.shortcuts = components.Shortcuts{}
	}
	return []tea.Cmd{waitTerminal(msg.term)}
}

// dropGoneRunbooks stops and forgets the terminals of runbooks a rescan or a
// reload removed, and closes the view of one that is gone.
func (m *AppModel) dropGoneRunbooks() []tea.Cmd {
	for name, tabs := range m.runbookTabs {
		if _, ok := m.runbookReg.Lookup(name); !ok {
			tabs.StopAll()
			delete(m.runbookTabs, name)
			delete(m.agentTabs, name)
			delete(m.agentEdits, name)
			delete(m.editorTabs, name)
		}
	}
	if !m.showRunbook {
		return nil
	}
	name := m.runbookView.Name()
	_, isRunbook := m.runbookReg.Lookup(name)
	_, isSequence := m.runbookReg.LookupSequence(name)
	if isRunbook || isSequence {
		return nil
	}
	m.showRunbook = false
	text := fmt.Sprintf("%s is no longer defined", name)
	return []tea.Cmd{func() tea.Msg { return warnFlash(text) }}
}

// runbookDeletedMsg reports a delete that ran off the UI goroutine.
type runbookDeletedMsg struct {
	name string
	plan runbook.Deletion
	err  error
}

// deleteRunbook deletes the runbook or sequence called name, the user having
// said yes. Its terminals stop first: they run in the folder about to go, an
// editor saving after the delete would bring files back, and Windows does not
// delete a folder a program is in. The delete itself runs off the UI
// goroutine: a state folder can hold a virtualenv's thousands of files.
func (m *AppModel) deleteRunbook(name string) []tea.Cmd {
	if tabs := m.runbookTabs[name]; tabs != nil {
		tabs.StopAll()
	}
	reg := m.runbookReg
	return []tea.Cmd{func() tea.Msg {
		plan, err := reg.Delete(name)
		return runbookDeletedMsg{name: name, plan: plan, err: err}
	}}
}

// runbookDeleted closes the view of what was deleted and says how it went.
func (m *AppModel) runbookDeleted(msg runbookDeletedMsg) []tea.Cmd {
	switch msg.plan.Kind {
	case runbook.DeleteItem, runbook.DeleteSequence, runbook.DeleteSource:
		// The registry wrote den.yaml: the Config pane shows the file.
		m.configPane = m.configPane.Refresh()
	}
	// Gone even when the name is still listed: a runbook of the same name den
	// skipped until now takes it, and is not the one whose view was open.
	_, isRunbook := m.runbookReg.Lookup(msg.name)
	_, isSequence := m.runbookReg.LookupSequence(msg.name)
	if msg.err == nil || (!isRunbook && !isSequence) {
		delete(m.runbookTabs, msg.name)
		delete(m.agentTabs, msg.name)
		delete(m.agentEdits, msg.name)
		delete(m.editorTabs, msg.name)
		if m.showRunbook && m.runbookView.Name() == msg.name {
			m.showRunbook = false
		}
	}
	m.runbooksPane = m.runbooksPane.Refresh()
	flash := msgs.StatusFlashMsg{Text: "deleted " + msg.name, Hold: 3 * time.Second}
	if msg.err != nil {
		m.logger.Warn("runbook delete: " + msg.err.Error())
		flash = warnFlash(msg.err.Error())
	}
	return append(m.dropGoneRunbooks(), func() tea.Msg { return flash })
}

// isRunbookTerminal reports whether term is one of a runbook's terminals.
func (m *AppModel) isRunbookTerminal(term liveTerminal) bool {
	for _, tabs := range m.runbookTabs {
		for n := 1; n <= tabs.Len(); n++ {
			if tabs.Term(tabs.Position(n)) == components.Terminal(term) {
				return true
			}
		}
	}
	return false
}

// runbookTerminalLines lists the runbooks with running terminals, for the
// restart prompt.
func runbookTerminalLines(tabs map[string]*components.Tabs) []string {
	names := make([]string, 0, len(tabs))
	for name, t := range tabs {
		if t.Running() > 0 {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		n := tabs[name].Running()
		out = append(out, fmt.Sprintf("%d %s  %s", n, plural(n, "terminal", "terminals"), name))
	}
	return out
}

// commandLine runs a configured command line, arguments and all, through the
// system shell, replacing that shell where it can so the tab's program is the
// command itself.
func commandLine(line string) termsession.Command {
	if runtime.GOOS == "windows" {
		path, args := platform.ShellCommand(line)
		return termsession.Command{Path: path, Args: args}
	}
	return termsession.Command{Path: "/bin/sh", Args: []string{"-c", "exec " + line}}
}

// editorCommand opens file in editor, which may carry arguments ("code
// --wait"). The file is passed as an argument, never spliced into the command
// line, so no name can break out of it.
func editorCommand(editor, file string) termsession.Command {
	if runtime.GOOS == "windows" {
		path, args := platform.ShellCommand(editor + ` "` + file + `"`)
		return termsession.Command{Path: path, Args: args}
	}
	return termsession.Command{Path: "/bin/sh", Args: []string{"-c", "exec " + editor + ` "$1"`, "den-edit", file}}
}

// programName is what a terminal's tab is called: the program's own name.
func programName(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "shell"
	}
	return strings.TrimSuffix(filepath.Base(fields[0]), ".exe")
}

// runbookKey routes a key while the runbook view is open, as the detail view's
// are: to the view first when it needs every key, then h for its shortcuts,
// then esc back to the list, then the view.
func (m *AppModel) runbookKey(msg tea.KeyPressMsg) []tea.Cmd {
	var cmd tea.Cmd
	if m.runbookView.CapturingKeys() {
		m.runbookView, cmd = m.runbookView.Update(msg)
		return []tea.Cmd{cmd}
	}
	if k := msg.String(); k == "h" || k == "?" {
		m.shortcuts = components.OpenShortcuts(m.runbookView.Name(), m.runbookView.Shortcuts())
		return nil
	}
	if isBack(msg) && !m.runbookView.OwnsBack() {
		m.showRunbook = false
		// The view's E may have kept a draft, which is a row of the list.
		m.runbooksPane = m.runbooksPane.Refresh()
		return nil
	}
	m.runbookView, cmd = m.runbookView.Update(msg)
	return []tea.Cmd{cmd}
}
