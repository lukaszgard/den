package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/runbook/source"
	"github.com/lukaszgard/den/internal/secrets"
	"github.com/lukaszgard/den/internal/secrets/providers"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/applog"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/tui/screens/applogs"
	"github.com/lukaszgard/den/internal/tui/screens/awspane"
	"github.com/lukaszgard/den/internal/tui/screens/configview"
	"github.com/lukaszgard/den/internal/tui/screens/connect"
	"github.com/lukaszgard/den/internal/tui/screens/createenv"
	"github.com/lukaszgard/den/internal/tui/screens/detail"
	"github.com/lukaszgard/den/internal/tui/screens/help"
	"github.com/lukaszgard/den/internal/tui/screens/runbooks"
	secretspane "github.com/lukaszgard/den/internal/tui/screens/secrets"
	"github.com/lukaszgard/den/internal/tui/screens/tunnel"
	"github.com/lukaszgard/den/internal/tui/screens/updates"
	vpnpane "github.com/lukaszgard/den/internal/tui/screens/vpn"
	"github.com/lukaszgard/den/internal/update"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	// tickInterval drives anything moving on screen; idleTickInterval keeps
	// clocks and countdowns current when nothing is, without redrawing ten
	// times a second for nothing.
	tickInterval     = 100 * time.Millisecond
	idleTickInterval = time.Second
	leftPaneWidth    = 22
	flashDuration    = time.Second
)

// paneID identifies a left-menu pane by name.
type paneID = string

const (
	paneConnect   paneID = "connect"
	paneTunnel    paneID = "tunnel"
	paneRunbooks  paneID = "runbooks"
	paneVPN       paneID = "vpn"
	paneAWS       paneID = "aws"
	paneSecrets   paneID = "secrets"
	paneCreateEnv paneID = "create-env"
	paneConfig    paneID = "config"
	paneLogs      paneID = "logs"
	paneHelp      paneID = "help"
	paneUpdates   paneID = "updates"
)

// leftMenuItem describes one entry in the left navigation panel.
type leftMenuItem struct {
	label string
	icon  string
	color string
	desc  string
}

var paneMetadata = map[paneID]leftMenuItem{
	paneConnect:   {"Connect", "\uf1e6 ", "#F4A261", "All services — databases, tunnels, VPN & AWS"},
	paneTunnel:    {"Tunnel", "\uf0c1 ", "#A78BFA", "SSH tunnels"},
	paneRunbooks:  {"Runbooks", "\uf120 ", "#5BC8AF", "Run your scripts with den's context"},
	paneVPN:       {"VPN", "\uf084 ", "#674EFF", "openfortivpn + SAML"},
	paneAWS:       {"AWS", "\ue7ad ", "#FF9900", "AWS profiles & SSO login"},
	paneSecrets:   {"Secrets", "\uf023 ", "#E9C46A", "SOPS, AWS Secrets Manager, KeePassXC & Vault — view, copy, edit"},
	paneCreateEnv: {"Create Env", "\uf308 ", "#2496ED", "Start docker compose environment"},
	paneConfig:    {"Config", "\uf013 ", "#A0AEC0", "View & edit configuration file"},
	paneLogs:      {"Logs", "\uf022 ", "#68D391", "Application event log"},
	paneHelp:      {"Help", "\uf059 ", "#B794F4", "How each part of den works"},
	paneUpdates:   {"Updates", "\uf0aa ", "#4FD1C5", "Check for new versions"},
}

// openURL opens an address in the default browser. It is a variable so the
// end-to-end tests, which run den on a pty, never open one.
var openURL = platform.OpenURL

var defaultMenuOrder = []paneID{
	paneConnect, paneTunnel, paneRunbooks, paneVPN, paneAWS, paneSecrets, paneCreateEnv, paneConfig, paneLogs, paneHelp, paneUpdates,
}

// AppModel is the root Bubble Tea model — a fixed left menu + swappable right panel.
type AppModel struct {
	manager         *service.Manager
	ctx             context.Context
	cancel          context.CancelFunc
	configPath      string
	cfg             *config.Config // nil without a config file
	awsCfgPath      string
	credentialsPath string

	// cfgFile reads den.yaml again and applies it; nil without a file. See
	// configfile.go.
	cfgFile *ConfigFile
	// cfgProblems are what the last check of den.yaml found. With an error
	// among them the file is not what den runs: den keeps the last config
	// that had none, or none at all when it started with a broken file.
	cfgProblems config.Problems
	// cfgDeferred names the connections a checked den.yaml would stop, while
	// the user has put applying it off.
	cfgDeferred []string
	// cfgPrompt is the pop-up a reload opened, nil when none is; while it is
	// open it gets every key.
	cfgPrompt *configPrompt
	// leftovers is the pop-up offering to stop what keeps a VPN from
	// connecting, nil when none is open; it gets every key too. See
	// leftovers.go.
	leftovers *leftoverPrompt
	// startEditor opens den.yaml in the user's editor.
	startEditor editorStarter

	// light is set once the terminal reported a light background (see
	// tea.BackgroundColorMsg); the zero value is the dark palette, which shows
	// until it replies, so there is no blank first frame.
	light bool

	// keyDisambiguation is set once the terminal reported that it tells keys
	// apart unambiguously (tea.KeyboardEnhancementsMsg); the terminal tabs act on
	// it, see components.Tabs.
	keyDisambiguation bool

	// Left menu state
	menuOrder   []paneID
	leftCursor  int
	leftFocused bool // when true, keys control the left panel

	// Right panels (one per left menu item, always kept alive for state preservation)
	connectPane   connect.Model
	tunnelPane    tunnel.Model
	runbooksPane  runbooks.Model
	vpnPane       vpnpane.Model
	awsPane       awspane.Model
	secretsPane   secretspane.Model
	createEnvPane createenv.Model
	configPane    configview.Model
	logsPane      applogs.Model
	helpPane      help.Model
	updatesPane   updates.Model

	// Runbook registry. Run state lives here rather than in the pane so a
	// window resize (which rebuilds panes) can never orphan a running script.
	runbookReg *runbook.Registry

	// vpnUp is the last known "connected" state of each VPN, by name, so
	// auto-connect reacts to a VPN coming up or going down, not to every event.
	vpnUp map[string]bool

	// App-level logger
	logger *applog.Logger

	// Detail overlay
	showDetail  bool
	detailModel detail.Model

	// Runbook overlay: the runbook view, as the detail view is a service's.
	showRunbook bool
	runbookView runbooks.View
	// runbookTabs are the terminal tabs of each runbook, by name, and
	// startProgram runs a program for one. See runbookterms.go.
	runbookTabs  map[string]*components.Tabs
	startProgram programStarter
	// agentTabs are the tabs, by runbook and tab id, that run an agent, with the
	// agent's name so a restart starts the same one, and editorTabs those that
	// run an editor (see editorTab).
	agentTabs  map[string]map[int]string
	editorTabs map[string]map[int]editorTab
	// agentEdits are the agent tabs that change the runbook, so a restart
	// tells the agent so.
	agentEdits map[string]map[int]bool
	// sourceEdits are the editors open on a file of a source runbook's
	// checkout: when one closes, a changed file makes the runbook ask again.
	sourceEdits map[liveTerminal]sourceEdit
	// lastRescan is when the runbook folders were last read again.
	lastRescan time.Time

	// shortcuts is the pop-up of what the keys do (h or ?), over the detail
	// view or a pane. While it is open it gets every key.
	shortcuts components.Shortcuts

	// zones are where the clickable areas of the last frame landed, and
	// lastClick the zone clicked last and when: a second click on it soon
	// after is a double-click. See mouse.go.
	zones     components.Zones
	lastClick struct {
		id string
		at time.Time
	}
	// sel is the text a drag selects, and screenText the last frame without
	// its highlight: what a drag copies is what was on screen. mouseOff is set
	// while M has given the mouse back to the terminal. link is the address
	// the button went down on, opened when it comes up unless it dragged.
	sel        selection
	screenText string
	mouseOff   bool
	link       string
	// linker finds the links in each frame. See components.Linker.
	linker components.Linker
	// pointer is the mouse pointer den asked the terminal for last. See
	// hover in mouse.go.
	pointer pointer
	// sameFrame is set by an update that changed nothing on screen, a move
	// of the mouse, so View hands back lastView rather than drawing it again.
	sameFrame bool
	lastView  *tea.View

	// terminals are the terminal tabs of each service that has any, and
	// startTerminal runs a client for one. See terminals.go.
	terminals     map[service.Service]*detail.Tabs
	startTerminal terminalStarter

	// Version info
	version       string
	latestVersion string // empty until update check completes

	// Status bar flash
	flashMsg   string
	flashUntil time.Time
	flashWarn  bool

	width, height int
	err           string

	// restart is set when the user confirmed restarting into a freshly
	// installed version; the CLI re-execs once the program has exited.
	restart bool
}

// RestartRequested reports whether den quit to restart into a new version.
func (m *AppModel) RestartRequested() bool { return m.restart }

// Options configures the TUI.
type Options struct {
	// ConfigPath is the config file the Config pane shows and reload re-reads;
	// empty without one.
	ConfigPath string
	// Config is the loaded config file, or nil without one.
	Config *config.Config
	// Version is den's own version, shown in the menu and used by Updates.
	Version string
	// ConfigFile reads ConfigPath again and applies it, keeping the running
	// connections of services that did not change. Nil disables R and e.
	ConfigFile *ConfigFile
	// ConfigProblems are what checking ConfigPath at startup found. With an
	// error among them Config is nil and den opens on the Config pane, the
	// problems in front, so the file can be fixed from den itself.
	ConfigProblems config.Problems
	// RunbookHistory keeps finished runbook runs; nil keeps none, which is
	// what tests want.
	RunbookHistory *runbook.History
	// RunbookSources is where runbook sources are checked out, and
	// RunbookTrust the record of which of their runbooks the user has run;
	// nil reads no sources.
	RunbookSources *source.Store
	RunbookTrust   *runbook.Trust
	// RunbookState holds each runbook's DEN_RUNBOOK_STATE folder; "" keeps
	// none.
	RunbookState string
	// RunbookDrafts keeps the requests typed for an agent; nil keeps none.
	RunbookDrafts *runbook.Drafts
}

// New creates the root model over mgr's services.
func New(mgr *service.Manager, opts Options) *AppModel {
	const width, height = 80, 24 // replaced by the first WindowSizeMsg
	ctx, cancel := context.WithCancel(context.Background())
	cfg := opts.Config
	svcs := mgr.Services()
	logger := applog.New()
	if logs := cfg.LogsSettings(); logs != nil {
		_ = logger.Configure(logs.Level, logs.FileLocation)
	}
	logger.Trace("TUI model initializing")
	rw := width - leftPaneWidth - 1
	rh := height - 3
	awsCfgPath, credentialsPath := cfg.AWSConfigPath(), cfg.AWSCredentialsPath()
	var menuOrder, disabled []string
	if cfg != nil {
		menuOrder, disabled = cfg.MenuOrder, cfg.DisabledPanes
	}
	runbookReg := buildRunbookRegistry(cfg, opts.ConfigPath, mgr, logger)
	runbookReg.SetHistory(opts.RunbookHistory, func(err error) { logger.Warn("runbooks: " + err.Error()) })
	if opts.RunbookSources != nil {
		runbookReg.SetSources(opts.RunbookSources, opts.RunbookTrust)
	}
	if opts.RunbookState != "" {
		runbookReg.SetStateRoot(opts.RunbookState)
	}
	// Last: dropping the saved requests of runbooks that are gone reads the
	// folders the sources and state above list.
	if opts.RunbookDrafts != nil {
		runbookReg.SetDrafts(opts.RunbookDrafts)
	}
	terminals := map[service.Service]*detail.Tabs{}
	runbookTabs := map[string]*components.Tabs{}
	m := &AppModel{
		manager:         mgr,
		ctx:             ctx,
		cancel:          cancel,
		configPath:      opts.ConfigPath,
		cfg:             cfg,
		cfgFile:         opts.ConfigFile,
		cfgProblems:     opts.ConfigProblems,
		startEditor:     startEditor,
		awsCfgPath:      awsCfgPath,
		credentialsPath: credentialsPath,
		logger:          logger,
		version:         opts.Version,
		menuOrder:       buildMenuOrder(menuOrder, disabled),
		leftFocused:     true,
		connectPane:     connect.New(svcs, rw, height),
		tunnelPane:      tunnel.New(filterByType(svcs, service.TypeTunnel), rw, height),
		runbookReg:      runbookReg,
		runbooksPane:    runbooks.New(runbookReg, ctx, runningTerminals(runbookTabs), rw, height).WithOutput(logger.Output),
		vpnPane:         vpnpane.New(filterByType(svcs, service.TypeVPN), rw, height).SetConfigPath(opts.ConfigPath),
		awsPane:         awspane.New(filterByType(svcs, service.TypeAWS), awsCfgPath, credentialsPath, rw, height),
		secretsPane:     newSecretsPane(cfg, awsCfgPath, rw, height),
		createEnvPane:   createenv.New(svcs, rw, height),
		configPane:      configview.New(opts.ConfigPath, rw, rh).SetProblems(opts.ConfigProblems),
		logsPane:        applogs.New(logger, rw, rh),
		helpPane:        help.New(rw, rh),
		updatesPane:     updates.New(opts.Version, cfg.UpdatesSettings(), logger, closedByRestart(mgr, terminals, runbookTabs), rw, rh),
		vpnUp:           map[string]bool{},
		terminals:       terminals,
		startTerminal:   startClient,
		runbookTabs:     runbookTabs,
		startProgram:    startProgram,
		agentTabs:       map[string]map[int]string{},
		agentEdits:      map[string]map[int]bool{},
		editorTabs:      map[string]map[int]editorTab{},
		sourceEdits:     map[liveTerminal]sourceEdit{},
		lastRescan:      time.Now(),
		width:           width,
		height:          height,
	}
	for _, p := range opts.ConfigProblems {
		logger.Warn("den.yaml: " + p.String())
	}
	// Warnings alone leave den as it was, so the only sign is a dot on the
	// menu; say once what it is and where the list is.
	if n := opts.ConfigProblems.Warnings(); n > 0 && opts.ConfigProblems.Errors() == 0 {
		m.flashMsg = fmt.Sprintf("den.yaml: %s — p in Config lists them", count(n, "warning", "warnings"))
		m.flashWarn = true
		m.flashUntil = time.Now().Add(5 * time.Second)
	}
	// A broken den.yaml opens den on the Config pane with its problems in
	// front: den cannot fix the file, but it can open it and apply the fix.
	if opts.ConfigProblems.Errors() > 0 {
		m.cfgPrompt = &configPrompt{problems: opts.ConfigProblems}
		if i := m.paneIndexOf(paneConfig); i >= 0 {
			m.leftCursor, m.leftFocused = i, false
		}
	}
	return m
}

// newSecretsPane builds the Secrets pane for cfg. Its items share one view of
// the AWS logins, which the pane watches to refresh their statuses.
func newSecretsPane(cfg *config.Config, awsCfgPath string, width, height int) secretspane.Model {
	logins := secrets.NewAWSLogins(awsCfgPath)
	return secretspane.NewWithAWS(providers.Items(cfg, logins), logins, width, height)
}

// buildRunbookRegistry builds the runbook registry for cfg. Failures are
// logged rather than fatal: a broken runbooks: block must not stop den from
// opening connections, which is still its main job.
func buildRunbookRegistry(cfg *config.Config, cfgPath string, mgr *service.Manager, logger *applog.Logger) *runbook.Registry {
	reg, err := runbook.NewRegistry(cfg, cfgPath, runbookPorts(mgr))
	if err != nil {
		logger.Warn("runbooks: " + err.Error())
		reg, _ = runbook.NewRegistry(nil, cfgPath, nil)
	}
	logRunbookWarnings(reg, logger)
	return reg
}

func logRunbookWarnings(reg *runbook.Registry, logger *applog.Logger) {
	for _, w := range reg.Warnings() {
		logger.Warn("runbooks: " + w.String())
	}
}

// runbookPorts exposes the local ports of live connections to runbooks as
// DEN_PORT_<TYPE>_<NAME>. This is the thing a plain shell cannot know, and the
// reason a runbook is worth running from inside den at all.
func runbookPorts(mgr *service.Manager) runbook.PortSource {
	return runbook.PortsFrom(func() []runbook.ServiceState {
		svcs := mgr.Services()
		out := make([]runbook.ServiceState, 0, len(svcs))
		for _, svc := range svcs {
			info, st := svc.Info(), svc.State()
			out = append(out, runbook.ServiceState{
				Type:      info.Type,
				Name:      info.Name,
				LocalPort: st.LocalPort,
				Connected: st.Status == service.StatusConnected,
			})
		}
		return out
	})
}

// currentPane returns the paneID of the currently selected left-menu item.
func (m *AppModel) currentPane() paneID {
	if m.leftCursor >= 0 && m.leftCursor < len(m.menuOrder) {
		return m.menuOrder[m.leftCursor]
	}
	return ""
}

// paneIndexOf returns the menu index for the given paneID, or -1 if not found.
func (m *AppModel) paneIndexOf(id paneID) int {
	for i, p := range m.menuOrder {
		if p == id {
			return i
		}
	}
	return -1
}

func (m *AppModel) Init() tea.Cmd {
	m.logger.Trace("app launched — version " + m.version)
	cfg := m.updatesPane.UpdatesConfig()
	cmds := []tea.Cmd{
		tickCmd(tickInterval),
		// The reply decides the markdown, help and form palettes; panes draw the
		// dark ones until it comes.
		tea.RequestBackgroundColor,
		waitForUpdate(m.manager),
		reconcileRunningVPNs(m.ctx, m.manager),
		// The first renewal at once: a login whose access token ran out while
		// den was closed would otherwise read as expired for a minute.
		func() tea.Msg { return ssoRenewMsg{} },
		func() tea.Msg { return msgs.UpdateCheckStartedMsg{} },
		updates.CheckForUpdates(cfg, m.logger),
	}
	// The note about den.yaml's warnings is the only sign of them on the first
	// screen; the update check goes on without a flash of its own rather than
	// replace it.
	if m.flashMsg == "" {
		cmds = append(cmds, func() tea.Msg { return msgs.StatusFlashMsg{Text: "checking for updates…"} })
	}
	return tea.Batch(cmds...)
}

func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	// before runs ahead of cmds, in order: see the key press.
	var before tea.Cmd
	km := msgs.GlobalKeys
	m.sameFrame = false

	switch msg := msg.(type) {
	// ── window resize ──────────────────────────────────────────────────────
	case tea.WindowSizeMsg:
		m.logger.Trace(fmt.Sprintf("WindowSizeMsg: %dx%d", msg.Width, msg.Height))
		m.width = msg.Width
		m.height = msg.Height
		// What a selection covered has moved.
		m.sel = selection{}
		// Panes keep their cursor, filter and scroll position across a resize.
		rw := max(msg.Width-leftPaneWidth-1, 10)
		for _, id := range allPanes {
			m.pane(id).setSize(rw, msg.Height)
		}
		if m.showDetail {
			// Resized in place, not rebuilt, so the log scroll survives.
			m.detailModel, _ = m.detailModel.Update(tea.WindowSizeMsg{Width: msg.Width, Height: detailHeight(msg.Height)})
		}
		if m.showRunbook {
			m.runbookView, _ = m.runbookView.Update(tea.WindowSizeMsg{Width: msg.Width, Height: detailHeight(msg.Height)})
		}

	// ── terminal background ────────────────────────────────────────────────
	case tea.BackgroundColorMsg:
		m.setDark(msg.IsDark())

	// ── keyboard enhancements ──────────────────────────────────────────────
	case tea.KeyboardEnhancementsMsg:
		m.keyDisambiguation = msg.SupportsKeyDisambiguation()
		for _, tabs := range m.terminals {
			tabs.SetKeyDisambiguation(m.keyDisambiguation)
		}
		for _, tabs := range m.runbookTabs {
			tabs.SetKeyDisambiguation(m.keyDisambiguation)
		}

	// ── service event (fan-in from manager) ────────────────────────────────
	case msgs.ServiceEventMsg:
		// Exactly one waiter is armed at a time: Init starts it and each event
		// re-arms it, so events arrive as fast as services send them.
		cmds = append(cmds, waitForUpdate(m.manager))
		for _, id := range allPanes {
			cmds = append(cmds, m.pane(id).serviceEvent(msg))
		}
		if m.showDetail {
			m.detailModel, _ = m.detailModel.Update(msg)
		}
		// Log service state changes at debug level
		u := msg.Update
		info := u.Info
		status := u.State.Status.String()
		logLine := fmt.Sprintf("[%s] %s → %s", info.Type, info.Name, status)
		if u.State.Error != nil {
			logLine += fmt.Sprintf(" (%v)", u.State.Error)
		}
		m.logger.Debug(logLine)
		if info.Type == service.TypeVPN {
			cmds = append(cmds, m.vpnChanged(info.Name, u.State.Status)...)
		}
		// Not while it is reconnecting: a client may well survive that.
		if u.State.Status == service.StatusIdle || u.State.Status == service.StatusError {
			m.stopTerminals(info)
		}

	case vpnsAdoptedMsg:
		for _, name := range msg.names {
			cmds = append(cmds, m.vpnChanged(name, service.StatusConnected)...)
		}

	case ssoRenewMsg:
		cmds = append(cmds, renewSSO(m.ctx, filterByType(m.manager.Services(), service.TypeAWS)), ssoRenewTick())

	// ── tick ───────────────────────────────────────────────────────────────
	case msgs.TickMsg:
		if m.flashMsg != "" && !msg.Time.Before(m.flashUntil) {
			m.flashMsg = ""
		}
		if m.showDetail {
			m.detailModel, _ = m.detailModel.Update(msg)
		}
		if m.showRunbook {
			m.runbookView, _ = m.runbookView.Update(msg)
		}
		if m.rescanDue(msg.Time) {
			cmds = append(cmds, m.rescanRunbooks())
		}
		var logsCmd tea.Cmd
		m.logsPane, logsCmd = m.logsPane.Update(msg, false)
		cmds = append(cmds, logsCmd)
		// Runbook output is polled from the registry rather than pushed through
		// the update channel, which drops events when it is full.
		var rbCmd tea.Cmd
		m.runbooksPane, rbCmd = m.runbooksPane.Update(msg, false)
		cmds = append(cmds, rbCmd)
		// While its list is on screen the Secrets pane also follows AWS logins
		// no service event announces: one made outside den, a token running out.
		if !m.showDetail && m.currentPane() == paneSecrets {
			var secretsCmd tea.Cmd
			m.secretsPane, secretsCmd = m.secretsPane.Update(msg, false)
			cmds = append(cmds, secretsCmd)
		}

	// ── open detail overlay ────────────────────────────────────────────────
	case msgs.OpenDetailMsg:
		for _, svc := range m.manager.Services() {
			if svc.Info().Name == msg.ServiceName && svc.Info().Type == msg.ServiceType {
				m.detailModel = detail.New(svc, m.tabsFor(svc), m.width, detailHeight(m.height))
				m.showDetail = true
				break
			}
		}

	// ── runbook view ───────────────────────────────────────────────────────
	case msgs.OpenRunbookMsg:
		cmds = append(cmds, m.openRunbook(msg)...)

	case msgs.OpenRunbookTerminalMsg:
		cmds = append(cmds, m.openRunbookTerminal(msg)...)

	case runbooksRescannedMsg:
		cmds = append(cmds, m.dropGoneRunbooks()...)

	case msgs.DeleteRunbookMsg:
		cmds = append(cmds, m.deleteRunbook(msg.Name)...)

	case runbookDeletedMsg:
		cmds = append(cmds, m.runbookDeleted(msg)...)

	// ── terminal tabs ──────────────────────────────────────────────────────
	case msgs.OpenTerminalMsg:
		cmds = append(cmds, m.openTerminal(msg)...)

	case terminalStartedMsg:
		cmds = append(cmds, m.terminalStarted(msg)...)

	case terminalChangedMsg:
		if msg.term.Alive() {
			cmds = append(cmds, waitTerminal(msg.term))
			break
		}
		// Asked before editorClosed takes the editor's tab away.
		runbookTerm := m.isRunbookTerminal(msg.term)
		m.agentExited(msg.term)
		edited := m.sourceEditClosed(msg.term)
		m.editorClosed(msg.term)
		if edited != nil || (m.runbookReg.RescanOn() && runbookTerm) {
			// An editor closing is the moment a runbook.yaml may have changed.
			cmds = append(cmds, m.rescanRunbooks(), edited)
		}

	case msgs.DisconnectMsg:
		if svc, ok := m.findService(msg.ServiceName, msg.ServiceType); ok {
			cmds = append(cmds, m.runAction(actDisconnect, svc)...)
		}

	// What keeps a VPN from connecting, checked after a c or a stop; see
	// leftovers.go.
	case leftoversMsg:
		cmds = append(cmds, m.leftoversFound(msg)...)

	// A runbook source fetched in the background: the Runbooks pane shows
	// what it did, in its pop-up or the status bar.
	case msgs.RunbookSourceDoneMsg:
		var c tea.Cmd
		m.runbooksPane, c = m.runbooksPane.Update(msg, false)
		cmds = append(cmds, c)

	// ── status bar flash ───────────────────────────────────────────────────
	case msgs.StatusFlashMsg:
		m.flashMsg = msg.Text
		m.flashWarn = msg.Warn
		hold := flashDuration
		if msg.Hold > 0 {
			hold = msg.Hold
		}
		m.flashUntil = time.Now().Add(hold)

	// ── config reload ──────────────────────────────────────────────────────
	// Checking and applying read files and stop services, so they run off
	// the UI goroutine and report back; see configfile.go.
	case msgs.ConfigReloadMsg:
		cmds = append(cmds, m.checkConfig(reloadAsked, nil))

	case msgs.EditConfigMsg:
		cmds = append(cmds, m.editConfig(nil))

	case configEditedMsg:
		cmds = append(cmds, m.configEdited(msg))

	case configCheckedMsg:
		cmds = append(cmds, m.configChecked(msg))

	case configReloadedMsg:
		cmds = append(cmds, m.configReloaded(msg)...)

	// ── updates check started (manual or startup) ─────────────────────────
	case msgs.UpdateCheckStartedMsg:
		m.logger.Info("update check: checking for latest release…")
		var c tea.Cmd
		m.updatesPane, c = m.updatesPane.Update(msg, false)
		cmds = append(cmds, c)

	// ── updates check result ───────────────────────────────────────────────
	case msgs.UpdateCheckResultMsg:
		if msg.Err != nil {
			m.logger.Error("update check failed: " + msg.Err.Error())
		} else if msg.Latest == "" {
			m.logger.Warn("update check: no release tag returned by API")
		} else if update.SameVersion(msg.Latest, m.updatesPane.Version()) {
			m.logger.Info("update check: up to date (" + msg.Latest + ")")
		} else {
			m.logger.Info("update check: new version available → " + msg.Latest)
		}
		if msg.Err == nil {
			m.latestVersion = msg.Latest
		}
		var c tea.Cmd
		m.updatesPane, c = m.updatesPane.Update(msg, false)
		cmds = append(cmds, c)

	// ── auto-install started ───────────────────────────────────────────────
	case msgs.UpdateInstallStartedMsg:
		m.logger.Info("auto-install: starting installation of " + m.latestVersion)
		var c tea.Cmd
		m.updatesPane, c = m.updatesPane.Update(msg, false)
		cmds = append(cmds, c)

	// ── auto-install progress ──────────────────────────────────────────────
	case msgs.UpdateInstallProgressMsg:
		m.logger.Info("auto-install: " + msg.Step)
		var c tea.Cmd
		m.updatesPane, c = m.updatesPane.Update(msg, false)
		cmds = append(cmds, c)

	// ── auto-install result ────────────────────────────────────────────────
	case msgs.UpdateInstallResultMsg:
		if msg.Err != nil {
			m.logger.Error("auto-install failed: " + msg.Err.Error())
		} else {
			m.logger.Info("auto-install: complete — restart den to use the new version")
			// The restart prompt stays in the Updates pane: a modal elsewhere
			// would take the next key, and a y typed into a search box would
			// drop every connection.
			if update.CanReexec && (m.leftFocused || m.currentPane() != paneUpdates) {
				text := m.latestVersion + " installed — open Updates to restart"
				cmds = append(cmds, func() tea.Msg { return msgs.StatusFlashMsg{Text: text, Hold: 10 * time.Second} })
			}
		}
		var c tea.Cmd
		m.updatesPane, c = m.updatesPane.Update(msg, false)
		cmds = append(cmds, c)

	// ── download progress ──────────────────────────────────────────────────
	case msgs.UpdateDownloadProgressMsg:
		var c tea.Cmd
		m.updatesPane, c = m.updatesPane.Update(msg, false)
		cmds = append(cmds, c)

	// ── restart into the installed version ─────────────────────────────────
	case msgs.UpdateRestartMsg:
		m.logger.Info("restarting into " + msg.Version)
		m.shutdown()
		m.restart = true
		return m, tea.Sequence(m.setPointer(pointerOwn), tea.Quit)

	// ── open file in $EDITOR ───────────────────────────────────────────────
	case msgs.OpenEditorMsg:
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		path := msg.Path
		cmd := exec.Command(editor, path)
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return msgs.AWSConfigEditedMsg{Err: err}
		})

	// ── open URL in default browser ───────────────────────────────────────
	case msgs.OpenBrowserMsg:
		url := msg.URL
		cmds = append(cmds, func() tea.Msg {
			if err := openURL(url); err != nil {
				return msgs.ErrorMsg{Err: fmt.Errorf("open browser: %w", err)}
			}
			return msgs.StatusFlashMsg{Text: "opening browser…"}
		})

	case msgs.AWSConfigEditedMsg:
		m.awsPane = m.awsPane.Reload()
		if msg.Err != nil {
			cmds = append(cmds, func() tea.Msg {
				return msgs.StatusFlashMsg{Text: fmt.Sprintf("editor error: %v", msg.Err)}
			})
		} else {
			cmds = append(cmds, func() tea.Msg {
				return msgs.StatusFlashMsg{Text: "AWS config reloaded"}
			})
		}

	// ── SSO login ──────────────────────────────────────────────────────────
	// ── error ──────────────────────────────────────────────────────────────
	case msgs.ErrorMsg:
		errStr := msg.Err.Error()
		if isSSOError(errStr) {
			m.err = "SSO session expired — go to AWS panel (↑↓ in left menu) to login"
		} else {
			m.err = errStr
		}
		m.logger.Log("ERROR: " + errStr)

	// ── keyboard ───────────────────────────────────────────────────────────
	case tea.KeyPressMsg:
		// ctrl+[ is esc, which only a terminal with key disambiguation tells apart.
		msg = components.NormalizeEsc(msg)
		focus := "left"
		if m.showDetail {
			focus = "detail"
		} else if !m.leftFocused {
			focus = "right/" + string(m.currentPane())
		}
		m.logger.Trace("key: " + msg.String() + " [" + focus + "]")
		// A key ends a selection, as in a terminal; the key still counts.
		m.sel = selection{}
		// A key may quit or hand the terminal to another program, an editor
		// or a login, which must not get den's hand pointer: the terminal's
		// own comes back first, and the hand with the next move of the mouse.
		before = m.setPointer(pointerOwn)

		// The shortcuts pop-up takes every key: esc closes it before what is
		// under it, and a c or d pressed while reading what it does must not
		// reach the service behind it. ctrl+c still quits from a pane.
		if m.shortcuts.Open() && msg.String() != "ctrl+c" {
			m.shortcuts = m.shortcuts.Key(msg.String(), m.width, m.height)
			break
		}
		// So do the pop-up of a reload and the one about leftovers.
		if m.cfgPrompt != nil && msg.String() != "ctrl+c" {
			cmds = append(cmds, m.configPromptKey(msg.String()))
			break
		}
		if m.leftovers != nil && msg.String() != "ctrl+c" {
			cmds = append(cmds, m.leftoverPromptKey(msg.String()))
			break
		}

		// The runbook view, like the detail view, captures all keys.
		if m.showRunbook {
			cmds = append(cmds, m.runbookKey(msg)...)
			break
		}

		// Detail overlay captures all keys
		if m.showDetail {
			// A client being typed into, a tab name being edited and a close
			// being confirmed need every key: esc, backspace, c, d, r and h
			// are all things one types at a database prompt.
			if m.detailModel.CapturingKeys() {
				var detailCmd tea.Cmd
				m.detailModel, detailCmd = m.detailModel.Update(msg)
				cmds = append(cmds, detailCmd)
				break
			}
			if k := msg.String(); k == "h" || k == "?" {
				m.shortcuts = components.OpenShortcuts(m.detailModel.Service().Info().Name, append(m.detailModel.Shortcuts(), viewShortcuts))
				break
			}
			if msg.String() == "M" {
				cmds = append(cmds, m.toggleMouse())
				break
			}
			if key.Matches(msg, km.Back) || msg.String() == "esc" {
				m.showDetail = false
				return m, tea.Sequence(before, tea.Batch(cmds...))
			}
			svc := m.detailModel.Service()
			switch {
			case key.Matches(msg, km.Connect):
				cmds = append(cmds, m.runAction(actConnect, svc)...)
			case key.Matches(msg, km.Disconnect):
				// A disconnect stops the clients in the service's terminal
				// tabs, and esc makes it easy to be back on den's keys with a
				// query half typed. The view asks first; its yes comes back
				// as a DisconnectMsg.
				if m.detailModel.RunningTerminals() > 0 {
					m.detailModel = m.detailModel.AskDisconnect()
					break
				}
				cmds = append(cmds, m.runAction(actDisconnect, svc)...)
			// r refreshes a credential; for tunnels and VPNs the detail view
			// itself treats it as the reconnect toggle.
			case key.Matches(msg, km.Refresh) && refreshesToken(svc):
				cmds = append(cmds, m.runAction(actRefresh, svc)...)
			default:
				var detailCmd tea.Cmd
				m.detailModel, detailCmd = m.detailModel.Update(msg)
				cmds = append(cmds, detailCmd)
			}
			break
		}

		switch {
		// A q typed into a search box is text, not quit; ctrl+c still quits.
		case key.Matches(msg, km.Quit) && (msg.String() != "q" || m.leftFocused || !m.inSearch()):
			m.shutdown()
			return m, tea.Sequence(before, tea.Quit)

		// A pop-up keeps tab: it would leave for the menu with the pop-up
		// still drawn, and what is typed next would move the menu instead.
		case msg.String() == "tab" && (m.leftFocused || !m.paneModal()):
			m.leftFocused = !m.leftFocused

		// M, as R, unless it is being typed or a pop-up has the keys.
		case msg.String() == "M" && !m.paneModal() && (m.leftFocused || !m.inSearch()):
			cmds = append(cmds, m.toggleMouse())

		case m.leftFocused:
			cmds = append(cmds, m.handleLeftKey(msg)...)

		default:
			cmds = append(cmds, m.handleRightKey(msg)...)
		}

	// ── mouse ──────────────────────────────────────────────────────────────
	// Only the left button and the wheel up and down mean anything; see
	// mouse.go.
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			m.sel = selection{}
			cmds = append(cmds, m.click(msg.X, msg.Y)...)
			m.press(msg.X, msg.Y)
		}

	// The terminal reports every move, for the hand over a link. One with no
	// button held changes nothing on screen.
	case tea.MouseMotionMsg:
		if msg.Button == tea.MouseLeft {
			m.drag(msg.X, msg.Y)
		} else {
			m.sameFrame = true
		}
		cmds = append(cmds, m.hover(msg.X, msg.Y, msg.Button != tea.MouseNone))

	// Not every terminal says which button was let go of.
	case tea.MouseReleaseMsg:
		cmds = append(cmds, m.release(), m.hover(msg.X, msg.Y, false))

	case tea.MouseWheelMsg:
		m.sel = selection{}
		if msg.Button == tea.MouseWheelUp || msg.Button == tea.MouseWheelDown {
			cmds = append(cmds, m.wheel(msg)...)
		}

	// A mouse message of a kind den does not know is nothing to it; without
	// this case it would reach the Secrets pane below.
	case tea.MouseMsg:

	// ── paste ──────────────────────────────────────────────────────────────
	// Pasted text goes where typed text would: a terminal, a name, a search
	// box, a form. Anywhere else it is dropped, as it never matched a key.
	case tea.PasteMsg:
		switch {
		case m.popUp():
		case m.showRunbook:
			if m.runbookView.CapturingKeys() {
				var c tea.Cmd
				m.runbookView, c = m.runbookView.Update(msg)
				cmds = append(cmds, c)
			}
		case m.showDetail:
			if m.detailModel.CapturingKeys() {
				var c tea.Cmd
				m.detailModel, c = m.detailModel.Update(msg)
				cmds = append(cmds, c)
			}
		case !m.leftFocused:
			if p := m.pane(m.currentPane()); p != nil {
				cmds = append(cmds, p.update(msg, true))
			}
		}

	default:
		// The runbook parameter form advances itself through commands that
		// emit their own message types. Nothing else here knows about them, so
		// without this they would be dropped and the form would never move
		// past its first field.
		if m.runbooksPane.AwaitingForm() {
			var c tea.Cmd
			m.runbooksPane, c = m.runbooksPane.Update(msg, true)
			cmds = append(cmds, c)
		}
		if m.showRunbook && m.runbookView.AwaitingForm() {
			var c tea.Cmd
			m.runbookView, c = m.runbookView.Update(msg)
			cmds = append(cmds, c)
		}
		// Decrypt results, edit exits and clipboard timers.
		var c tea.Cmd
		m.secretsPane, c = m.secretsPane.Update(msg, false)
		cmds = append(cmds, c)
	}

	// Decrypted values are only kept while the Secrets pane has focus.
	if m.leftFocused || m.showDetail || m.showRunbook || m.currentPane() != paneSecrets {
		m.secretsPane = m.secretsPane.Hide()
	}

	// The next tick is scheduled after the update so its rate reflects what the
	// screen shows now.
	if _, ok := msg.(msgs.TickMsg); ok {
		cmds = append(cmds, tickCmd(m.tickEvery()))
	}

	return m, tea.Sequence(before, tea.Batch(cmds...))
}

// tickEvery is the fast interval while anything on screen moves — a status
// flash, the detail view's log, a runbook's output, the log pane, a service on
// its way up or down with its spinner and countdown — and the idle one
// otherwise.
func (m *AppModel) tickEvery() time.Duration {
	if m.flashMsg != "" || m.showDetail || m.showRunbook || m.currentPane() == paneLogs {
		return tickInterval
	}
	if m.runbookReg != nil && m.runbookReg.AnyRunning() {
		return tickInterval
	}
	for _, svc := range m.manager.Services() {
		switch svc.State().Status {
		case service.StatusConnecting, service.StatusReconnecting, service.StatusStopping:
			return tickInterval
		}
	}
	return idleTickInterval
}

// handleLeftKey handles key events when the left panel is focused.
func (m *AppModel) handleLeftKey(msg tea.KeyPressMsg) []tea.Cmd {
	km := msgs.GlobalKeys
	switch {
	case key.Matches(msg, km.Up):
		if m.leftCursor > 0 {
			m.leftCursor--
		}
	case key.Matches(msg, km.Down):
		if m.leftCursor < len(m.menuOrder)-1 {
			m.leftCursor++
		}
	case key.Matches(msg, km.Select), msg.String() == "right", msg.String() == "l":
		m.leftFocused = false
	case key.Matches(msg, km.Help):
		if idx := m.paneIndexOf(paneHelp); idx >= 0 {
			m.leftCursor = idx
		}
		m.leftFocused = false
	case msg.String() == "L":
		if idx := m.paneIndexOf(paneLogs); idx >= 0 {
			m.leftCursor = idx
		}
		m.leftFocused = false
	}
	return nil
}

// inSearch reports whether the active right pane has a search box taking
// keystrokes, which then must not trigger global keys.
func (m *AppModel) inSearch() bool {
	p := m.pane(m.currentPane())
	return p != nil && p.searching()
}

// paneModal reports whether the active right pane has a pop-up open that
// keeps the keyboard.
func (m *AppModel) paneModal() bool {
	p := m.pane(m.currentPane())
	return p != nil && p.modal()
}

// handleRightKey routes a key to the focused right pane: the keys every pane
// shares first (back to the menu, reload), then the pane's service keys, then
// the pane itself.
func (m *AppModel) handleRightKey(msg tea.KeyPressMsg) []tea.Cmd {
	p := m.pane(m.currentPane())
	if p == nil {
		return nil
	}
	km := msgs.GlobalKeys
	searching := p.searching()

	// h and ? open the shortcuts of a pane that has them, unless they are
	// being typed.
	if k := msg.String(); (k == "h" || k == "?") && !searching {
		if groups := p.shortcuts(); groups != nil {
			m.shortcuts = components.OpenShortcuts(paneMetadata[m.currentPane()].label, append(groups, paneShortcuts))
			return nil
		}
	}

	// esc, ← and h return to the menu — unless the pane uses the key itself or
	// a search box has the keyboard.
	if (isBack(msg) || msg.String() == "left" || msg.String() == "h") && !p.ownsBack(msg) && !searching {
		m.leftFocused = true
		return nil
	}
	// R reloads the config from any pane, unless it is being typed.
	if key.Matches(msg, km.ReloadConfig) && !searching {
		return []tea.Cmd{func() tea.Msg { return msgs.ConfigReloadMsg{} }}
	}

	var cmds []tea.Cmd
	if !searching {
		cmds = append(cmds, m.paneServiceKey(p, msg)...)
	}
	return append(cmds, p.update(msg, true))
}

// ── View ──────────────────────────────────────────────────────────────────────

// View is the root model's screen. den runs on the terminal's alternate screen,
// which a view asks for itself; the panes and components below it draw plain
// strings. It also asks for the clicks and the wheel, unless M gave the mouse
// to the terminal, takes out of the frame the zones the panes marked,
// remembering where they landed, draws the addresses on it as links, and
// draws the selection of a drag over it.
//
// The terminal reports every move of the mouse, one a cell, and a redraw is
// most of what each costs; a move changes nothing on screen, so the frame
// drawn last is handed back.
func (m *AppModel) View() tea.View {
	if m.sameFrame && m.lastView != nil {
		return *m.lastView
	}
	content, cursor := m.screen()
	content, m.zones = components.ScanZones(content)
	content, links := m.linker.Links(content)
	m.zones = append(m.zones, links...)
	m.screenText = content
	if m.sel.shown {
		content = components.Highlight(content, m.sel.spans())
	}
	v := tea.NewView(content)
	v.AltScreen = true
	if !m.mouseOff {
		v.MouseMode = tea.MouseModeAllMotion
	}
	v.Cursor = cursor
	v.WindowTitle = m.windowTitle()
	v.ProgressBar = m.progressBar()
	m.lastView = &v
	return v
}

// windowTitle names the terminal's tab or window after den's state, so den is
// found among many: "den · Runbooks · 3 connected".
func (m *AppModel) windowTitle() string {
	title := "den · " + paneMetadata[m.currentPane()].label
	connected := 0
	for _, svc := range m.manager.Services() {
		if svc.State().Status == service.StatusConnected {
			connected++
		}
	}
	if connected > 0 {
		title += fmt.Sprintf(" · %d connected", connected)
	}
	return title
}

// progressBar is the terminal's tab progress bar: an update downloading, or a
// runbook or sequence running. nil clears it.
func (m *AppModel) progressBar() *tea.ProgressBar {
	if bar := m.updatesPane.Progress(); bar != nil {
		return bar
	}
	if m.runbookReg != nil && m.runbookReg.AnyRunning() {
		return tea.NewProgressBar(tea.ProgressBarIndeterminate, 0)
	}
	return nil
}

// screen is the frame, with the pop-up that has the keyboard over it, and the
// terminal cursor to show on it: the one of a client being typed into, never
// one a pop-up covers.
func (m *AppModel) screen() (string, *tea.Cursor) {
	frame, cursor := m.frame()
	if m.shortcuts.Open() {
		return components.Overlay(frame, m.shortcuts.View(m.width, m.height), m.width, m.height), nil
	}
	if m.cfgPrompt != nil {
		return components.Overlay(frame, m.cfgPrompt.view(m.width, m.height, m.cfg != nil), m.width, m.height), nil
	}
	if m.leftovers != nil {
		return components.Overlay(frame, m.leftovers.view(m.width), m.width, m.height), nil
	}
	return frame, cursor
}

// popUp reports whether a pop-up is open. It takes every key, and no click,
// paste or wheel may answer it or reach what it covers.
func (m *AppModel) popUp() bool {
	return m.shortcuts.Open() || m.cfgPrompt != nil || m.leftovers != nil
}

// mouseShortcut is M, which hands the mouse to the terminal from any pane or
// view.
var mouseShortcut = components.Shortcut{Keys: "M", Text: "Give the mouse to the terminal, to select as it does; M takes it back"}

// paneShortcuts are the keys every pane has from the root model; they close
// the pop-up of a pane that lists its own.
var paneShortcuts = components.ShortcutGroup{Title: "den", Keys: []components.Shortcut{
	{Keys: "tab", Text: "Switch between the menu and the panel (not while a pop-up is open)"},
	{Keys: "R", Text: "Reload den.yaml"},
	mouseShortcut,
	{Keys: "h ?", Text: "Open or close this help"},
	{Keys: "esc", Text: "Back to the menu (or ←)"},
	{Keys: "q", Text: "Quit den"},
}}

// viewShortcuts are the keys the detail and runbook views have from the root
// model; they close those views' pop-ups.
var viewShortcuts = components.ShortcutGroup{Title: "den", Keys: []components.Shortcut{mouseShortcut}}

// frame is the screen without a pop-up over it, and the cursor of the terminal
// tab on it, which only the two full-screen views can have.
func (m *AppModel) frame() (string, *tea.Cursor) {
	// Full-screen runbook view
	if m.showRunbook {
		view, cursor := m.runbookView.Render()
		return lipgloss.JoinVertical(lipgloss.Left, view, m.renderStatusBar()), cursor
	}

	// Full-screen detail overlay
	if m.showDetail {
		view, cursor := m.detailModel.Render()
		return lipgloss.JoinVertical(lipgloss.Left, view, m.renderStatusBar()), cursor
	}

	rw := m.width - leftPaneWidth - 1
	rh := m.height - 3

	left := m.renderLeft(rh)
	sep := renderSeparator(rh)
	right := m.renderRight(rw, rh)

	body := lipgloss.JoinHorizontal(lipgloss.Top, left, sep, right)
	statusBar := m.renderStatusBar()
	helpBar := m.renderHelpBar()

	return lipgloss.JoinVertical(lipgloss.Left,
		renderHeader(m.width, m.cfg.HeaderTitle()),
		body,
		statusBar,
		helpBar,
	), nil
}

// detailHeight is the rows the detail overlay gets: View puts the status bar
// under it. Bubble Tea drops the top lines of a frame taller than the terminal,
// so handing it the full height cut off the box's top border.
func detailHeight(termHeight int) int {
	return termHeight - 1
}

// renderHeader is the title on one row: a long one is cut with … instead of
// wrapping, since a second row would push the frame past the terminal height.
func renderHeader(width int, title string) string {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFFDF5")).
		Background(lipgloss.Color("#1a1a2e")).
		Padding(0, 2).
		Width(width).
		Render(ansi.Truncate("  "+title, max(width-4, 1), "…"))
}

func (m *AppModel) renderLeft(height int) string {
	var rows []string
	rows = append(rows, "")
	for i, id := range m.menuOrder {
		item, ok := paneMetadata[id]
		if !ok {
			continue
		}
		selected := i == m.leftCursor
		focused := m.leftFocused && selected

		icon := lipgloss.NewStyle().Foreground(lipgloss.Color(item.color)).Render(item.icon)
		label := icon + "  " + item.label
		// The Config entry carries a dot and the number of problems while
		// den.yaml has any, in the colour of the worst, so they are seen from
		// any pane.
		if id == paneConfig && len(m.cfgProblems) > 0 {
			dot := lipgloss.Color("#E9C46A")
			if m.cfgProblems.Errors() > 0 {
				dot = lipgloss.Color("#FF5F87")
			}
			label += " " + lipgloss.NewStyle().Foreground(dot).Render(fmt.Sprintf("● %d", len(m.cfgProblems)))
		}

		var row string
		if focused {
			row = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFDF5")).
				Background(lipgloss.Color("#1a1a2e")).
				Width(leftPaneWidth).
				Render(" ► " + label)
		} else if selected {
			row = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#6C91BF")).
				Width(leftPaneWidth).
				Render(" ► " + label)
		} else {
			row = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#9B9B9B")).
				Width(leftPaneWidth).
				Render("   " + label)
		}
		rows = append(rows, components.Zone(components.ZoneID("menu", i), row))
	}

	// Version badge — the update hint gets its own line since the pane is too narrow for both
	versionStyle := lipgloss.NewStyle().Width(leftPaneWidth).Foreground(lipgloss.Color("#626262"))
	footer := []string{
		lipgloss.NewStyle().Width(leftPaneWidth).Foreground(lipgloss.Color("#333355")).Render(strings.Repeat("─", leftPaneWidth)),
		versionStyle.Render(" App Version: " + displayVersion(m.version)),
	}
	if m.latestVersion != "" && strings.TrimPrefix(m.latestVersion, "v") != strings.TrimPrefix(m.version, "v") {
		footer = append(footer, versionStyle.Render(" \uf0aa "+displayVersion(m.latestVersion)+" available"))
	} else if m.latestVersion != "" {
		footer = append(footer, versionStyle.Render(" (latest)"))
	}

	// Pad to leave room for the version badge at the bottom
	for len(rows) < height-len(footer) {
		rows = append(rows, lipgloss.NewStyle().Width(leftPaneWidth).Render(""))
	}
	rows = append(rows, footer...)

	return strings.Join(rows, "\n")
}

// displayVersion adds the "v" prefix GoReleaser strips from {{.Version}};
// non-numeric versions such as "dev" are returned unchanged.
func displayVersion(v string) string {
	if v != "" && v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}

func renderSeparator(height int) string {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("#333355"))
	line := style.Render("│")
	lines := make([]string, height)
	for i := range lines {
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func (m *AppModel) renderRight(width, height int) string {
	p := m.pane(m.currentPane())
	if p == nil {
		return ""
	}
	return fitHeight(p.view(width, height, !m.leftFocused), height)
}

// fitHeight cuts s to its first height lines. Bubble Tea drops the top lines of
// a frame taller than the terminal, so a pane one line too tall took den's
// header with it; cutting at the bottom keeps the header and the menu in place.
func fitHeight(s string, height int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= height {
		return s
	}
	return strings.Join(lines[:height], "\n")
}

// renderStatusBar is the status line, with a reminder at its right end while
// the terminal has the mouse: den's clicks do nothing then, which would
// otherwise look like a fault.
func (m *AppModel) renderStatusBar() string {
	line := m.statusLine()
	if !m.mouseOff {
		return line
	}
	marker := "  " + components.Hints(components.HelpKey{Key: "M", Text: "mouse to den"}) + "  "
	room := max(m.width-lipgloss.Width(marker), 0)
	line = ansi.Truncate(line, room, "…")
	return ansi.Truncate(line+strings.Repeat(" ", room-lipgloss.Width(line))+marker, m.width, "")
}

// statusLine is what the status bar says: a flash, what keeps den.yaml from
// running, an error, or what is selected.
func (m *AppModel) statusLine() string {
	if m.flashMsg != "" && m.flashWarn {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F5A623")).
			Padding(0, 2).
			Render("⚠  " + m.flashMsg)
	}
	if m.flashMsg != "" {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575")).
			Padding(0, 2).
			Render("✓  " + m.flashMsg)
	}
	// A den.yaml den does not run comes before other errors: until it is
	// fixed or applied, what the file says is not what den does.
	if text := m.configStatus(); text != "" {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF5F87")).
			Padding(0, 2).
			Render(ansi.Truncate("⚠  "+text, max(m.width-4, 1), "…"))
	}
	if m.err != "" {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF5F87")).
			Padding(0, 2).
			Render("⚠  " + m.err)
	}
	var text string
	if m.showDetail {
		text = serviceSummary(m.detailModel.Service())
	} else if svc, ok := m.selectedService(); ok {
		text = serviceSummary(svc)
	} else if item, ok := paneMetadata[m.currentPane()]; ok {
		text = item.desc
	}
	if text == "" {
		return ""
	}
	// One line only: a wrapped status line would push the whole layout down.
	if limit := m.width - 4; limit > 1 && len([]rune(text)) > limit {
		text = string([]rune(text)[:limit-1]) + "…"
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#626262")).
		Padding(0, 2).
		Render(text)
}

// selectedService returns the row highlighted in the focused right pane, for
// the panes that list services.
func (m *AppModel) selectedService() (service.Service, bool) {
	if m.leftFocused {
		return nil, false
	}
	p := m.pane(m.currentPane())
	if p == nil {
		return nil, false
	}
	return p.selected()
}

// serviceSummary describes svc for the status line: what kind of service it is,
// its name and, when the config gives one, its description.
func serviceSummary(svc service.Service) string {
	info := svc.Info()
	text := service.TypeLabel(info.Type) + " — " + info.Name
	if d, ok := svc.(service.Describer); ok && d.Description() != "" {
		text += " · " + d.Description()
	}
	return text
}

func (m *AppModel) renderHelpBar() string {
	entries := []helpEntry{
		{"tab", "switch panel"},
		{"↑↓", "navigate"},
		{"→/enter", "focus right"},
		{"←/esc", "focus left"},
		{"q", "quit"},
	}
	if !m.leftFocused {
		if p := m.pane(m.currentPane()); p != nil {
			if own := p.help(); own != nil {
				entries = own
			}
		}
	}

	keys := make([]components.HelpKey, len(entries))
	for i, e := range entries {
		keys[i] = components.HelpKey{Key: e.key, Text: e.desc}
	}
	return "  " + components.Hints(keys...)
}

// ── tea.Cmd helpers ───────────────────────────────────────────────────────────

func tickCmd(every time.Duration) tea.Cmd {
	return tea.Tick(every, func(t time.Time) tea.Msg {
		return msgs.TickMsg{Time: t}
	})
}

// ssoRenewEvery is how often the AWS SSO sessions' access tokens are renewed.
// They last an hour; a minute keeps a token that ran out unused from staying
// expired for long. A variable so tests can shorten it.
var ssoRenewEvery = time.Minute

// ssoRenewMsg asks for a renewal of every AWS SSO session's access token.
type ssoRenewMsg struct{}

func ssoRenewTick() tea.Cmd {
	return tea.Tick(ssoRenewEvery, func(time.Time) tea.Msg { return ssoRenewMsg{} })
}

// renewSSO renews the access token of each AWS SSO session. A login made
// before den started has no login of den's watching it, and starting one would
// open the browser, so the TUI drives the renewal: RefreshToken renews a token
// that ran out and never logs in. The Cmd ends, so no goroutine outlives den.
func renewSSO(ctx context.Context, sessions []service.Service) tea.Cmd {
	if len(sessions) == 0 {
		return nil
	}
	return func() tea.Msg {
		for _, svc := range sessions {
			_ = svc.RefreshToken(ctx) // a failure is in the session's log
		}
		return nil
	}
}

// waitForUpdate blocks until the manager has a service event and delivers it.
// It replaced polling one event per tick, which let events queue up behind a
// 100 ms tick and dropped them once the services' buffers filled.
func waitForUpdate(mgr *service.Manager) tea.Cmd {
	return func() tea.Msg {
		select {
		case u := <-mgr.Updates():
			return msgs.ServiceEventMsg{Update: u}
		case <-mgr.Done():
			return nil
		}
	}
}

func startService(ctx context.Context, mgr *service.Manager, svc service.Service) tea.Cmd {
	return func() tea.Msg {
		if err := mgr.StartOne(ctx, svc); err != nil {
			if errors.Is(err, service.ErrAlreadyRunning) {
				return warnFlash("already running")
			}
			return msgs.ErrorMsg{Err: err}
		}
		return nil
	}
}

// connectCmds starts svc, or briefly warns when it is already up. Checking here
// rather than relying on Start's error avoids racing the "connecting..." flash,
// and stops a second tunnel being launched while one waits to reconnect.
//
// A service that cannot connect beside processes left from an earlier session
// looks for them first, and starts only when there are none.
func (m *AppModel) connectCmds(svc service.Service) []tea.Cmd {
	if isRunning(svc) {
		return []tea.Cmd{func() tea.Msg { return warnFlash("already running") }}
	}
	if la, ok := svc.(service.LeftoverAware); ok {
		if other := m.otherRunning(svc); other != "" {
			text := other + " — den runs one VPN at a time; disconnect it first"
			return []tea.Cmd{func() tea.Msg { return warnFlash(text) }}
		}
		return []tea.Cmd{checkLeftovers(m.ctx, svc, la)}
	}
	return m.startCmds(svc)
}

// startCmds starts svc unless it is already up.
func (m *AppModel) startCmds(svc service.Service) []tea.Cmd {
	if isRunning(svc) {
		return []tea.Cmd{func() tea.Msg { return warnFlash("already running") }}
	}
	flash := msgs.StatusFlashMsg{Text: "connecting..."}
	if warning := m.vpnDownWarning(svc); warning != "" {
		flash = warnFlash(warning)
	}
	return []tea.Cmd{
		startService(m.ctx, m.manager, svc),
		func() tea.Msg { return flash },
	}
}

// warnFlash is a status bar warning that stays long enough to read.
func warnFlash(text string) msgs.StatusFlashMsg {
	return msgs.StatusFlashMsg{Text: text, Warn: true, Hold: 3 * time.Second}
}

// isRunning reports whether svc is up or on its way up.
func isRunning(svc service.Service) bool {
	switch svc.State().Status {
	case service.StatusConnecting, service.StatusConnected, service.StatusReconnecting:
		return true
	}
	return false
}

// refreshesToken reports whether r (refresh token) means anything for svc: only
// services holding an expiring IAM credential, plus AWS SSO sessions (whose
// refresh renews an expired access token and re-reads the token cache). For
// the rest RefreshToken is a no-op, so offering it would only mislead.
func refreshesToken(svc service.Service) bool {
	return svc.Info().Type == service.TypeAWS || service.HoldsToken(svc)
}

// reconcileRunningVPNs adopts any VPN tunnel that survived a previous den
// session. vpn.New seeds StatusConnected when it detects a live tunnel at
// startup, or StatusConnecting when its health_check has yet to confirm it;
// nothing has started a VPN yet, so either can only be that. StartOne here
// drains the service's updates channel and runs its check and drop-detection
// goroutine so the tunnel is monitored and disconnectable.
//
// Only the confirmed ones are reported as up. The others send Connected
// through their updates once the check passes, and auto-connect follows that.
func reconcileRunningVPNs(ctx context.Context, mgr *service.Manager) tea.Cmd {
	return func() tea.Msg {
		var adopted []string
		for _, svc := range mgr.Services() {
			if svc.Info().Type != service.TypeVPN {
				continue
			}
			switch svc.State().Status {
			case service.StatusConnected:
				_ = mgr.StartOne(ctx, svc)
				adopted = append(adopted, svc.Info().Name)
			case service.StatusConnecting:
				_ = mgr.StartOne(ctx, svc)
			}
		}
		if len(adopted) == 0 {
			return nil
		}
		return vpnsAdoptedMsg{names: adopted}
	}
}

func stopService(ctx context.Context, svc service.Service) tea.Cmd {
	return func() tea.Msg {
		if err := svc.Stop(ctx); err != nil {
			return msgs.ErrorMsg{Err: err}
		}
		return nil
	}
}

func refreshToken(ctx context.Context, svc service.Service) tea.Cmd {
	return func() tea.Msg {
		if err := svc.RefreshToken(ctx); err != nil {
			return msgs.ErrorMsg{Err: err}
		}
		return nil
	}
}

func isSSOError(msg string) bool {
	return strings.Contains(msg, "InvalidGrantException") ||
		strings.Contains(msg, "refresh cached SSO token") ||
		strings.Contains(msg, "SSO token") ||
		strings.Contains(msg, "sso login")
}

// filterByType returns only services whose Info().Type matches the given type string.
func filterByType(svcs []service.Service, typ string) []service.Service {
	var out []service.Service
	for _, s := range svcs {
		if s.Info().Type == typ {
			out = append(out, s)
		}
	}
	return out
}

// buildMenuOrder returns the configured menu order — unknown panes dropped,
// the default when none remain — minus the disabled panes.
func buildMenuOrder(from, disabled []string) []paneID {
	var order []paneID
	for _, id := range from {
		if _, ok := paneMetadata[id]; ok {
			order = append(order, id)
		}
	}
	if len(order) == 0 {
		order = defaultMenuOrder
	}
	off := make(map[string]bool, len(disabled))
	for _, id := range disabled {
		off[id] = true
	}
	out := make([]paneID, 0, len(order))
	for _, id := range order {
		if !off[id] {
			out = append(out, id)
		}
	}
	return out
}

// reloadSummary is the status line after a reload.
func reloadSummary(services, stopped, stillRunning int) string {
	text := fmt.Sprintf("config reloaded — %d services", services)
	if stillRunning > 0 {
		text += fmt.Sprintf(", %d %s kept", stillRunning, plural(stillRunning, "connection", "connections"))
	}
	if stopped > 0 {
		text += fmt.Sprintf(", %d changed %s stopped", stopped, plural(stopped, "connection", "connections"))
	}
	return text
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func countRunning(svcs []service.Service) int {
	n := 0
	for _, s := range svcs {
		if isRunning(s) {
			n++
		}
	}
	return n
}

// shutdown stops everything den started, for a quit or a restart.
func (m *AppModel) shutdown() {
	// First, while nothing is stopped yet: what was typed for an agent since
	// the form last saved it.
	m.runbooksPane.SaveDraft()
	m.runbookView.SaveDraft()
	// Stop runbooks before cancelling the context so their scripts get an
	// interrupt and a grace period rather than being orphaned.
	if m.runbookReg != nil {
		m.runbookReg.StopAll()
	}
	m.stopAllTerminals()
	m.cancel()
	m.manager.StopAll(m.ctx)
	m.manager.Close()
	m.logger.Close()
}

// closedByRestart lists what a restart would close, for the restart prompt.
// AWS SSO sessions are left out: their token lives in the aws CLI's cache and
// the restarted den finds it again.
func closedByRestart(mgr *service.Manager, terminals map[service.Service]*detail.Tabs, runbookTabs map[string]*components.Tabs) func() []string {
	return func() []string {
		var out []string
		for _, svc := range mgr.Services() {
			info := svc.Info()
			if info.Type == service.TypeAWS || !isRunning(svc) {
				continue
			}
			line := info.Type + "  " + info.Name
			if port := svc.State().LocalPort; port > 0 {
				line += fmt.Sprintf("  :%d", port)
			}
			out = append(out, line)
			// The clients in its terminal tabs go with it, and with them
			// whatever was open in them: a transaction, an unsaved edit.
			if tabs := terminals[svc]; tabs != nil && tabs.Running() > 0 {
				n := tabs.Running()
				out = append(out, fmt.Sprintf("%d %s  %s", n, plural(n, "terminal", "terminals"), info.Name))
			}
		}
		return append(out, runbookTerminalLines(runbookTabs)...)
	}
}
