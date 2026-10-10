package components

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

// markdownChrome is the rows MarkdownView draws around its viewport: a blank
// line, the title, a blank line, the box's top and bottom border, a blank line
// and the hint.
const markdownChrome = 7

// autoStyle is glamour v1's name for "pick by the terminal's background",
// which GLAMOUR_STYLE may still hold.
const autoStyle = "auto"

// stdoutIsTerminal is a variable so a test can stand in for a terminal.
var stdoutIsTerminal = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

// markdownStyle is the glamour style to render with: GLAMOUR_STYLE, or notty
// when the output is not a terminal, or dark or light by the background the
// terminal reported (dark until it does).
func markdownStyle(isDark bool) string {
	if s := os.Getenv("GLAMOUR_STYLE"); s != "" && s != autoStyle {
		return s
	}
	if !stdoutIsTerminal() {
		return styles.NoTTYStyle
	}
	if isDark {
		return styles.DarkStyle
	}
	return styles.LightStyle
}

// renderMarkdown renders src in style so every line fits textWidth columns.
// It returns src unchanged when glamour fails: plain markdown is still
// readable.
func renderMarkdown(src, style string, textWidth int) string {
	// glamour adds its 2-column margin outside the wrap width, and bullets and
	// inline code can run past it by another column or two. The viewport
	// re-wraps any line wider than itself, which splits a table's rule over
	// two rows, so wrap 4 columns short.
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStylePath(style),
		glamour.WithWordWrap(max(textWidth-4, 1)),
	)
	if err != nil {
		return src
	}
	rendered, err := renderer.Render(src)
	if err != nil {
		return src
	}
	return rendered
}

// RenderMarkdown renders src in den's markdown style for a text area width
// columns wide, on a dark or a light background.
func RenderMarkdown(src string, width int, isDark bool) string {
	return renderMarkdown(src, markdownStyle(isDark), width)
}

// MarkdownView is a markdown document in a titled, bordered, scrollable box
// with a key hint and the scroll position under it. The help pane and the
// changelog both use it, so they cannot drift apart in how they look.
//
// / searches it: the view jumps to the first line holding what is typed, and
// n and N step through the rest. A long document is only useful if what you
// came for can be found without scrolling through all of it.
type MarkdownView struct {
	title, source string
	hint          []HelpKey
	vp            viewport.Model
	renderedAt    int // text width source was last rendered at
	width, height int // what SetSize was last given, to render again on SetDark

	// light is set once the terminal reported a light background; the zero
	// value is the dark palette, which shows until it replies.
	light bool

	header    string                                                    // replaces the title line when set
	lineStyle func(plain string, width int, isDark bool) (string, bool) // restyles rendered lines

	// lines is the rendered document and plain the same lines without their
	// styling, which is what a search matches: escape codes would otherwise
	// split a word or match themselves.
	lines, plain []string

	typing  bool   // the search box has the keyboard
	query   string // what is searched for; empty when no search is on
	origin  int    // the scroll offset when the search started, for esc
	matches []int  // lines of plain holding query
	current int    // the match the view is on, an index into matches
}

// NewMarkdownView creates a view of the markdown source. Nothing is rendered
// until SetSize gives it a width.
func NewMarkdownView(title, source string, hint ...HelpKey) MarkdownView {
	return MarkdownView{title: title, source: source, hint: hint, vp: NewViewport(0, 0)}
}

// SetHeader shows line, already styled, in place of the title: the Help pane
// puts a topic's icon, title and summary there.
func (v *MarkdownView) SetHeader(line string) { v.header = line }

// SetLineStyle has restyle look at every rendered line, by its plain text, the
// text width and whether the background is dark, and replace the ones it
// returns true for. glamour styles a heading level as a whole; the Help pane
// gives each of its sections its own icon and colour this way. Search matches
// the restyled text.
func (v *MarkdownView) SetLineStyle(restyle func(plain string, width int, isDark bool) (string, bool)) {
	v.lineStyle = restyle
	v.renderedAt = 0 // render again with it
}

// Dark reports whether the document is drawn for a dark background.
func (v MarkdownView) Dark() bool { return !v.light }

// SetDark switches the document to the palette of a dark or a light
// background, rendering it again when it was drawn already.
func (v *MarkdownView) SetDark(isDark bool) {
	if v.light == !isDark {
		return
	}
	v.light = !isDark
	if v.renderedAt != 0 {
		v.renderedAt = 0
		v.SetSize(v.width, v.height)
	}
}

// SetSize fits the view to a pane width×height. glamour wraps while it
// renders, so the document is rendered again whenever the text width changes:
// rendered once, it would keep den's 80-column startup placeholder forever.
func (v *MarkdownView) SetSize(width, height int) {
	v.width, v.height = width, height
	// The box is 2 margin + 2 border + 2 padding wider than its text.
	textWidth := max(width-8, 1)
	v.vp.SetWidth(textWidth)
	v.vp.SetHeight(max(height-markdownChrome, 4))
	if textWidth != v.renderedAt {
		v.lines = strings.Split(renderMarkdown(v.source, markdownStyle(!v.light), textWidth), "\n")
		v.plain = make([]string, len(v.lines))
		for i, line := range v.lines {
			v.plain[i] = ansi.Strip(line)
			if v.lineStyle == nil {
				continue
			}
			if styled, ok := v.lineStyle(v.plain[i], textWidth, !v.light); ok {
				v.lines[i], v.plain[i] = styled, ansi.Strip(styled)
			}
		}
		v.renderedAt = textWidth
		// Rewrapping moves every line: find the matches again, and stay on
		// the one the view was on.
		v.findMatches()
		v.showMatch()
	}
	// A taller viewport lowers the furthest offset; clamp so the view never
	// scrolls past the end of the document.
	v.vp.SetYOffset(v.vp.YOffset())
}

// Searching reports that the search box has the keyboard, so keys are text.
func (v MarkdownView) Searching() bool { return v.typing }

// HasQuery reports that a search is on: esc then clears it rather than
// closing whatever shows the view.
func (v MarkdownView) HasQuery() bool { return v.query != "" }

// Search shows the first line holding query, as if it had been typed after /:
// the Help pane carries a search from its topic list into the document it
// opens. Without a match the search is dropped and the view stays at the top.
func (v *MarkdownView) Search(query string) {
	v.typing, v.query, v.origin, v.current = false, query, 0, 0
	v.findMatches()
	if len(v.matches) == 0 {
		v.query = ""
	}
	v.showMatch()
}

// Update scrolls and searches the document.
func (v *MarkdownView) Update(msg tea.Msg) tea.Cmd {
	// Pasted text is typed into the search box, and nothing anywhere else.
	if paste, ok := msg.(tea.PasteMsg); ok {
		if v.typing {
			v.query += paste.Content
			v.findMatches()
			v.current = v.firstMatchFrom(v.origin)
			v.showMatch()
		}
		return nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if v.typing {
			v.typeKey(key)
			return nil
		}
		switch key.String() {
		case "/":
			v.typing, v.query, v.origin = true, "", v.vp.YOffset()
			v.findMatches()
			v.showMatch()
			return nil
		case "n", "N":
			if len(v.matches) > 0 {
				step := 1
				if key.String() == "N" {
					step = len(v.matches) - 1
				}
				v.current = (v.current + step) % len(v.matches)
				v.showMatch()
			}
			return nil
		case "esc", "backspace":
			if v.query != "" {
				v.query = ""
				v.findMatches()
				v.showMatch()
				return nil
			}
		}
	}
	var cmd tea.Cmd
	v.vp, cmd = v.vp.Update(msg)
	return cmd
}

// typeKey edits the search while the box has the keyboard. The view follows
// every letter, so the search ends as soon as what was meant is on screen.
func (v *MarkdownView) typeKey(key tea.KeyPressMsg) {
	switch key.String() {
	case "enter":
		v.typing = false
		if len(v.matches) == 0 {
			v.query = ""
		}
		return
	case "esc":
		v.typing, v.query = false, ""
		v.findMatches()
		v.showMatch()
		v.vp.SetYOffset(v.origin)
		return
	case "backspace":
		if v.query == "" {
			v.typing = false
			return
		}
		runes := []rune(v.query)
		v.query = string(runes[:len(runes)-1])
	default:
		if key.Text == "" || key.Mod.Contains(tea.ModAlt) {
			return
		}
		v.query += key.Text
	}
	v.findMatches()
	v.current = v.firstMatchFrom(v.origin)
	v.showMatch()
}

// findMatches lists the lines holding the query, case aside, keeping the
// current match where it was if there still is one there.
func (v *MarkdownView) findMatches() {
	v.matches = v.matches[:0]
	if q := strings.ToLower(v.query); q != "" {
		for i, line := range v.plain {
			if strings.Contains(strings.ToLower(line), q) {
				v.matches = append(v.matches, i)
			}
		}
	}
	v.current = min(v.current, max(len(v.matches)-1, 0))
}

// firstMatchFrom is the index of the first match at or below line, wrapping
// to the first one: typing searches onward from where the reader is.
func (v MarkdownView) firstMatchFrom(line int) int {
	for i, m := range v.matches {
		if m >= line {
			return i
		}
	}
	return 0
}

// showMatch redraws the document with the current match, and no other line,
// marked, and scrolls that match to the top of the box with one line of what
// comes before it above. Without a match it only redraws.
func (v *MarkdownView) showMatch() {
	v.vp.SetContent(strings.Join(v.marked(), "\n"))
	if len(v.matches) > 0 {
		v.vp.SetYOffset(v.matches[v.current] - 1)
	}
}

// marked is the document with the current match shown in reverse video. The
// line loses its own colours for that, which is what makes it stand out; its
// width stays the same, so the box does not move.
func (v MarkdownView) marked() []string {
	if len(v.matches) == 0 {
		return v.lines
	}
	line := v.matches[v.current]
	plain, lower := v.plain[line], strings.ToLower(v.plain[line])
	query := strings.ToLower(v.query)
	out := slices.Clone(v.lines)
	at := strings.Index(lower, query)
	// Lowercasing can change a text's length in bytes, and the offsets in
	// lower then miss plain; mark the whole line instead.
	if at < 0 || len(lower) != len(plain) || len(query) != len(v.query) {
		out[line] = searchMark.Render(plain)
		return out
	}
	end := at + len(v.query)
	out[line] = plain[:at] + searchMark.Render(plain[at:end]) + plain[end:]
	return out
}

var searchMark = lipgloss.NewStyle().Reverse(true)

// View renders the title, the boxed document and the hint.
func (v MarkdownView) View() string {
	head := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ECFD65"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#6C91BF")).
		Padding(0, 1).
		MarginLeft(2).
		Width(v.vp.Width() + 4). // the box, border included
		Render(v.vp.View())

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#626262")).
		MarginLeft(2).
		Render(ansi.Truncate(v.hintLine(), v.vp.Width()+2, "…"))

	title := "  " + head.Render(v.title)
	if v.header != "" {
		title = "  " + v.header
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		"",
		title,
		"",
		box,
		"",
		hint,
	)
}

// hintLine is the line under the box: the keys, or the search being typed and
// where it stands, then how far down the document the view is.
func (v MarkdownView) hintLine() string {
	scrolled := hintText.Render(fmt.Sprintf(" %3.0f%%", v.vp.ScrollPercent()*100))
	found := "no match"
	if len(v.matches) > 0 {
		found = fmt.Sprintf("%d/%d", v.current+1, len(v.matches))
	}
	switch {
	case v.typing:
		if v.query != "" {
			found = hintText.Render(found + "  ")
		} else {
			found = ""
		}
		return "/" + v.query + "█  " + found + Hints(HelpKey{"enter", "done"}, HelpKey{"esc", "cancel"})
	case v.query != "":
		return Hints(HelpKey{"n N", "next/previous " + found}, HelpKey{"esc", "clear search"}) + scrolled
	}
	return Hints(append(slices.Clone(v.hint), HelpKey{"/", "search"})...) + scrolled
}
