package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/tui/screens/applogs"
	"github.com/lukaszgard/den/internal/tui/screens/awspane"
	"github.com/lukaszgard/den/internal/tui/screens/configview"
	"github.com/lukaszgard/den/internal/tui/screens/connect"
	"github.com/lukaszgard/den/internal/tui/screens/createenv"
	"github.com/lukaszgard/den/internal/tui/screens/help"
	"github.com/lukaszgard/den/internal/tui/screens/runbooks"
	secretspane "github.com/lukaszgard/den/internal/tui/screens/secrets"
	"github.com/lukaszgard/den/internal/tui/screens/tunnel"
	"github.com/lukaszgard/den/internal/tui/screens/updates"
	vpnpane "github.com/lukaszgard/den/internal/tui/screens/vpn"
)

// pane is what the root model needs from a right-hand panel. Each screen keeps
// its own concrete API; a small adapter below maps it onto this, so routing,
// resizing, the help bar and the service keys are written once instead of once
// per screen.
type pane interface {
	// update delivers a message; focused is false for background traffic.
	update(msg tea.Msg, focused bool) tea.Cmd
	view(width, height int, focused bool) string
	// setSize resizes the pane for a terminal of width×height, keeping its state.
	setSize(width, height int)
	// searching reports that a text input has the keyboard, so keys are text,
	// not commands.
	searching() bool
	// modal reports that a pop-up over the pane keeps the keyboard: tab does
	// not leave it for the menu.
	modal() bool
	// ownsBack reports that the pane handles this h/esc itself — to close its
	// help, an open file or a prompt — rather than returning to the menu.
	ownsBack(msg tea.KeyPressMsg) bool
	// selected is the service under the pane's cursor, if it lists services.
	selected() (service.Service, bool)
	// keys maps the service keys this pane offers to what they do.
	keys() map[string]serviceAction
	// help lists the help bar entries while the pane has focus; nil means the
	// generic navigation entries.
	help() []helpEntry
	// shortcuts lists the pane's keys for the shortcuts pop-up (h or ?); nil
	// for a pane without one, and while h is not its help: in a prompt, say.
	shortcuts() []components.ShortcutGroup
	// serviceEvent delivers a service status change to panes that show them.
	serviceEvent(msg msgs.ServiceEventMsg) tea.Cmd
}

// darkAware is implemented by the panes whose colours depend on the terminal's
// background: markdown, help and forms.
type darkAware interface {
	setDark(isDark bool)
}

// mouseAware is implemented by the panes whose rows can be clicked. They mark
// each row with components.Zone as they draw it; click moves the cursor to
// the row the zone id names, and ignores one the pane cannot act on now.
type mouseAware interface {
	click(id string) tea.Cmd
}

type helpEntry struct{ key, desc string }

// setDark hands the terminal's background to everything whose palette depends
// on it, and remembers it for what opens later.
func (m *AppModel) setDark(isDark bool) {
	m.light = !isDark
	for _, id := range allPanes {
		if p, ok := m.pane(id).(darkAware); ok {
			p.setDark(isDark)
		}
	}
	if m.showRunbook {
		m.runbookView = m.runbookView.SetDark(isDark)
	}
}

// pane returns the adapter for id over m's own pane field, so changes the pane
// makes land in m.
func (m *AppModel) pane(id paneID) pane {
	switch id {
	case paneConnect:
		return connectAdapter{m: &m.connectPane}
	case paneTunnel:
		return tunnelAdapter{m: &m.tunnelPane}
	case paneRunbooks:
		return runbooksAdapter{m: &m.runbooksPane}
	case paneVPN:
		return vpnAdapter{m: &m.vpnPane}
	case paneAWS:
		return awsAdapter{m: &m.awsPane}
	case paneSecrets:
		return secretsAdapter{m: &m.secretsPane}
	case paneCreateEnv:
		return createEnvAdapter{m: &m.createEnvPane}
	case paneConfig:
		return configAdapter{m: &m.configPane}
	case paneLogs:
		return logsAdapter{m: &m.logsPane}
	case paneHelp:
		return helpAdapter{m: &m.helpPane}
	case paneUpdates:
		return updatesAdapter{m: &m.updatesPane}
	}
	return nil
}

// allPanes lists every pane, in or out of the menu: hidden panes still get
// resized and still track service states.
var allPanes = []paneID{
	paneConnect, paneTunnel, paneRunbooks, paneVPN, paneAWS, paneSecrets,
	paneCreateEnv, paneConfig, paneLogs, paneHelp, paneUpdates,
}

// isBack reports the keys that close something or return to the menu.
func isBack(msg tea.KeyPressMsg) bool {
	return key.Matches(msg, msgs.GlobalKeys.Back) || msg.String() == "esc"
}

// Most panes need none of the optional behaviour.
type basePane struct{}

func (basePane) searching() bool                           { return false }
func (basePane) modal() bool                               { return false }
func (basePane) ownsBack(tea.KeyPressMsg) bool             { return false }
func (basePane) selected() (service.Service, bool)         { return nil, false }
func (basePane) keys() map[string]serviceAction            { return nil }
func (basePane) help() []helpEntry                         { return nil }
func (basePane) shortcuts() []components.ShortcutGroup     { return nil }
func (basePane) serviceEvent(msgs.ServiceEventMsg) tea.Cmd { return nil }

// ── Connect ──────────────────────────────────────────────────────────────────

type connectAdapter struct{ m *connect.Model }

func (p connectAdapter) update(msg tea.Msg, focused bool) (c tea.Cmd) {
	*p.m, c = p.m.Update(msg, focused)
	return c
}
func (p connectAdapter) view(w, h int, f bool) string      { return p.m.View(w, h, f) }
func (p connectAdapter) setSize(w, h int)                  { *p.m = p.m.SetSize(w, h) }
func (p connectAdapter) searching() bool                   { return p.m.Searching() }
func (connectAdapter) modal() bool                         { return false }
func (p connectAdapter) ownsBack(tea.KeyPressMsg) bool     { return false }
func (p connectAdapter) selected() (service.Service, bool) { return p.m.SelectedService() }
func (p connectAdapter) keys() map[string]serviceAction {
	return map[string]serviceAction{
		"c": actConnect, "d": actDisconnect, "r": actRefresh, "a": actAutoReconnect, "y": actCopy,
	}
}
func (p connectAdapter) help() []helpEntry {
	entries := []helpEntry{{"tab", "switch panel"}, {"↑↓", "navigate"}, {"c", "connect"}, {"d", "disconnect"}}
	if svc, ok := p.m.SelectedService(); ok && refreshesToken(svc) {
		entries = append(entries, helpEntry{"r", "refresh"})
	}
	return append(entries, helpEntry{"y", "copy cmd"}, helpEntry{"enter", "detail"},
		helpEntry{"R", "reload config"}, helpEntry{"q", "quit"})
}
func (p connectAdapter) shortcuts() []components.ShortcutGroup         { return p.m.Shortcuts() }
func (p connectAdapter) serviceEvent(msg msgs.ServiceEventMsg) tea.Cmd { return p.update(msg, false) }
func (p connectAdapter) click(id string) tea.Cmd                       { *p.m = p.m.Click(id); return nil }

// ── Tunnel ───────────────────────────────────────────────────────────────────

type tunnelAdapter struct{ m *tunnel.Model }

func (p tunnelAdapter) update(msg tea.Msg, focused bool) (c tea.Cmd) {
	*p.m, c = p.m.Update(msg, focused)
	return c
}
func (p tunnelAdapter) view(w, h int, f bool) string      { return p.m.View(w, h, f) }
func (p tunnelAdapter) setSize(w, h int)                  { *p.m = p.m.SetSize(w, h) }
func (p tunnelAdapter) searching() bool                   { return false }
func (tunnelAdapter) modal() bool                         { return false }
func (p tunnelAdapter) ownsBack(tea.KeyPressMsg) bool     { return false }
func (p tunnelAdapter) selected() (service.Service, bool) { return p.m.SelectedService() }
func (p tunnelAdapter) keys() map[string]serviceAction {
	return map[string]serviceAction{
		"c": actConnect, "d": actDisconnect, "r": actToggleReconnect, "m": actToggleMonitor,
		"H": actToggleHealth, "v": actToggleAutoConnect, "y": actCopy, "enter": actDetail,
	}
}
func (p tunnelAdapter) help() []helpEntry {
	return []helpEntry{{"tab", "switch panel"}, {"↑↓", "navigate"}, {"enter", "detail"}, {"c", "connect"},
		{"d", "disconnect"}, {"r", "reconnect"}, {"m", "monitor"}, {"H", "health"}, {"v", "auto-connect"},
		{"y", "copy cmd"}, {"q", "quit"}}
}
func (p tunnelAdapter) shortcuts() []components.ShortcutGroup         { return p.m.Shortcuts() }
func (p tunnelAdapter) serviceEvent(msg msgs.ServiceEventMsg) tea.Cmd { return p.update(msg, false) }
func (p tunnelAdapter) click(id string) tea.Cmd                       { *p.m = p.m.Click(id); return nil }

// ── VPN ──────────────────────────────────────────────────────────────────────

type vpnAdapter struct{ m *vpnpane.Model }

func (p vpnAdapter) update(msg tea.Msg, focused bool) (c tea.Cmd) {
	*p.m, c = p.m.Update(msg, focused)
	return c
}
func (p vpnAdapter) view(w, h int, f bool) string      { return p.m.View(w, h, f) }
func (p vpnAdapter) setSize(w, h int)                  { *p.m = p.m.SetSize(w, h) }
func (p vpnAdapter) searching() bool                   { return false }
func (vpnAdapter) modal() bool                         { return false }
func (p vpnAdapter) ownsBack(tea.KeyPressMsg) bool     { return false }
func (p vpnAdapter) selected() (service.Service, bool) { return p.m.SelectedService() }
func (p vpnAdapter) keys() map[string]serviceAction {
	return map[string]serviceAction{
		"c": actConnect, "d": actDisconnect, "r": actToggleReconnect, "H": actToggleHealth, "enter": actDetail,
	}
}
func (p vpnAdapter) help() []helpEntry {
	return []helpEntry{{"tab", "switch panel"}, {"↑↓", "navigate"}, {"enter", "detail"}, {"c", "connect"},
		{"d", "disconnect"}, {"r", "reconnect"}, {"H", "health"}, {"q", "quit"}}
}
func (p vpnAdapter) shortcuts() []components.ShortcutGroup         { return p.m.Shortcuts() }
func (p vpnAdapter) serviceEvent(msg msgs.ServiceEventMsg) tea.Cmd { return p.update(msg, false) }
func (p vpnAdapter) click(id string) tea.Cmd                       { *p.m = p.m.Click(id); return nil }

// ── AWS ──────────────────────────────────────────────────────────────────────

type awsAdapter struct{ m *awspane.Model }

func (p awsAdapter) update(msg tea.Msg, focused bool) (c tea.Cmd) {
	*p.m, c = p.m.Update(msg, focused)
	return c
}
func (p awsAdapter) view(w, h int, f bool) string      { return p.m.View(w, h, f) }
func (p awsAdapter) setSize(w, h int)                  { *p.m = p.m.SetSize(w, h) }
func (p awsAdapter) searching() bool                   { return p.m.Searching() }
func (awsAdapter) modal() bool                         { return false }
func (p awsAdapter) ownsBack(tea.KeyPressMsg) bool     { return false }
func (p awsAdapter) selected() (service.Service, bool) { return p.m.SelectedService() }

// SSO login goes through the service manager rather than a second `aws sso
// login` of its own, so the AWS rows get the same status, logging and
// token-cache handling as everything else.
func (p awsAdapter) keys() map[string]serviceAction {
	return map[string]serviceAction{"enter": actLogin, "c": actLogin, "d": actCancelLogin}
}
func (p awsAdapter) help() []helpEntry {
	return []helpEntry{{"tab", "switch panel"}, {"↑↓", "navigate"}, {"enter", "sso login"}, {"o", "open browser"},
		{"e", "edit config"}, {"E", "edit credentials"}, {"q", "quit"}}
}
func (p awsAdapter) shortcuts() []components.ShortcutGroup         { return p.m.Shortcuts() }
func (p awsAdapter) serviceEvent(msg msgs.ServiceEventMsg) tea.Cmd { return p.update(msg, false) }
func (p awsAdapter) click(id string) tea.Cmd                       { *p.m = p.m.Click(id); return nil }

// ── Create Env ───────────────────────────────────────────────────────────────

type createEnvAdapter struct{ m *createenv.Model }

func (p createEnvAdapter) update(msg tea.Msg, focused bool) (c tea.Cmd) {
	*p.m, c = p.m.Update(msg, focused)
	return c
}
func (p createEnvAdapter) view(w, h int, f bool) string      { return p.m.View(w, h, f) }
func (p createEnvAdapter) setSize(w, h int)                  { *p.m = p.m.SetSize(w, h) }
func (p createEnvAdapter) searching() bool                   { return false }
func (createEnvAdapter) modal() bool                         { return false }
func (p createEnvAdapter) ownsBack(tea.KeyPressMsg) bool     { return false }
func (p createEnvAdapter) selected() (service.Service, bool) { return p.m.SelectedService() }
func (p createEnvAdapter) keys() map[string]serviceAction {
	return map[string]serviceAction{"c": actStart, "enter": actStart}
}
func (p createEnvAdapter) help() []helpEntry                             { return nil }
func (createEnvAdapter) shortcuts() []components.ShortcutGroup           { return nil }
func (p createEnvAdapter) serviceEvent(msg msgs.ServiceEventMsg) tea.Cmd { return p.update(msg, false) }
func (p createEnvAdapter) click(id string) tea.Cmd                       { *p.m = p.m.Click(id); return nil }

// ── Runbooks ─────────────────────────────────────────────────────────────────

type runbooksAdapter struct {
	m *runbooks.Model
}

func (p runbooksAdapter) update(msg tea.Msg, focused bool) (c tea.Cmd) {
	*p.m, c = p.m.Update(msg, focused)
	return c
}
func (p runbooksAdapter) view(w, h int, f bool) string { return p.m.View(w, h, f) }
func (p runbooksAdapter) setSize(w, h int)             { *p.m = p.m.SetSize(w, h) }
func (p runbooksAdapter) setDark(isDark bool)          { *p.m = p.m.SetDark(isDark) }
func (p runbooksAdapter) searching() bool              { return p.m.Searching() }
func (p runbooksAdapter) modal() bool                  { return p.m.Modal() }

// The runbooks pane owns esc while a prompt, a confirmation or the output
// viewport has focus, and h always: in the list it opens the shortcuts, in a
// prompt it is text.
func (p runbooksAdapter) ownsBack(msg tea.KeyPressMsg) bool {
	return msg.String() == "h" || (isBack(msg) && p.m.Busy())
}
func (runbooksAdapter) selected() (service.Service, bool)         { return nil, false }
func (runbooksAdapter) keys() map[string]serviceAction            { return nil }
func (runbooksAdapter) help() []helpEntry                         { return nil }
func (p runbooksAdapter) shortcuts() []components.ShortcutGroup   { return p.m.Shortcuts() }
func (runbooksAdapter) serviceEvent(msgs.ServiceEventMsg) tea.Cmd { return nil }
func (p runbooksAdapter) click(id string) tea.Cmd                 { *p.m = p.m.Click(id); return nil }

// ── Secrets ──────────────────────────────────────────────────────────────────

type secretsAdapter struct{ m *secretspane.Model }

func (p secretsAdapter) update(msg tea.Msg, focused bool) (c tea.Cmd) {
	*p.m, c = p.m.Update(msg, focused)
	return c
}
func (p secretsAdapter) view(w, h int, f bool) string { return p.m.View(w, h, f) }
func (p secretsAdapter) setSize(w, h int)             { *p.m = p.m.SetSize(w, h) }
func (p secretsAdapter) searching() bool              { return p.m.Searching() }
func (secretsAdapter) modal() bool                    { return false }

// esc closes an open file first.
func (p secretsAdapter) ownsBack(msg tea.KeyPressMsg) bool     { return isBack(msg) && p.m.Busy() }
func (secretsAdapter) selected() (service.Service, bool)       { return nil, false }
func (secretsAdapter) keys() map[string]serviceAction          { return nil }
func (p secretsAdapter) shortcuts() []components.ShortcutGroup { return p.m.Shortcuts() }
func (p secretsAdapter) click(id string) tea.Cmd               { *p.m = p.m.Click(id); return nil }
func (secretsAdapter) help() []helpEntry {
	return []helpEntry{{"tab", "switch panel"}, {"↑↓", "navigate"}, {"enter", "view"}, {"/", "filter"},
		{"r", "reveal"}, {"a", "reveal all"}, {"t", "tree"}, {"y", "copy"}, {"e", "edit"}, {"l", "lock"}, {"L", "login"},
		{"esc", "close"}, {"q", "quit"}}
}

// An SSO login, logout or expiry changes which secrets can be read.
func (p secretsAdapter) serviceEvent(msg msgs.ServiceEventMsg) (c tea.Cmd) {
	if msg.Update.Info.Type != service.TypeAWS {
		return nil
	}
	*p.m, c = p.m.RecheckAWS()
	return c
}

// ── Config ───────────────────────────────────────────────────────────────────

type configAdapter struct {
	basePane
	m *configview.Model
}

func (p configAdapter) update(msg tea.Msg, focused bool) (c tea.Cmd) {
	*p.m, c = p.m.Update(msg, focused)
	return c
}
func (p configAdapter) view(w, h int, f bool) string          { return p.m.View(w, h, f) }
func (p configAdapter) setSize(w, h int)                      { *p.m = p.m.SetSize(w, h-3) }
func (p configAdapter) searching() bool                       { return p.m.Searching() }
func (p configAdapter) ownsBack(msg tea.KeyPressMsg) bool     { return p.m.OwnsBack(msg) }
func (p configAdapter) shortcuts() []components.ShortcutGroup { return p.m.Shortcuts() }
func (p configAdapter) click(id string) tea.Cmd               { *p.m = p.m.Click(id); return nil }

// ── Logs ─────────────────────────────────────────────────────────────────────

type logsAdapter struct {
	basePane
	m *applogs.Model
}

func (p logsAdapter) update(msg tea.Msg, focused bool) (c tea.Cmd) {
	*p.m, c = p.m.Update(msg, focused)
	return c
}
func (p logsAdapter) view(w, h int, f bool) string { return p.m.View(w, h, f) }
func (p logsAdapter) setSize(w, h int)             { *p.m = p.m.SetSize(w, h-3) }
func (p logsAdapter) help() []helpEntry {
	output := "hide run output"
	if p.m.OutputHidden() {
		output = "show run output"
	}
	return []helpEntry{{"tab", "switch panel"}, {"↑↓", "scroll"}, {"o", output}, {"q", "quit"}}
}

// ── Help ─────────────────────────────────────────────────────────────────────

type helpAdapter struct {
	basePane
	m *help.Model
}

func (p helpAdapter) update(msg tea.Msg, focused bool) (c tea.Cmd) {
	*p.m, c = p.m.Update(msg, focused)
	return c
}
func (p helpAdapter) view(w, h int, f bool) string { return p.m.View(w, h, f) }
func (p helpAdapter) setSize(w, h int)             { *p.m = p.m.SetSize(w, h-3) }
func (p helpAdapter) setDark(isDark bool)          { *p.m = p.m.SetDark(isDark) }
func (p helpAdapter) searching() bool              { return p.m.Searching() }

// esc, ← and backspace close an open topic, or clear a search, before they
// return to the menu.
func (p helpAdapter) ownsBack(msg tea.KeyPressMsg) bool {
	return (isBack(msg) || msg.String() == "left") && p.m.Busy()
}
func (p helpAdapter) shortcuts() []components.ShortcutGroup { return p.m.Shortcuts() }
func (p helpAdapter) click(id string) tea.Cmd               { *p.m = p.m.Click(id); return nil }

// ── Updates ──────────────────────────────────────────────────────────────────

type updatesAdapter struct {
	basePane
	m *updates.Model
}

func (p updatesAdapter) update(msg tea.Msg, focused bool) (c tea.Cmd) {
	*p.m, c = p.m.Update(msg, focused)
	return c
}
func (p updatesAdapter) view(w, h int, f bool) string { return p.m.View(w, h, f) }
func (p updatesAdapter) setSize(w, h int)             { *p.m = p.m.SetSize(w, h-3) }
func (p updatesAdapter) setDark(isDark bool)          { *p.m = p.m.SetDark(isDark) }
func (p updatesAdapter) searching() bool              { return p.m.Searching() }

// h is the updates help; esc closes the help, the changelog, the version
// picker (a question in it first) or the restart prompt (answering "later").
func (p updatesAdapter) ownsBack(msg tea.KeyPressMsg) bool {
	return msg.String() == "h" ||
		(isBack(msg) && (p.m.ShowingHelp() || p.m.ShowingChangelog() || p.m.ShowingPicker() || p.m.ShowingRestartPrompt()))
}
func (updatesAdapter) help() []helpEntry {
	return []helpEntry{{"tab", "switch panel"}, {"c", "check updates"}, {"v", "versions"}, {"q", "quit"}}
}
