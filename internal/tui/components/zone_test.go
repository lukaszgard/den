package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// zoneAt is the zone at x, y of a scanned frame, or "" when there is none.
func zoneAt(zones Zones, x, y int) string {
	id, _ := zones.At(x, y)
	return id
}

func TestZoneRoundTrip(t *testing.T) {
	frame := "ab" + Zone("one", "cde") + "fg\n" + Zone("two", "hi")
	clean, zones := ScanZones(frame)
	if clean != "abcdefg\nhi" {
		t.Fatalf("clean = %q, want the frame without its markers", clean)
	}
	for _, c := range []struct {
		x, y int
		want string
	}{{1, 0, ""}, {2, 0, "one"}, {4, 0, "one"}, {5, 0, ""}, {0, 1, "two"}, {1, 1, "two"}, {2, 1, ""}, {0, 2, ""}} {
		if got := zoneAt(zones, c.x, c.y); got != c.want {
			t.Errorf("At(%d,%d) = %q, want %q", c.x, c.y, got, c.want)
		}
	}
}

func TestZoneInnermostWins(t *testing.T) {
	_, zones := ScanZones(Zone("outer", "ab"+Zone("inner", "cd")+"ef"))
	for x, want := range []string{"outer", "outer", "inner", "inner", "outer", "outer"} {
		if got := zoneAt(zones, x, 0); got != want {
			t.Errorf("At(%d,0) = %q, want %q", x, got, want)
		}
	}
}

// A zone's column counts cells, not bytes: a wide character before it takes
// two of them.
func TestZoneAfterWideCharacters(t *testing.T) {
	_, zones := ScanZones("日本 " + Zone("z", "x"))
	if zoneAt(zones, 4, 0) != "" || zoneAt(zones, 5, 0) != "z" {
		t.Fatalf("the zone should be at column 5, after two wide characters and a space")
	}
}

// Each line of a multi-line string is marked on its own, so a zone survives a
// layout that handles lines one at a time.
func TestZoneMarksEveryLine(t *testing.T) {
	_, zones := ScanZones(Zone("m", "a\nbb\nccc"))
	for y, w := range []int{1, 2, 3} {
		if zoneAt(zones, w-1, y) != "m" || zoneAt(zones, w, y) != "" {
			t.Errorf("line %d: the zone should cover %d cells", y, w)
		}
	}
}

// A start whose end was wrapped onto the next line runs to the end of its own;
// the end alone on the next line marks nothing.
func TestZoneUnbalancedMarkers(t *testing.T) {
	marked := Zone("w", "abcd")
	cut := strings.Index(marked, "cd")
	_, zones := ScanZones(marked[:cut] + "  \n" + marked[cut:] + "x")
	if zoneAt(zones, 3, 0) != "w" {
		t.Error("an unclosed zone should run to the end of its line")
	}
	if zoneAt(zones, 0, 1) != "" || zoneAt(zones, 2, 1) != "" {
		t.Error("an end without a start should mark nothing")
	}
}

func TestScanZonesLeavesNoMarker(t *testing.T) {
	frame := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")).Render(Zone("a", "red")) + "\n" + Zone("b", Zone("c", "x"))
	clean, _ := ScanZones(frame)
	if strings.Contains(clean, "\x1b_") || strings.Contains(clean, "den-zone") {
		t.Fatalf("a marker is left in %q", clean)
	}
	if ansi.Strip(clean) != "red\nx" {
		t.Fatalf("clean = %q", ansi.Strip(clean))
	}
}

func TestScanZonesWithoutMarkersReturnsTheFrame(t *testing.T) {
	frame := "plain\nframe"
	clean, zones := ScanZones(frame)
	if clean != frame || len(zones) != 0 {
		t.Fatalf("ScanZones(%q) = %q, %d zones", frame, clean, len(zones))
	}
}

// Markers take no cells and pass through what the views do to a line on its
// way to the screen.
func TestZonesSurviveTheLayout(t *testing.T) {
	row := Zone("row", "hello")
	cases := []struct {
		name string
		s    string
		x, y int
	}{
		{"Width", lipgloss.NewStyle().Width(20).Render(row), 0, 0},
		{"MaxWidth", lipgloss.NewStyle().MaxWidth(3).Render(row), 2, 0},
		{"Padding", lipgloss.NewStyle().Padding(1, 2).Render(row), 2, 1},
		{"Border", lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Render(row), 1, 1},
		{"JoinHorizontal", lipgloss.JoinHorizontal(lipgloss.Top, "left ", "a\n"+row), 5, 1},
		{"JoinVertical", lipgloss.JoinVertical(lipgloss.Left, "top", row), 0, 1},
		{"Truncate", ansi.Truncate("abc"+row, 5, ""), 3, 0},
		{"Overlay pop-up", Overlay("1234567890\n1234567890\n1234567890", row, 10, 3), 3, 1},
	}
	for _, c := range cases {
		if lipgloss.Width(c.s) != lipgloss.Width(ansi.Strip(c.s)) {
			t.Errorf("%s: the markers take cells", c.name)
		}
		clean, zones := ScanZones(c.s)
		if got := zoneAt(zones, c.x, c.y); got != "row" {
			t.Errorf("%s: At(%d,%d) = %q, want the row, in\n%s", c.name, c.x, c.y, got, clean)
		}
	}
}

// What a pop-up covers cannot be clicked: Overlay draws it without its zones.
func TestOverlayDropsTheZonesUnderThePopUp(t *testing.T) {
	bg := Zone("under", "0123456789") + "\n" + Zone("under", "0123456789") + "\n" + Zone("under", "0123456789")
	_, zones := ScanZones(Overlay(bg, "pop", 10, 3))
	for y := range 3 {
		for x := range 10 {
			if id := zoneAt(zones, x, y); id != "" {
				t.Fatalf("At(%d,%d) = %q under a pop-up", x, y, id)
			}
		}
	}
}

func TestZoneIndex(t *testing.T) {
	for _, c := range []struct {
		id, prefix string
		want       int
		ok         bool
	}{
		{ZoneID("row", 12), "row", 12, true},
		{"row:0", "row", 0, true},
		{"row:", "row", 0, false},
		{"row:-1", "row", 0, false},
		{"rows:3", "row", 0, false},
		{"run:3", "row", 0, false},
		{"row:x", "row", 0, false},
	} {
		got, ok := ZoneIndex(c.id, c.prefix)
		if got != c.want || ok != c.ok {
			t.Errorf("ZoneIndex(%q, %q) = %d, %v, want %d, %v", c.id, c.prefix, got, ok, c.want, c.ok)
		}
	}
}

// cellWidth measures what den draws as ansi.StringWidth does: colours, links
// and other escape sequences take no cells, wide characters two.
func TestCellWidthAgreesWithStringWidth(t *testing.T) {
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")).Bold(true)
	for _, s := range []string{
		"",
		"plain ascii",
		red.Render("styled") + " text",
		"日本語 and ascii",
		" icon " + red.Render("  AWS"),
		"é combining",
		"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\ after",
		"\x1b]0;title\x07bell-terminated",
		"\x1b_den-zone:x\x1b\\inside",
		"tab\tand\x7fcontrol",
		"\x1b[1;38;2;1;2;3mtruecolour\x1b[0m",
		"\x1b[", // a sequence cut short
		"👍 ok",
	} {
		if got, want := cellWidth(s), ansi.StringWidth(s); got != want {
			t.Errorf("cellWidth(%q) = %d, want %d", s, got, want)
		}
	}
}
