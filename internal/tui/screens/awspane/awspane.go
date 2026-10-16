// Package awspane implements the AWS left-menu pane.
// It reads profiles from ~/.aws/config and lets the user trigger aws sso login
// per profile or SSO session.
package awspane

import (
	"fmt"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// rowKind distinguishes the two things this pane lists. SSO sessions are
// services den can act on; profiles are reference data read from the AWS config.
type rowKind int

const (
	rowSession rowKind = iota
	rowProfile
)

// row is one navigable line. Sessions and profiles share a single cursor so the
// cursor can never index a list other than the one on screen — the previous
// split model clamped against the unfiltered profiles while rendering the
// filtered ones, and could not reach the session rows at all.
type row struct {
	kind    rowKind
	session service.Service   // kind == rowSession
	profile config.AWSProfile // kind == rowProfile
}

type Model struct {
	sessions        []service.Service
	states          map[string]service.RuntimeState
	profiles        []config.AWSProfile
	cursor          int
	profileOffset   int
	width           int
	height          int
	cfgPath         string
	credentialsPath string
	ssoCacheDir     string
	lastErr         string
	searching       bool
	query           string
}

// Searching reports whether the search bar is currently active.
func (m Model) Searching() bool { return m.searching }

// Shortcuts lists the keys of the pane for the shortcuts pop-up (h or ?). How
// the sessions are curated is in its Help topic (docs/help) and docs/aws, not
// here: the pop-up has to fit the terminal.
func (m Model) Shortcuts() []components.ShortcutGroup {
	return []components.ShortcutGroup{
		{Title: "SSO sessions", Note: "On a profile, these act on its SSO session.", Keys: []components.Shortcut{
			{Keys: "↑ ↓", Text: "Select a session or profile (or k, j)"},
			{Keys: "enter c", Text: "Log in: aws sso login, in the browser"},
			{Keys: "d", Text: "Cancel a login in progress"},
			{Keys: "o", Text: "Open the SSO start page in a browser"},
			{Keys: "/", Text: "Filter profiles; sessions stay listed"},
		}},
		{Title: "AWS files", Keys: []components.Shortcut{
			{Keys: "e", Text: "Edit the AWS config in $EDITOR"},
			{Keys: "E", Text: "Edit the AWS credentials file"},
		}},
	}
}

// rows is the flat, ordered list the cursor indexes: every SSO session, then the
// profiles matching the current filter. Sessions are deliberately exempt from
// the filter — there are few of them, they are the point of the pane, and
// keeping them fixed means typing a query cannot move the cursor's origin.
func (m Model) rows() []row {
	out := make([]row, 0, len(m.sessions)+len(m.profiles))
	for _, s := range m.sessions {
		out = append(out, row{kind: rowSession, session: s})
	}
	for _, p := range m.filteredProfiles() {
		out = append(out, row{kind: rowProfile, profile: p})
	}
	return out
}

func (m Model) selected() (row, bool) {
	rows := m.rows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return row{}, false
	}
	return rows[m.cursor], true
}

// SelectedService returns the SSO session the highlighted row acts on, mirroring
// the tunnel and VPN panes so app.go can drive it through the service manager.
//
// A profile row resolves to the session it references, so logging in from a
// profile keeps working. There is deliberately no "first session" fallback: a
// profile with no usable session returns false and SelectionHint explains why,
// rather than silently authenticating something the user never selected.
func (m Model) SelectedService() (service.Service, bool) {
	r, ok := m.selected()
	if !ok {
		return nil, false
	}
	if r.kind == rowSession {
		return r.session, true
	}
	if r.profile.SSOSession == "" {
		return nil, false
	}
	return m.sessionByName(r.profile.SSOSession)
}

// SelectionHint explains why the highlighted row cannot be acted on, so the
// caller can flash it. Empty means the row is actionable.
func (m Model) SelectionHint() string {
	r, ok := m.selected()
	if !ok || r.kind == rowSession {
		return ""
	}
	if r.profile.SSOSession == "" {
		return fmt.Sprintf("%s: no SSO configured", r.profile.Name)
	}
	if _, found := m.sessionByName(r.profile.SSOSession); !found {
		// The session exists in the AWS config but den.yaml curated it away.
		return fmt.Sprintf("%s: session %q is not listed in aws.sessions", r.profile.Name, r.profile.SSOSession)
	}
	return ""
}

// selectedStartURL returns the SSO portal URL for the highlighted row, or a
// message explaining why there is none.
func (m Model) selectedStartURL() (url, problem string) {
	svc, ok := m.SelectedService()
	if !ok {
		if hint := m.SelectionHint(); hint != "" {
			return "", hint
		}
		return "", "no SSO session selected"
	}
	if withURL, ok := svc.(interface{ StartURL() string }); ok {
		if u := withURL.StartURL(); u != "" {
			return u, ""
		}
	}
	return "", fmt.Sprintf("%s: no sso_start_url configured", svc.Info().Name)
}

// clampOffset keeps the profile scroll window around the cursor. The offset is
// in profile space, so session rows above it must be subtracted out first.
func (m Model) clampOffset() Model {
	profIdx := m.cursor - len(m.sessions)
	if profIdx < 0 {
		m.profileOffset = 0
		return m
	}
	if profIdx < m.profileOffset {
		m.profileOffset = profIdx
	}
	return m
}

// Click moves the cursor to the session or profile the zone id marks.
func (m Model) Click(id string) Model {
	if i, ok := components.ZoneIndex(id, "row"); ok && i < len(m.rows()) {
		m.cursor = i
		m = m.clampOffset()
	}
	return m
}

// SSOCacheDir returns the SSO cache directory used by this pane.
func (m Model) SSOCacheDir() string { return m.ssoCacheDir }

// sessionByName finds a registered session service by its [sso-session] name,
// which is what profiles reference via sso_session.
func (m Model) sessionByName(session string) (service.Service, bool) {
	for _, s := range m.sessions {
		if sess, ok := s.(interface{ Session() string }); ok && sess.Session() == session {
			return s, true
		}
	}
	return nil, false
}

func (m Model) filteredProfiles() []config.AWSProfile {
	if m.query == "" {
		return m.profiles
	}
	q := strings.ToLower(m.query)
	var out []config.AWSProfile
	for _, p := range m.profiles {
		if strings.Contains(strings.ToLower(p.Name), q) ||
			strings.Contains(strings.ToLower(p.SSOSession), q) {
			out = append(out, p)
		}
	}
	return out
}

// New creates an AWS pane from the registered SSO session services, loading the
// profile list from cfgPath immediately.
//
// Sessions arrive as services rather than being re-parsed here: they have
// already had den.yaml curation applied during registration, and taking them
// second-hand is what used to let this pane and the Connect list disagree.
func New(sessions []service.Service, cfgPath, credentialsPath string, width, height int) Model {
	states := make(map[string]service.RuntimeState, len(sessions))
	for _, s := range sessions {
		states[s.Info().Name] = s.State()
	}
	m := Model{
		sessions:        sessions,
		states:          states,
		cfgPath:         cfgPath,
		credentialsPath: credentialsPath,
		ssoCacheDir:     config.SSOCacheDir(cfgPath),
		width:           width,
		height:          height,
	}
	m = m.load()
	return m
}

func (m Model) load() Model {
	all, err := config.LoadAWSProfiles(m.cfgPath)
	if err != nil {
		m.lastErr = fmt.Sprintf("could not load %s: %v", m.cfgPath, err)
		m.profiles = nil
		return m
	}
	m.lastErr = ""
	m.profiles = nil
	for _, p := range all {
		if p.Kind != "sso-session" {
			m.profiles = append(m.profiles, p)
		}
	}
	return m
}

// SetSize updates the pane dimensions.
func (m Model) SetSize(width, height int) Model {
	m.width = width
	m.height = height
	return m
}

// Reload re-reads the AWS config file.
func (m Model) Reload() Model {
	return m.load()
}

func (m Model) Update(msg tea.Msg, focused bool) (Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case msgs.ServiceEventMsg:
		// Keep the TTL bars live, the same way the tunnel and VPN panes do.
		if msg.Update.Info.Type == service.TypeAWS {
			m.states[msg.Update.Info.Name] = msg.Update.State
		}
	// Pasted text goes into the filter, as typed text does.
	case tea.PasteMsg:
		if focused && m.searching {
			m.query += msg.Content
			// Land on the first session row, which filtering never hides.
			m.cursor = 0
			m.profileOffset = 0
		}
	case tea.KeyPressMsg:
		if !focused {
			break
		}
		if m.searching {
			switch msg.String() {
			case "esc":
				m.searching = false
				m.query = ""
			case "backspace":
				if m.query == "" {
					m.searching = false
				} else {
					runes := []rune(m.query)
					m.query = string(runes[:len(runes)-1])
					m = m.clampCursor()
				}
			case "up", "k":
				if m.cursor > 0 {
					m.cursor--
					m = m.clampOffset()
				}
			case "down", "j":
				if m.cursor < len(m.rows())-1 {
					m.cursor++
					m = m.clampOffset()
				}
			default:
				if msg.Text != "" {
					m.query += msg.Text
					// Land on the first session row, which filtering never hides.
					m.cursor = 0
					m.profileOffset = 0
				}
			}
		} else {
			switch msg.String() {
			case "/":
				m.searching = true
				m.query = ""
			case "up", "k":
				if m.cursor > 0 {
					m.cursor--
					m = m.clampOffset()
				}
			case "down", "j":
				if m.cursor < len(m.rows())-1 {
					m.cursor++
					m = m.clampOffset()
				}
			case "o":
				url, problem := m.selectedStartURL()
				if problem != "" {
					text := problem
					cmds = append(cmds, func() tea.Msg {
						return msgs.StatusFlashMsg{Text: text}
					})
					break
				}
				cmds = append(cmds, func() tea.Msg {
					return msgs.OpenBrowserMsg{URL: url}
				})
			case "e":
				path := m.cfgPath
				cmds = append(cmds, func() tea.Msg {
					return msgs.OpenEditorMsg{Path: path}
				})
			case "E":
				if m.credentialsPath != "" {
					path := m.credentialsPath
					cmds = append(cmds, func() tea.Msg {
						return msgs.OpenEditorMsg{Path: path}
					})
				}
			}
		}
	}

	return m, tea.Batch(cmds...)
}

// clampCursor keeps the cursor inside the row list after the filter narrows it.
func (m Model) clampCursor() Model {
	if n := len(m.rows()); m.cursor >= n {
		m.cursor = max(0, n-1)
	}
	return m.clampOffset()
}

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	awsLogoStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF9900"))

	cfgPathStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	ssoBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#FF9900")).
			Padding(0, 1)

	ssoTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF9900"))

	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))

	noTokenStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87"))

	loggedInStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575"))

	profileListTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#6C91BF"))

	selectedRowStyle = lipgloss.NewStyle().
				Bold(true).
				Background(lipgloss.Color("#1a1a3e")).
				Foreground(lipgloss.Color("#FFFDF5"))

	detailTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#6C91BF"))

	detailKeyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
	detailValStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFDF5"))
)

// untilClock is when a login ends on the local clock. The zone is named
// because the caches den reads from write UTC; an end on another day gets
// its weekday.
func untilClock(expires, now time.Time) string {
	expires = expires.Local()
	ey, em, ed := expires.Date()
	ny, nm, nd := now.Local().Date()
	if ey == ny && em == nm && ed == nd {
		return "until " + expires.Format("15:04:05 MST")
	}
	return "until " + expires.Format("Mon 15:04:05 MST")
}

func (m Model) View(width, height int, focused bool) string {
	// ── Header ──────────────────────────────────────────────────────────────
	header := "  " + awsLogoStyle.Render("\ue7ad  AWS") + "  " + cfgPathStyle.Render(m.cfgPath)

	if m.lastErr != "" {
		return lipgloss.JoinVertical(lipgloss.Left,
			header,
			lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Padding(0, 2).Render(m.lastErr),
		)
	}

	// ── SSO Login box ────────────────────────────────────────────────────────
	ssoBoxInnerWidth := max(width-8, 20) // account for border + padding

	var ssoRows []string
	if len(m.sessions) == 0 {
		ssoRows = append(ssoRows, noTokenStyle.Render("  No SSO sessions configured"))
	} else {
		for i, sess := range m.sessions {
			name := sess.Info().Name
			// Token state comes from the service now, which owns the cache
			// reads and already reconciles the den cache against the AWS CLI's.
			st, ok := m.states[name]
			if !ok {
				st = sess.State()
			}
			label := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFDF5")).Render(name)
			if focused && m.cursor == i {
				label = selectedRowStyle.Render("► " + name)
			}
			var tokenPart string
			switch {
			case st.Status == service.StatusConnected && st.TokenExpires == nil:
				// A refresh token keeps the login alive and no file says
				// until when; the Secrets pane counts it, so this does too.
				tokenPart = loggedInStyle.Render("logged in") + dimStyle.Render(" · token renews on next use") +
					"  " + dimStyle.Render("↵ login")
				if lipgloss.Width(label+"  "+tokenPart) > ssoBoxInnerWidth {
					tokenPart = loggedInStyle.Render("logged in") + "  " + dimStyle.Render("↵ login")
				}
			case st.TokenExpires == nil || time.Until(*st.TokenExpires) <= 0:
				tokenPart = noTokenStyle.Render("No active token") + "  " +
					dimStyle.Render("↵ to login")
			default:
				total := st.TokenTTL
				if total <= 0 {
					total = 12 * time.Hour
				}
				clock := " · " + untilClock(*st.TokenExpires, time.Now())
				budget := ssoBoxInnerWidth - len(name) - 6
				bar := components.TokenBarWithTotal(st.TokenExpires, total, budget-lipgloss.Width(clock))
				tokenPart = bar + clock + "  " + dimStyle.Render("↵ login")
				// A wrapped row would push the profile list down a line, so a
				// box too narrow for the clock keeps the row it had without it.
				if lipgloss.Width(label+"  "+tokenPart) > ssoBoxInnerWidth {
					bar = components.TokenBarWithTotal(st.TokenExpires, total, budget)
					tokenPart = bar + "  " + dimStyle.Render("↵ login")
				}
			}
			ssoRows = append(ssoRows, components.Zone(components.ZoneID("row", i), label+"  "+tokenPart))
		}
	}

	ssoContent := ssoTitleStyle.Render("SSO Login") + "\n" +
		strings.Join(ssoRows, "\n")
	ssoBox := ssoBoxStyle.Width(width - 2).Render(ssoContent)

	// ── Profile split ────────────────────────────────────────────────────────
	// Reserve height: 1 header + ssoBox height + 1 blank + 1 hint
	ssoBoxLines := strings.Count(ssoBox, "\n") + 1
	splitHeight := max(height-1-ssoBoxLines-1-1, 3)

	leftWidth := width * 2 / 5
	rightWidth := width - leftWidth - 1 // 1 for separator

	// Left: profile list
	listTitle := profileListTitleStyle.Render("Profiles")
	listLines := []string{listTitle}

	listAvail := splitHeight - 1 // minus title row

	// The cursor spans sessions then profiles; the scroll offset is in profile
	// space, so shift the cursor down by the session rows rendered above.
	profCursor := m.cursor - len(m.sessions)
	if profCursor >= m.profileOffset+listAvail {
		m.profileOffset = profCursor - listAvail + 1
	}

	displayProfiles := m.filteredProfiles()
	for i, p := range displayProfiles {
		if i < m.profileOffset {
			continue
		}
		if len(listLines)-1 >= listAvail {
			break
		}
		name := p.Name
		maxNameW := leftWidth - 4
		if len(name) > maxNameW {
			name = name[:maxNameW-1] + "…"
		}
		// The cursor spans sessions then profiles, and so do the zones.
		zone := components.ZoneID("row", len(m.sessions)+i)
		if focused && i == profCursor {
			row := selectedRowStyle.Width(leftWidth).Render(fmt.Sprintf("► %s", name))
			listLines = append(listLines, components.Zone(zone, row))
		} else {
			row := fmt.Sprintf("  %s", dimStyle.Render(name))
			listLines = append(listLines, components.Zone(zone, row))
		}
	}

	// Pad left column to splitHeight
	for len(listLines) < splitHeight {
		listLines = append(listLines, "")
	}

	// Right: detail view for the selected row
	detailLines := []string{detailTitleStyle.Render("Details")}
	switch sel, ok := m.selected(); {
	case ok && sel.kind == rowSession:
		session := sel.session.Info().Name
		if withSession, isSess := sel.session.(interface{ Session() string }); isSess {
			session = withSession.Session()
		}
		detailLines = append(detailLines, dimStyle.Render(fmt.Sprintf("[sso-session %s]", session)))
		detail := func(k, v string) {
			if v == "" {
				return
			}
			line := fmt.Sprintf("  %s = %s",
				detailKeyStyle.Render(fmt.Sprintf("%-18s", k)), detailValStyle.Render(v))
			if lipgloss.Width(line) > rightWidth {
				line = line[:rightWidth]
			}
			detailLines = append(detailLines, line)
		}
		if name := sel.session.Info().Name; name != session {
			detail("name", name) // display name from den.yaml
		}
		if d, isDesc := sel.session.(interface{ Description() string }); isDesc {
			detail("description", d.Description())
		}
		if u, isURL := sel.session.(interface{ StartURL() string }); isURL {
			detail("sso_start_url", u.StartURL())
		}
		detail("command", sel.session.ConnectCommand())
	case ok && sel.kind == rowProfile:
		detailLines = append(detailLines, dimStyle.Render(fmt.Sprintf("[profile %s]", sel.profile.Name)))
		for _, kv := range sel.profile.RawKeys {
			keyPart := detailKeyStyle.Render(fmt.Sprintf("%-18s", kv.Key))
			valPart := detailValStyle.Render(kv.Value)
			line := fmt.Sprintf("  %s = %s", keyPart, valPart)
			// Truncate to right panel width
			if lipgloss.Width(line) > rightWidth {
				line = line[:rightWidth]
			}
			detailLines = append(detailLines, line)
		}
	default:
		detailLines = append(detailLines, dimStyle.Render("Nothing selected"))
	}

	// Pad right column to splitHeight
	for len(detailLines) < splitHeight {
		detailLines = append(detailLines, "")
	}

	// Combine left + separator + right line by line
	sep := lipgloss.NewStyle().Foreground(lipgloss.Color("#444444")).Render("│")
	var splitRows []string
	for i := 0; i < splitHeight; i++ {
		left := ""
		if i < len(listLines) {
			left = listLines[i]
		}
		right := ""
		if i < len(detailLines) {
			right = detailLines[i]
		}
		// Pad left to leftWidth
		leftPadded := lipgloss.NewStyle().Width(leftWidth).Render(left)
		splitRows = append(splitRows, leftPadded+sep+right)
	}

	// ── Search bar / hint ─────────────────────────────────────────────────────
	var hintLine string
	if focused {
		if m.searching {
			prefix := dimStyle.Render("/ ")
			query := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFDF5")).Render(m.query)
			count := dimStyle.Render(fmt.Sprintf("(%d results)", len(displayProfiles)))
			hintLine = "  " + prefix + query + "█  " + count
		} else {
			hintLine = "  " + components.HintLine(m.width-2, true, components.HelpKey{Key: "/", Text: "filter profiles"})
		}
	} else {
		hintLine = "  " + components.HintLine(m.width-2, false, components.HelpKey{Key: "→/enter", Text: "focus"})
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		ssoBox,
		strings.Join(splitRows, "\n"),
		hintLine,
	)
}
