package components

import (
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// StaticCursor gives a text input the steady reverse-video cursor Bubbles v1
// drew when nothing delivered blink ticks to it. Bubbles v2 blinks by default,
// and a cursor that blinks off with no tick to bring it back stays invisible.
func StaticCursor(in *textinput.Model) {
	s := PlainInputStyles()
	s.Cursor.Blink = false
	in.SetStyles(s)
}

// NewViewport is a Bubbles viewport of the given size that scrolls up and down
// only. Bubbles v2 adds left/right and h/l horizontal scrolling, which would
// shift a line wider than the box sideways; den's v1 viewports cut such a line
// and kept h and l for navigation.
func NewViewport(width, height int) viewport.Model {
	vp := viewport.New(viewport.WithWidth(width), viewport.WithHeight(height))
	vp.KeyMap.Left.SetEnabled(false)
	vp.KeyMap.Right.SetEnabled(false)
	return vp
}

// PlainInputStyles are the styles Bubbles v1 gave a text input: no colour on
// the prompt or the text, only the dimmed placeholder. v2 colours both and
// dims a blurred input's text, and its prompt is a light grey that disappears
// on a light terminal.
func PlainInputStyles() textinput.Styles {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	state := textinput.StyleState{
		Text:        lipgloss.NewStyle(),
		Placeholder: dim,
		Suggestion:  dim,
		Prompt:      lipgloss.NewStyle(),
	}
	return textinput.Styles{
		Focused: state,
		Blurred: state,
		Cursor:  textinput.CursorStyle{Shape: tea.CursorBlock, Blink: true},
	}
}
