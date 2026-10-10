package applogs

import (
	"strings"

	"github.com/lukaszgard/den/internal/tui/applog"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Model displays app-level logs in a scrollable viewport, with the output of
// the runs started in this den among them unless o hides it.
type Model struct {
	logger     *applog.Logger
	logView    components.LogView
	version    uint64
	hideOutput bool
	width      int
	height     int
}

func New(logger *applog.Logger, width, height int) Model {
	m := Model{
		logger: logger,
		width:  width,
		height: height,
	}
	m.logView = components.NewLogView(width-2, max(1, height-4))
	m.refresh()
	return m
}

// SetSize changes the pane's size and keeps the scroll position.
func (m Model) SetSize(width, height int) Model {
	m.width = width
	m.height = height
	m.logView.Resize(width-2, max(1, height-4))
	return m
}

func (m Model) Update(msg tea.Msg, focused bool) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.TickMsg:
		if m.logger.Version() != m.version {
			m.refresh()
		}
	case tea.KeyPressMsg:
		if !focused {
			break
		}
		if msg.String() == "o" {
			m.hideOutput = !m.hideOutput
			m.refresh()
			break
		}
		m.logView.Update(msg)
	}
	return m, nil
}

// OutputHidden reports whether o has hidden the runs' output.
func (m Model) OutputHidden() bool { return m.hideOutput }

func (m Model) View(width, height int, focused bool) string {
	title := "App Logs"
	output := "hide run output"
	if m.hideOutput {
		title += " · run output hidden"
		output = "show run output"
	}
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#6C91BF")).
		Padding(0, 1).
		Render(title)

	hint := "  " + components.Hints(
		components.HelpKey{Key: "↑↓ pgup pgdn", Text: "scroll"},
		components.HelpKey{Key: "o", Text: output},
	)

	borderColor := "#333355"
	if focused {
		borderColor = "#6C91BF"
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Width(width).
		Height(height-2).
		Padding(0, 1).
		Render(m.logView.View())

	return strings.Join([]string{header, box, hint}, "\n")
}

func (m *Model) refresh() {
	m.version = m.logger.Version()
	m.logView.SetContent(m.logger.Lines(!m.hideOutput))
}
