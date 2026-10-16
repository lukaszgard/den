package updates

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/update"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// pickerSince is the first den with the version picker, the skip list and
// updates.pin. An older one offers the latest release again and knows none of
// them, which a downgrade to it must say.
const pickerSince = "v1.8.0"

// picker is the version picker: every published release, to install any of
// them, an older one included, which is how someone gets off a bad release.
type picker struct {
	open    bool
	loading bool
	err     string // the list could not be loaded
	note    string // what the last key did, or why it did nothing
	at      string // the version under the cursor
	showPre bool   // list release candidates; on with the prerelease channel
	// changed is set once the skip list was edited, so closing the picker
	// checks again under it.
	changed bool
	prefs   update.Prefs

	notes bool // the notes of the release under the cursor are open
	view  components.MarkdownView

	confirm     bool
	chosen      update.Release
	skipCurrent bool // a downgrade puts the version it leaves on the skip list
}

func (m Model) pin() string {
	if m.updatesCfg == nil {
		return ""
	}
	return m.updatesCfg.Pin
}

func (m Model) channel() string {
	if m.updatesCfg == nil || m.updatesCfg.Channel == "" {
		return update.ChannelStable
	}
	return m.updatesCfg.Channel
}

// currentYanked is why the running release is yanked, or "" when it is not
// (or no list says).
func (m Model) currentYanked() string {
	for _, r := range m.releases {
		if update.SameVersion(r.Version, m.version) {
			return r.Yanked
		}
	}
	return ""
}

// whyLine is the check's why, unless the yanked line above it says it
// already.
func (m Model) whyLine() string {
	if reason := m.currentYanked(); reason != "" && m.why == update.Canonical(m.version)+" is yanked: "+reason {
		return ""
	}
	return m.why
}

// downgrade reports whether installing rel goes back from the running version.
func (m Model) downgrade(rel update.Release) bool {
	return update.IsVersion(m.version) && update.Compare(rel.Version, m.version) < 0
}

// openPicker opens the version picker on the releases of the last check, or
// loads them: the check only keeps a list when the source publishes
// releases.json, and the picker may then ask the API once.
func (m Model) openPicker() (Model, tea.Cmd) {
	m.picker = picker{open: true, showPre: m.channel() == update.ChannelPrerelease, at: update.Canonical(m.version)}
	prefs, err := update.LoadPrefs(m.prefsPath)
	if err != nil {
		m.picker.note = err.Error()
	}
	m.picker.prefs = prefs
	if m.releases != nil {
		return m, nil
	}
	m.picker.loading = true
	return m, m.loadReleases()
}

func (m Model) loadReleases() tea.Cmd {
	src := m.source()
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), checkTimeout)
		defer cancel()
		rels, err := src.Releases(c, runtime.GOOS, runtime.GOARCH)
		return msgs.UpdateReleasesMsg{Releases: rels, Err: err}
	}
}

func (m Model) releasesLoaded(msg msgs.UpdateReleasesMsg) Model {
	m.picker.loading = false
	if msg.Err != nil {
		m.picker.err = msg.Err.Error()
		return m
	}
	m.picker.err = ""
	m.releases = msg.Releases
	if m.releases == nil {
		m.releases = []update.Release{} // listed, and empty: not loaded again
	}
	return m
}

// pickerRows are the releases the picker lists: release candidates only when
// shown, or when one is the running or the pinned version.
func (m Model) pickerRows() []update.Release {
	var rows []update.Release
	for _, r := range m.releases {
		if m.picker.showPre || (update.Policy{}).Admits(r) ||
			update.SameVersion(r.Version, m.version) || (m.pin() != "" && update.SameVersion(r.Version, m.pin())) {
			rows = append(rows, r)
		}
	}
	return rows
}

// cursor is the row of the version under the cursor, or the first row.
func (m Model) cursor(rows []update.Release) int {
	for i, r := range rows {
		if update.SameVersion(r.Version, m.picker.at) {
			return i
		}
	}
	return 0
}

func (m Model) pickerKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	p := &m.picker
	key := msg.String()
	if p.notes {
		// A search being typed takes every key, and esc ends a search before
		// it closes the notes.
		if p.view.Searching() || (p.view.HasQuery() && key == "esc") {
			return m, p.view.Update(msg)
		}
		if key == "esc" || key == "s" {
			p.notes = false
			return m, nil
		}
		return m, p.view.Update(msg)
	}
	if p.confirm {
		// Only an explicit y installs, as for a restart: this replaces the
		// binary, with an older one perhaps.
		switch key {
		case "y", "Y":
			skip := ""
			if p.skipCurrent {
				skip = m.version
			}
			return m.install(p.chosen, skip)
		case "x":
			if m.downgrade(p.chosen) {
				p.skipCurrent = !p.skipCurrent
			}
		case "n", "N", "esc", "backspace":
			p.confirm = false
		}
		return m, nil
	}

	rows := m.pickerRows()
	i := m.cursor(rows)
	p.note = ""
	switch key {
	case "esc", "backspace":
		changed := p.changed
		m.picker = picker{}
		if changed {
			m.state = stateChecking
			return m, tea.Batch(func() tea.Msg { return msgs.UpdateCheckStartedMsg{} }, m.Check())
		}
		return m, nil
	case "up", "k":
		if i > 0 {
			p.at = rows[i-1].Version
		}
	case "down", "j":
		if i+1 < len(rows) {
			p.at = rows[i+1].Version
		}
	case "p":
		p.showPre = !p.showPre
	case "r":
		if !p.loading {
			p.loading, p.err = true, ""
			return m, m.loadReleases()
		}
	case "enter", "s", "x":
		if p.loading || len(rows) == 0 {
			break
		}
		rel := rows[i]
		p.at = rel.Version
		switch key {
		case "enter":
			switch {
			case update.SameVersion(rel.Version, m.version):
				p.note = rel.Version + " is the version you run"
			case !m.source().CanInstall(rel):
				p.note = fmt.Sprintf("%s has no %s/%s build to install", rel.Version, runtime.GOOS, runtime.GOARCH)
			default:
				p.confirm, p.chosen, p.skipCurrent = true, rel, m.downgrade(rel)
			}
		case "s":
			notes := strings.TrimSpace(rel.Notes)
			if notes == "" {
				notes = "No release notes were published with " + rel.Version + "."
			}
			p.view = components.NewMarkdownView("Release notes", "# "+rel.Version+"\n\n"+notes,
				components.HelpKey{Key: "esc/s", Text: "close"}, components.HelpKey{Key: "↑↓", Text: "scroll"})
			p.view.SetDark(!m.light)
			p.view.SetSize(m.width, m.height)
			p.notes = true
		case "x":
			m = m.toggleSkip(rel.Version)
		}
	}
	return m, nil
}

// toggleSkip adds version to this machine's skip list or takes it off, and
// saves the list at once: it is read by the next check, here or in another den.
func (m Model) toggleSkip(version string) Model {
	p := &m.picker
	prefs, err := update.LoadPrefs(m.prefsPath)
	if err != nil {
		p.note = err.Error()
		return m
	}
	note := version + " is no longer skipped"
	if !prefs.Unskip(version) {
		prefs.Skip(version)
		note = version + " is skipped: update checks will not offer it"
	}
	if err := update.SavePrefs(m.prefsPath, prefs); err != nil {
		p.note = "could not save the skip list: " + err.Error()
		return m
	}
	p.prefs, p.note, p.changed = prefs, note, true
	return m
}

// skipVersion puts version on the skip list after a downgrade has left it.
// The install has happened either way, so a failure is only logged.
func (m Model) skipVersion(version string) Model {
	prefs, err := update.LoadPrefs(m.prefsPath)
	if err == nil {
		prefs.Skip(version)
		err = update.SavePrefs(m.prefsPath, prefs)
	}
	if err != nil {
		m.logger.Warn("update: could not skip " + version + ": " + err.Error())
		return m
	}
	m.logger.Info("update: " + update.Canonical(version) + " is skipped in update checks")
	return m
}

var (
	pickerDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
	pickerFaint    = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	pickerCurrent  = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575"))
	pickerUp       = lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65"))
	pickerDown     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C91BF"))
	pickerRC       = lipgloss.NewStyle().Foreground(lipgloss.Color("#C792EA"))
	pickerPinned   = lipgloss.NewStyle().Foreground(lipgloss.Color("#4FD1C5"))
	pickerYanked   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87"))
	pickerNoBuild  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F5A623"))
	pickerSelected = lipgloss.NewStyle().Bold(true)
)

// pickerRow is one release: version, date, what installing it would be, and
// what marks it.
func (m Model) pickerRow(rel update.Release, selected bool, src update.Source) string {
	mark := "  "
	version := fmt.Sprintf("%-14s", rel.Version)
	if selected {
		mark = pickerUp.Render("▸ ")
		version = pickerSelected.Render(version)
	}
	date := strings.Repeat(" ", 10)
	if !rel.Date.IsZero() {
		date = rel.Date.Format("2006-01-02")
	}
	var relation string
	switch {
	case update.SameVersion(rel.Version, m.version):
		relation = pickerCurrent.Render(fmt.Sprintf("%-12s", "current"))
	case m.downgrade(rel):
		relation = pickerDown.Render(fmt.Sprintf("%-12s", "↓ downgrade"))
	default:
		relation = pickerUp.Render(fmt.Sprintf("%-12s", "↑ upgrade"))
	}
	var tags []string
	if rel.Prerelease || update.IsPrerelease(rel.Version) {
		tags = append(tags, pickerRC.Render("rc"))
	}
	if m.pin() != "" && update.SameVersion(rel.Version, m.pin()) {
		tags = append(tags, pickerPinned.Render("pinned"))
	}
	if m.picker.prefs.Skips(rel.Version) {
		tags = append(tags, pickerFaint.Render("skipped"))
	}
	if rel.Yanked != "" {
		tags = append(tags, pickerYanked.Render("⚠ yanked: "+rel.Yanked))
	}
	if !src.CanInstall(rel) {
		tags = append(tags, pickerNoBuild.Render(fmt.Sprintf("no %s/%s build", runtime.GOOS, runtime.GOARCH)))
	}
	return mark + version + "  " + pickerDim.Render(date) + "  " + relation + "  " + strings.Join(tags, "  ")
}

func (m Model) renderPicker(width, height int, focused bool) string {
	p := m.picker
	head := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ECFD65"))
	sub := "Current " + m.version + "  ·  channel " + m.channel()
	if pin := m.pin(); pin != "" {
		sub += "  ·  pinned to " + update.Canonical(pin)
	}
	top := []string{"", "  " + head.Render("Updates › Versions"), "  " + pickerDim.Render(sub), ""}

	// The rows around the body: the four above it, and a blank, the note and
	// the hint below.
	room := max(height-len(top)-3, 1)
	var body []string
	var keys []components.HelpKey
	rows := m.pickerRows()
	switch {
	case p.confirm:
		body = strings.Split(m.renderConfirm(width), "\n")
		keys = m.confirmKeys()
	case p.loading:
		body = []string{"  " + pickerUp.Render("Loading releases…")}
		keys = []components.HelpKey{{Key: "esc", Text: "close"}}
	case p.err != "":
		body = []string{"  " + pickerYanked.Bold(true).Render("Could not list the releases")}
		for line := range strings.SplitSeq(p.err, ": ") {
			body = append(body, "  "+pickerYanked.Render("  "+line))
		}
		keys = []components.HelpKey{{Key: "r", Text: "retry"}, {Key: "esc", Text: "close"}}
	case len(rows) == 0:
		body = []string{"  " + pickerFaint.Render("No releases to list.")}
		if !p.showPre && len(m.releases) > 0 {
			body = append(body, "  "+pickerFaint.Render("Only release candidates are published: p shows them."))
		}
		keys = []components.HelpKey{{Key: "p", Text: "release candidates"}, {Key: "esc", Text: "close"}}
	default:
		src := m.source()
		cur := m.cursor(rows)
		start := 0
		if len(rows) > room {
			start = min(max(cur-room/2, 0), len(rows)-room)
		}
		end := min(start+room, len(rows))
		for i := start; i < end; i++ {
			body = append(body, ansi.Truncate("  "+m.pickerRow(rows[i], i == cur, src), max(width-2, 10), "…"))
		}
		if start > 0 || end < len(rows) {
			body[len(body)-1] = pickerFaint.Render(fmt.Sprintf("  … %d of %d", end, len(rows)))
		}
		pre := components.HelpKey{Key: "p", Text: "show rc"}
		if p.showPre {
			pre.Text = "hide rc"
		}
		keys = []components.HelpKey{{Key: "↑↓", Text: "move"}, {Key: "enter", Text: "install"}, {Key: "s", Text: "notes"},
			{Key: "x", Text: "skip"}, pre, {Key: "esc", Text: "close"}}
	}

	note := ""
	if p.note != "" {
		note = "  " + pickerDim.Render(p.note)
	}
	hint := "  " + components.HintLine(width-2, focused, keys...)
	// A confirm box on a short terminal is cut, not the hint that answers it.
	body = strings.Split(clampRows(strings.Join(body, "\n"), room), "\n")
	lines := append(top, body...)
	lines = append(lines, "", note, hint)
	return clampRows(strings.Join(lines, "\n"), height)
}

func (m Model) confirmKeys() []components.HelpKey {
	keys := []components.HelpKey{{Key: "y", Text: "install"}, {Key: "n", Text: "cancel"}}
	if m.downgrade(m.picker.chosen) {
		keys = append(keys, components.HelpKey{Key: "x", Text: "skip " + update.Canonical(m.version)})
	}
	return keys
}

// renderConfirm asks before installing the chosen release and says what it
// means: which way it goes, whether it is yanked, and what an older den does
// not know.
func (m Model) renderConfirm(width int) string {
	p := m.picker
	rel := p.chosen
	warn := lipgloss.NewStyle().Foreground(lipgloss.Color("#F5A623"))

	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F5A623")).Render("Install " + rel.Version + "?")}
	switch {
	case m.downgrade(rel):
		lines = append(lines, pickerDown.Render("↓ downgrade from "+update.Canonical(m.version)))
	case update.IsVersion(m.version):
		lines = append(lines, pickerUp.Render("↑ upgrade from "+update.Canonical(m.version)))
	default:
		lines = append(lines, pickerDim.Render("replaces this "+m.version+" build"))
	}
	if rel.Yanked != "" {
		lines = append(lines, "", pickerYanked.Render("⚠ yanked: "+rel.Yanked))
	}
	// -0 is below every prerelease of it: v1.8.0-rc.1 has the picker too.
	if update.Compare(rel.Version, pickerSince+"-0") < 0 {
		lines = append(lines, "", warn.Render("⚠ "+rel.Version+" is older than the version picker ("+pickerSince+
			"): it offers the latest release again, and knows no skip list or updates.pin."))
	}
	if pin := m.pin(); pin != "" && !update.SameVersion(rel.Version, pin) {
		lines = append(lines, "", warn.Render("updates.pin holds den on "+update.Canonical(pin)+": the next check offers it again."))
	}
	if m.downgrade(rel) {
		lines = append(lines, "",
			pickerDim.Render("den.yaml keys and services newer than "+rel.Version+" are ignored or rejected by it."))
		box := "☐"
		if p.skipCurrent {
			box = "☑"
		}
		lines = append(lines, "", box+" skip "+update.Canonical(m.version)+" in update checks")
	}
	lines = append(lines, "", lipgloss.NewStyle().Bold(true).Render("Install? ")+components.Hints(m.confirmKeys()...))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#F5A623")).
		Padding(1, 2).
		MarginLeft(2).
		Width(max(width-10, 20) + 2).
		Render(strings.Join(lines, "\n"))
}
