package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaszgard/den/internal/tui/components"
)

// den captures the mouse: a click selects what it lands on, a double-click
// does what enter does there, and the wheel moves through what is under the
// pointer as the arrow keys do. Where a click lands is read from the zones the
// panes marked in the last frame (components.Zone), never worked out again
// from their layout.
//
// Without the capture the terminal turned the wheel into ↑ and ↓ on the
// alternate screen, which every pane already handles; the wheel still arrives
// as those keys, so no pane needs code of its own for it.

// wheelStep is how many arrow presses one notch of the wheel stands for.
const wheelStep = 1

// arrow is the key one notch of the wheel stands for.
func arrow(up bool) tea.KeyPressMsg {
	if up {
		return tea.KeyPressMsg{Code: tea.KeyUp}
	}
	return tea.KeyPressMsg{Code: tea.KeyDown}
}

var enterKey = tea.KeyPressMsg{Code: tea.KeyEnter}

// inPanel reports that the cell x, y is in the panel right of the menu: under
// den's header and above the status and help bars.
func (m *AppModel) inPanel(x, y int) bool {
	return x > leftPaneWidth && y >= 1 && y <= m.height-3
}

// click handles a press of the left button on the cell x, y. A second click on
// the same zone within components.DoubleClick is a double-click.
func (m *AppModel) click(x, y int) []tea.Cmd {
	id, _ := m.zones.At(x, y)
	switch {
	case id != "":
	case m.showRunbook && m.runbookView.TerminalAt(x, y):
		id = "body"
	case m.showDetail && m.detailModel.TerminalAt(x, y):
		id = "body"
	}
	now := time.Now()
	double := id != "" && id == m.lastClick.id && now.Sub(m.lastClick.at) < components.DoubleClick
	m.lastClick.id, m.lastClick.at = id, now
	if double {
		// A third click starts a new pair rather than opening twice.
		m.lastClick.id = ""
	}

	// A stray click must never answer a question, nor reach what a pop-up
	// covers.
	var cmd tea.Cmd
	switch {
	case m.shortcuts.Open() || m.cfgPrompt != nil:
		return nil
	case m.showRunbook:
		m.runbookView, cmd = m.runbookView.Click(id, double)
		return []tea.Cmd{cmd}
	case m.showDetail:
		m.detailModel, cmd = m.detailModel.Click(id, double)
		return []tea.Cmd{cmd}
	case m.paneModal():
		return nil
	}

	if i, ok := components.ZoneIndex(id, "menu"); ok && i < len(m.menuOrder) {
		m.leftCursor, m.leftFocused = i, true
		if double {
			return m.handleLeftKey(enterKey)
		}
		return nil
	}
	if !m.inPanel(x, y) {
		return nil
	}
	m.leftFocused = false
	p, ok := m.pane(m.currentPane()).(mouseAware)
	if !ok || id == "" {
		return nil
	}
	cmds := []tea.Cmd{p.click(id)}
	if double {
		cmds = append(cmds, m.handleRightKey(enterKey)...)
	}
	return cmds
}

// wheel handles one notch of the wheel: the arrow it stands for, sent to what
// is under the pointer. While anything asks a question it is dropped, since
// "any other key" would read the arrow as an answer; the shortcuts pop-up
// scrolls with it wherever the pointer is.
func (m *AppModel) wheel(msg tea.MouseWheelMsg) []tea.Cmd {
	up := msg.Button == tea.MouseWheelUp
	key := arrow(up)
	var cmds []tea.Cmd
	switch {
	case m.shortcuts.Open():
		for range wheelStep {
			m.shortcuts = m.shortcuts.Key(key.String(), m.width, m.height)
		}
		return nil
	case m.cfgPrompt != nil:
		return nil
	// A full-screen view takes the wheel wherever the pointer is: it is all
	// one area, and a terminal being typed into gets the arrows, as before.
	case m.showRunbook:
		for range wheelStep {
			var cmd tea.Cmd
			m.runbookView, cmd = m.runbookView.Wheel(up)
			cmds = append(cmds, cmd)
		}
		return cmds
	case m.showDetail:
		for range wheelStep {
			var cmd tea.Cmd
			m.detailModel, cmd = m.detailModel.Wheel(up)
			cmds = append(cmds, cmd)
		}
		return cmds
	// The menu ignores the wheel: a notch too many would switch panels.
	case !m.inPanel(msg.X, msg.Y) || m.paneModal():
		return nil
	}
	p := m.pane(m.currentPane())
	if p == nil {
		return nil
	}
	// The panel scrolls without taking the focus, as a window under the
	// pointer does.
	for range wheelStep {
		cmds = append(cmds, p.update(key, true))
	}
	return cmds
}
