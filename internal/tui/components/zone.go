package components

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// A zone is a clickable area marked while a view renders, found again in the
// finished frame. Panes compute their scroll offsets inside View and some rows
// span several lines, so a click mapped through a copy of that geometry would
// drift from what is drawn; a mark carried by the text itself lands wherever
// the text does, after clipping, joins and pop-ups.
//
// The marks are APC strings (ESC _ … ESC \), which take no cells:
// ansi.StringWidth and lipgloss.Width count them as nothing, ansi.Truncate
// keeps them past its cut and ansi.Strip drops them. The root model removes
// them with ScanZones before the frame reaches the terminal.
const (
	zoneMark  = "\x1b_den-zone"
	zoneStart = zoneMark + ":" // then the id and the terminator
	zoneEnd   = zoneMark + zoneST
	zoneST    = "\x1b\\"
)

// DoubleClick is how soon a second click on the same zone must follow the first
// to count as a double-click, the enter of the mouse.
const DoubleClick = 500 * time.Millisecond

// Zone marks s as the area id. Each line of a multi-line s is marked on its
// own, so the mark survives layouts that handle lines one at a time.
func Zone(id, s string) string {
	start := zoneStart + id + zoneST
	if !strings.Contains(s, "\n") {
		return start + s + zoneEnd
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = start + line + zoneEnd
	}
	return strings.Join(lines, "\n")
}

// ZoneID is the id of the index-th zone of a kind: "row:12".
func ZoneID(prefix string, index int) string { return prefix + ":" + strconv.Itoa(index) }

// ZoneIndex is the index in an id ZoneID made with prefix.
func ZoneIndex(id, prefix string) (int, bool) {
	rest, ok := strings.CutPrefix(id, prefix+":")
	if !ok {
		return 0, false
	}
	i, err := strconv.Atoi(rest)
	if err != nil || i < 0 {
		return 0, false
	}
	return i, true
}

// zoneArea is where a zone landed: cells [x0, x1) of row y. depth counts the
// zones around it, so the innermost one wins a click.
type zoneArea struct {
	id        string
	y, x0, x1 int
	depth     int
}

// Zones are the areas of one frame.
type Zones []zoneArea

// At is the innermost zone at cell x of row y.
func (z Zones) At(x, y int) (id string, ok bool) {
	depth := -1
	for _, a := range z {
		if a.y == y && x >= a.x0 && x < a.x1 && a.depth > depth {
			id, depth, ok = a.id, a.depth, true
		}
	}
	return id, ok
}

// ScanZones removes the marks from frame and returns where each zone landed.
// A start whose end was wrapped away runs to the end of its line; an end with
// no start is dropped.
//
// It runs on every frame, so the lines without a mark are copied in bulk and
// only the text before a mark is measured.
func ScanZones(frame string) (clean string, zones Zones) {
	next := strings.Index(frame, zoneMark)
	if next < 0 {
		return frame, nil
	}
	var (
		b     strings.Builder
		stack []openZone
		y     int
	)
	b.Grow(len(frame))
	for next >= 0 {
		// The lines up to the one with the next mark, as they are.
		from := strings.LastIndexByte(frame[:next], '\n') + 1
		b.WriteString(frame[:from])
		y += strings.Count(frame[:from], "\n")
		line := frame[from:]
		rest := ""
		if nl := strings.IndexByte(line, '\n'); nl >= 0 {
			line, rest = line[:nl], line[nl:]
		}
		zones, stack = scanLine(&b, line, y, zones, stack[:0])
		frame = rest
		next = strings.Index(frame, zoneMark)
	}
	b.WriteString(frame)
	return b.String(), zones
}

// openZone is a zone whose start was seen on the line being scanned.
type openZone struct {
	id string
	x  int
}

// scanLine writes line y without its marks to b and adds the zones on it.
func scanLine(b *strings.Builder, line string, y int, zones Zones, stack []openZone) (Zones, []openZone) {
	x := 0
	for {
		i := strings.Index(line, zoneMark)
		if i < 0 {
			break
		}
		b.WriteString(line[:i])
		x += cellWidth(line[:i])
		line = line[i+len(zoneMark):]
		end := strings.Index(line, zoneST)
		if end < 0 {
			line = "" // a mark cut short: nothing after it is text
			break
		}
		if id, ok := strings.CutPrefix(line[:end], ":"); ok {
			stack = append(stack, openZone{id, x})
		} else if n := len(stack); n > 0 {
			zones = append(zones, zoneArea{stack[n-1].id, y, stack[n-1].x, x, n - 1})
			stack = stack[:n-1]
		}
		line = line[end+len(zoneST):]
	}
	b.WriteString(line)
	if len(stack) > 0 {
		x += cellWidth(line)
		for d, o := range stack {
			zones = append(zones, zoneArea{o.id, y, o.x, x, d})
		}
	}
	return zones, stack
}

// cellWidth is ansi.StringWidth for what den draws, without its parser:
// escape sequences are skipped, a run of plain ASCII counts its bytes, and
// only text with other characters, which may be wide or join what follows, is
// measured in full.
func cellWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == ansi.ESC:
			i += escLen(s[i:])
		case c >= 0x80:
			j := i + 1
			for j < len(s) && s[j] != ansi.ESC {
				j++
			}
			w += ansi.StringWidth(s[i:j])
			i = j
		case c >= 0x20 && c != ansi.DEL:
			w++
			i++
		default:
			i++
		}
	}
	return w
}

// escLen is the length of the escape sequence s starts with: a CSI up to its
// final byte, a string (OSC, DCS, APC, PM, SOS) up to its terminator, any
// other ESC with the byte after it.
func escLen(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[':
		for j := 2; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				return j + 1
			}
		}
		return len(s)
	case ']', 'P', '_', '^', 'X':
		for j := 2; j < len(s); j++ {
			switch {
			case s[j] == ansi.BEL:
				return j + 1
			case s[j] == ansi.ESC && j+1 < len(s) && s[j+1] == '\\':
				return j + 2
			}
		}
		return len(s)
	}
	return 2
}
