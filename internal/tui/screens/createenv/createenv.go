// Package createenv implements the right-panel "Create Env" view.
// It shows docker-compose services and lets the user bring them up/down.
package createenv

import (
	"fmt"
	"strings"

	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Model is the Create Env right-pane model.
type Model struct {
	services []service.Service // only docker-type services
	states   map[string]service.RuntimeState
	cursor   int
	width    int
	height   int
}

// SelectedService returns the currently highlighted docker service.
func (m Model) SelectedService() (service.Service, bool) {
	if m.cursor < 0 || m.cursor >= len(m.services) {
		return nil, false
	}
	return m.services[m.cursor], true
}

// Click moves the cursor to the row the zone id marks.
func (m Model) Click(id string) Model {
	if i, ok := components.ZoneIndex(id, "row"); ok && i < len(m.services) {
		m.cursor = i
	}
	return m
}

// New creates a CreateEnv pane, filtering the service list to docker types.
func New(allServices []service.Service, width, height int) Model {
	var dockerSvcs []service.Service
	states := make(map[string]service.RuntimeState)
	for _, svc := range allServices {
		if svc.Info().Type == service.TypeDocker {
			dockerSvcs = append(dockerSvcs, svc)
			states[svc.Info().Name] = svc.State()
		}
	}
	return Model{
		services: dockerSvcs,
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

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg, focused bool) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.ServiceEventMsg:
		if m.states != nil {
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
			if m.cursor < len(m.services)-1 {
				m.cursor++
			}
		}
	}
	return m, nil
}

func (m Model) View(width, height int, focused bool) string {
	if len(m.services) == 0 {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262")).
			Padding(2, 3).
			Render("No docker services configured.\n\nAdd a service with type: docker to\nyour den.yaml to get started.")
	}

	var rows []string
	for i, svc := range m.services {
		info := svc.Info()
		st := m.states[info.Name]
		dot := components.StatusDot(st.Status)

		selected := focused && i == m.cursor
		nameStyle := lipgloss.NewStyle()
		if selected {
			nameStyle = nameStyle.Bold(true).
				Background(lipgloss.Color("#1a1a3e")).
				Foreground(lipgloss.Color("#FFFDF5"))
		}

		status := lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Render("idle")
		switch st.Status {
		case service.StatusConnected:
			status = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575")).Render("running")
		case service.StatusConnecting:
			status = lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65")).Render("starting…")
		case service.StatusError:
			status = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Render("error")
		}

		line := fmt.Sprintf("  %s %s   %s", dot, nameStyle.Render(info.Name), status)
		if selected {
			line = lipgloss.NewStyle().
				Background(lipgloss.Color("#1a1a3e")).
				Width(width - 2).
				Render(line)
		}
		rows = append(rows, components.Zone(components.ZoneID("row", i), line))
	}

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#2ECC71")).
		Padding(0, 2).
		Render("Docker Environments")

	hint := ""
	if focused {
		hint = components.HintLine(width, true, components.HelpKey{Key: "enter/c", Text: "start"},
			components.HelpKey{Key: "d", Text: "stop"}, components.HelpKey{Key: "→", Text: "open detail"})
	} else {
		hint = components.HintLine(width, false, components.HelpKey{Key: "→/enter", Text: "focus"})
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		"",
		header,
		"",
		strings.Join(rows, "\n"),
		"",
		"  "+hint,
	)
}
