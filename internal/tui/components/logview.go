package components

import (
	"regexp"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// LogView wraps a bubbles viewport for scrollable log display.
type LogView struct {
	vp     viewport.Model
	ready  bool
	width  int
	height int
	// base is the escape sequence that sets the text colour of the view.
	base string
	// kept is keepBase of the lines last shown: a log is set again whole on
	// every new line, and its regexp, run over thousands of unchanged
	// coloured lines, was most of a refresh. Replaced on every SetContent,
	// never changed, so a copy of the view keeps its own.
	kept map[string]string
}

// logColour is the colour of a log line's own text, under any colour the
// process printed.
var logColour = lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))

// NewLogView creates a LogView with the given dimensions.
func NewLogView(width, height int) LogView {
	vp := NewViewport(width, height)
	vp.Style = logColour
	// The sequence lipgloss itself puts before the text, so it is the colour
	// the program's colour profile later turns into what the terminal has.
	base, _, _ := strings.Cut(logColour.Render("x"), "x")
	return LogView{vp: vp, width: width, height: height, ready: true, base: base}
}

// sgr matches a colour or style sequence, with its parameters in the group.
var sgr = regexp.MustCompile("\x1b\\[([0-9;:]*)m")

// resetsColour reports whether an SGR sequence with these parameters puts the
// text back to the terminal's default colour: a reset (0, or none) or the
// default foreground (39). The extended colours, 38 and 48, carry numbers of
// their own, such as the 0 in 38;5;0 (black), that are not resets.
func resetsColour(params string) bool {
	if params == "" {
		return true
	}
	fields := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "0", "00", "39":
			return true
		case "38", "48", "58":
			// 38;5;n is three fields, 38;2;r;g;b five.
			if i+1 < len(fields) && fields[i+1] == "2" {
				i += 4
			} else {
				i += 2
			}
		}
	}
	return false
}

// keepBase puts the view's text colour back after every sequence in line that
// resets the colour: a process that prints a green mark and resets would
// otherwise leave the rest of the line in the terminal's default colour,
// among grey lines.
func (l *LogView) keepBase(line string) string {
	if !strings.Contains(line, "\x1b[") {
		return line
	}
	return sgr.ReplaceAllStringFunc(line, func(seq string) string {
		if resetsColour(sgr.FindStringSubmatch(seq)[1]) {
			return seq + l.base
		}
		return seq
	})
}

// SetContent updates the displayed log lines.
// Auto-scrolls to bottom only if the viewport was already at the bottom,
// so manual scrolling up is preserved when new lines arrive.
func (l *LogView) SetContent(lines []string) {
	atBottom := l.vp.AtBottom()
	kept := make([]string, len(lines))
	memo := make(map[string]string)
	for i, line := range lines {
		k, ok := l.kept[line]
		if !ok {
			k = l.keepBase(line)
		}
		kept[i], memo[line] = k, k
	}
	l.kept = memo
	l.vp.SetContent(strings.Join(kept, "\n"))
	if atBottom {
		l.vp.GotoBottom()
	}
}

// Resize updates the viewport dimensions. A view following the tail keeps
// following it: the scroll offset was computed for the old height, so a
// shorter view would hide the newest lines and a taller one would leave blank
// rows under them.
func (l *LogView) Resize(width, height int) {
	atBottom := l.vp.AtBottom()
	l.width = width
	l.height = height
	l.vp.SetWidth(width)
	l.vp.SetHeight(height)
	if atBottom {
		l.vp.GotoBottom()
	}
}

// GotoBottom shows the newest lines and follows them from there.
func (l *LogView) GotoBottom() {
	l.vp.GotoBottom()
}

// Update handles viewport key messages.
func (l *LogView) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	l.vp, cmd = l.vp.Update(msg)
	return cmd
}

// View renders the log view.
func (l *LogView) View() string {
	return l.vp.View()
}
