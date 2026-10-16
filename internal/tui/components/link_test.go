package components

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// links is Links of a Linker that has seen no frame.
func links(frame string) (string, Zones) {
	var l Linker
	return l.Links(frame)
}

// drawn is line as a terminal draws it, to read each cell's style and link.
func drawn(t *testing.T, line string) *vt.Emulator {
	t.Helper()
	e := vt.NewEmulator(80, 2)
	t.Cleanup(func() { _ = e.Close() })
	if _, err := e.Write([]byte(line)); err != nil {
		t.Fatal(err)
	}
	return e
}

func colorsEqual(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// linkCells checks that the cells [x0, x1) of line 0 of e link to target,
// drawn in want's style, and that the cells either side of them do not.
func linkCells(t *testing.T, e *vt.Emulator, x0, x1 int, target string) {
	t.Helper()
	want := drawn(t, linkOn+"x").CellAt(0, 0)
	for x := x0; x < x1; x++ {
		if c := e.CellAt(x, 0); c.Link.URL != target || !want.Style.Equal(&c.Style) {
			t.Errorf("cell %d (%q) links to %q in %+v, want %q in the link style", x, c.Content, c.Link.URL, c.Style, target)
		}
	}
	for _, x := range []int{x0 - 1, x1} {
		if x < 0 {
			continue
		}
		if c := e.CellAt(x, 0); c.Link.URL != "" || c.Style.Underline != 0 {
			t.Errorf("cell %d (%q) next to the link is a link too: %q, %+v", x, c.Content, c.Link.URL, c.Style)
		}
	}
}

// A frame without an address is handed back as it is, as most frames are.
func TestLinksLeaveAFrameWithoutAnAddressAlone(t *testing.T) {
	frame := "plain\n" + lipgloss.NewStyle().Bold(true).Render("bold") + "\nmailto:someone"
	got, zones := links(frame)
	if got != frame || zones != nil {
		t.Fatalf("Links = %q, %v, want the frame as it was and no zones", got, zones)
	}
}

// An address in plain text becomes a link drawn in the link style, whose zone
// covers its cells and names it; the text stays the same.
func TestLinksMarkAnAddress(t *testing.T) {
	const target = "https://den-tui.com/docs?q=1#top"
	frame := "first line\nsee " + target + " for more"
	got, zones := links(frame)
	if ansi.Strip(got) != frame {
		t.Fatalf("the text changed: %q", ansi.Strip(got))
	}
	x1 := 4 + len(target)
	for x, want := range map[int]string{3: "", 4: "link:" + target, x1 - 1: "link:" + target, x1: ""} {
		if got := zoneAt(zones, x, 1); got != want {
			t.Errorf("At(%d,1) = %q, want %q", x, got, want)
		}
	}
	if zoneAt(zones, 4, 0) != "" {
		t.Error("the line above the address has a link")
	}
	if url, ok := LinkTarget(zoneAt(zones, 4, 1)); !ok || url != target {
		t.Fatalf("LinkTarget = %q, %v, want %q", url, ok, target)
	}
	linkCells(t, drawn(t, strings.Split(got, "\n")[1]), 4, x1, target)
}

// After the link, the text is in the colours it had before: a red line stays
// red after the address in it.
func TestLinksPutTheColoursAfterTheLinkBack(t *testing.T) {
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")).Background(lipgloss.Color("#202020"))
	line := red.Render("err: https://x.com/a now")
	got, _ := links(line)
	e := drawn(t, got)
	before, after := e.CellAt(0, 0), e.CellAt(len("err: https://x.com/a "), 0)
	if !before.Style.Equal(&after.Style) || after.Link.URL != "" {
		t.Fatalf("after the link the text is drawn in %+v, linked to %q, want %+v as before it", after.Style, after.Link.URL, before.Style)
	}
	// The address keeps the line's background under the link's colour.
	if c := e.CellAt(5, 0); c.Link.URL != "https://x.com/a" || c.Style.Underline == 0 || !colorsEqual(c.Style.Bg, before.Style.Bg) {
		t.Fatalf("the address is drawn in %+v linked to %q, want underlined on the line's background", c.Style, c.Link.URL)
	}
}

// What ends a sentence is not part of the address, and a closing bracket is
// only when the address opened it.
func TestLinksTrimWhatEndsTheSentence(t *testing.T) {
	for _, c := range []struct{ line, want string }{
		{"see https://x.com/a.", "https://x.com/a"},
		{"go to https://x.com/a, then", "https://x.com/a"},
		{"(see https://x.com/a)", "https://x.com/a"},
		{"(see https://x.com/a).", "https://x.com/a"},
		{"is it https://x.com/a?", "https://x.com/a"},
		{"https://en.wikipedia.org/wiki/Go_(language).", "https://en.wikipedia.org/wiki/Go_(language)"},
		{"[https://x.com/list?a[]=1]", "https://x.com/list?a[]=1"},
		{`"https://x.com/q"`, "https://x.com/q"},
		{"HTTP://X.COM/Up", "HTTP://X.COM/Up"},
	} {
		_, zones := links(c.line)
		at := strings.Index(c.line, "http")
		if at < 0 {
			at = strings.Index(c.line, "HTTP")
		}
		if got := zoneAt(zones, at, 0); got != "link:"+c.want {
			t.Errorf("%q: zone %q, want link:%s", c.line, got, c.want)
		}
		if got := zoneAt(zones, at+len(c.want), 0); got != "" {
			t.Errorf("%q: the link runs on past %s, onto %q", c.line, c.want, got)
		}
	}
}

// An address cut short with … is not one: its target is not the one on
// screen. Neither is a scheme with nothing after it, nor one inside a word.
func TestLinksSkipWhatIsNotAWholeAddress(t *testing.T) {
	for _, line := range []string{
		"open https://den-tui.com/a-very-long-pa…",
		"just https:// here",
		"xhttps://x.com",
		"ftp://x.com/file",
	} {
		got, zones := links(line)
		if got != line || len(zones) != 0 {
			t.Errorf("%q: Links = %q, %v, want it as it was", line, got, zones)
		}
	}
}

// A link already in the frame, glamour's or a program's in a terminal tab,
// keeps its text and look, whatever ends its sequences, and gets a zone; the
// address it shows is not linked again.
func TestLinksFindTheLinksAlreadyThere(t *testing.T) {
	for name, line := range map[string]string{
		"lipgloss": "go: " + lipgloss.NewStyle().Hyperlink("https://den-tui.com").Render("den site") + " now",
		"bel":      "go: \x1b]8;;https://den-tui.com\x07den site\x1b]8;;\x07 now",
		"params":   "go: \x1b]8;id=1;https://den-tui.com\x1b\\den site\x1b]8;;\x1b\\ now",
		"address":  "go: \x1b]8;;https://den-tui.com\x07den site\x1b]8;;\x07 https://den-tui.com/other",
	} {
		got, zones := links(line)
		if name != "address" && got != line {
			t.Errorf("%s: the line changed to %q", name, got)
		}
		if zoneAt(zones, 3, 0) != "" || zoneAt(zones, 4, 0) != "link:https://den-tui.com" ||
			zoneAt(zones, 11, 0) != "link:https://den-tui.com" || zoneAt(zones, 12, 0) != "" {
			t.Errorf("%s: zones %v, want the link's 8 cells from 4", name, zones)
		}
	}

	// Its text, an address itself, opens what the link says.
	line := "\x1b]8;;https://a.com\x07https://b.com\x1b]8;;\x07"
	got, zones := links(line)
	if got != line || len(zones) != 1 || zoneAt(zones, 0, 0) != "link:https://a.com" {
		t.Fatalf("Links = %q, %v, want the line as it was and one zone to a.com", got, zones)
	}

	// One left open runs to the end of its line.
	_, zones = links("ab\x1b]8;;https://a.com\x07cd\nef")
	if zoneAt(zones, 3, 0) != "link:https://a.com" || zoneAt(zones, 0, 1) != "" {
		t.Fatalf("zones %v, want the open link to the end of its line only", zones)
	}
}

// A link to anything but http(s) is the terminal's: a click must never start
// an app.
func TestLinksGiveOtherSchemesNoZone(t *testing.T) {
	for _, line := range []string{
		"\x1b]8;;file:///etc/hosts\x07hosts\x1b]8;;\x07",
		"\x1b]8;;vscode://file/tmp/x\x1b\\open\x1b]8;;\x1b\\",
		"\x1b]8;;https://\x07no host\x1b]8;;\x07",
	} {
		if got, zones := links(line); got != line || len(zones) != 0 {
			t.Errorf("%q: Links = %q, %v, want it as it was, without a zone", line, got, zones)
		}
	}
}

// A link's cells are counted as the terminal draws them: wide characters
// before it take two.
func TestLinksAfterWideCharacters(t *testing.T) {
	_, zones := links("日本 https://x.com")
	if zoneAt(zones, 4, 0) != "" || zoneAt(zones, 5, 0) != "link:https://x.com" || zoneAt(zones, 5+len("https://x.com"), 0) != "" {
		t.Fatalf("zones %v, want the link from column 5", zones)
	}
}

// A text input's cursor drawn in reverse video inside an address still shows,
// and the rest of the address is still a link.
func TestLinksKeepAReverseCellInside(t *testing.T) {
	got, _ := links("https://x.com/\x1b[7ma\x1b[27mb")
	e := drawn(t, got)
	reverse := drawn(t, "\x1b[7mx").CellAt(0, 0).Style.Attrs
	cursor, after := e.CellAt(len("https://x.com/"), 0), e.CellAt(len("https://x.com/a"), 0)
	if cursor.Style.Attrs&reverse == 0 || cursor.Link.URL != "https://x.com/ab" || cursor.Style.Underline == 0 {
		t.Fatalf("the cursor cell is drawn in %+v linked to %q, want reverse video in the link", cursor.Style, cursor.Link.URL)
	}
	if after.Style.Attrs&reverse != 0 || after.Link.URL != "https://x.com/ab" || after.Style.Underline == 0 {
		t.Fatalf("the cell after the cursor is drawn in %+v linked to %q, want the link style", after.Style, after.Link.URL)
	}
	want := drawn(t, linkOn+"x").CellAt(0, 0)
	for x := range len("https://x.com/") {
		if c := e.CellAt(x, 0); c.Link.URL != "https://x.com/ab" || !want.Style.Equal(&c.Style) {
			t.Errorf("cell %d (%q) is drawn in %+v linked to %q, want the link style", x, c.Content, c.Style, c.Link.URL)
		}
	}
}

// Two addresses on a line are two links, each with the text around it as it
// was; a line further down counts its own row.
func TestLinksSeveralAddresses(t *testing.T) {
	frame := "one\na https://a.com b https://b.com c\n\nd https://d.com"
	got, zones := links(frame)
	if ansi.Strip(got) != frame {
		t.Fatalf("the text changed: %q", ansi.Strip(got))
	}
	for _, c := range []struct {
		x, y int
		want string
	}{{2, 1, "link:https://a.com"}, {18, 1, "link:https://b.com"}, {16, 1, ""}, {2, 3, "link:https://d.com"}} {
		if got := zoneAt(zones, c.x, c.y); got != c.want {
			t.Errorf("At(%d,%d) = %q, want %q", c.x, c.y, got, c.want)
		}
	}
	e := drawn(t, strings.Split(got, "\n")[1])
	linkCells(t, e, 2, 15, "https://a.com")
	linkCells(t, e, 18, 31, "https://b.com")
}

// A link in a row a pane marked wins the click over the row.
func TestALinkIsOverTheZoneItIsIn(t *testing.T) {
	clean, zones := ScanZones(Zone("row:1", "name https://x.com"))
	clean, found := links(clean)
	zones = append(zones, found...)
	if zoneAt(zones, 0, 0) != "row:1" || zoneAt(zones, 5, 0) != "link:https://x.com" {
		t.Fatalf("zones %v: the row should be under its name, the link under the address", zones)
	}
	if ansi.Strip(clean) != "name https://x.com" {
		t.Fatalf("the text changed: %q", ansi.Strip(clean))
	}
}

// A Linker draws a line it drew in the last frame as it did, on the row the
// line is on now, and forgets the lines that left the frame.
func TestALinkerRemembersTheLastFrame(t *testing.T) {
	var l Linker
	first, _ := l.Links("a https://a.com\nb https://b.com")
	again, zones := l.Links("top\nb https://b.com\na https://a.com")
	if a, b := strings.Split(first, "\n"), strings.Split(again, "\n"); a[0] != b[2] || a[1] != b[1] {
		t.Fatalf("the lines are drawn differently the second time:\n%q\n%q", first, again)
	}
	if zoneAt(zones, 2, 1) != "link:https://b.com" || zoneAt(zones, 2, 2) != "link:https://a.com" || zoneAt(zones, 2, 0) != "" {
		t.Fatalf("zones %v, want each link on the row its line moved to", zones)
	}
	l.Links("c https://c.com")
	if _, ok := l.kept["a https://a.com"]; ok || len(l.kept) != 1 {
		t.Fatalf("kept %v, want only the line of the last frame", l.kept)
	}
	l.Links("no address")
	if l.kept != nil {
		t.Fatal("a frame without an address should leave nothing kept")
	}
}

func TestLinkTarget(t *testing.T) {
	for id, want := range map[string]string{"link:https://x.com": "https://x.com", "link:": "", "row:1": "", "": ""} {
		if got, ok := LinkTarget(id); got != want || ok != (want != "") {
			t.Errorf("LinkTarget(%q) = %q, %v, want %q", id, got, ok, want)
		}
	}
}
