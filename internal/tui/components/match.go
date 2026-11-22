package components

import (
	"slices"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// MatchStyle and CurrentMatchStyle are how a search match looks wherever /
// finds it: a pane's filtered list, a Help topic, the changelog, a run's log.
// A match sets its own text and background colours, so it reads on a dark and
// a light terminal alike and over a selected row; the one a view jumped to is
// the brighter.
var (
	MatchStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#1a1a1a")).Background(lipgloss.Color("#A89A4E"))
	CurrentMatchStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1a1a1a")).Background(lipgloss.Color("#ECFD65"))
)

// MarkMatches lights up every place query is found in line, ignoring case,
// as a list filter matches. line may be styled: its plain text is searched,
// and its own colours come back after each match, so a selected row keeps
// its bar. Mark the finished row, or a cell that carries the row's colours
// itself: the colours come back only as far as line has them, so a plain cell
// marked and then put inside a bar would lose the bar after the match.
//
// With spans (from, to in cells), only the matches inside one of them light
// up: the columns the filter searched, not a tag or a status beside them that
// happens to hold the query.
func MarkMatches(line, query string, spans ...[2]int) string {
	if query == "" {
		return line
	}
	hits := foldHits(ansi.Strip(line), query)
	if len(spans) > 0 {
		hits = slices.DeleteFunc(hits, func(h [2]int) bool {
			return !slices.ContainsFunc(spans, func(s [2]int) bool { return s[0] <= h[0] && h[1] <= s[1] })
		})
	}
	if len(hits) == 0 {
		return line
	}
	return markHits(line, hits, -1)
}

// markHits lights up hits (cells) in line, the one numbered cur brighter.
func markHits(line string, hits [][2]int, cur int) string {
	ranges := make([]lipgloss.Range, len(hits))
	for i, h := range hits {
		style := MatchStyle
		if i == cur {
			style = CurrentMatchStyle
		}
		ranges[i] = lipgloss.NewRange(h[0], h[1], style)
	}
	return lipgloss.StyleRanges(line, ranges...)
}

// markAllCurrent lights up every hit in line as the current match: a
// document steps through its matches a line at a time.
func markAllCurrent(line string, hits [][2]int) string {
	ranges := make([]lipgloss.Range, len(hits))
	for i, h := range hits {
		ranges[i] = lipgloss.NewRange(h[0], h[1], CurrentMatchStyle)
	}
	return lipgloss.StyleRanges(line, ranges...)
}

// foldHits is where query is found in plain, ignoring case: the cells of
// each place, left to right and not overlapping.
func foldHits(plain, query string) [][2]int {
	if query == "" {
		return nil
	}
	var hits [][2]int
	for i := 0; i < len(plain); {
		if n, ok := prefixFold(plain[i:], query); ok {
			c0 := ansi.StringWidth(plain[:i])
			if c1 := c0 + ansi.StringWidth(plain[i:i+n]); c1 > c0 {
				hits = append(hits, [2]int{c0, c1})
			}
			i += n
			continue
		}
		_, w := utf8.DecodeRuneInString(plain[i:])
		i += w
	}
	return hits
}

// ContainsFold reports whether sub is in s, ignoring case. It compares rune
// by rune in place rather than lowercasing s: s may be a decrypted value, and
// a lowercased copy would be one more plaintext string that the Secrets pane
// cannot clear and that lingers until the garbage collector frees it.
func ContainsFold(s, sub string) bool {
	for i := 0; ; {
		if _, ok := prefixFold(s[i:], sub); ok {
			return true
		}
		if i == len(s) {
			return false
		}
		_, w := utf8.DecodeRuneInString(s[i:])
		i += w
	}
}

// prefixFold reports whether s starts with prefix, ignoring case, and how
// many bytes of s it covers. Runes are compared one at a time because a rune
// and its other case may differ in length (K and the Kelvin sign), so byte
// offsets in one cannot be used in the other.
func prefixFold(s, prefix string) (int, bool) {
	n := 0
	for prefix != "" {
		if n == len(s) {
			return 0, false
		}
		a, wa := utf8.DecodeRuneInString(s[n:])
		b, wb := utf8.DecodeRuneInString(prefix)
		if !equalFoldRune(a, b) {
			return 0, false
		}
		n, prefix = n+wa, prefix[wb:]
	}
	return n, true
}

// equalFoldRune reports whether a and b are the same letter in any case,
// walking a's case-folding orbit as strings.EqualFold does.
func equalFoldRune(a, b rune) bool {
	if a == b {
		return true
	}
	for r := unicode.SimpleFold(a); r != a; r = unicode.SimpleFold(r) {
		if r == b {
			return true
		}
	}
	return false
}
