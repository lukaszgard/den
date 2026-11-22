package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestContainsFold(t *testing.T) {
	for _, c := range []struct {
		s, sub string
		want   bool
	}{
		{"hunter2", "hunt", true},
		{"hunter2", "ER2", true},
		{"Postgres", "gReS", true},
		{"ÉCOLE", "école", true},
		{"école", "ÉCOLE", true},
		{"ÿ-host", "HOST", true},   // a multibyte rune before the match
		{"Kelvin", "Kelvin", true}, // the Kelvin sign is 3 bytes, K one
		{"any", "", true},
		{"", "", true},
		{"ab", "abc", false},
		{"hunter2", "tok", false},
		{"", "a", false},
	} {
		if got := ContainsFold(c.s, c.sub); got != c.want {
			t.Errorf("ContainsFold(%q, %q) = %v, want %v", c.s, c.sub, got, c.want)
		}
	}
}

func TestFoldHitsAreCells(t *testing.T) {
	for _, c := range []struct {
		plain, query string
		want         [][2]int
	}{
		{"prod-db prod", "PROD", [][2]int{{0, 4}, {8, 12}}},
		{"aaaa", "aa", [][2]int{{0, 2}, {2, 4}}}, // not overlapping
		{"日本 prod", "prod", [][2]int{{5, 9}}},    // wide runes take two cells
		{"Kelvin", "kel", [][2]int{{0, 3}}},
		{"prod", "", nil},
		{"prod", "x", nil},
	} {
		got := foldHits(c.plain, c.query)
		if len(got) != len(c.want) {
			t.Errorf("foldHits(%q, %q) = %v, want %v", c.plain, c.query, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("foldHits(%q, %q) = %v, want %v", c.plain, c.query, got, c.want)
				break
			}
		}
	}
}

func TestMarkMatchesLightsUpEveryMatch(t *testing.T) {
	blue := lipgloss.NewStyle().Foreground(lipgloss.Color("#6C91BF"))
	line := blue.Render("db.password") + " prod Prod"
	got := MarkMatches(line, "PROD")
	if ansi.Strip(got) != ansi.Strip(line) {
		t.Fatalf("the text changed: %q", ansi.Strip(got))
	}
	for _, want := range []string{MatchStyle.Render("prod"), MatchStyle.Render("Prod")} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in %q", want, got)
		}
	}
	if !strings.HasPrefix(got, blue.Render("db.password")) {
		t.Errorf("the line's own colours must stay around the matches: %q", got)
	}
	if MarkMatches(line, "") != line || MarkMatches(line, "zzz") != line {
		t.Error("without a match the line must come back as it was")
	}
}

// A selected row's bar comes back after a match: the text after it keeps its
// background, as the text before it.
func TestMarkMatchesKeepsTheSelectedBar(t *testing.T) {
	bar := lipgloss.NewStyle().Background(lipgloss.Color("#1a1a3e"))
	line := bar.Render("  name hunter after")
	got := MarkMatches(line, "hunter")
	_, after, ok := strings.Cut(got, MatchStyle.Render("hunter"))
	if !ok {
		t.Fatalf("the match is not lit up: %q", got)
	}
	if !strings.HasPrefix(after, "\x1b[48;2;26;26;62m after") {
		t.Errorf("the bar must come back before the text after the match: %q", after)
	}
}

func TestMarkMatchesWithinSpans(t *testing.T) {
	line := "  value   in value"
	got := MarkMatches(line, "value", [2]int{2, 7})
	if strings.Count(got, MatchStyle.Render("value")) != 1 {
		t.Fatalf("only the match inside the span must light up: %q", got)
	}
	if !strings.HasSuffix(got, "in value") {
		t.Errorf("the match outside the span must stay as it was: %q", got)
	}
	if MarkMatches(line, "val", [2]int{0, 2}) != line {
		t.Error("a match only partly inside a span must not light up")
	}
}

// A document lights up every match, those on the line it jumped to brighter.
func TestMarkdownViewLightsUpEveryMatch(t *testing.T) {
	v := NewMarkdownView("Help", "# Title\n\nThe needle one.\n\nA Needle two.\n", HelpKey{"esc", "back"})
	v.SetSize(100, 20)
	press(&v, "/", "needle")
	out := v.vp.View()
	if !strings.Contains(out, CurrentMatchStyle.Render("needle")) {
		t.Errorf("the match jumped to must be the brighter:\n%q", out)
	}
	if !strings.Contains(out, MatchStyle.Render("Needle")) {
		t.Errorf("every other match must light up too:\n%q", out)
	}
	press(&v, "enter", "n")
	out = v.vp.View()
	if !strings.Contains(out, CurrentMatchStyle.Render("Needle")) || !strings.Contains(out, MatchStyle.Render("needle")) {
		t.Errorf("n must move the brighter mark to the next match:\n%q", out)
	}
}
