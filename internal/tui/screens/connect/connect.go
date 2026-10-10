// Package connect implements the right-panel "Connect" view.
// It shows all configured services with live status and lets the user
// start/stop tunnels or drill into a per-service detail overlay.
package connect

import (
	"fmt"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Model is the Connect right-pane model.
type Model struct {
	services  []service.Service
	states    map[string]service.RuntimeState
	cursor    int
	width     int
	height    int
	searching bool
	query     string
}

// Searching reports whether the search bar is currently active.
func (m Model) Searching() bool { return m.searching }

// Shortcuts lists the keys of the pane for the shortcuts pop-up (h or ?). How
// services are configured is in their Help topics (docs/help), not here: the
// pop-up has to fit the terminal.
func (m Model) Shortcuts() []components.ShortcutGroup {
	return []components.ShortcutGroup{
		{Title: "Services", Keys: []components.Shortcut{
			{Keys: "↑ ↓", Text: "Select a service (or k, j)"},
			{Keys: "enter", Text: "Open its detail view: logs, terminals"},
			{Keys: "/", Text: "Filter by name, type or environment"},
			{Keys: "c", Text: "Connect: open the tunnel, mint a token"},
			{Keys: "d", Text: "Disconnect: close the tunnel"},
			{Keys: "y", Text: "Copy its connect command (no secret)"},
		}},
		{Title: "Token and tunnel", Note: "Tokens renew by themselves while connected.", Keys: []components.Shortcut{
			{Keys: "r", Text: "Mint a new token now"},
			{Keys: "a", Text: "SSM port-forward auto-reconnect on/off"},
		}},
	}
}

// filtered returns services matching the current query (case-insensitive).
func (m Model) filtered() []service.Service {
	if m.query == "" {
		return m.services
	}
	q := strings.ToLower(m.query)
	var out []service.Service
	for _, svc := range m.services {
		info := svc.Info()
		if strings.Contains(strings.ToLower(info.Name), q) ||
			strings.Contains(strings.ToLower(info.Type), q) ||
			strings.Contains(strings.ToLower(info.Environment), q) {
			out = append(out, svc)
		}
	}
	return out
}

// svcKey returns a composite key for a service to avoid name collisions across types.
func svcKey(info service.ServiceInfo) string { return info.Name + ":" + info.Type }

// New creates a Connect pane from the manager's current service list.
func New(services []service.Service, width, height int) Model {
	states := make(map[string]service.RuntimeState, len(services))
	for _, svc := range services {
		states[svcKey(svc.Info())] = svc.State()
	}
	return Model{
		services: services,
		states:   states,
		width:    width,
		height:   height,
	}
}

// SetSize changes the pane's size and keeps its cursor, filter and states.
func (m Model) SetSize(width, height int) Model {
	m.width = width
	m.height = height
	return m
}

// SetServices swaps in the service list after a config reload. The cursor
// stays on the service it was on when that service survived the reload, so
// R does not throw the user back to the top of a long list.
func (m Model) SetServices(services []service.Service) Model {
	var selected string
	if svc, ok := m.SelectedService(); ok {
		selected = svcKey(svc.Info())
	}
	fresh := New(services, m.width, m.height)
	m.services, m.states = fresh.services, fresh.states
	list := m.filtered()
	m.cursor = min(m.cursor, max(0, len(list)-1))
	for i, svc := range list {
		if svcKey(svc.Info()) == selected {
			m.cursor = i
			break
		}
	}
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg, focused bool) (Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case msgs.ServiceEventMsg:
		m.states[svcKey(msg.Update.Info)] = msg.Update.State

	// Pasted text goes into the filter, as typed text does.
	case tea.PasteMsg:
		if focused && m.searching {
			m.query += msg.Content
			m.cursor = 0
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
					filtered := m.filtered()
					if m.cursor >= len(filtered) {
						m.cursor = max(0, len(filtered)-1)
					}
				}
			case "up", "k":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down", "j":
				filtered := m.filtered()
				if m.cursor < len(filtered)-1 {
					m.cursor++
				}
			case "enter":
				filtered := m.filtered()
				if m.cursor >= 0 && m.cursor < len(filtered) {
					svc := filtered[m.cursor]
					cmds = append(cmds, func() tea.Msg {
						return msgs.OpenDetailMsg{ServiceName: svc.Info().Name, ServiceType: svc.Info().Type}
					})
				}
				m.searching = false
				m.query = ""
			default:
				if msg.Text != "" {
					m.query += msg.Text
					m.cursor = 0
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
				}
			case "down", "j":
				if m.cursor < len(m.services)-1 {
					m.cursor++
				}
			case "enter":
				if m.cursor >= 0 && m.cursor < len(m.services) {
					svc := m.services[m.cursor]
					cmds = append(cmds, func() tea.Msg {
						return msgs.OpenDetailMsg{ServiceName: svc.Info().Name, ServiceType: svc.Info().Type}
					})
				}
			}
		}
	}

	return m, tea.Batch(cmds...)
}

// SelectedService returns the currently highlighted service (respects search filter).
func (m Model) SelectedService() (service.Service, bool) {
	list := m.filtered()
	if m.cursor < 0 || m.cursor >= len(list) {
		return nil, false
	}
	return list[m.cursor], true
}

// Cursor returns the current cursor index within the active list.
func (m Model) Cursor() int { return m.cursor }

func (m Model) View(width, height int, focused bool) string {
	var sections []string
	sections = append(sections, "")

	// ── Services section ──────────────────────────────────────────────────
	svcHeader := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#6C91BF")).
		Padding(0, 2).
		Render("Services")
	sections = append(sections, svcHeader)

	displayList := m.filtered()
	if len(m.services) == 0 {
		// One row per line: the padding below counts rows.
		sections = append(sections, strings.Split(lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262")).
			Padding(0, 3).
			Render("No services configured.\n\nCreate a den.yaml file to get started."), "\n")...)
	} else if len(displayList) == 0 {
		sections = append(sections, lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262")).
			Padding(0, 3).
			Render("No services match your search."))
	} else {
		for i, svc := range displayList {
			info := svc.Info()
			st := m.states[svcKey(info)]

			dot := components.StatusDot(st.Status)
			badge := components.TypeBadge(info.Type)

			const portWidth = 6 // e.g. ":50110"
			var portStr string
			if st.LocalPort > 0 {
				raw := fmt.Sprintf(":%d", st.LocalPort)
				padded := raw + strings.Repeat(" ", portWidth-len(raw))
				portStr = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#9B9B9B")).
					Render(padded)
			} else {
				portStr = strings.Repeat(" ", portWidth)
			}

			extra := statusExtra(st)

			nameMax := 22
			name := info.Name
			if len([]rune(name)) > nameMax {
				name = string([]rune(name)[:nameMax-1]) + "…"
			}
			padded := name + strings.Repeat(" ", nameMax-len([]rune(name)))

			selected := focused && i == m.cursor
			rowStyle := lipgloss.NewStyle()
			if selected {
				rowStyle = rowStyle.
					Bold(true).
					Background(lipgloss.Color("#1a1a3e")).
					Foreground(lipgloss.Color("#FFFDF5"))
			}

			line := fmt.Sprintf("  %s %s  %s  %s  %s",
				dot, rowStyle.Render(padded), badge, portStr, extra)

			if selected {
				line = lipgloss.NewStyle().
					Background(lipgloss.Color("#1a1a3e")).
					Width(width - 2).
					Render(line)
			}
			sections = append(sections, line)
		}
	}

	// Pad to pin the search bar to the bottom (footer = blank line + bar line = 2 lines)
	for len(sections) < height-2 {
		sections = append(sections, "")
	}

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	white := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFDF5"))
	unfocusedHint := components.HintLine(width-2, false, components.HelpKey{Key: "→/enter", Text: "focus"})
	if focused {
		if m.searching {
			prefix := dim.Render("/ ")
			query := white.Render(m.query)
			count := dim.Render(fmt.Sprintf("(%d results)", len(displayList)))
			sections = append(sections, "", "  "+prefix+query+"█  "+count)
		} else {
			// The keys themselves are listed in the help bar below.
			sections = append(sections, "", "  "+components.HintLine(width-2, true, components.HelpKey{Key: "/", Text: "filter"}))
		}
	} else {
		sections = append(sections, "", "  "+unfocusedHint)
	}

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func statusExtra(st service.RuntimeState) string {
	if st.TokenExpires != nil {
		remaining := time.Until(*st.TokenExpires)
		if remaining > 0 {
			mins := int(remaining.Minutes())
			secs := int(remaining.Seconds()) % 60
			var c lipgloss.Style
			switch {
			case remaining < 2*time.Minute:
				c = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87"))
			case remaining < 5*time.Minute:
				c = lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65"))
			default:
				c = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575"))
			}
			var ttl string
			if hours := mins / 60; hours > 0 {
				ttl = fmt.Sprintf("token: %dh%02dm%02ds", hours, mins%60, secs)
			} else {
				ttl = fmt.Sprintf("token: %dm%02ds", mins, secs)
			}
			return c.Render(ttl)
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Render("token: expired")
	}

	switch st.Status {
	case service.StatusConnected:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575")).Render("connected")
	case service.StatusConnecting:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65")).Render("connecting…")
	case service.StatusReconnecting:
		label := "reconnecting…"
		if st.ReconnectAt != nil {
			if d := time.Until(*st.ReconnectAt); d > 0 {
				label = fmt.Sprintf("reconnecting (%ds)", int(d.Seconds())+1)
			}
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#F5A623")).Render(label)
	case service.StatusError:
		if st.Error != nil {
			msg := st.Error.Error()
			if isSSOError(msg) {
				return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Render("SSO expired — go to AWS panel")
			}
			if len(msg) > 28 {
				msg = msg[:27] + "…"
			}
			return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Render(msg)
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Render("error")
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Render("idle")
	}
}

func isSSOError(msg string) bool {
	return strings.Contains(msg, "InvalidGrantException") ||
		strings.Contains(msg, "refresh cached SSO token") ||
		strings.Contains(msg, "SSO token") ||
		strings.Contains(msg, "sso login")
}
