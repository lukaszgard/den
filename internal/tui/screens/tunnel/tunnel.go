// Package tunnel implements the "Tunnel" right-pane — a full-width SSH tunnel manager.
package tunnel

import (
	"fmt"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/service"
	tunnelsvc "github.com/lukaszgard/den/internal/service/tunnel"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Model is the Tunnel right-pane model.
type Model struct {
	tunnels []service.Service
	states  map[string]service.RuntimeState
	cursor  int
	width   int
	height  int
}

// Shortcuts lists the keys of the pane for the shortcuts pop-up (h or ?). How
// tunnels are configured is in their Help topic (docs/help) and docs/tunnel,
// not here: the pop-up has to fit the terminal.
func (m Model) Shortcuts() []components.ShortcutGroup {
	return []components.ShortcutGroup{
		{Title: "Tunnels", Keys: []components.Shortcut{
			{Keys: "↑ ↓", Text: "Select a tunnel (or k, j)"},
			{Keys: "c", Text: "Start the selected tunnel"},
			{Keys: "d", Text: "Stop it"},
			{Keys: "enter", Text: "Open its detail view: logs and ports"},
			{Keys: "y", Text: "Copy its command"},
		}},
		{Title: "Runtime toggles", Note: "Toggles last until den exits.", Keys: []components.Shortcut{
			{Keys: "r", Text: "Reconnect on/off: restart after a drop"},
			{Keys: "m", Text: "Monitor on/off: re-check ports every 15 s"},
			{Keys: "H", Text: "Health check on/off: probe the target"},
			{Keys: "v", Text: "Auto-connect on/off: follow its VPN"},
		}},
	}
}

// New creates a Tunnel pane from the given tunnel services.
func New(tunnels []service.Service, width, height int) Model {
	states := make(map[string]service.RuntimeState, len(tunnels))
	for _, t := range tunnels {
		states[t.Info().Name] = t.State()
	}
	return Model{
		tunnels: tunnels,
		states:  states,
		width:   width,
		height:  height,
	}
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
		if msg.Update.Info.Type == service.TypeTunnel {
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
			if m.cursor < len(m.tunnels)-1 {
				m.cursor++
			}
		}
	}
	return m, nil
}

// SelectedService returns the currently highlighted tunnel service.
func (m Model) SelectedService() (service.Service, bool) {
	if m.cursor < 0 || m.cursor >= len(m.tunnels) {
		return nil, false
	}
	return m.tunnels[m.cursor], true
}

// boxHeight leaves innerH = boxHeight-2 lines for the left column, which must fit
// the tallest case: status + uptime + name + the four toggle rows (connected,
// with auto_connect), or status + name + error + the toggles (failed). Too small
// and renderStatusBox truncates with leftLines[:innerH], silently dropping the
// last toggle.
const boxHeight = 9

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
		Foreground(lipgloss.Color("#A78BFA")).
		Padding(0, 2).
		Render("Tunnels")
	rows = append(rows, header)
	rows = append(rows, "")

	if len(m.tunnels) == 0 {
		// One row per line: the padding below counts rows.
		rows = append(rows, strings.Split(lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262")).
			Padding(0, 3).
			Render("No tunnels configured.\n\nAdd a tunnels: section\nto your config file."), "\n")...)
	} else {
		maxName := max(width-20, 10)
		for i, t := range m.tunnels {
			info := t.Info()
			st := m.states[info.Name]
			dot := components.StatusDot(st.Status)

			name := info.Name
			if len([]rune(name)) > maxName {
				name = string([]rune(name)[:maxName-1]) + "…"
			}

			statusStr := statusLabel(st)
			selected := focused && i == m.cursor

			// Pad name so status is right-aligned
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

	// Interior width = width - 2 (border chars)
	innerW := max(width-2, 4)
	halfW := innerW / 2
	rightW := innerW - halfW

	// Inner height = height - 2 (border top+bottom)
	innerH := max(height-2, 1)

	if len(m.tunnels) == 0 || m.cursor >= len(m.tunnels) {
		empty := lipgloss.NewStyle().Width(innerW).Height(innerH).Render("")
		return borderStyle.Render(empty)
	}

	t := m.tunnels[m.cursor]
	info := t.Info()
	st := m.states[info.Name]

	// --- Left half: status ---
	var leftLines []string
	dot := components.StatusDot(st.Status)
	leftLines = append(leftLines, dot+" "+statusLabel(st))

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
	// on a ServiceEventMsg, and a stopped tunnel has nobody draining its update
	// channel, so a toggle pressed while it is down would never reach the cache.
	leftLines = append(leftLines, components.ToggleRows(t)...)

	for len(leftLines) < innerH {
		leftLines = append(leftLines, "")
	}
	leftContent := lipgloss.NewStyle().
		Width(halfW).
		Height(innerH).
		Render(strings.Join(leftLines[:innerH], "\n"))

	// --- Right half: port mappings ---
	var rightLines []string
	rightLines = append(rightLines, lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#6C91BF")).
		Render("Port mappings"))

	for _, f := range tunnelsvc.ParseForwards(t.ConnectCommand()) {
		rightLines = append(rightLines, lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9B9B9B")).
			Render(f.String()))
	}
	for len(rightLines) < innerH {
		rightLines = append(rightLines, "")
	}
	rightContent := lipgloss.NewStyle().
		Width(rightW).
		Height(innerH).
		Render(strings.Join(rightLines[:innerH], "\n"))

	inner := lipgloss.JoinHorizontal(lipgloss.Top, leftContent, rightContent)
	return borderStyle.Render(inner)
}

func (m Model) renderHint(width int, focused bool) string {
	return components.HintLine(width, focused,
		components.HelpKey{Key: "↑↓", Text: "navigate"}, components.HelpKey{Key: "c", Text: "connect"},
		components.HelpKey{Key: "d", Text: "disconnect"}, components.HelpKey{Key: "r", Text: "reconnect"},
		components.HelpKey{Key: "m", Text: "monitor"}, components.HelpKey{Key: "H", Text: "health"},
		components.HelpKey{Key: "v", Text: "auto-connect"}, components.HelpKey{Key: "y", Text: "copy"},
		components.HelpKey{Key: "enter", Text: "detail"}, components.HelpKey{Key: "h", Text: "help"})
}

func statusLabel(st service.RuntimeState) string {
	switch st.Status {
	case service.StatusConnected:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575")).Render("connected")
	case service.StatusConnecting:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65")).Render("connecting")
	case service.StatusError:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Render("error")
	case service.StatusStopping:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65")).Render("stopping")
	case service.StatusReconnecting:
		secs := 0
		if st.ReconnectAt != nil {
			if d := time.Until(*st.ReconnectAt); d > 0 {
				secs = int(d.Seconds()) + 1
			}
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#F5A623")).
			Render(fmt.Sprintf("reconnecting (%ds)", secs))
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Render("idle")
	}
}
