// Package msgs contains shared TUI message types and key bindings.
// It is a leaf package (no imports from sibling tui packages) to avoid import cycles.
package msgs

import (
	"time"

	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/update"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// --- Message types ---

// ServiceEventMsg carries a status update from a service to the TUI.
type ServiceEventMsg struct {
	Update service.StatusUpdate
}

// TickMsg is sent on each polling tick.
type TickMsg struct {
	Time time.Time
}

// ErrorMsg carries a user-visible error.
type ErrorMsg struct {
	Err error
}

// OpenDetailMsg requests a full-screen detail overlay for a service.
type OpenDetailMsg struct {
	ServiceName string
	ServiceType string
}

// OpenTerminalMsg asks the root model to run a service's client in a terminal
// tab of the detail view, already logged in: in a new tab, or, when TabID is
// set, again in that tab after its client exited.
type OpenTerminalMsg struct {
	ServiceName string
	ServiceType string
	TabID       int
}

// OpenRunbookMsg asks the root model for the full-screen view of a runbook or
// sequence. Edit also opens its script in the editor, as for a runbook just
// created; Agent instead starts an agent in a terminal tab to write it from
// that description, the one called AgentName ("" for runbooks.agent's).
// AgentEdit instead opens the question E asks, what the runbook's agent
// should change in it.
type OpenRunbookMsg struct {
	Name      string
	Edit      bool
	Agent     string
	AgentName string
	AgentEdit bool
}

// DeleteRunbookMsg asks the root model to delete the runbook or sequence
// called Name for good, once the user said yes: its terminals stop first, as
// they run in the folder about to go.
type DeleteRunbookMsg struct {
	Name string
}

// RunbookSourceDoneMsg reports a runbook source operation that ran off the
// UI goroutine — an update, a sync, a first fetch — in Lines, or why it failed.
type RunbookSourceDoneMsg struct {
	Lines []string
	Err   error
}

// OpenRunbookTerminalMsg asks the root model for a terminal in the runbook
// view: the runbook's shell in its folder, with Edit its script in the
// editor, or with File that file in the editor (a run's log). TabID set
// restarts that tab's program after it exited. Scratch is a directory that
// holds File and goes when the editor exits cleanly. AgentEdit starts the
// runbook's agent to change it as Request says ("" asks nothing of it).
type OpenRunbookTerminalMsg struct {
	Name      string
	Edit      bool
	File      string
	Scratch   string
	TabID     int
	AgentEdit bool
	Request   string
}

// DisconnectMsg asks the root model to disconnect a service, once the detail
// view has had the user confirm that its terminal tabs' clients may be stopped.
type DisconnectMsg struct {
	ServiceName string
	ServiceType string
}

// ConfigReloadMsg asks den to check den.yaml again and apply it: R from any
// pane, r in the Config pane.
type ConfigReloadMsg struct{}

// EditConfigMsg asks den to open den.yaml in the user's editor (e in the Config
// pane); when the editor closes, a changed file is checked and applied.
type EditConfigMsg struct{}

// StatusFlashMsg requests a temporary status bar message.
type StatusFlashMsg struct {
	Text string
	Warn bool          // render as a warning (orange ⚠) rather than a confirmation (green ✓)
	Hold time.Duration // how long it stays; zero means about a second
}

// FlashToggle reports the new state of a runtime toggle in the status bar, e.g.
// "monitor enabled". The dots in the panes are rendered from the service itself,
// so this is confirmation of the keypress, not the source of truth.
func FlashToggle(name string, enabled bool) tea.Cmd {
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	return func() tea.Msg { return StatusFlashMsg{Text: name + " " + state} }
}

// UpdateCheckStartedMsg is sent when the user manually triggers an update check.
type UpdateCheckStartedMsg struct{}

// UpdateCheckResultMsg carries the result of a release check.
type UpdateCheckResultMsg struct {
	Latest  string
	URL     string // release page (GitHub) or archive location (S3)
	Notes   string // release notes (markdown text)
	Release update.Release
	Err     error
}

// UpdateInstallStartedMsg is sent immediately when the user triggers auto-install.
type UpdateInstallStartedMsg struct{}

// UpdateInstallProgressMsg carries one completed install step and the next cmd to run.
type UpdateInstallProgressMsg struct {
	Step    string
	NextCmd tea.Cmd // next step to run; nil after the last step
}

// UpdateDownloadProgressMsg is sent periodically while an archive is downloading.
type UpdateDownloadProgressMsg struct {
	Downloaded int64
	Total      int64   // 0 = unknown
	NextCmd    tea.Cmd // nil when polling should stop
}

// UpdateInstallResultMsg carries the final outcome of an auto-install attempt.
type UpdateInstallResultMsg struct {
	Err error
}

// UpdateRestartMsg is sent when the user confirms restarting into the version
// just installed. The root model stops everything and quits; the CLI re-execs.
type UpdateRestartMsg struct {
	Version string
}

// SSO login has no message of its own: the AWS pane hands the selected session
// service to the manager like every other pane, so it shares one implementation
// of `aws sso login` with the service itself.

// OpenEditorMsg requests opening a file path in $EDITOR.
type OpenEditorMsg struct {
	Path string
}

// OpenBrowserMsg requests opening a URL in the default browser.
type OpenBrowserMsg struct {
	URL string
}

// AWSConfigEditedMsg is sent when the editor for an AWS file exits.
type AWSConfigEditedMsg struct {
	Err error
}

// --- Key bindings ---

// KeyMap defines global key bindings for the TUI.
type KeyMap struct {
	Up            key.Binding
	Down          key.Binding
	Select        key.Binding
	Connect       key.Binding
	Disconnect    key.Binding
	Refresh       key.Binding
	AutoReconnect key.Binding
	ReloadConfig  key.Binding
	Copy          key.Binding
	Filter        key.Binding
	Help          key.Binding
	Back          key.Binding
	Quit          key.Binding
}

// GlobalKeys is the default key map.
var GlobalKeys = KeyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	Select: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "detail"),
	),
	Connect: key.NewBinding(
		key.WithKeys("c"),
		key.WithHelp("c", "connect"),
	),
	Disconnect: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "disconnect"),
	),
	Refresh: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "refresh"),
	),
	AutoReconnect: key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "auto-reconnect"),
	),
	ReloadConfig: key.NewBinding(
		key.WithKeys("R"),
		key.WithHelp("R", "reload config"),
	),
	Copy: key.NewBinding(
		key.WithKeys("y"),
		key.WithHelp("y", "copy cmd"),
	),
	Filter: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "filter"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
	Back: key.NewBinding(
		key.WithKeys("esc", "backspace"),
		key.WithHelp("esc", "back"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
}
