package detail

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/service"
	tunnelsvc "github.com/lukaszgard/den/internal/service/tunnel"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Model is the detail/service view model.
type Model struct {
	svc     service.Service
	state   service.RuntimeState
	logSeq  uint64 // svc.LogSeq() when the log view was last filled
	logView components.LogView
	width   int
	height  int

	// strip holds the service's terminal tabs, owned by the root model so
	// they outlive this view (nil for a service without a client den can
	// run), and the keyboard modes they bring.
	strip components.TabStrip
	// confirmDisconnect is set while d on a service with running clients
	// waits for its answer.
	confirmDisconnect bool
}

// New creates a new detail Model for the given service, over its terminal
// tabs when it has any (nil otherwise). The view renders exactly height lines.
func New(svc service.Service, tabs *Tabs, width, height int) Model {
	m := Model{
		svc:     svc,
		state:   svc.State(),
		logSeq:  svc.LogSeq(),
		logView: components.NewLogView(width-4, 1),
		width:   width,
		height:  height,
	}
	info := svc.Info()
	m.strip = components.NewTabStrip(tabs, func(tabID int) tea.Cmd {
		return func() tea.Msg {
			return msgs.OpenTerminalMsg{ServiceName: info.Name, ServiceType: info.Type, TabID: tabID}
		}
	})
	m.fitBody()
	m.logView.SetContent(svc.Logs())
	return m
}

func (m Model) Init() tea.Cmd { return nil }

// View draws the box from the first row, then a blank line and the key help
// line. Nothing sits above the box: a row there would be one the logs or a
// terminal tab could use.
const chromeLines = 2

// boxInnerHeight is the rows inside the box's border.
func (m Model) boxInnerHeight() int {
	return max(m.height-chromeLines-2, 10)
}

// bodySize is the area under the tab bar, which the logs and the terminals
// share: whatever rows the box has left once the sections above it are laid
// out. A fixed height let a wrapped connect command or the token bar grow the
// box past the terminal, and Bubble Tea then drops the top lines of the frame —
// the box's top border with them.
func (m Model) bodySize() (cols, rows int) {
	cols, rows, _ = m.bodyArea(m.upperSection())
	return cols, rows
}

// bodyArea is bodySize for the already rendered sections above the tab bar,
// and the row of the screen the body starts on: under the box's top border,
// those sections and the tab bar.
func (m Model) bodyArea(upperSection string) (cols, rows, top int) {
	upper := lipgloss.Height(lipgloss.NewStyle().Width(m.width - 2).Render(upperSection))
	return m.width - 4, max(m.boxInnerHeight()-upper-components.TabBarLines, 1), 1 + upper + components.TabBarLines
}

// fitBody sizes the log view and the terminal on screen to that area. A
// terminal that is not shown keeps its size until it is: resizing one makes
// its client redraw.
func (m *Model) fitBody() {
	cols, rows := m.bodySize()
	m.logView.Resize(cols, rows)
	if term := m.terminal(); term != nil {
		term.Resize(cols, rows)
	}
}

// TerminalSize is the size a terminal opened in this view starts with.
func (m Model) TerminalSize() (cols, rows int) { return m.bodySize() }

// terminal is the terminal on screen, or nil while the logs are.
func (m Model) terminal() Terminal { return m.strip.Current() }

// CapturingKeys reports that the view needs every key itself: a client is
// being typed into, a tab is being renamed, or a question is waiting for its
// answer. The root model must then not act on esc, backspace, c, d, r or h.
func (m Model) CapturingKeys() bool {
	return m.strip.Capturing() || m.confirmDisconnect
}

// StartTyping hands the keyboard to the terminal on screen, if one is.
func (m Model) StartTyping() Model {
	if m.terminal() != nil {
		m.strip.StartTyping()
		m.confirmDisconnect = false
	}
	return m
}

// RunningTerminals is how many clients a disconnect of the service would stop.
func (m Model) RunningTerminals() int {
	if m.strip.Tabs() == nil {
		return 0
	}
	return m.strip.Tabs().Running()
}

// AskDisconnect has the view confirm a disconnect before it happens. esc makes
// leaving a terminal easy, and the keys typed next are den's: without the
// question, the d of a query typed a moment too late would stop every client
// of the service. On y the view sends a DisconnectMsg.
func (m Model) AskDisconnect() Model {
	m.confirmDisconnect = true
	return m
}

// refreshLogs refills the log view when the service has logged since the last
// fill. Comparing the sequence number rather than the line count matters once
// the log buffer is full: its length then stays put while lines keep coming.
func (m Model) refreshLogs() Model {
	if seq := m.svc.LogSeq(); seq != m.logSeq {
		m.logSeq = seq
		m.logView.SetContent(m.svc.Logs())
	}
	return m
}

// ConnectCommand returns the connect command string for the current service.
func (m Model) ConnectCommand() string {
	return m.svc.ConnectCommand()
}

// Service returns the underlying service.
func (m Model) Service() service.Service {
	return m.svc
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case msgs.ServiceEventMsg:
		if msg.Update.Info.Name == m.svc.Info().Name && msg.Update.Info.Type == m.svc.Info().Type {
			m.state = msg.Update.State
			m = m.refreshLogs()
		}

	case msgs.TickMsg:
		m = m.refreshLogs()

	// Pasted text goes to the terminal being typed into or the name being
	// edited, and nowhere else: it is text, not den's keys.
	case tea.PasteMsg:
		_, cmd := m.strip.Paste(msg)
		return m, cmd

	case tea.KeyPressMsg:
		if handled, cmd := m.tabKey(msg); handled {
			cmds = append(cmds, cmd)
			break
		}
		if m.svc.Info().Type == service.TypeVPN {
			switch msg.String() {
			case "r":
				if rsvc, ok := m.svc.(service.Reconnectable); ok {
					cmds = append(cmds, msgs.FlashToggle("reconnect", rsvc.ToggleReconnect()))
				}
			case "H":
				if hsvc, ok := m.svc.(service.HealthCheckable); ok {
					if hsvc.HealthCheckTarget() == "" {
						cmds = append(cmds, func() tea.Msg {
							return msgs.StatusFlashMsg{Text: "no health_check configured"}
						})
						break
					}
					cmds = append(cmds, msgs.FlashToggle("health check", hsvc.ToggleHealthCheck()))
				}
			}
			break
		}
		if m.svc.Info().Type == service.TypeTunnel && msg.String() == "r" {
			if rsvc, ok := m.svc.(service.Reconnectable); ok {
				cmds = append(cmds, msgs.FlashToggle("reconnect", rsvc.ToggleReconnect()))
			}
			break
		}
		if m.svc.Info().Type == service.TypeTunnel && msg.String() == "m" {
			if msvc, ok := m.svc.(service.Monitorable); ok {
				cmds = append(cmds, msgs.FlashToggle("monitor", msvc.ToggleMonitor()))
			}
			break
		}
		if m.svc.Info().Type == service.TypeTunnel && msg.String() == "H" {
			if hsvc, ok := m.svc.(service.HealthCheckable); ok {
				if hsvc.HealthCheckTarget() == "" {
					cmds = append(cmds, func() tea.Msg {
						return msgs.StatusFlashMsg{Text: "no health_check configured"}
					})
					break
				}
				cmds = append(cmds, msgs.FlashToggle("health check", hsvc.ToggleHealthCheck()))
			}
			break
		}
		// Non-tunnel services use 'a' for the reconnect toggle because 'r' is the
		// IAM token refresh for rds/redis.
		if m.svc.Info().Type != service.TypeTunnel && msg.String() == "a" {
			if rsvc, ok := m.svc.(service.Reconnectable); ok {
				cmds = append(cmds, msgs.FlashToggle("auto-reconnect", rsvc.ToggleReconnect()))
			}
			break
		}
		switch msg.String() {
		case "y":
			connectCmd := m.svc.ConnectCommand()
			statusText := "copied!"
			if m.svc.Info().Type == service.TypeRedis {
				if token := m.svc.State().Token; token != "" {
					connectCmd += fmt.Sprintf(` -a "%s"`, token)
					statusText = "copied with password!"
				}
			}
			cmds = append(cmds, components.Copy(connectCmd, statusText))
		case "Y":
			sc, ok := service.SecretCommand(m.svc)
			if !ok {
				break
			}
			connectCmd := sc.ConnectCommandWithSecret()
			cmds = append(cmds, components.Copy(connectCmd, "copied with password!"))
		case "p":
			token := m.state.Token
			if token == "" {
				cmds = append(cmds, func() tea.Msg {
					return msgs.StatusFlashMsg{Text: "no token yet — connect first"}
				})
			} else {
				cmds = append(cmds, components.Copy(token, "token copied!"))
			}
		}
	}

	// Any message can change what sits above the logs: a resize, a state
	// carrying a new token, a toggle key, the reconnect countdown.
	m.fitBody()
	// The log view scrolls on keys, which are not meant for it while a
	// terminal is on screen in its place.
	if _, isKey := msg.(tea.KeyPressMsg); !isKey || m.terminal() == nil {
		cmds = append(cmds, m.logView.Update(msg))
	}
	return m, tea.Batch(cmds...)
}

// Click handles a click on the zone id, a second one in a row on it being a
// double-click: a tab, or the screen of the terminal on it. Not while a
// disconnect waits for its answer: a stray click is none.
func (m Model) Click(id string, double bool) (Model, tea.Cmd) {
	if m.confirmDisconnect {
		return m, nil
	}
	_, cmd := m.strip.Click(id, double)
	m.fitBody()
	return m, cmd
}

// Wheel handles a notch of the wheel as the arrow it stands for: it scrolls
// the logs, and reaches the program of a terminal being typed into. While the
// view asks something it is dropped, since any other key keeps what is asked
// about. Update keeps an arrow off the logs while a terminal hides them.
func (m Model) Wheel(up bool) (Model, tea.Cmd) {
	if m.confirmDisconnect || m.strip.Renaming() || m.strip.ConfirmingClose() {
		return m, nil
	}
	key := tea.KeyPressMsg{Code: tea.KeyDown}
	if up {
		key = tea.KeyPressMsg{Code: tea.KeyUp}
	}
	return m.Update(key)
}

// tabKey handles the keys of the terminal tabs, and the answer to a
// disconnect question, and reports whether msg was one. Anything else falls
// through to the keys the view always had.
func (m *Model) tabKey(msg tea.KeyPressMsg) (handled bool, cmd tea.Cmd) {
	if m.confirmDisconnect {
		m.confirmDisconnect = false
		if msg.String() != "y" {
			return true, nil
		}
		info := m.svc.Info()
		return true, func() tea.Msg {
			return msgs.DisconnectMsg{ServiceName: info.Name, ServiceType: info.Type}
		}
	}
	return m.strip.Key(msg)
}

func (m Model) View() string {
	view, _ := m.Render()
	return view
}

// Render is View with the cursor of the terminal being typed into, from the
// top left of the view, or nil when there is none to show.
func (m Model) Render() (view string, cursor *tea.Cursor) {
	body := m.logView.View()
	upper := m.upperSection()
	if term := m.terminal(); term != nil {
		cols, rows, top := m.bodyArea(upper)
		body = components.Zone("body", fitLines(term.View(), rows))
		cursor = m.strip.Cursor(1, top, cols, rows) // 1: the box's left border
	}
	boxContent := strings.Join([]string{upper, m.strip.Bar(m.width - 4), body}, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#6C91BF")).
		Width(m.width).
		Height(m.boxInnerHeight() + 2).
		Render(boxContent)

	return lipgloss.JoinVertical(lipgloss.Left,
		box,
		"",
		// A line wider than the terminal would wrap and push the frame's top
		// off screen; the keys at its end are the ones that can be spared.
		lipgloss.NewStyle().MaxWidth(m.width).Render(m.helpLine()),
	), cursor
}

// fitLines returns view as exactly rows lines, so a terminal can neither grow
// the box nor leave it short.
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

// upperSection renders everything in the box above the logs: the header, the
// connect command and, for a service holding an expiring credential, its TTL.
func (m Model) upperSection() string {
	info := m.svc.Info()
	st := m.state

	dot := components.StatusDot(st.Status)
	statusStr := strings.ToUpper(st.Status.String())
	if st.Status == service.StatusReconnecting && st.ReconnectAt != nil {
		secs := 0
		if d := time.Until(*st.ReconnectAt); d > 0 {
			secs = int(d.Seconds()) + 1
		}
		statusStr = fmt.Sprintf("RECONNECTING (%ds)", secs)
	}
	statusLabel := lipgloss.NewStyle().Bold(true).Render(statusStr)

	// Header: <icon> name   Status: X   Local port: Y   [Auto-reconnect: ●/○]
	typeTag := components.TypeBadge(info.Type)
	nameTag := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFDF5")).Render(info.Name)
	var header string
	if info.Type == service.TypeVPN {
		header = fmt.Sprintf(" %s %s Status: %s %s", typeTag, nameTag, dot, statusLabel)
		if toggles := toggleTags(m.svc); toggles != "" {
			header += "  " + toggles
		}
	} else {
		portLabel, portText := "Local port:", fmt.Sprintf("%d", st.LocalPort)
		if info.Type == service.TypeTunnel {
			portLabel, portText = tunnelPorts(m.svc.ConnectCommand())
		}
		portTag := portLabel + " " + lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B")).Render(portText)
		// Read the toggles from the service, not from st: a status snapshot only
		// refreshes while something drains the service's update channel, so a
		// toggle pressed on a stopped service would never reach it.
		if toggles := toggleTags(m.svc); toggles != "" {
			header = fmt.Sprintf(" %s %s Status: %s %s %s  %s",
				typeTag, nameTag, dot, statusLabel, portTag, toggles,
			)
		} else {
			header = fmt.Sprintf(" %s %s Status: %s %s %s",
				typeTag, nameTag, dot, statusLabel, portTag,
			)
		}
	}

	// Connect command
	displayCmd := m.svc.ConnectCommand()
	var connectSection string
	if info.Type == service.TypeVPN {
		connectSection = " " + lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65")).
			Background(lipgloss.Color("#1a1a1a")).
			Padding(0, 1).
			Render(displayCmd)
	} else {
		connectSection = fmt.Sprintf(" %s  %s",
			lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65")).
				Background(lipgloss.Color("#1a1a1a")).
				Padding(0, 1).
				Render(displayCmd),
			components.Hints(components.HelpKey{Key: "y", Text: "copy"}),
		)
	}

	// Token bar (services holding an expiring credential)
	tokenSection := ""
	if st.TokenExpires != nil {
		bar := components.TokenBar(st.TokenExpires, m.width-4)
		if st.TokenTTL > 0 {
			bar = components.TokenBarWithTotal(st.TokenExpires, st.TokenTTL, m.width-4)
		}
		tokenSection = lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.NewStyle().Foreground(lipgloss.Color("#6C91BF")).Render("─ Token TTL "),
			" "+bar+"  "+components.Hints(components.HelpKey{Key: "r", Text: "refresh"}),
		)
	}

	rule := lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Render(strings.Repeat("─", m.width-4))
	lines := []string{header, rule, connectSection}
	if tokenSection != "" {
		lines = append(lines, rule, tokenSection)
	}
	lines = append(lines, rule)
	return strings.Join(lines, "\n")
}

// toggleTags renders the runtime toggles a service supports as one compact header
// segment, e.g. "Reconnect: ● Monitor: ○ Health: ●". Every value is read live from
// the service through its capability interface, so the dots track a keypress even
// while the service is stopped and nothing is draining its update channel.
func toggleTags(svc service.Service) string {
	var tags []string
	if rc, ok := svc.(service.Reconnectable); ok {
		// A data store's toggle is its a key, "auto-reconnect"; a bare
		// "Reconnect" next to a token bar read as if it governed the token too.
		label := "Auto-reconnect: "
		if typ := svc.Info().Type; typ == service.TypeTunnel || typ == service.TypeVPN {
			label = "Reconnect: "
		}
		tags = append(tags, label+components.ToggleDot(rc.ReconnectEnabled()))
	}
	if mo, ok := svc.(service.Monitorable); ok {
		tags = append(tags, "Monitor: "+components.ToggleDot(mo.Monitoring()))
	}
	if hc, ok := svc.(service.HealthCheckable); ok {
		if hc.HealthCheckTarget() == "" {
			tags = append(tags, "Health: "+components.ToggleDotUnavailable())
		} else {
			tags = append(tags, "Health: "+components.ToggleDot(hc.HealthCheckEnabled()))
		}
	}
	return strings.Join(tags, "  ")
}

// tunnelPorts labels every -D/-L port a tunnel command opens, since a tunnel can
// have several where other services have exactly one.
func tunnelPorts(cmd string) (label, ports string) {
	forwards := tunnelsvc.ParseForwards(cmd)
	switch len(forwards) {
	case 0:
		return "Local port:", "—"
	case 1:
		return "Local port:", strconv.Itoa(forwards[0].Port)
	}
	list := make([]string, len(forwards))
	for i, f := range forwards {
		list[i] = strconv.Itoa(f.Port)
	}
	return "Local ports:", strings.Join(list, ", ")
}

type helpKey struct{ k, v string }

func (h helpKey) shared() components.HelpKey { return components.HelpKey{Key: h.k, Text: h.v} }

// connectionKeys is how many keys every service's help line starts with: h, c
// and d.
const connectionKeys = 3

// helpLine lists the keys for what has the keyboard right now.
func (m Model) helpLine() string {
	tabs := m.strip.Tabs()
	if tabs == nil {
		return renderHelp(serviceKeys(m.svc))
	}
	if m.confirmDisconnect {
		n := tabs.Running()
		terminals := "terminals"
		if n == 1 {
			terminals = "terminal"
		}
		return components.HelpNote(fmt.Sprintf("disconnect %s? it stops %d running %s", m.svc.Info().Name, n, terminals)) +
			renderHelp([]helpKey{{"y", "disconnect"}, {"any other key", "keep"}})
	}
	if line, ok := m.strip.ModeHelp(helpKey{"d", "disconnect"}.shared(), helpKey{"esc", "back"}.shared()); ok {
		return line
	}
	// The terminal keys follow h, c and d rather than ending the line, which
	// is cut at the terminal's width: a service with a token has enough keys
	// to push t past column 120, and then nothing on screen says the service
	// has terminals.
	tabKeys := []helpKey{{"t", "terminal"}}
	if tabs.Len() > 0 {
		tabKeys = append(tabKeys, helpKey{"←→", "tabs"})
	}
	return renderHelp(slices.Insert(serviceKeys(m.svc), connectionKeys, tabKeys...))
}

// serviceKeys are the keys of the service itself, ending with esc. h comes
// first: the line is cut at the terminal's width, and the pop-up behind h is
// where the keys that were cut can still be found.
func serviceKeys(svc service.Service) []helpKey {
	// connectionKeys counts these three.
	keys := []helpKey{
		{"h", "help"},
		{"c", "connect"},
		{"d", "disconnect"},
	}
	switch svc.Info().Type {
	case service.TypeTunnel:
		keys = append(keys,
			helpKey{"r", "reconnect"},
			helpKey{"m", "monitor"},
			helpKey{"H", "health"},
			helpKey{"y", "copy cmd"},
		)
	case service.TypeVPN:
		keys = append(keys,
			helpKey{"r", "reconnect"},
			helpKey{"H", "health"},
		)
	default:
		holdsToken := service.HoldsToken(svc)
		if holdsToken {
			keys = append(keys, helpKey{"r", "refresh"})
		}
		if _, ok := svc.(service.Reconnectable); ok {
			keys = append(keys, helpKey{"a", "auto-reconnect"})
		}
		keys = append(keys, helpKey{"y", "copy cmd"})
		if _, ok := service.SecretCommand(svc); ok {
			keys = append(keys, helpKey{"Y", "copy cmd+password"})
		}
		if holdsToken {
			keys = append(keys, helpKey{"p", "copy token"})
		}
	}
	return append(keys, helpKey{"esc", "back"})
}

func renderHelp(keys []helpKey) string {
	shared := make([]components.HelpKey, len(keys))
	for i, k := range keys {
		shared[i] = k.shared()
	}
	return "  " + components.Hints(shared...)
}
