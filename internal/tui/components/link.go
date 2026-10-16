package components

import (
	"net/url"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// den takes the mouse, so the terminal's own cmd+click never reaches a link
// on screen: den finds the links in the finished frame and opens them itself.
// Panes mark nothing. Links runs once over the frame, which reaches every
// pane, pop-up, bar and terminal tab, and the addresses den never knew about:
// a log line, a runbook's output.

// linkStyle is how an address in den's text is drawn: underlined, in the blue
// of a hint's key.
var linkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C91BF")).Underline(true)

// linkOn is the sequence lipgloss puts before a link's text, so the program's
// colour profile turns it into what the terminal has, as it does any colour.
var linkOn, _, _ = strings.Cut(linkStyle.Render("x"), "x")

// linkPrefix starts the zone id of a link; the address follows.
const linkPrefix = "link:"

// linkDepth puts a link over the zones around it: a click on an address in a
// row opens the address rather than selecting the row.
const linkDepth = 1 << 20

// address is an http(s) address in plain text. It is made of the characters
// a URL may hold, so a space, a quote, a panel's border or a … ends it.
var address = regexp.MustCompile(`(?i)\bhttps?://[a-z0-9\-._~:/?#\[\]@!$&()*+,;=%]+`)

// LinkTarget is the address a zone id of Links stands for.
func LinkTarget(id string) (string, bool) {
	target, ok := strings.CutPrefix(id, linkPrefix)
	if !ok || target == "" {
		return "", false
	}
	return target, true
}

// Linker draws the http(s) addresses in a frame as links and finds where
// every link landed. An address becomes an OSC 8 link in linkStyle; a link
// already there (glamour's, or one a program in a terminal tab printed) keeps
// its look. Only http(s) links get a zone: a click must never start an app,
// so a file:// or custom-scheme link is the terminal's to open.
//
// The zero Linker is ready. It remembers the lines of the last frame: one
// with an address is mostly drawn again unchanged, a log's on every tick, and
// reading it again cost as much as a third of a redraw.
type Linker struct {
	kept map[string]linkedLine
}

// linkedLine is a line with its addresses drawn as links, and their zones on
// row 0.
type linkedLine struct {
	line  string
	zones Zones
}

// Links is frame with its links drawn, and where they landed. It runs on
// every frame, so a frame without "://" comes back as it is and only the lines
// with one are read.
func (l *Linker) Links(frame string) (string, Zones) {
	next := strings.Index(frame, "://")
	if next < 0 {
		l.kept = nil
		return frame, nil
	}
	var (
		b     strings.Builder
		zones Zones
		y     int
	)
	kept := make(map[string]linkedLine, len(l.kept))
	b.Grow(len(frame) + 256)
	for next >= 0 {
		from := strings.LastIndexByte(frame[:next], '\n') + 1
		b.WriteString(frame[:from])
		y += strings.Count(frame[:from], "\n")
		line, rest := frame[from:], ""
		if nl := strings.IndexByte(line, '\n'); nl >= 0 {
			line, rest = line[:nl], line[nl:]
		}
		k, ok := l.kept[line]
		if !ok {
			k = linkLine(line)
		}
		kept[line] = k
		b.WriteString(k.line)
		for _, z := range k.zones {
			z.y = y
			zones = append(zones, z)
		}
		frame = rest
		next = strings.Index(frame, "://")
	}
	l.kept = kept
	b.WriteString(frame)
	return b.String(), zones
}

// textSpan is the bytes [p0, p1) of a line's plain text.
type textSpan struct {
	p0, p1 int
	target string
}

// linkLine draws the addresses on line as links and finds the zones of the
// links on it.
func linkLine(line string) linkedLine {
	var zones Zones
	plain, at, osc := plainText(line)
	for _, l := range osc {
		if web(l.target) && l.p1 > l.p0 {
			zones = append(zones, zoneArea{linkPrefix + l.target, 0, cellWidth(plain[:l.p0]), cellWidth(plain[:l.p1]), linkDepth})
		}
	}
	var bare []textSpan
	for _, m := range address.FindAllStringIndex(plain, -1) {
		// A … right after it cut the address short: its target is not
		// the one on screen.
		if strings.HasPrefix(plain[m[1]:], "…") {
			continue
		}
		s := textSpan{p0: m[0], target: trimAddress(plain[m[0]:m[1]])}
		s.p1 = s.p0 + len(s.target)
		if !web(s.target) || overlaps(s, osc) {
			continue
		}
		bare = append(bare, s)
		zones = append(zones, zoneArea{linkPrefix + s.target, 0, cellWidth(plain[:s.p0]), cellWidth(plain[:s.p1]), linkDepth})
	}
	// Right to left, so the bytes of the ones before stay where at says.
	for i := len(bare) - 1; i >= 0; i-- {
		s := bare[i]
		b0, b1 := at[s.p0], at[s.p1-1]+1
		line = line[:b0] +
			ansi.SetHyperlink(s.target) + linkOn + keepLink(line[b0:b1]) + ansi.ResetHyperlink() +
			// The colours the text after it had before.
			"\x1b[m" + sgrs(line[:b1]) + line[b1:]
	}
	return linkedLine{line, zones}
}

// plainText is line without its escape sequences, where each byte of that text
// is in line, and the OSC 8 links on it, in that text. A link still open at the
// end of the line runs to it.
func plainText(line string) (plain string, at []int, osc []textSpan) {
	var b strings.Builder
	b.Grow(len(line))
	at = make([]int, 0, len(line))
	open := -1
	var target string
	for i := 0; i < len(line); {
		if line[i] != ansi.ESC {
			b.WriteByte(line[i])
			at = append(at, i)
			i++
			continue
		}
		n := escLen(line[i:])
		if t, ok := hyperlink(line[i : i+n]); ok {
			if open >= 0 {
				osc = append(osc, textSpan{open, b.Len(), target})
			}
			open, target = -1, t
			if t != "" {
				open = b.Len()
			}
		}
		i += n
	}
	if open >= 0 {
		osc = append(osc, textSpan{open, b.Len(), target})
	}
	return b.String(), at, osc
}

// hyperlink is the target of an OSC 8 sequence, "" for the one that ends a
// link, and false for any other sequence.
func hyperlink(seq string) (string, bool) {
	body, ok := strings.CutPrefix(seq, "\x1b]8;")
	if !ok {
		return "", false
	}
	body = strings.TrimSuffix(strings.TrimSuffix(body, "\x07"), "\x1b\\")
	_, target, _ := strings.Cut(body, ";") // after the parameters
	return target, true
}

// web reports that target is an http(s) address with a host.
func web(target string) bool {
	u, err := url.Parse(target)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// trimAddress drops what ends the sentence rather than the address: a full
// stop, a comma, a bracket closed after it that opened before it.
func trimAddress(s string) string {
	for s != "" {
		switch c := s[len(s)-1]; {
		case strings.IndexByte(".,;:!?", c) >= 0:
		case c == ')' && strings.Count(s, "(") < strings.Count(s, ")"):
		case c == ']' && strings.Count(s, "[") < strings.Count(s, "]"):
		default:
			return s
		}
		s = s[:len(s)-1]
	}
	return s
}

func overlaps(s textSpan, spans []textSpan) bool {
	for _, o := range spans {
		if s.p0 < o.p1 && o.p0 < s.p1 {
			return true
		}
	}
	return false
}

// keepLink is the text of a link with linkStyle set again after each colour
// or style sequence in it, which would otherwise draw the rest of the link as
// plain text. The style is added to, not replaced: a text input's cursor in
// reverse video still shows.
func keepLink(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	return sgr.ReplaceAllString(s, "${0}"+linkOn)
}

// sgrs is the colour and style sequences in s, in order: written after a
// reset, they set what s left set.
func sgrs(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return ""
	}
	return strings.Join(sgr.FindAllString(s, -1), "")
}
