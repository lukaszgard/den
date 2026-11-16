package components

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/tui/testkeys"

	"github.com/charmbracelet/x/ansi"
)

func TestLogViewKeepsItsColourAfterAScriptsReset(t *testing.T) {
	l := NewLogView(40, 2)
	if l.base == "" {
		t.Fatal("no base colour sequence")
	}
	for name, reset := range map[string]string{"0": "\x1b[0m", "empty": "\x1b[m", "default fg": "\x1b[39m", "reset+bold": "\x1b[0;1m"} {
		l.SetContent([]string{"\x1b[32m✔" + reset + " ok"})
		want := "✔" + reset + l.base + " ok"
		if got := l.View(); !strings.Contains(got, want) {
			t.Errorf("%s: view = %q, want the base colour back right after the reset (%q)", name, got, want)
		}
	}
}

func TestLogViewLeavesColoursAndPlainLinesAlone(t *testing.T) {
	l := NewLogView(40, 3)
	l.SetContent([]string{"plain", "\x1b[1;31mred\x1b[22m bold off", "\x1b[38;5;0mblack\x1b[38;2;0;0;0m rgb"})
	got := l.View()
	for _, want := range []string{"plain", "\x1b[1;31mred\x1b[22m bold off", "\x1b[38;5;0mblack\x1b[38;2;0;0;0m rgb"} {
		if !strings.Contains(got, want) {
			t.Errorf("view = %q, want %q untouched: none of these resets the colour", got, want)
		}
	}
}

func TestResetsColour(t *testing.T) {
	for params, want := range map[string]bool{
		"": true, "0": true, "39": true, "1;0": true, "0;32": true,
		"1": false, "32": false, "1;31": false, "22": false,
		"38;5;0": false, "38;2;0;0;0": false, "48;5;0;32": false, "38:2::0:0:0": false,
		"38;5;0;0": true, "38;2;1;2;3;39": true,
	} {
		if got := resetsColour(params); got != want {
			t.Errorf("resetsColour(%q) = %v, want %v", params, got, want)
		}
	}
}

// numbered is n lines, "line 0" to "line n-1".
func numbered(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	return lines
}

func TestLogViewSearchLightsUpTheMatchOnAColouredLine(t *testing.T) {
	l := NewLogView(40, 3)
	l.SetContent([]string{"\x1b[31mred\x1b[0m error here", "nothing"})
	l.SetQuery("error")
	if cur, total := l.MatchInfo(); cur != 1 || total != 1 {
		t.Fatalf("MatchInfo = %d/%d, want 1/1", cur, total)
	}
	// "red " is four cells: the escape sequences around it take none.
	if m := l.matches[0]; m.line != 0 || m.col0 != 4 || m.col1 != 9 {
		t.Errorf("match = %+v, want line 0, cells 4-9", m)
	}
	got := l.View()
	if want := currentMatchStyle.Render("error"); !strings.Contains(got, want) {
		t.Errorf("view = %q, want the match lit up as %q", got, want)
	}
	if !strings.Contains(got, "\x1b[31mred") {
		t.Errorf("view = %q, want the line's own colour kept before the match", got)
	}
	if plain := ansi.Strip(got); !strings.Contains(plain, "red error here") {
		t.Errorf("view = %q, want the line's text unchanged", plain)
	}
}

func TestLogViewSearchIsSmartCase(t *testing.T) {
	l := NewLogView(40, 5)
	l.SetContent([]string{"Error one", "error two", "ERROR three", "fine"})
	for q, want := range map[string]int{"error": 3, "Error": 1, "ERROR": 1, "err": 3, "nothing": 0} {
		l.ClearQuery()
		l.SetQuery(q)
		if _, total := l.MatchInfo(); total != want {
			t.Errorf("%q: %d matches, want %d", q, total, want)
		}
	}
}

func TestLogViewSearchTakesTheQueryAsPlainText(t *testing.T) {
	l := NewLogView(40, 5)
	l.SetContent([]string{"a.b", "axb", "(x)"})
	l.SetQuery("a.b")
	if _, total := l.MatchInfo(); total != 1 {
		t.Errorf("a.b: %d matches, want 1: the dot is a dot, not any character", total)
	}
	l.SetQuery("(x")
	if _, total := l.MatchInfo(); total != 1 {
		t.Errorf("(x: %d matches, want 1", total)
	}
}

func TestLogViewNextAndPrevWrapAround(t *testing.T) {
	l := NewLogView(40, 5) // all of it on screen: the search starts at line 0
	l.SetContent([]string{"x a", "b", "x c x", "d"})
	l.SetQuery("x")
	steps := []struct {
		step func()
		want int
	}{
		{func() {}, 1}, {l.Next, 2}, {l.Next, 3}, {l.Next, 1}, {l.Prev, 3}, {l.Prev, 2},
	}
	for i, s := range steps {
		s.step()
		if cur, total := l.MatchInfo(); cur != s.want || total != 3 {
			t.Errorf("step %d: MatchInfo = %d/%d, want %d/3", i, cur, total, s.want)
		}
	}
}

func TestLogViewFirstMatchIsTheFirstFromTheTop(t *testing.T) {
	l := NewLogView(40, 4)
	lines := numbered(30)
	lines[2], lines[20] = "hit early", "hit late"
	l.SetContent(lines)
	l.Update(testkeys.Press("g"))
	for range 10 {
		l.Update(testkeys.Press("down"))
	}
	l.SetQuery("hit")
	if cur, _ := l.MatchInfo(); cur != 2 {
		t.Errorf("current match = %d, want 2: the first at or below line 10, on screen when the search began", cur)
	}
	// Typing on searches from where the search began, not from the jump.
	l.SetQuery("hit l")
	if cur, total := l.MatchInfo(); cur != 1 || total != 1 {
		t.Errorf("MatchInfo = %d/%d, want 1/1", cur, total)
	}
}

func TestLogViewKeepsItsMatchAsLinesArrive(t *testing.T) {
	l := NewLogView(40, 3)
	lines := []string{"x one", "two", "x three"}
	l.SetContent(lines)
	l.SetQuery("x")
	l.Next()
	l.SetContent(append(lines, "x four"))
	if cur, total := l.MatchInfo(); cur != 2 || total != 3 {
		t.Errorf("MatchInfo = %d/%d, want 2/3: the same match, and the new line's", cur, total)
	}
	if !strings.Contains(l.View(), currentMatchStyle.Render("x")) {
		t.Error("the current match is no longer lit up after new lines")
	}
}

func TestLogViewJumpStopsFollowingAndGFollowsAgain(t *testing.T) {
	l := NewLogView(40, 3)
	lines := numbered(20)
	l.SetContent(lines)
	if !l.Following() || !l.vp.AtBottom() {
		t.Fatal("a new view should follow the newest lines")
	}
	l.SetQuery("line 5")
	if l.Following() {
		t.Error("a jump to a match should stop following")
	}
	if !strings.Contains(l.View(), "line 5") {
		t.Errorf("view = %q, want the match on screen", ansi.Strip(l.View()))
	}
	l.SetContent(append(lines, "line 20", "line 21"))
	if !strings.Contains(l.View(), "line 5") {
		t.Errorf("view = %q: new lines pulled the view away from the match", ansi.Strip(l.View()))
	}
	l.Update(testkeys.Press("G"))
	if !l.Following() || !strings.Contains(l.View(), "line 21") {
		t.Errorf("after G: following %v, view %q; want the newest lines, followed", l.Following(), ansi.Strip(l.View()))
	}
	l.Update(testkeys.Press("home"))
	if l.Following() || !strings.Contains(l.View(), "line 0") {
		t.Errorf("after home: following %v, view %q; want the top, not followed", l.Following(), ansi.Strip(l.View()))
	}
	l.Update(testkeys.Press("end"))
	if !l.Following() {
		t.Error("end should follow again")
	}
}

func TestLogViewScrollingUpPausesAndBackDownFollows(t *testing.T) {
	l := NewLogView(40, 3)
	l.SetContent(numbered(10))
	l.Update(testkeys.Press("up"))
	if l.Following() {
		t.Error("scrolling up should stop following")
	}
	l.SetContent(numbered(12))
	if strings.Contains(l.View(), "line 11") {
		t.Error("a paused view went to the new lines")
	}
	l.Update(testkeys.Press("pgdown"))
	if !l.Following() {
		t.Error("scrolling down to the bottom should follow again")
	}
}

func TestLogViewToggleFollow(t *testing.T) {
	l := NewLogView(40, 3)
	l.SetContent(numbered(10))
	l.ToggleFollow()
	if l.Following() {
		t.Fatal("ToggleFollow should pause a following view")
	}
	l.SetContent(numbered(12))
	if strings.Contains(l.View(), "line 11") {
		t.Error("a paused view went to the new lines")
	}
	l.ToggleFollow()
	if !l.Following() || !strings.Contains(l.View(), "line 11") {
		t.Errorf("ToggleFollow on a paused view: following %v, view %q; want the newest lines", l.Following(), ansi.Strip(l.View()))
	}
}

func TestLogViewFIsNoLongerAPageDown(t *testing.T) {
	l := NewLogView(40, 3)
	l.SetContent(numbered(20))
	l.Update(testkeys.Press("g"))
	l.Update(testkeys.Press("f"))
	if !strings.Contains(l.View(), "line 0") {
		t.Errorf("f scrolled the log: %q", ansi.Strip(l.View()))
	}
	l.Update(testkeys.Press("space"))
	if strings.Contains(l.View(), "line 0") {
		t.Error("space should still page down")
	}
}

func TestLogViewHidesOnlyTimestamps(t *testing.T) {
	l := NewLogView(40, 4)
	l.SetContent([]string{"[12:00:01] one", "── last run ──", "[not a stamp] two", "[12:00:02] \x1b[32mgreen\x1b[0m"})
	l.SetHideStamps(true)
	got := ansi.Strip(l.View())
	for _, want := range []string{"one", "── last run ──", "[not a stamp] two", "green"} {
		if !strings.Contains(got, want) {
			t.Errorf("view = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "12:00") {
		t.Errorf("view = %q, want no timestamps", got)
	}
	if text := l.PlainText(); strings.Contains(text, "12:00") {
		t.Errorf("PlainText = %q: a copy takes the log as shown", text)
	}
	l.SetHideStamps(false)
	if got := ansi.Strip(l.View()); !strings.Contains(got, "[12:00:01] one") {
		t.Errorf("view = %q, want the timestamps back", got)
	}
}

func TestLogViewJumpsToAMatchBelowWrappedLines(t *testing.T) {
	l := NewLogView(10, 3)
	lines := make([]string, 0, 12)
	for range 10 {
		lines = append(lines, strings.Repeat("abcdefghij", 4)) // four rows each, wrapped
	}
	lines = append(lines, "needle", "after")
	l.SetContent(lines)
	l.Update(testkeys.Press("g"))
	l.SetWrap(true)
	l.SetQuery("needle")
	if got := ansi.Strip(l.View()); !strings.Contains(got, "needle") {
		t.Errorf("view = %q, want the match on screen: it is on line 10, but row 40", got)
	}
	// Wrapped, a long line shows whole.
	l.ClearQuery()
	l.Update(testkeys.Press("g"))
	if got := ansi.Strip(l.View()); strings.Count(got, "abcdefghij") != 3 {
		t.Errorf("view = %q, want the first line wrapped over the three rows", got)
	}
	l.SetWrap(false)
	if got := ansi.Strip(l.View()); strings.Count(got, "abcdefghij") != 3 || strings.Contains(got, "abcdefghijabc") {
		t.Errorf("view = %q, want three lines, each cut at the edge", got)
	}
}

func TestLogViewPlainTextHasNoEscapeSequences(t *testing.T) {
	l := NewLogView(40, 3)
	l.SetContent([]string{"\x1b[1;31mred\x1b[0m done", "plain"})
	l.SetQuery("done")
	if got, want := l.PlainText(), "red done\nplain"; got != want {
		t.Errorf("PlainText = %q, want %q", got, want)
	}
	if l.Len() != 2 {
		t.Errorf("Len = %d, want 2", l.Len())
	}
}

// colouredLog is n lines of a run's output as the view gets them: a time in
// front, a coloured mark, a reset.
func colouredLog(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("[12:00:%02d] \x1b[32m✔\x1b[0m step %d of the migration applied in 20ms", i%60, i)
	}
	return lines
}

// benchNewLines is a live run of a 5000-line log: every line that arrives sets
// the whole log again, and the view is drawn.
func benchNewLines(b *testing.B, query string) {
	l := NewLogView(120, 30)
	lines := colouredLog(5000)
	l.SetContent(lines)
	if query != "" {
		l.SetQuery(query)
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		lines = append(lines[1:], fmt.Sprintf("[12:01:00] \x1b[32m✔\x1b[0m step %d of the migration applied in 20ms", 5000+i))
		i++
		l.SetContent(lines)
		_ = l.View()
	}
}

func BenchmarkLogViewNewLine(b *testing.B) { benchNewLines(b, "") }

// A search for a word on every tenth line.
func BenchmarkLogViewNewLineSearched(b *testing.B) { benchNewLines(b, "step 12") }
