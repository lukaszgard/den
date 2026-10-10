package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/tui/screens/awspane"
	"github.com/lukaszgard/den/internal/tui/screens/createenv"
	"github.com/lukaszgard/den/internal/tui/screens/runbooks"
	"github.com/lukaszgard/den/internal/tui/screens/tunnel"
	vpnpane "github.com/lukaszgard/den/internal/tui/screens/vpn"
)

// ConfigFile is how the TUI reads den.yaml again and applies it to the running
// session. A reload never applies a file with errors: den keeps running the
// last config that had none, says what is wrong, and offers to fix or undo it.
type ConfigFile struct {
	// Check reads the file and lists its problems. The config is nil when any
	// problem is an error; err is for a file that cannot be read at all.
	Check func() (*config.Config, config.Problems, error)
	// WouldStop names the running connections applying cfg would stop, those
	// whose entries it changes or removes, without changing anything.
	WouldStop func(cfg *config.Config) []string
	// Apply makes the manager match cfg, keeping the services that did not
	// change, running or not.
	Apply func(ctx context.Context, cfg *config.Config) service.ReconcileResult
}

// reloadOrigin is what made den read den.yaml again.
type reloadOrigin int

const (
	reloadAsked    reloadOrigin = iota // R, or r in the Config pane
	reloadEdited                       // the editor den opened closed on a changed file
	reloadRestored                     // u put the file back as it was before the edit
)

// configCheckedMsg is the outcome of a check of den.yaml, run off the UI
// goroutine: reading the file and listing the AWS sessions can be slow.
type configCheckedMsg struct {
	cfg      *config.Config
	problems config.Problems
	stops    []string // running connections applying cfg would stop
	origin   reloadOrigin
	before   []byte // the file before den's editor changed it; nil for R
}

// configEditedMsg reports that the editor den opened on den.yaml closed.
type configEditedMsg struct {
	before []byte // the file before the edit
	err    error
}

// configReloadedMsg reports that a checked config was applied.
type configReloadedMsg struct {
	cfg      *config.Config
	problems config.Problems // warnings only: a config with errors is never applied
	res      service.ReconcileResult
	origin   reloadOrigin
}

// configPrompt is the pop-up a reload opens. With errors in problems it says
// why the file was not applied; otherwise it asks before applying cfg, which
// stops the running connections in stops.
type configPrompt struct {
	problems config.Problems
	cfg      *config.Config
	stops    []string
	origin   reloadOrigin
	// before is the file as it was before den's editor changed it, which u
	// puts back; nil when there is no edit to undo.
	before []byte
}

func (p *configPrompt) failed() bool { return p.problems.Errors() > 0 }

// editorStarter runs the editor on path and reports its exit through done.
// Tests replace it: an editor needs the terminal.
type editorStarter func(path string, done func(error) tea.Msg) tea.Cmd

func startEditor(path string, done func(error) tea.Msg) tea.Cmd {
	c := editorCommand(platform.UserEditor(), path)
	return tea.ExecProcess(exec.Command(c.Path, c.Args...), func(err error) tea.Msg { return done(err) })
}

// editConfig opens den.yaml in the user's editor. before is what the file held
// before the first of a run of edits ("e: edit again" keeps it, so u still
// undoes all of them); nil reads it now.
func (m *AppModel) editConfig(before []byte) tea.Cmd {
	if m.cfgFile == nil {
		return func() tea.Msg { return warnFlash("no config file to edit") }
	}
	if before == nil {
		data, err := os.ReadFile(m.configPath)
		if err != nil {
			return func() tea.Msg { return warnFlash("cannot read den.yaml: " + err.Error()) }
		}
		before = data
	}
	return m.startEditor(m.configPath, func(err error) tea.Msg { return configEditedMsg{before: before, err: err} })
}

// configEdited decides what a closed editor leads to: nothing for a file it
// left as it was, unless that file still has the errors it was opened to fix.
func (m *AppModel) configEdited(msg configEditedMsg) tea.Cmd {
	if msg.err != nil {
		m.logger.Warn("editor: " + msg.err.Error())
	}
	now, err := os.ReadFile(m.configPath)
	if err == nil && bytes.Equal(now, msg.before) && m.cfgProblems.Errors() == 0 {
		return func() tea.Msg { return msgs.StatusFlashMsg{Text: "den.yaml unchanged"} }
	}
	return m.checkConfig(reloadEdited, msg.before)
}

// checkConfig reads den.yaml and checks it off the UI goroutine.
func (m *AppModel) checkConfig(origin reloadOrigin, before []byte) tea.Cmd {
	if m.cfgFile == nil {
		return func() tea.Msg { return warnFlash("no config file to reload") }
	}
	file := m.cfgFile
	return func() tea.Msg {
		cfg, ps, err := file.Check()
		if err != nil {
			cfg, ps = nil, config.Problems{{Severity: config.SeverityError, Msg: err.Error()}}
		}
		var stops []string
		if cfg != nil && file.WouldStop != nil {
			stops = file.WouldStop(cfg)
		}
		return configCheckedMsg{cfg: cfg, problems: ps, stops: stops, origin: origin, before: before}
	}
}

// configChecked acts on a check: a file with errors opens the pop-up that
// says so, one that would stop connections the pop-up that asks, and any
// other is applied.
func (m *AppModel) configChecked(msg configCheckedMsg) tea.Cmd {
	m.configPane = m.configPane.Refresh().SetProblems(msg.problems)
	if msg.cfg == nil {
		m.cfgProblems, m.cfgDeferred = msg.problems, nil
		m.logger.Warn(fmt.Sprintf("den.yaml not applied, %s:", msg.problems.Summary()))
		for _, p := range msg.problems {
			m.logger.Warn("  " + p.String())
		}
		m.cfgPrompt = &configPrompt{problems: msg.problems, origin: msg.origin, before: msg.before}
		return nil
	}
	if len(msg.stops) > 0 {
		m.cfgPrompt = &configPrompt{problems: msg.problems, cfg: msg.cfg, stops: msg.stops, origin: msg.origin, before: msg.before}
		return nil
	}
	return m.applyConfig(msg.cfg, msg.problems, msg.origin)
}

// applyConfig applies a checked config off the UI goroutine: stopping
// services can take a while.
func (m *AppModel) applyConfig(cfg *config.Config, ps config.Problems, origin reloadOrigin) tea.Cmd {
	file, ctx := m.cfgFile, m.ctx
	return func() tea.Msg {
		return configReloadedMsg{cfg: cfg, problems: ps, res: file.Apply(ctx, cfg), origin: origin}
	}
}

// configPromptKey handles a key while the reload pop-up is open; it takes
// every key, so one pressed while reading it never reaches the pane behind.
func (m *AppModel) configPromptKey(k string) tea.Cmd {
	p := m.cfgPrompt
	if p.failed() {
		switch k {
		case "e":
			m.cfgPrompt = nil
			return m.editConfig(p.before)
		case "u":
			if p.before == nil {
				return nil
			}
			m.cfgPrompt = nil
			if err := writeKeepingMode(m.configPath, p.before); err != nil {
				return func() tea.Msg { return warnFlash("cannot restore den.yaml: " + err.Error()) }
			}
			m.logger.Info("den.yaml restored as it was before the edit")
			return m.checkConfig(reloadRestored, nil)
		case "esc", "q", "enter":
			m.cfgPrompt = nil
		}
		return nil
	}
	switch k {
	case "y":
		m.cfgPrompt = nil
		return m.applyConfig(p.cfg, p.problems, p.origin)
	case "n", "esc", "q":
		m.cfgPrompt = nil
		m.cfgDeferred = p.stops
		m.configPane = m.configPane.SetProblems(p.problems)
	}
	return nil
}

// writeKeepingMode writes data over an existing file without changing its
// permissions.
func writeKeepingMode(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, info.Mode().Perm())
}

// configReloaded brings the session in line with a config that was applied:
// the menu, the panes, the log settings.
func (m *AppModel) configReloaded(msg configReloadedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	for _, err := range msg.res.Errors {
		m.logger.Warn("config reload: " + err.Error())
	}
	for _, p := range msg.problems {
		m.logger.Warn("den.yaml: " + p.String())
	}
	m.cfg = msg.cfg
	m.cfgProblems, m.cfgDeferred = msg.problems, nil
	m.awsCfgPath, m.credentialsPath = m.cfg.AWSConfigPath(), m.cfg.AWSCredentialsPath()
	if logs := m.cfg.LogsSettings(); logs != nil {
		_ = m.logger.Configure(logs.Level, logs.FileLocation)
	} else {
		_ = m.logger.Configure("", "")
	}
	m.setMenu(m.cfg)
	rw := m.width - leftPaneWidth - 1
	svcs := m.manager.Services()
	m.dropRemovedTerminals()
	m.connectPane = m.connectPane.SetServices(svcs)
	m.tunnelPane = tunnel.New(filterByType(svcs, service.TypeTunnel), rw, m.height)
	m.vpnPane = vpnpane.New(filterByType(svcs, service.TypeVPN), rw, m.height).SetConfigPath(m.configPath)
	m.createEnvPane = createenv.New(svcs, rw, m.height)
	// The registry reconciles: runbooks keep their runs, and one that is
	// running when it changes or goes away finishes first.
	m.runbookReg.Reload(m.cfg, m.configPath)
	logRunbookWarnings(m.runbookReg, m.logger)
	m.runbooksPane = runbooks.New(m.runbookReg, m.ctx, runningTerminals(m.runbookTabs), rw, m.height).
		WithOutput(m.logger.Output).SetDark(!m.light)
	cmds = append(cmds, m.dropGoneRunbooks()...)
	m.awsPane = awspane.New(filterByType(svcs, service.TypeAWS), m.awsCfgPath, m.credentialsPath, rw, m.height)
	m.secretsPane = newSecretsPane(m.cfg, m.awsCfgPath, rw, m.height)
	m.updatesPane = m.updatesPane.SetUpdatesConfig(m.cfg.UpdatesSettings())
	m.configPane = m.configPane.Refresh().SetProblems(msg.problems)
	// Errors from the previous config no longer apply.
	m.err = ""
	text := reloadSummary(len(svcs), msg.res.Stopped, countRunning(svcs))
	m.logger.Log(text)
	if msg.origin == reloadRestored {
		text = "den.yaml restored, " + text
	}
	flash := msgs.StatusFlashMsg{Text: text, Hold: 3 * time.Second}
	if n := msg.problems.Warnings(); n > 0 {
		flash = msgs.StatusFlashMsg{Text: fmt.Sprintf("%s — %s, see Config", text, msg.problems.Summary()), Warn: true, Hold: 5 * time.Second}
	}
	return append(cmds, func() tea.Msg { return flash })
}

// setMenu rebuilds the left menu from cfg's menu_order and disabled_panes,
// keeping the cursor on the panel it was on while that panel is still there.
func (m *AppModel) setMenu(cfg *config.Config) {
	current := m.currentPane()
	var order, disabled []string
	if cfg != nil {
		order, disabled = cfg.MenuOrder, cfg.DisabledPanes
	}
	m.menuOrder = buildMenuOrder(order, disabled)
	if i := m.paneIndexOf(current); i >= 0 {
		m.leftCursor = i
		return
	}
	// The panel went away: the menu takes the keyboard, nearer the top.
	m.leftCursor = max(min(m.leftCursor, len(m.menuOrder)-1), 0)
	m.leftFocused = true
}

// configStatus is the status bar while den.yaml on disk is not what den runs:
// it has errors, or applying it was put off.
func (m *AppModel) configStatus() string {
	if n := m.cfgProblems.Errors(); n > 0 {
		running := "den runs the last good version"
		if m.cfg == nil {
			running = "den runs without it"
		}
		return fmt.Sprintf("den.yaml has %s, %s — see Config", count(n, "error", "errors"), running)
	}
	if len(m.cfgDeferred) > 0 {
		return fmt.Sprintf("den.yaml not applied: it stops %s — R applies it", strings.Join(m.cfgDeferred, ", "))
	}
	return ""
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

var (
	promptErr   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF5F87"))
	promptTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ECFD65"))
	promptNote  = lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
	promptDim   = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	promptWarn  = lipgloss.NewStyle().Foreground(lipgloss.Color("#E9C46A"))
	promptRed   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87"))
)

// promptWidth is the widest a reload pop-up's text gets: wider, a long
// problem would stretch it across a wide screen.
const promptWidth = 100

// view draws the pop-up for a screen of width×height, never larger than it.
// running says whether den runs an earlier version of the file.
func (p *configPrompt) view(width, height int, running bool) string {
	inner := max(min(width-6, promptWidth), 10)
	var content []string
	var footer []components.HelpKey
	if p.failed() {
		note := "den runs without it until it is fixed"
		if running {
			note = "den keeps running the last good version"
		}
		content = append(content, promptErr.Render("den.yaml has "+p.problems.Summary()), promptNote.Render(note), "")
		edit := "edit"
		if p.origin == reloadEdited {
			edit = "edit again"
		}
		footer = append(footer, components.HelpKey{Key: "e", Text: edit})
		if p.before != nil {
			footer = append(footer, components.HelpKey{Key: "u", Text: "undo the edit"})
		}
		footer = append(footer, components.HelpKey{Key: "esc", Text: "close"})
	} else {
		what, them := fmt.Sprintf("%d running connections", len(p.stops)), "them"
		if len(p.stops) == 1 {
			what, them = "a running connection", "it"
		}
		content = append(content, promptTitle.Render("Apply den.yaml?"),
			promptNote.Render("It changes or removes "+what+", so applying it stops:"), "")
		for _, name := range p.stops {
			content = append(content, "  • "+name)
		}
		if len(p.problems) > 0 {
			content = append(content, "")
		}
		footer = []components.HelpKey{{Key: "y", Text: "apply and stop " + them}, {Key: "n", Text: "not now"}}
	}
	// The problems, as many as fit: p in the Config pane lists them all.
	room := max(height-len(content)-6, 1)
	for i, pr := range p.problems {
		if i == room-1 && len(p.problems) > room {
			content = append(content, promptDim.Render(fmt.Sprintf("… and %d more — p in Config lists them", len(p.problems)-i)))
			break
		}
		icon := promptRed.Render("✗ ")
		if pr.Severity == config.SeverityWarning {
			icon = promptWarn.Render("! ")
		}
		where := ""
		if pr.Line > 0 {
			where = promptDim.Render(fmt.Sprintf("line %d  ", pr.Line))
		}
		content = append(content, icon+where+pr.Msg)
	}
	content = append(content, "", components.Hints(footer...))
	for i, line := range content {
		content[i] = ansi.Truncate(line, inner, "…")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#6C91BF")).
		Padding(0, 2).
		Render(strings.Join(content, "\n"))
}
