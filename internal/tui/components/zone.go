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
func ScanZones(frame string) (clean string, zones Zones) {
	if !strings.Contains(frame, zoneMark) {
		return frame, nil
	}
	lines := strings.Split(frame, "\n")
	type open struct {
		id string
		x  int
	}
	for y, line := range lines {
		if !strings.Contains(line, zoneMark) {
			continue
		}
		var (
			b     strings.Builder
			stack []open
			x     int
		)
		b.Grow(len(line))
		for {
			i := strings.Index(line, zoneMark)
			if i < 0 {
				break
			}
			b.WriteString(line[:i])
			x += ansi.StringWidth(line[:i])
			line = line[i+len(zoneMark):]
			end := strings.Index(line, zoneST)
			if end < 0 {
				line = "" // a mark cut short: nothing after it is text
				break
			}
			if id, ok := strings.CutPrefix(line[:end], ":"); ok {
				stack = append(stack, open{id, x})
			} else if n := len(stack); n > 0 {
				zones = append(zones, zoneArea{stack[n-1].id, y, stack[n-1].x, x, n - 1})
				stack = stack[:n-1]
			}
			line = line[end+len(zoneST):]
		}
		b.WriteString(line)
		x += ansi.StringWidth(line)
		for d, o := range stack {
			zones = append(zones, zoneArea{o.id, y, o.x, x, d})
		}
		lines[y] = b.String()
	}
	return strings.Join(lines, "\n"), zones
}
