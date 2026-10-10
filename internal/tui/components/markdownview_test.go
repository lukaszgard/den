package components

import (
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/tui/testkeys"

	"github.com/charmbracelet/x/ansi"

	den "github.com/lukaszgard/den"

	"charm.land/lipgloss/v2"
)

// testDoc is long enough to scroll and wide enough to wrap, with a table:
// the shapes that suffered when the help was stuck at its startup width.
var testDoc = "# Title\n\n" +
	strings.Repeat("A paragraph long enough to wrap at any pane width den runs in, and then some more words. ", 4) + "\n\n" +
	"| Key | Action |\n|-----|--------|\n" +
	strings.Repeat("| `c` | Connect the selected service and keep its tunnel open while den runs |\n", 30)

func widestLine(s string) int {
	widest := 0
	for line := range strings.SplitSeq(s, "\n") {
		widest = max(widest, lipgloss.Width(line))
	}
	return widest
}

// widestText ignores the padding the viewport adds, which is as wide as the
// viewport whatever the text inside it. Tests render in glamour's notty style,
// so no escape codes hide behind the trailing spaces.
func widestText(s string) int {
	widest := 0
	for line := range strings.SplitSeq(s, "\n") {
		widest = max(widest, lipgloss.Width(strings.TrimRight(line, " ")))
	}
	return widest
}

func TestMarkdownViewReflowsOnResize(t *testing.T) {
	v := NewMarkdownView("Help", testDoc, HelpKey{"esc", "back"})
	v.SetSize(60, 30)
	narrow := widestText(v.vp.View())
	v.SetSize(160, 30)
	wide := widestText(v.vp.View())

	if narrow > 60-8 {
		t.Fatalf("text at 60 columns is %d wide, wider than the box", narrow)
	}
	if wide <= 60 || wide > 160-8 {
		t.Fatalf("text at 160 columns is %d wide, want re-rendered to (60, 152]", wide)
	}
}

// glamour's lines run wider than its wrap width. A line wider than the
// viewport is re-wrapped there, which is how a table rule came out split over
// two rows. Checked on the real documents, in every style den can pick.
func TestRenderedDocsFitTheBox(t *testing.T) {
	docs := map[string]string{"changelog": den.Changelog}
	topics, err := fs.Glob(den.HelpDocs(), "*.md")
	if err != nil || len(topics) == 0 {
		t.Fatalf("no help topics: %v", err)
	}
	for _, name := range topics {
		src, err := fs.ReadFile(den.HelpDocs(), name)
		if err != nil {
			t.Fatal(err)
		}
		docs["help/"+name] = string(src)
	}
	for _, style := range []string{"dark", "light", "notty"} {
		for name, src := range docs {
			for _, textWidth := range []int{40, 52, 120} {
				for i, line := range strings.Split(renderMarkdown(src, style, textWidth), "\n") {
					if w := lipgloss.Width(line); w > textWidth {
						t.Errorf("%s/%s at %d: line %d is %d wide: %q", style, name, textWidth, i, w, strings.TrimSpace(line))
					}
				}
			}
		}
	}
}

func TestMarkdownViewFitsPane(t *testing.T) {
	for _, size := range [][2]int{{60, 11}, {100, 30}, {160, 50}} {
		w, h := size[0], size[1]
		v := NewMarkdownView("Help", testDoc, HelpKey{"esc", "back"})
		v.SetSize(w, h)
		out := ansi.Strip(v.View())
		if got := widestLine(out); got > w {
			t.Errorf("%dx%d: view is %d wide", w, h, got)
		}
		if got := strings.Count(out, "\n") + 1; got > h {
			t.Errorf("%dx%d: view is %d rows", w, h, got)
		}
		if !strings.Contains(out, "╭") || !strings.Contains(out, "Help") {
			t.Errorf("%dx%d: missing the titled box:\n%s", w, h, out)
		}
	}
}

func TestMarkdownViewScrollHint(t *testing.T) {
	v := NewMarkdownView("Help", testDoc, HelpKey{"esc", "back"})
	v.SetSize(100, 20)
	if out := ansi.Strip(v.View()); !strings.Contains(out, "esc: back  /: search   0%") {
		t.Fatalf("hint should show the keys and the top of the document:\n%s", out)
	}
	v.Update(testkeys.Press("pgdown"))
	if out := ansi.Strip(v.View()); strings.Contains(out, "/: search   0%") {
		t.Fatal("page down should move the scroll position")
	}
}

func TestMarkdownViewClampsOffsetWhenTaller(t *testing.T) {
	v := NewMarkdownView("Help", testDoc, HelpKey{"esc", "back"})
	v.SetSize(100, 20)
	v.vp.GotoBottom()
	v.SetSize(100, 40)
	if !v.vp.AtBottom() || v.vp.PastBottom() {
		t.Fatal("a taller view should stay at the bottom, not scroll past it")
	}
}

// searchDoc has a word on three lines far apart, so a jump to each scrolls.
func searchDoc() string {
	var b strings.Builder
	b.WriteString("# Title\n\n")
	for i := range 60 {
		switch i {
		case 15:
			b.WriteString("The needle one.\n\n")
		case 35:
			b.WriteString("A Needle two.\n\n")
		case 55:
			b.WriteString("And needle three.\n\n")
		default:
			fmt.Fprintf(&b, "Filler paragraph %d.\n\n", i)
		}
	}
	return b.String()
}

func press(v *MarkdownView, keys ...string) {
	for _, k := range keys {
		switch k {
		case "enter", "esc", "backspace", "pgdown":
			v.Update(testkeys.Press(k))
		default:
			for _, r := range k {
				v.Update(testkeys.Text(string(r)))
			}
		}
	}
}

// onTop reports that the box shows text within its first two lines: a jump
// puts the match at the top, one line of context above it, unless the
// document ends too soon for that.
func onTop(v MarkdownView, text string) bool {
	// The match is drawn in reverse video, which splits the text in two.
	view := ansi.Strip(v.vp.View())
	lines := strings.Split(view, "\n")
	if v.vp.AtBottom() {
		return strings.Contains(view, text)
	}
	return strings.Contains(strings.Join(lines[:min(2, len(lines))], "\n"), text)
}

func TestMarkdownViewSearchJumpsWhileTyping(t *testing.T) {
	v := NewMarkdownView("Help", searchDoc(), HelpKey{"esc", "back"})
	v.SetSize(100, 20)
	press(&v, "/", "nee")
	if !v.Searching() || !onTop(v, "needle one") {
		t.Fatalf("typing should jump to the first match:\n%s", ansi.Strip(v.View()))
	}
	if out := ansi.Strip(v.View()); !strings.Contains(out, "/nee█  1/3") {
		t.Fatalf("the hint should show the search and its count:\n%s", out)
	}
	press(&v, "enter")
	if v.Searching() || !v.HasQuery() {
		t.Fatal("enter should end typing and keep the search")
	}
	press(&v, "n")
	if !onTop(v, "Needle two") || !strings.Contains(ansi.Strip(v.View()), "2/3") {
		t.Fatalf("n should go to the next match, case aside:\n%s", ansi.Strip(v.View()))
	}
	press(&v, "n", "n")
	if !onTop(v, "needle one") {
		t.Fatalf("n past the last match should wrap to the first:\n%s", ansi.Strip(v.View()))
	}
	press(&v, "N")
	if !onTop(v, "needle three") {
		t.Fatalf("N before the first match should wrap to the last:\n%s", ansi.Strip(v.View()))
	}
	press(&v, "esc")
	if v.HasQuery() || !strings.Contains(ansi.Strip(v.View()), "esc: back  /: search") {
		t.Fatalf("esc should clear the search:\n%s", ansi.Strip(v.View()))
	}
}

// Typing searches onward from where the reader is, not from the top.
func TestMarkdownViewSearchStartsAtTheView(t *testing.T) {
	v := NewMarkdownView("Help", searchDoc(), HelpKey{"esc", "back"})
	v.SetSize(100, 20)
	for !strings.Contains(v.vp.View(), "Filler paragraph 40") {
		press(&v, "pgdown")
	}
	press(&v, "/", "needle")
	if !onTop(v, "needle three") || !strings.Contains(ansi.Strip(v.View()), "3/3") {
		t.Fatalf("the search should find the next match below the view:\n%s", ansi.Strip(v.View()))
	}
}

func TestMarkdownViewSearchEscReturns(t *testing.T) {
	v := NewMarkdownView("Help", searchDoc(), HelpKey{"esc", "back"})
	v.SetSize(100, 20)
	press(&v, "pgdown")
	before := v.vp.YOffset()
	press(&v, "/", "needle three", "esc")
	if v.Searching() || v.HasQuery() || v.vp.YOffset() != before {
		t.Fatalf("esc while typing should cancel the search and scroll back to %d, at %d", before, v.vp.YOffset())
	}
}

func TestMarkdownViewSearchWithoutMatch(t *testing.T) {
	v := NewMarkdownView("Help", searchDoc(), HelpKey{"esc", "back"})
	v.SetSize(100, 20)
	press(&v, "/", "haystack")
	if !strings.Contains(ansi.Strip(v.View()), "no match") {
		t.Fatalf("the hint should say nothing matched:\n%s", ansi.Strip(v.View()))
	}
	press(&v, "enter")
	if v.Searching() || v.HasQuery() {
		t.Fatal("enter on a search without a match should drop it")
	}
	press(&v, "/", "x", "backspace", "backspace")
	if v.Searching() {
		t.Fatal("backspace on an empty search should close it")
	}
}

// A resize rewraps the document; the search stays on the match it was on.
func TestMarkdownViewSearchSurvivesResize(t *testing.T) {
	v := NewMarkdownView("Help", searchDoc(), HelpKey{"esc", "back"})
	v.SetSize(100, 20)
	press(&v, "/", "needle", "enter", "n")
	v.SetSize(60, 16)
	if !onTop(v, "Needle two") || !strings.Contains(ansi.Strip(v.View()), "2/3") {
		t.Fatalf("after a resize the view should still be on the second match:\n%s", ansi.Strip(v.View()))
	}
	if got := widestLine(ansi.Strip(v.View())); got > 60 {
		t.Fatalf("the view is %d wide in a 60-wide pane", got)
	}
}

// A long search does not widen the view: the hint is cut to the box.
func TestMarkdownViewSearchHintFits(t *testing.T) {
	v := NewMarkdownView("Help", searchDoc(), HelpKey{"esc", "back"})
	v.SetSize(60, 20)
	press(&v, "/", strings.Repeat("needle ", 20))
	if got := widestLine(ansi.Strip(v.View())); got > 60 {
		t.Fatalf("the view is %d wide in a 60-wide pane:\n%s", got, ansi.Strip(v.View()))
	}
}

// Search is how the Help pane opens a topic at the match of the list's search:
// the view starts on the first match, n goes on from there, and a query that
// matches nothing leaves no search behind.
func TestMarkdownViewSearchCarriesAQuery(t *testing.T) {
	v := NewMarkdownView("Help", searchDoc(), HelpKey{"esc", "back"})
	v.SetSize(100, 20)
	v.Search("needle")
	if !v.HasQuery() || v.Searching() {
		t.Fatal("Search should leave a search on, without the box taking the keyboard")
	}
	if out := ansi.Strip(v.View()); !strings.Contains(out, "1/3") || !strings.Contains(out, "needle one") {
		t.Fatalf("the view should open on the first match:\n%s", out)
	}
	v.Update(testkeys.Text("n"))
	if !strings.Contains(ansi.Strip(v.View()), "2/3") {
		t.Fatal("n should go on to the second match")
	}

	v = NewMarkdownView("Help", searchDoc(), HelpKey{"esc", "back"})
	v.SetSize(100, 20)
	v.Search("haystack")
	if v.HasQuery() || v.vp.YOffset() != 0 {
		t.Fatal("a query without a match should leave no search and the view at the top")
	}
}

// SetLineStyle replaces the lines it picks, and search matches what they now
// read; SetHeader replaces the title line.
func TestMarkdownViewLineStyleAndHeader(t *testing.T) {
	v := NewMarkdownView("Help", searchDoc(), HelpKey{"esc", "back"})
	v.SetLineStyle(func(plain string, width int, _ bool) (string, bool) {
		if strings.Contains(plain, "needle one") {
			return "  ★ restyled", true
		}
		return "", false
	})
	v.SetHeader("my header")
	v.SetSize(100, 20)
	v.Search("restyled")
	out := ansi.Strip(v.View())
	if !strings.Contains(out, "★ restyled") || strings.Contains(out, "needle one") {
		t.Fatalf("the picked line should be replaced:\n%s", out)
	}
	if !strings.Contains(out, "1/1") {
		t.Fatalf("search should match the restyled text:\n%s", out)
	}
	if !strings.Contains(out, "my header") || strings.Contains(strings.Split(out, "\n")[1], "Help") {
		t.Fatalf("the header should replace the title:\n%s", out)
	}
}

// ── palette ──────────────────────────────────────────────────────────────────

// asTerminal makes markdownStyle see a terminal for the test.
func asTerminal(t *testing.T) {
	t.Helper()
	old := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdoutIsTerminal = old })
}

func TestMarkdownStyleFollowsTheBackground(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "")
	asTerminal(t)
	if got := markdownStyle(true); got != "dark" {
		t.Errorf("dark background: style = %q, want dark", got)
	}
	if got := markdownStyle(false); got != "light" {
		t.Errorf("light background: style = %q, want light", got)
	}
}

func TestMarkdownStylePriority(t *testing.T) {
	asTerminal(t)
	t.Setenv("GLAMOUR_STYLE", "dracula")
	if got := markdownStyle(false); got != "dracula" {
		t.Errorf("GLAMOUR_STYLE = dracula: style = %q", got)
	}
	// glamour v1's "auto" means: pick by the background.
	t.Setenv("GLAMOUR_STYLE", "auto")
	if got := markdownStyle(false); got != "light" {
		t.Errorf("GLAMOUR_STYLE = auto: style = %q, want light", got)
	}
	// Without a terminal nothing is coloured, whatever the background says.
	t.Setenv("GLAMOUR_STYLE", "")
	stdoutIsTerminal = func() bool { return false }
	if got := markdownStyle(false); got != "notty" {
		t.Errorf("no terminal: style = %q, want notty", got)
	}
}

// A document drawn already is rendered again in the other palette.
func TestMarkdownViewSetDarkRendersAgain(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "")
	asTerminal(t)
	v := NewMarkdownView("Help", testDoc, HelpKey{"esc", "back"})
	v.SetSize(80, 30)
	dark := v.View()
	v.SetDark(false)
	if light := v.View(); light == dark {
		t.Fatal("a light background should render the document in the light style")
	}
	v.SetDark(true)
	if v.View() != dark {
		t.Fatal("a dark background should bring the dark rendering back")
	}
}
