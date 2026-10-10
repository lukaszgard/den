package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/prereq"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/termsession"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/tui/screens/detail"
)

// The terminal tabs of the detail view run a service's client inside den,
// logged in. The root model owns them: the detail view is rebuilt every time
// it opens, and a client has to outlive that.

// liveTerminal is a tab's client as the root model needs it: what the detail
// view shows, plus the signal that its screen moved or it exited.
type liveTerminal interface {
	detail.Terminal
	Changed() <-chan struct{}
}

// terminalStarter runs a client in a terminal of cols×rows. Tests replace it.
type terminalStarter func(cmd service.ClientCommand, cols, rows int) (liveTerminal, error)

// startClient finds cmd's tool the way den doctor does, which includes the
// keg-only Homebrew installs that are not on PATH, and runs it on a pty. The
// user's shell is no tool of the catalog: it is whatever they log in with.
func startClient(cmd service.ClientCommand, cols, rows int) (liveTerminal, error) {
	path := platform.UserShell()
	if cmd.Tool != service.ShellTool {
		found, ok := prereq.Find(cmd.Tool)
		if !ok {
			return nil, fmt.Errorf("%s not found — %s", cmd.Tool, prereq.InstallHint(cmd.Tool))
		}
		path = found
	}
	s, err := termsession.Start(termsession.Command{Path: path, Args: cmd.Args, Env: cmd.Env}, cols, rows)
	if err != nil {
		return nil, err
	}
	return components.NewTermView(s), nil
}

// terminalStartedMsg reports a client started for svc, or a program for the
// runbook named runbook: for a new tab, or for the tab tabID whose program had
// exited.
type terminalStartedMsg struct {
	svc     service.Service
	runbook string
	tabID   int
	name    string
	term    liveTerminal
	err     error
	// agent is the name of the agent a runbook's agent tab runs, "" for any other
	// tab: restarting it starts that agent again.
	agent string
	// agentEdit is set when that agent changes the runbook, not writes it.
	agentEdit bool
	// edit is set for an editor on a file of a source runbook's checkout.
	edit *sourceEdit
	// editor is set for an editor's new tab.
	editor *editorTab
}

// terminalChangedMsg says term's screen moved or its client exited. Handling
// it redraws the view, which is all a change needs.
type terminalChangedMsg struct{ term liveTerminal }

// waitTerminal blocks until term has something new to show. Exactly one is
// armed per running client, as with waitForUpdate for services, so what a
// client prints appears as it arrives rather than on the next tick.
func waitTerminal(term liveTerminal) tea.Cmd {
	return func() tea.Msg {
		<-term.Changed()
		return terminalChangedMsg{term: term}
	}
}

// tabsFor returns svc's terminal tabs, creating them on first use; nil for a
// service without a client den can run.
func (m *AppModel) tabsFor(svc service.Service) *detail.Tabs {
	if _, ok := service.Terminal(svc); !ok {
		return nil
	}
	tabs := m.terminals[svc]
	if tabs == nil {
		tabs = detail.NewTabs()
		tabs.SetKeyDisambiguation(m.keyDisambiguation)
		m.terminals[svc] = tabs
	}
	return tabs
}

func (m *AppModel) findService(name, typ string) (service.Service, bool) {
	for _, svc := range m.manager.Services() {
		if info := svc.Info(); info.Name == name && info.Type == typ {
			return svc, true
		}
	}
	return nil, false
}

// openTerminal starts the client the detail view asked for. Forking runs off
// the UI goroutine; the result comes back as a terminalStartedMsg.
func (m *AppModel) openTerminal(msg msgs.OpenTerminalMsg) []tea.Cmd {
	svc, ok := m.findService(msg.ServiceName, msg.ServiceType)
	if !ok {
		return nil
	}
	opener, ok := service.Terminal(svc)
	if !ok {
		return nil
	}
	cmd, err := opener.TerminalCommand()
	if err != nil {
		text := err.Error() + " — press c first"
		return []tea.Cmd{func() tea.Msg { return warnFlash(text) }}
	}
	cols, rows := m.detailModel.TerminalSize()
	start := m.startTerminal
	return []tea.Cmd{func() tea.Msg {
		term, err := start(cmd, cols, rows)
		return terminalStartedMsg{svc: svc, tabID: msg.TabID, name: cmd.Tool, term: term, err: err}
	}}
}

// terminalStarted puts a started client into its tab and gives it the
// keyboard. A client that arrives with nowhere to go — its service removed by
// a reload, its tab closed, the tabs full — is stopped again.
func (m *AppModel) terminalStarted(msg terminalStartedMsg) []tea.Cmd {
	if msg.err != nil {
		m.logger.Error("terminal: " + msg.err.Error())
		// A flash, not the sticky error line: installing the client fixes
		// this, and nothing would clear the error afterwards.
		flash := msgs.StatusFlashMsg{Text: msg.err.Error(), Warn: true, Hold: 8 * time.Second}
		return []tea.Cmd{func() tea.Msg { return flash }}
	}
	if msg.runbook != "" {
		return m.runbookTerminalStarted(msg)
	}
	tabs := m.terminals[msg.svc]
	placed := false
	if _, known := m.findService(msg.svc.Info().Name, msg.svc.Info().Type); known && tabs != nil {
		if msg.tabID != 0 {
			placed = tabs.Replace(msg.tabID, msg.term)
		} else {
			_, placed = tabs.Add(msg.name, msg.term)
		}
	}
	if !placed {
		msg.term.Close()
		return nil
	}
	m.logger.Debug(fmt.Sprintf("terminal: %s started for %s", msg.name, msg.svc.Info().Name))
	// Only when it is the one on screen: the user may have moved to another
	// tab while a restarted client was coming up.
	if m.showDetail && m.detailModel.Service() == msg.svc && tabs.Current() == detail.Terminal(msg.term) {
		m.detailModel = m.detailModel.StartTyping()
		// A pop-up opened while the client was starting would cover the
		// terminal and take the keys meant for it.
		m.shortcuts = components.Shortcuts{}
	}
	return []tea.Cmd{waitTerminal(msg.term)}
}

// stopTerminals stops the clients of a service that went down. Their
// connections are gone and their credential will not be refreshed for them;
// the tabs stay, with their names and last screens, to be restarted once the
// service is back.
func (m *AppModel) stopTerminals(info service.ServiceInfo) {
	if svc, ok := m.findService(info.Name, info.Type); ok {
		if tabs := m.terminals[svc]; tabs != nil {
			tabs.StopAll()
		}
	}
}

// dropRemovedTerminals stops and forgets the terminals of services a reload
// removed.
func (m *AppModel) dropRemovedTerminals() {
	current := make(map[service.Service]bool)
	for _, svc := range m.manager.Services() {
		current[svc] = true
	}
	for svc, tabs := range m.terminals {
		if !current[svc] {
			tabs.StopAll()
			delete(m.terminals, svc)
		}
	}
}

// stopAllTerminals stops every client and every runbook terminal, for a quit
// or a restart.
func (m *AppModel) stopAllTerminals() {
	for _, tabs := range m.terminals {
		tabs.StopAll()
	}
	for _, tabs := range m.runbookTabs {
		tabs.StopAll()
	}
}
