package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"
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
//
// The capture also takes the terminal's own drag-to-select, so den selects
// for itself: a drag highlights the text it passes over, kept to the part of
// the screen it started in (the menu, the panel, the inside of a view's box,
// one bar), and letting go copies it. M hands the mouse back to the terminal
// for what that cannot do: lines cut short, scrollback.

// selection is a drag: from the cell the button went down on, the anchor, to
// the one the pointer is on, the head, both kept in area.
type selection struct {
	area           components.Rect
	ax, ay, hx, hy int
	// dragging lasts from the press to its release; shown is set once the
	// pointer leaves the anchor's cell, so a click selects nothing.
	dragging, shown bool
}

func (s selection) spans() []components.Span {
	return components.SelectionSpans(s.area, s.ax, s.ay, s.hx, s.hy)
}

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
	case m.popUp():
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
	case m.cfgPrompt != nil || m.leftovers != nil:
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

// selectArea is the part of the screen a drag pressed on the cell x, y keeps
// to, so a selection never runs from a panel into the menu beside it or over
// a box's border: the menu, the panel, the inside of a full-screen view's box,
// or the one row of a bar. Under a pop-up there is none, as a pop-up takes no
// click either.
func (m *AppModel) selectArea(x, y int) components.Rect {
	row := components.Rect{X0: 0, Y0: y, X1: m.width, Y1: y + 1}
	switch {
	case m.popUp():
		return components.Rect{}
	case m.showRunbook || m.showDetail:
		box := m.detailModel.TextArea()
		if m.showRunbook {
			if m.runbookView.PopUp() {
				return components.Rect{}
			}
			box = m.runbookView.TextArea()
		}
		switch {
		// Pressed on the side of the border: the drag starts at its edge.
		case y >= box.Y0 && y < box.Y1:
			return box
		// Under the box's bottom border: the help line or the status bar.
		case y > box.Y1:
			return row
		}
		return components.Rect{}
	case m.paneModal():
		return components.Rect{}
	case y < 1 || y > m.height-3:
		return row
	case x < leftPaneWidth:
		return components.Rect{X0: 0, Y0: 1, X1: leftPaneWidth, Y1: m.height - 2}
	}
	return components.Rect{X0: leftPaneWidth + 1, Y0: 1, X1: m.width, Y1: m.height - 2}
}

// press starts a drag on the cell x, y, after the click on it has done what it
// does: a double-click may have opened a view, which the drag then keeps to.
func (m *AppModel) press(x, y int) {
	area := m.selectArea(x, y)
	if area.Empty() {
		return
	}
	x, y = area.Clamp(x, y)
	m.sel = selection{area: area, ax: x, ay: y, hx: x, hy: y, dragging: true}
}

// drag moves the head of the selection to the cell x, y.
func (m *AppModel) drag(x, y int) {
	if !m.sel.dragging {
		return
	}
	m.sel.hx, m.sel.hy = m.sel.area.Clamp(x, y)
	if m.sel.hx != m.sel.ax || m.sel.hy != m.sel.ay {
		m.sel.shown = true
		// A drag is not a click: a click right after it starts a new pair.
		m.lastClick.id = ""
	}
}

// release ends a drag and copies what it selected from the frame on screen.
// The highlight stays, to show what was copied, until a key, a click or the
// wheel.
func (m *AppModel) release() tea.Cmd {
	if !m.sel.dragging {
		return nil
	}
	m.sel.dragging = false
	if !m.sel.shown {
		return nil
	}
	text := components.SelectedText(m.screenText, m.sel.spans())
	if text == "" {
		return nil
	}
	return components.Copy(text, "copied "+count(strings.Count(text, "\n")+1, "line", "lines"))
}

// toggleMouse gives the mouse to the terminal, or takes it back. With it the
// terminal selects as it always did, which den's own selection cannot match:
// a line cut short with …, a wrapped line as one, the scrollback. View asks
// for the mouse again, or stops asking, on the next frame.
func (m *AppModel) toggleMouse() tea.Cmd {
	m.mouseOff = !m.mouseOff
	m.sel = selection{}
	text := "den has the mouse again"
	if m.mouseOff {
		text = "mouse given to the terminal — M takes it back"
	}
	return func() tea.Msg { return msgs.StatusFlashMsg{Text: text, Hold: 3 * time.Second} }
}
