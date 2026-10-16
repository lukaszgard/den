// Package updates implements the right-panel "Updates" view.
// It shows the current version and allows checking for new releases, read from
// the GitHub release feed (or its API) or from an S3 bucket, and installing any
// published version, older ones included, from the version picker.
package updates

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	den "github.com/lukaszgard/den"
	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/tui/applog"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/update"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type updateState int

const (
	stateIdle updateState = iota
	stateChecking
	stateUpToDate
	stateUpdateAvailable
	stateError
	stateInstalling
	stateInstallDone
	stateInstallError
)

// installContext carries shared state between install steps via closures.
type installContext struct {
	src          update.Source
	rel          update.Release
	archivePath  string // downloaded archive, removed once the install ends
	tempBinPath  string // extracted binary before it replaces finalBinPath
	finalBinPath string // the running binary, symlinks resolved
	// Progress tracking — written by the download, read by the poll command.
	downloadedBytes atomic.Int64
	totalBytes      atomic.Int64 // 0 = unknown
	downloadDone    atomic.Bool
}

// result is the install's outcome, naming the version it was for.
func (ctx *installContext) result(err error) tea.Msg {
	return msgs.UpdateInstallResultMsg{Version: ctx.rel.Version, Err: err}
}

// Model is the Updates right-pane model.
type Model struct {
	version         string
	updatesCfg      *config.UpdatesConfig
	logger          *applog.Logger
	state           updateState
	latest          string
	url             string
	release         update.Release // the release an install fetches
	notes           string         // release notes for the latest version
	errMsg          string
	installErr      string
	progressLines   []string
	downloadedBytes int64
	downloadTotal   int64
	showHelp        bool
	showChangelog   bool
	changelog       components.MarkdownView
	width           int
	height          int
	// light is set once the terminal reported a light background; the zero
	// value is the dark palette, which shows until it replies.
	light bool
	// confirmRestart shows the restart prompt after a successful install.
	confirmRestart bool
	// active lists what a restart would close; read when the prompt renders,
	// so it is current rather than a snapshot from the end of the install.
	active func() []string

	// releases is what the last check or picker load listed, newest first;
	// nil until one did, and after a config reload, which may change the source.
	releases []update.Release
	why      string // why the check offered m.latest, from update.Why
	picker   picker
	// prefsPath is this machine's updates.json (the skip list); "" keeps none.
	prefsPath string
	// skipAfter is the version a downgrade leaves, which goes on the skip list
	// once the install has succeeded.
	skipAfter string
	// newSource builds the release source; update.NewSource but in tests.
	newSource func(*config.UpdatesConfig) update.Source
}

// New creates an Updates pane. active lists the connections a restart would
// close; nil means none.
func New(version string, cfg *config.UpdatesConfig, logger *applog.Logger, active func() []string, width, height int) Model {
	return Model{version: version, updatesCfg: cfg, logger: logger, active: active, width: width, height: height,
		newSource: update.NewSource}
}

// WithPrefs keeps the skip list in path, this machine's updates.json.
func (m Model) WithPrefs(path string) Model {
	m.prefsPath = path
	return m
}

// source is where this pane reads releases from, for the current config.
func (m Model) source() update.Source { return m.newSource(m.updatesCfg) }

func (m Model) Init() tea.Cmd { return nil }

// Version returns the current version string.
func (m Model) Version() string { return m.version }

// UpdatesConfig returns the configured UpdatesConfig (may be nil).
func (m Model) UpdatesConfig() *config.UpdatesConfig { return m.updatesCfg }

// SetUpdatesConfig changes where the next check looks, after a config reload.
// The releases listed so far may be another source's, so they are dropped.
func (m Model) SetUpdatesConfig(cfg *config.UpdatesConfig) Model {
	m.updatesCfg = cfg
	m.releases = nil
	return m
}

// ShowingHelp reports whether the help popup is currently open.
func (m Model) ShowingHelp() bool { return m.showHelp }

// ShowingChangelog reports whether the changelog overlay is currently open.
func (m Model) ShowingChangelog() bool { return m.showChangelog }

// Searching reports that the search box of the changelog, or of a release's
// notes in the picker, has the keyboard.
func (m Model) Searching() bool {
	return (m.showChangelog && m.changelog.Searching()) || (m.picker.open && m.picker.notes && m.picker.view.Searching())
}

// ShowingPicker reports whether the version picker is open.
func (m Model) ShowingPicker() bool { return m.picker.open }

// Progress is the install in flight as the terminal's tab progress bar: the
// download as a percentage while its size is known, indeterminate for the other
// steps; nil when no install runs.
func (m Model) Progress() *tea.ProgressBar {
	if m.state != stateInstalling {
		return nil
	}
	if m.downloadTotal > 0 && m.downloadedBytes < m.downloadTotal {
		return tea.NewProgressBar(tea.ProgressBarDefault, int(m.downloadedBytes*100/m.downloadTotal))
	}
	return tea.NewProgressBar(tea.ProgressBarIndeterminate, 0)
}

// ShowingRestartPrompt reports whether the "restart now?" prompt is open.
func (m Model) ShowingRestartPrompt() bool { return m.confirmRestart }

// SetDark switches the changelog to the palette of a dark or a light
// background, which the terminal reports once the program runs.
func (m Model) SetDark(isDark bool) Model {
	m.light = !isDark
	if m.showChangelog {
		m.changelog.SetDark(isDark)
	}
	if m.picker.notes {
		m.picker.view.SetDark(isDark)
	}
	return m
}

// Dark reports whether the pane draws for a dark background, and ChangelogDark
// the same for the changelog, which keeps its own copy while it is open.
func (m Model) Dark() bool { return !m.light }

// ChangelogDark reports whether the changelog is drawn for a dark background.
func (m Model) ChangelogDark() bool { return m.changelog.Dark() }

// SetSize updates the pane dimensions without resetting check state.
func (m Model) SetSize(width, height int) Model {
	m.width = width
	m.height = height
	if m.showChangelog {
		m.changelog.SetSize(width, height)
	}
	if m.picker.notes {
		m.picker.view.SetSize(width, height)
	}
	return m
}

func (m Model) Update(msg tea.Msg, focused bool) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.UpdateCheckStartedMsg:
		m.state = stateChecking
		return m, nil

	case msgs.UpdateCheckResultMsg:
		if msg.Err != nil {
			m.state = stateError
			m.errMsg = msg.Err.Error()
			return m, nil
		}
		if msg.Latest == "" {
			m.state = stateError
			m.errMsg = "no release tag returned by API"
			return m, nil
		}
		m.latest = msg.Latest
		m.url = msg.URL
		m.release = msg.Release
		if m.release.Version == "" {
			m.release.Version = msg.Latest
		}
		m.notes = strings.TrimSpace(msg.Notes)
		m.why = msg.Why
		if msg.Releases != nil {
			m.releases = msg.Releases
		}
		if update.SameVersion(msg.Latest, m.version) {
			m.state = stateUpToDate
		} else {
			m.state = stateUpdateAvailable
		}

	case msgs.UpdateDownloadProgressMsg:
		m.downloadedBytes = msg.Downloaded
		m.downloadTotal = msg.Total
		return m, msg.NextCmd

	case msgs.UpdateInstallProgressMsg:
		m.progressLines = append(m.progressLines, "✓ "+msg.Step)
		return m, msg.NextCmd

	case msgs.UpdateReleasesMsg:
		m = m.releasesLoaded(msg)

	case msgs.UpdateInstallResultMsg:
		skip := m.skipAfter
		m.skipAfter = ""
		if msg.Err != nil {
			m.state = stateInstallError
			m.installErr = msg.Err.Error()
		} else {
			m.state = stateInstallDone
			m.confirmRestart = update.CanReexec
			if skip != "" {
				m = m.skipVersion(skip)
			}
		}

	case tea.KeyPressMsg:
		if !focused {
			break
		}
		// Block all keys while installing
		if m.state == stateInstalling {
			return m, nil
		}
		if m.showChangelog {
			// A search being typed takes every key, s and q included, and esc
			// ends a search before it closes the changelog.
			if m.changelog.Searching() || (m.changelog.HasQuery() && msg.String() == "esc") {
				return m, m.changelog.Update(msg)
			}
			if msg.String() == "esc" || msg.String() == "s" || msg.String() == "q" {
				m.showChangelog = false
				return m, nil
			}
			return m, m.changelog.Update(msg)
		}
		if m.showHelp {
			if msg.String() == "esc" || msg.String() == "h" || msg.String() == "q" {
				m.showHelp = false
			}
			return m, nil
		}
		if m.picker.open {
			return m.pickerKey(msg)
		}
		// Only an explicit y restarts — not enter: a restart drops every
		// connection, so it must not happen on a reflex keypress.
		if m.confirmRestart {
			switch msg.String() {
			case "y", "Y":
				m.confirmRestart = false
				version := m.release.Version
				return m, func() tea.Msg { return msgs.UpdateRestartMsg{Version: version} }
			case "n", "N", "esc", "backspace":
				m.confirmRestart = false
			}
			return m, nil
		}
		switch msg.String() {
		case "r":
			if m.state == stateInstallDone && update.CanReexec {
				m.confirmRestart = true
			}
		case "c":
			m.state = stateChecking
			return m, tea.Batch(
				func() tea.Msg { return msgs.UpdateCheckStartedMsg{} },
				m.Check(),
			)
		case "v":
			return m.openPicker()
		case "h":
			m.showHelp = true
		case "s":
			doc := changelogDoc(den.Changelog, m.latest, m.notes)
			m.changelog = components.NewMarkdownView("Changelog", doc,
				components.HelpKey{Key: "esc/s", Text: "close"}, components.HelpKey{Key: "↑↓", Text: "scroll"})
			m.changelog.SetDark(!m.light)
			m.changelog.SetSize(m.width, m.height)
			m.showChangelog = true
		case "i":
			if m.state != stateUpdateAvailable && m.state != stateInstallError {
				break
			}
			return m.install(m.release, "")
		}
	}
	return m, nil
}

// install starts installing rel through the step chain. skip, when set, is
// the version to put on the skip list once the install has succeeded: the one
// a downgrade leaves.
func (m Model) install(rel update.Release, skip string) (Model, tea.Cmd) {
	src := m.source()
	m.release = rel
	if !src.CanInstall(rel) {
		m.state = stateInstallError
		m.installErr = "no " + rel.Archive + " asset in this release — re-check for updates (c)"
		return m, nil
	}
	ctx := &installContext{src: src, rel: rel}
	m.state = stateInstalling
	m.picker = picker{}
	m.skipAfter = skip
	m.progressLines = nil
	m.installErr = ""
	m.downloadedBytes = 0
	m.downloadTotal = 0
	version := rel.Version
	return m, tea.Batch(
		func() tea.Msg { return msgs.UpdateInstallStartedMsg{Version: version} },
		stepResolveBinaryPath(ctx),
	)
}

func (m Model) View(width, height int, focused bool) string {
	if m.showChangelog {
		return m.changelog.View()
	}
	if m.showHelp {
		return clampRows(m.renderHelp(width, height), height)
	}
	if m.picker.open {
		if m.picker.notes {
			return m.picker.view.View()
		}
		return m.renderPicker(width, height, focused)
	}

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#ECFD65")).
		Padding(0, 2).
		Render("Updates")

	versionText := "Current version: " + m.version
	if pin := m.pin(); pin != "" {
		versionText += "  ·  pinned to " + update.Canonical(pin) + " (updates.pin)"
	}
	versionLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#9B9B9B")).
		Padding(0, 2).
		Render(versionText)

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
	code := lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65"))

	var statusLines []string
	// A yanked release is worth saying in every state: it is why the check
	// may offer an older one.
	if reason := m.currentYanked(); reason != "" {
		statusLines = append(statusLines,
			lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Bold(true).
				Render(fmt.Sprintf("⚠ %s is yanked: %s", update.Canonical(m.version), reason)), "")
	}
	switch m.state {
	case stateIdle:
		statusLines = append(statusLines,
			lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Render("Press c to check for latest version"),
		)
	case stateChecking:
		statusLines = append(statusLines,
			code.Render("Checking..."),
		)
	case stateUpToDate:
		statusLines = append(statusLines,
			lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575")).Render(fmt.Sprintf("✓ Up to date (%s)", m.latest)),
		)
		if why := m.whyLine(); why != "" {
			statusLines = append(statusLines, dim.Render("  "+why))
		}
	case stateUpdateAvailable:
		archive, extractCmds, installCmd := installInstructions(m.latest)
		downloadCmds, releasePage := m.downloadInstructions(archive)
		available := fmt.Sprintf("↑ %s available", m.latest)
		if update.Compare(m.latest, m.version) < 0 {
			available = fmt.Sprintf("↓ %s offered — older than this den", m.latest)
		}
		lines := []string{
			lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65")).Bold(true).Render(available),
		}
		if why := m.whyLine(); why != "" {
			lines = append(lines, dim.Render("  "+why))
		}
		// The notes are a whole CHANGELOG section; they open in the changelog
		// box, which scrolls.
		if m.newNotes() {
			lines = append(lines, "", dim.Render("What's new: press ")+code.Render("s"))
		}
		lines = append(lines, "", dim.Render("1. Download the release archive:"))
		for _, cmd := range downloadCmds {
			lines = append(lines, code.Render("   "+cmd))
		}
		lines = append(lines, "", dim.Render("2. Extract and install:"))
		for _, cmd := range extractCmds {
			lines = append(lines, code.Render("   "+cmd))
		}
		lines = append(lines, code.Render("   "+installCmd))
		lines = append(lines,
			"",
			dim.Render("3. Verify:"),
			code.Render("   den --version"),
		)
		if releasePage != "" {
			lines = append(lines, "", dim.Render("Release page: "+releasePage))
		}
		lines = append(lines, "", dim.Render("Or press i to auto-install."))
		statusLines = append(statusLines, lines...)

	case stateInstalling:
		lines := []string{
			lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65")).Bold(true).
				Render(fmt.Sprintf("⠋ Installing %s…", m.release.Version)),
			"",
		}
		if m.downloadedBytes > 0 {
			lines = append(lines, renderDownloadBar(m.downloadedBytes, m.downloadTotal, width-4))
		}
		for _, step := range m.progressLines {
			lines = append(lines, dim.Render("  "+step))
		}
		statusLines = append(statusLines, lines...)

	case stateInstallDone:
		statusLines = append(statusLines,
			lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575")).Bold(true).
				Render(fmt.Sprintf("✓ Installed %s successfully", m.release.Version)),
			"",
			dim.Render("Restart den to use the new version."),
		)
		if update.CanReexec {
			statusLines = append(statusLines, dim.Render("Press r to restart now."))
		}

	case stateInstallError:
		lines := []string{
			lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Bold(true).Render("Install failed"),
		}
		for line := range strings.SplitSeq(m.installErr, ": ") {
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Render("  "+line))
		}
		if len(m.progressLines) > 0 {
			lines = append(lines, "")
			for _, step := range m.progressLines {
				lines = append(lines, dim.Render("  "+step))
			}
		}
		statusLines = append(statusLines, lines...)

	case stateError:
		lines := []string{
			lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Bold(true).Render("Update check failed"),
		}
		for line := range strings.SplitSeq(m.errMsg, ": ") {
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Render("  "+line))
		}
		statusLines = append(statusLines, lines...)
	}
	status := "  " + strings.Join(statusLines, "\n  ")
	if m.confirmRestart {
		status = m.renderRestartPrompt(width)
	}
	// The rows around the status: a blank, the title, the version, two blanks
	// and the hint.
	status = clampRows(status, height-6)

	hint := ""
	if focused {
		changelog := components.HelpKey{Key: "s", Text: "changelog"}
		if m.newNotes() {
			changelog.Text = "what's new"
		}
		check, help := components.HelpKey{Key: "c", Text: "check"}, components.HelpKey{Key: "h", Text: "help"}
		versions := components.HelpKey{Key: "v", Text: "versions"}
		keys := []components.HelpKey{check, changelog, versions, help}
		switch {
		case m.confirmRestart:
			keys = []components.HelpKey{{Key: "y", Text: "restart now"}, {Key: "n", Text: "later"}}
		case m.state == stateInstallDone && update.CanReexec:
			keys = []components.HelpKey{{Key: "r", Text: "restart"}, check, changelog, versions, help}
		case m.state == stateUpdateAvailable || m.state == stateInstallError:
			keys = []components.HelpKey{check, {Key: "i", Text: "install"}, changelog, versions, help}
		}
		hint = components.HintLine(width, true, keys...)
	} else {
		hint = components.HintLine(width, false, components.HelpKey{Key: "→/enter", Text: "focus"})
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		"",
		header,
		versionLine,
		"",
		status,
		"",
		"  "+hint,
	)
}

// changelogDoc is what s shows: the changelog this binary was built with and,
// above it, the notes of a newer release, which that changelog cannot have. A
// build from a checkout that already lists the release is left as it is.
func changelogDoc(installed, latest, notes string) string {
	if notes == "" || hasRelease(installed, latest) {
		return installed
	}
	rest := strings.TrimPrefix(strings.TrimLeft(installed, "\n"), "# Changelog")
	return "# Changelog\n\n## " + latest + " — new, not installed yet\n\n" + notes + "\n\n" + strings.TrimLeft(rest, "\n")
}

// hasRelease reports whether changelog has a section for version. The header
// is the one the release workflow cuts the notes at.
func hasRelease(changelog, version string) bool {
	return strings.Contains(changelog, "## ["+strings.TrimPrefix(version, "v")+"]")
}

// newNotes reports whether s opens on the notes of a release newer than this
// binary.
func (m Model) newNotes() bool {
	return m.notes != "" && !hasRelease(den.Changelog, m.latest)
}

// clampRows cuts view to rows lines and marks the cut. A pane taller than its
// share makes the frame taller than the terminal, and Bubble Tea then drops
// the frame's top rows: the header and the menu.
func clampRows(view string, rows int) string {
	rows = max(rows, 1)
	lines := strings.Split(view, "\n")
	if len(lines) <= rows {
		return view
	}
	more := lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Render("  …")
	return strings.Join(append(lines[:rows-1], more), "\n")
}

// renderRestartPrompt asks whether to restart into the installed version and
// says what that closes: a restart is a quit, and nothing reconnects by itself.
func (m Model) renderRestartPrompt(width int) string {
	warn := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F5A623"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
	code := lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65"))

	lines := []string{
		lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575")).Bold(true).
			Render(fmt.Sprintf("✓ Installed %s", m.release.Version)),
		"",
		warn.Render("Restart den now?"),
		"",
	}
	var active []string
	if m.active != nil {
		active = m.active()
	}
	if len(active) == 0 {
		lines = append(lines, dim.Render("No connections are open."))
	} else {
		noun := "connections"
		if len(active) == 1 {
			noun = "connection"
		}
		lines = append(lines, dim.Render(fmt.Sprintf("Restarting closes %d %s:", len(active), noun)))
		for _, a := range active {
			lines = append(lines, code.Render("  • "+a))
		}
		lines = append(lines, "", dim.Render("They will not reconnect by themselves."))
	}
	lines = append(lines, "",
		lipgloss.NewStyle().Bold(true).Render("Restart? ")+
			components.Hints(components.HelpKey{Key: "y", Text: "restart now"}, components.HelpKey{Key: "n", Text: "later"}))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#F5A623")).
		Padding(1, 2).
		MarginLeft(2).
		Width(max(width-10, 20) + 2).
		Render(strings.Join(lines, "\n"))
}

func (m Model) renderHelp(width, height int) string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
	code := lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65"))
	head := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ECFD65"))

	var authLines []string
	if m.updatesCfg != nil && m.updatesCfg.S3Bucket != "" {
		authLines = []string{
			head.Render("What you need"),
			"",
			dim.Render("Configured to download from S3:"),
			code.Render("  Bucket:  " + m.updatesCfg.S3Bucket),
		}
		if m.updatesCfg.S3Prefix != "" {
			authLines = append(authLines, code.Render("  Prefix:  "+m.updatesCfg.S3Prefix))
		}
		if m.updatesCfg.AWSProfile != "" {
			authLines = append(authLines, code.Render("  Profile: "+m.updatesCfg.AWSProfile))
		}
		authLines = append(authLines, "", dim.Render("No token and no GitHub access needed."))
	} else {
		// The wording is the one of docs/updates and the Help topic.
		authLines = []string{
			head.Render("What you need"),
			"",
			dim.Render("Releases are read from:"),
			code.Render("  " + update.NewSource(m.updatesCfg).Location()),
			"",
			dim.Render("Public repository: nothing to configure. den needs HTTPS"),
			dim.Render("access to github.com; behind a proxy, set HTTPS_PROXY."),
			"",
			dim.Render("Private repository: export GITHUB_TOKEN before starting den:"),
			"",
			code.Render("  export GITHUB_TOKEN=<your-personal-access-token>"),
			"",
			dim.Render("The token needs Contents: read, or the repo scope."),
			"",
			dim.Render("Fork or GitHub Enterprise: set updates.github_api in den.yaml,"),
			dim.Render("plus GITHUB_TOKEN when the repository is private."),
			"",
			dim.Render("To use a mirror you host, configure updates.s3_bucket in den.yaml"),
			dim.Render("(no token and no GitHub access needed)."),
		}
	}

	lines := []string{
		head.Render("↑ Updates"),
		"",
		dim.Render("This panel checks for a newer version of den and"),
		dim.Render("shows install instructions. v lists every published"),
		dim.Render("version, to upgrade or go back to an older one."),
		"",
	}
	lines = append(lines, authLines...)
	lines = append(lines,
		"",
		head.Render("Keys"),
		"",
		dim.Render("  c   check for latest release"),
		dim.Render("  i   auto-install latest release"),
		dim.Render("  v   pick any version to install, older ones too"),
		dim.Render("      (s notes, x skip in checks, p release candidates)"),
		dim.Render("  r   restart into the installed version"),
		dim.Render("      (closes every open connection)"),
		dim.Render("  h   toggle this help"),
		dim.Render("  esc close this help"),
	)

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#6C91BF")).
		Padding(1, 2).
		MarginLeft(2).
		Width(width - 8).
		Render(content)

	closeHint := "  " + components.Hints(components.HelpKey{Key: "esc/h", Text: "close"})

	return lipgloss.JoinVertical(lipgloss.Left, "", box, "", closeHint)
}

// downloadInstructions returns the commands that fetch archive by hand and
// the release page, if the source has one. For S3 the release URL is the
// archive itself, which curl cannot fetch; the aws command carries the profile
// and region the install uses, on a line of their own so it fits the pane.
func (m Model) downloadInstructions(archive string) (cmds []string, releasePage string) {
	src := update.NewSource(m.updatesCfg)
	if !src.UsesS3() {
		releaseBase := strings.TrimSuffix(m.url, "/tag/"+m.latest)
		return []string{fmt.Sprintf("curl -LO %s/archive/%s", releaseBase, archive)}, m.url
	}
	var flags []string
	if src.AWSProfile != "" {
		flags = append(flags, "--profile "+src.AWSProfile)
	}
	if src.AWSRegion != "" {
		flags = append(flags, "--region "+src.AWSRegion)
	}
	cp := "aws s3 cp " + m.url + " ."
	if len(flags) == 0 {
		return []string{cp}, ""
	}
	return []string{cp + " \\", "  " + strings.Join(flags, " ")}, ""
}

// installInstructions returns the archive filename, extraction commands, and
// install command for the current OS/arch combination.
func installInstructions(version string) (archive string, extractCmds []string, installCmd string) {
	archive = update.ArchiveName(version, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		return archive, []string{"Expand-Archive " + archive + " -DestinationPath ."}, `Move-Item den.exe C:\Windows\System32\den.exe`
	}
	return archive, []string{"tar -xzf " + archive}, "mv den /usr/local/bin/den"
}

// Bounds for the network steps. The AWS CLI may first refresh SSO credentials,
// so the check gets more than a plain HTTP call would need.
const (
	checkTimeout    = time.Minute
	downloadTimeout = 15 * time.Minute
)

// ── Install step tea.Cmds ─────────────────────────────────────────────────────

func stepResolveBinaryPath(ctx *installContext) tea.Cmd {
	return func() tea.Msg {
		exe, err := update.Executable()
		if err != nil {
			return ctx.result(fmt.Errorf("resolve binary: %w", err))
		}
		ctx.finalBinPath = exe
		return msgs.UpdateInstallProgressMsg{
			Step:    "Resolved binary path: " + exe,
			NextCmd: stepDownloadArchive(ctx),
		}
	}
}

func stepDownloadArchive(ctx *installContext) tea.Cmd {
	download := func() tea.Msg {
		defer ctx.downloadDone.Store(true)
		path, err := update.TempFile(ctx.finalBinPath, "den-download-*")
		if err != nil {
			return ctx.result(fmt.Errorf("temp file: %w", err))
		}
		ctx.archivePath = path
		c, cancel := context.WithTimeout(context.Background(), downloadTimeout)
		defer cancel()
		if err := ctx.src.Download(c, ctx.rel, path, &ctx.downloadedBytes, &ctx.totalBytes); err != nil {
			os.Remove(path) //nolint:errcheck
			return ctx.result(fmt.Errorf("download: %w", err))
		}
		return msgs.UpdateInstallProgressMsg{
			Step:    fmt.Sprintf("Downloaded %s → %s", ctx.rel.Archive, path),
			NextCmd: stepVerifyArchive(ctx),
		}
	}
	return tea.Batch(download, pollDownloadProgress(ctx))
}

// pollDownloadProgress polls ctx every 100ms and sends UpdateDownloadProgressMsg.
// It reschedules itself via NextCmd until ctx.downloadDone is set.
func pollDownloadProgress(ctx *installContext) tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		downloaded := ctx.downloadedBytes.Load()
		// The S3 download runs in the aws CLI, so only the file size shows progress.
		if downloaded == 0 && ctx.archivePath != "" {
			if fi, err := os.Stat(ctx.archivePath); err == nil {
				downloaded = fi.Size()
			}
		}
		var next tea.Cmd
		if !ctx.downloadDone.Load() {
			next = pollDownloadProgress(ctx)
		}
		return msgs.UpdateDownloadProgressMsg{
			Downloaded: downloaded,
			Total:      ctx.totalBytes.Load(),
			NextCmd:    next,
		}
	})
}

// stepVerifyArchive checks the download against the release's checksums.txt.
// A mismatch aborts the install. A release without checksums — every release
// published before den verified downloads — installs with a warning.
func stepVerifyArchive(ctx *installContext) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), checkTimeout)
		defer cancel()
		warning, err := ctx.src.VerifyDownload(c, ctx.rel, ctx.archivePath)
		if err != nil {
			os.Remove(ctx.archivePath) //nolint:errcheck
			return ctx.result(fmt.Errorf("verify: %w", err))
		}
		step := "Verified SHA-256 against " + update.ChecksumsFile
		if warning != "" {
			step = "⚠ " + warning
		}
		return msgs.UpdateInstallProgressMsg{Step: step, NextCmd: stepExtractBinary(ctx)}
	}
}

func stepExtractBinary(ctx *installContext) tea.Cmd {
	return func() tea.Msg {
		bin, err := update.ExtractBinary(ctx.archivePath, runtime.GOOS, ctx.finalBinPath)
		if err != nil {
			os.Remove(ctx.archivePath) //nolint:errcheck
			return ctx.result(fmt.Errorf("extract: %w", err))
		}
		ctx.tempBinPath = bin
		return msgs.UpdateInstallProgressMsg{
			Step:    fmt.Sprintf("Extracted binary from %s → %s", ctx.archivePath, bin),
			NextCmd: stepReplaceBinary(ctx),
		}
	}
}

func stepReplaceBinary(ctx *installContext) tea.Cmd {
	return func() tea.Msg {
		defer os.Remove(ctx.archivePath) //nolint:errcheck
		if err := update.Replace(ctx.tempBinPath, ctx.finalBinPath); err != nil {
			return ctx.result(fmt.Errorf("replace binary: %w", err))
		}
		return msgs.UpdateInstallProgressMsg{
			Step:    "Replaced binary: " + ctx.finalBinPath,
			NextCmd: func() tea.Msg { return ctx.result(nil) },
		}
	}
}

// renderDownloadBar renders a single-line progress bar for the download step.
func renderDownloadBar(downloaded, total int64, width int) string {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
	if total <= 0 {
		return style.Render(fmt.Sprintf("  Downloading… %s", formatBytes(downloaded)))
	}
	pct := float64(downloaded) / float64(total)
	if pct > 1 {
		pct = 1
	}
	barWidth := max(width-22, 10)
	filled := int(pct * float64(barWidth))
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	return style.Render(fmt.Sprintf("  [%s] %3.0f%%  %s / %s", bar, pct*100, formatBytes(downloaded), formatBytes(total)))
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// Check looks up the release to offer: from S3 when updates.s3_bucket is
// configured, and otherwise from the GitHub release feed (the releases API with
// GITHUB_TOKEN or a GitHub Enterprise server), under the channel, the pin and
// this machine's skip list.
func (m Model) Check() tea.Cmd {
	src, cfg, current, prefsPath, logger := m.source(), m.updatesCfg, m.version, m.prefsPath, m.logger
	return func() tea.Msg {
		where := src.Location()
		logger.Trace("update check [" + where + "]: looking up the latest release")
		start := time.Now()
		prefs, err := update.LoadPrefs(prefsPath)
		if err != nil {
			// The skip list is a convenience; a broken one must not stop the check.
			logger.Warn("update check: " + err.Error() + " — checking without the skip list")
		}
		c, cancel := context.WithTimeout(context.Background(), checkTimeout)
		defer cancel()
		res, err := src.Check(c, runtime.GOOS, runtime.GOARCH, current, update.NewPolicy(cfg, prefs))
		logger.Trace(fmt.Sprintf("update check [%s]: done in %dms", where, time.Since(start).Milliseconds()))
		if err != nil {
			return msgs.UpdateCheckResultMsg{Err: err}
		}
		rel := res.Offer
		return msgs.UpdateCheckResultMsg{Latest: rel.Version, URL: rel.URL, Notes: rel.Notes, Release: rel,
			Releases: res.Index, Why: res.Why}
	}
}
