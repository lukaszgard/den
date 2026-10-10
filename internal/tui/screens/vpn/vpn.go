// Package vpn implements the "VPN" right-pane — a dedicated openfortivpn manager.
package vpn

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Model is the VPN right-pane model.
type Model struct {
	vpns   []service.Service
	states map[string]service.RuntimeState
	cursor int
	width  int
	height int
	// configPath is the den.yaml a new vpn: entry goes into; the empty pane says so.
	configPath string
}

// Shortcuts lists the keys of the pane for the shortcuts pop-up (h or ?). How
// a VPN is set up is in its Help topic (docs/help) and docs/vpn, not here:
// the pop-up has to fit the terminal.
func (m Model) Shortcuts() []components.ShortcutGroup {
	return []components.ShortcutGroup{
		{Title: "VPN", Keys: []components.Shortcut{
			{Keys: "↑ ↓", Text: "Select a VPN (or k, j)"},
			{Keys: "c", Text: "Connect: log in through the browser"},
			{Keys: "d", Text: "Disconnect"},
			{Keys: "enter", Text: "Open its detail view: logs and status"},
		}},
		{Title: "Runtime toggles", Note: "Toggles last until den exits.", Keys: []components.Shortcut{
			{Keys: "r", Text: "Reconnect on/off: restart after a drop"},
			{Keys: "H", Text: "Health check on/off: probe the target"},
		}},
	}
}

// New creates a VPN pane from the given vpn services.
func New(vpns []service.Service, width, height int) Model {
	states := make(map[string]service.RuntimeState, len(vpns))
	for _, v := range vpns {
		states[v.Info().Name] = v.State()
	}
	return Model{
		vpns:   vpns,
		states: states,
		width:  width,
		height: height,
	}
}

// SetConfigPath tells the pane which den.yaml is in use. Without one the pane
// names where a new den.yaml belongs, the config directory.
func (m Model) SetConfigPath(path string) Model {
	m.configPath = path
	return m
}

// emptyText is what the pane says with no VPN: where the entry goes, and that
// a gateway is all it needs.
func (m Model) emptyText(width int) string {
	path := m.configPath
	if path == "" {
		path = filepath.Join(platform.ConfigDir(), "den.yaml")
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#626262")).
		Padding(0, 3).
		Width(max(width, 20)).
		Render("No VPNs configured.\n\nAdd a vpn: entry to\n" + platform.TildePath(path) + "\n\nA gateway is enough.")
}

// SetSize changes the pane's size and keeps its cursor, filter and states.
func (m Model) SetSize(width, height int) Model {
	m.width = width
	m.height = height
	return m
}

func (m Model) Update(msg tea.Msg, focused bool) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.ServiceEventMsg:
		if msg.Update.Info.Type == service.TypeVPN {
			m.states[msg.Update.Info.Name] = msg.Update.State
		}
	case tea.KeyPressMsg:
		if !focused {
			break
		}
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.vpns)-1 {
				m.cursor++
			}
		}
	}
	return m, nil
}

// SelectedService returns the currently highlighted vpn service.
func (m Model) SelectedService() (service.Service, bool) {
	if m.cursor < 0 || m.cursor >= len(m.vpns) {
		return nil, false
	}
	return m.vpns[m.cursor], true
}

// boxHeight leaves innerH = boxHeight-2 lines for the left column, which must fit
// the tallest case: status + uptime + name + the two toggle rows (connected), or
// status + name + error + the toggles (failed). Too small and renderStatusBox
// truncates with leftLines[:innerH], silently dropping the last toggle.
const boxHeight = 7

func (m Model) View(width, height int, focused bool) string {
	listHeight := max(height-boxHeight-1, 1)
	body := m.renderList(width, listHeight, focused)
	statusBox := m.renderStatusBox(width, boxHeight)
	hint := m.renderHint(width, focused)
	return lipgloss.JoinVertical(lipgloss.Left, body, statusBox, hint)
}

func (m Model) renderList(width, height int, focused bool) string {
	var rows []string
	rows = append(rows, "")

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#674EFF")).
		Padding(0, 2).
		Render("VPN")
	rows = append(rows, header)
	rows = append(rows, "")

	if len(m.vpns) == 0 {
		// One row per line: the padding below counts rows.
		rows = append(rows, strings.Split(m.emptyText(width), "\n")...)
	} else {
		maxName := max(width-20, 10)
		for i, v := range m.vpns {
			info := v.Info()
			st := m.states[info.Name]
			dot := components.StatusDot(st.Status)

			name := info.Name
			if len([]rune(name)) > maxName {
				name = string([]rune(name)[:maxName-1]) + "…"
			}

			statusStr := statusLabel(st.Status)
			selected := focused && i == m.cursor

			padded := fmt.Sprintf("%-*s", maxName, name)
			line := fmt.Sprintf("  %s %s  %s", dot, padded, statusStr)
			if selected {
				line = lipgloss.NewStyle().
					Bold(true).
					Background(lipgloss.Color("#1a1a3e")).
					Foreground(lipgloss.Color("#FFFDF5")).
					Width(width - 2).
					Render(line)
			}
			rows = append(rows, line)
		}
	}

	for len(rows) < height {
		rows = append(rows, "")
	}

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (m Model) renderStatusBox(width, height int) string {
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#333355"))

	innerW := max(width-2, 4)
	halfW := innerW / 2
	rightW := innerW - halfW

	innerH := max(height-2, 1)

	if len(m.vpns) == 0 || m.cursor >= len(m.vpns) {
		empty := lipgloss.NewStyle().Width(innerW).Height(innerH).Render("")
		return borderStyle.Render(empty)
	}

	v := m.vpns[m.cursor]
	info := v.Info()
	st := m.states[info.Name]

	// Left half: status
	var leftLines []string
	dot := components.StatusDot(st.Status)
	leftLines = append(leftLines, dot+" "+st.Status.String())

	if st.Status == service.StatusConnected && st.StartedAt != nil {
		uptime := time.Since(*st.StartedAt)
		h := int(uptime.Hours())
		min := int(uptime.Minutes()) % 60
		sec := int(uptime.Seconds()) % 60
		leftLines = append(leftLines, lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262")).
			Render(fmt.Sprintf("uptime: %02d:%02d:%02d", h, min, sec)))
	}

	leftLines = append(leftLines, lipgloss.NewStyle().
		Foreground(lipgloss.Color("#9B9B9B")).
		Render(info.Name))

	if st.Error != nil {
		errMsg := st.Error.Error()
		if len([]rune(errMsg)) > halfW-2 {
			errMsg = string([]rune(errMsg)[:halfW-3]) + "…"
		}
		leftLines = append(leftLines, lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF5F87")).
			Render(errMsg))
	}

	// Read straight from the service rather than from st: m.states only refreshes
	// on a ServiceEventMsg, and an idle VPN has no goroutine draining its update
	// channel, so a toggle pressed while it is down would never reach the cache.
	leftLines = append(leftLines, components.ToggleRows(v)...)

	for len(leftLines) < innerH {
		leftLines = append(leftLines, "")
	}
	leftContent := lipgloss.NewStyle().
		Width(halfW).
		Height(innerH).
		Render(fitWidth(leftLines[:innerH], halfW))

	// Right half: gateway / description
	var rightLines []string
	rightLines = append(rightLines, lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#6C91BF")).
		Render("Gateway"))
	rightLines = append(rightLines, lipgloss.NewStyle().
		Foreground(lipgloss.Color("#9B9B9B")).
		Render(info.Environment))

	// The data-path target used to be repeated here; the Health toggle row on the
	// left now carries it, along with whether it is actually being probed.

	for len(rightLines) < innerH {
		rightLines = append(rightLines, "")
	}
	rightContent := lipgloss.NewStyle().
		Width(rightW).
		Height(innerH).
		Render(fitWidth(rightLines[:innerH], rightW))

	inner := lipgloss.JoinHorizontal(lipgloss.Top, leftContent, rightContent)
	return borderStyle.Render(inner)
}

// fitWidth joins lines cut to width. A Width style wraps a longer line instead,
// and the extra line made the box taller than boxHeight: on a ~100-column
// terminal a long VPN description pushed den's header off the screen.
func fitWidth(lines []string, width int) string {
	cut := make([]string, len(lines))
	for i, l := range lines {
		cut[i] = ansi.Truncate(l, width, "…")
	}
	return strings.Join(cut, "\n")
}

func (m Model) renderHint(width int, focused bool) string {
	return components.HintLine(width, focused,
		components.HelpKey{Key: "↑↓", Text: "navigate"}, components.HelpKey{Key: "c", Text: "connect"},
		components.HelpKey{Key: "d", Text: "disconnect"}, components.HelpKey{Key: "r", Text: "reconnect"},
		components.HelpKey{Key: "H", Text: "health"}, components.HelpKey{Key: "enter", Text: "detail"},
		components.HelpKey{Key: "h", Text: "help"})
}

func statusLabel(s service.Status) string {
	switch s {
	case service.StatusConnected:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575")).Render("connected")
	case service.StatusConnecting:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65")).Render("connecting")
	case service.StatusError:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Render("error")
	case service.StatusStopping:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65")).Render("stopping")
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Render("idle")
	}
}
