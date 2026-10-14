package help

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/testkeys"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// testDocs has two groups, so moving down crosses a heading, and one word,
// pgpass, that only the second topic has.
func testDocs() fstest.MapFS {
	topic := func(title, summary, body string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte("# " + title + "\n\n" + summary + "\n\n## What it is\n\n" + body + "\n")}
	}
	return fstest.MapFS{
		"README.md": {Data: []byte("# Help\n\nIntro.\n\n## Getting started\n\n- [Start](start.md)\n\n## Data stores\n\n- [RDS](rds.md)\n- [Redis](redis.md)\n")},
		"start.md":  topic("Start", "Find den.yaml", "den reads den.yaml."),
		"rds.md":    topic("RDS", "Postgres with tokens", "The token goes to ~/.pgpass."),
		"redis.md":  topic("Redis", "Caches", "Tokens for Redis."),
	}
}

func newTestModel(t *testing.T, width, height int) Model {
	t.Helper()
	m := newModel(testDocs(), width, height)
	if m.err != nil {
		t.Fatal(m.err)
	}
	return m
}

func press(m Model, keys ...string) Model {
	for _, k := range keys {
		m, _ = m.Update(testkeys.Press(k), true)
	}
	return m
}

func TestListGroupsTopicsUnderHeadings(t *testing.T) {
	m := newTestModel(t, 80, 30)
	view := ansi.Strip(m.View(80, 30, true))
	for _, want := range []string{"Help", "3 topics", "GETTING STARTED", "DATA STORES", "▸ Start", "Find den.yaml", "Postgres with tokens"} {
		if !strings.Contains(view, want) {
			t.Errorf("the list should show %q:\n%s", want, view)
		}
	}
}

func TestMovingSkipsHeadingsAndEnterOpens(t *testing.T) {
	m := press(newTestModel(t, 80, 30), "down")
	if !strings.Contains(ansi.Strip(m.View(80, 30, true)), "▸ RDS") {
		t.Fatal("down from the last topic of a group should land on the next group's first topic")
	}
	m = press(m, "down", "down", "down")
	if !strings.Contains(ansi.Strip(m.View(80, 30, true)), "▸ Redis") {
		t.Fatal("down should stop at the last topic")
	}
	m = press(m, "home")
	if !strings.Contains(ansi.Strip(m.View(80, 30, true)), "▸ Start") {
		t.Fatal("home should go to the first topic")
	}

	m = press(m, "down", "enter")
	if m.Reading() != "RDS" || !m.Busy() {
		t.Fatalf("enter should open RDS, reading %q", m.Reading())
	}
	view := ansi.Strip(m.View(80, 30, true))
	if !strings.Contains(view, "The token goes to") || strings.Contains(view, "# RDS") {
		t.Fatalf("the topic should show its text, titled above the box rather than with its H1:\n%s", view)
	}
	m = press(m, "esc")
	if m.Reading() != "" || m.Busy() {
		t.Fatal("esc should close the topic")
	}
	if !strings.Contains(ansi.Strip(m.View(80, 30, true)), "▸ RDS") {
		t.Fatal("back in the list, the cursor should still be on the topic just read")
	}
	if m = press(m, "right"); m.Reading() != "RDS" {
		t.Fatal("→ should open the topic too")
	}
	if m = press(m, "left"); m.Reading() != "" {
		t.Fatal("← should close it")
	}
}

func TestSearchFindsTopicsByTheirText(t *testing.T) {
	m := press(newTestModel(t, 80, 30), "/")
	if !m.Searching() {
		t.Fatal("/ should start a search")
	}
	m = press(m, "pgp", "ass")
	view := ansi.Strip(m.View(80, 30, true))
	if !strings.Contains(view, "/pgpass") || !strings.Contains(view, "1 topic") {
		t.Fatalf("the title should show the search and what it found:\n%s", view)
	}
	if strings.Contains(view, "Start") || strings.Contains(view, "GETTING STARTED") || !strings.Contains(view, "RDS") {
		t.Fatalf("only RDS mentions pgpass, and an empty group should hide:\n%s", view)
	}
	if !strings.Contains(view, "1 match") {
		t.Fatalf("a found topic should say how often it matches:\n%s", view)
	}

	m = press(m, "enter")
	if m.Reading() != "RDS" || m.Searching() {
		t.Fatal("enter should open the topic the search found")
	}
	if !m.doc.HasQuery() || !strings.Contains(ansi.Strip(m.View(80, 30, true)), "1/1") {
		t.Fatalf("the topic should open on the match:\n%s", ansi.Strip(m.View(80, 30, true)))
	}
	m = press(m, "esc")
	if m.Reading() != "RDS" || m.doc.HasQuery() {
		t.Fatal("the first esc should clear the topic's search only")
	}
	m = press(m, "esc")
	if m.Reading() != "" || !m.Busy() {
		t.Fatal("the next esc should go back to the list, still narrowed")
	}
	m = press(m, "esc")
	if m.Busy() {
		t.Fatal("esc in the list should clear the search")
	}
	if view := ansi.Strip(m.View(80, 30, true)); !strings.Contains(view, "▸ RDS") || !strings.Contains(view, "Start") {
		t.Fatalf("clearing the search should show every topic, the cursor still on RDS:\n%s", view)
	}
}

func TestSearchKeysWhileTyping(t *testing.T) {
	m := press(newTestModel(t, 80, 30), "/", "tokens")
	if view := ansi.Strip(m.View(80, 30, true)); !strings.Contains(view, "2 topics") {
		t.Fatalf("tokens is in two topics:\n%s", view)
	}
	m = press(m, "down", "enter")
	if m.Reading() != "Redis" {
		t.Fatalf("↓ while typing should move through what was found, reading %q", m.Reading())
	}

	m = press(newTestModel(t, 80, 30), "/", "nothing like it", "enter")
	if m.Reading() != "" || m.Busy() {
		t.Fatal("enter on a search that found nothing should drop it")
	}
	if view := ansi.Strip(press(m, "/", "zzz").View(80, 30, true)); !strings.Contains(view, "No topic mentions zzz") {
		t.Fatalf("a search that finds nothing should say so:\n%s", view)
	}

	m = press(newTestModel(t, 80, 30), "down", "/", "ab", "backspace", "backspace", "backspace")
	if m.Searching() || m.Busy() {
		t.Fatal("backspace on an empty search should close it")
	}
	m = press(newTestModel(t, 80, 30), "/", "redis", "esc")
	if m.Searching() || m.Busy() || !strings.Contains(ansi.Strip(m.View(80, 30, true)), "▸ Redis") {
		t.Fatal("esc while typing should drop the search and keep the cursor on the topic it found")
	}
}

// A list longer than the pane scrolls with the cursor, marks what is cut off,
// and never draws more lines than the pane has.
func TestLongListScrolls(t *testing.T) {
	docs := fstest.MapFS{}
	index := "## Many\n\n"
	for i := range 30 {
		if i == 15 {
			// A second group puts a gap and a heading among the hidden rows,
			// which the markers must not count.
			index += "\n## More\n\n"
		}
		name := fmt.Sprintf("t%02d", i)
		index += fmt.Sprintf("- [Topic %02d](%s.md)\n", i, name)
		docs[name+".md"] = &fstest.MapFile{Data: []byte(fmt.Sprintf("# Topic %02d\n\nSummary %d\n", i, i))}
	}
	docs["README.md"] = &fstest.MapFile{Data: []byte(index)}
	m := newModel(docs, 60, 15)
	if m.err != nil {
		t.Fatal(m.err)
	}
	view := ansi.Strip(m.View(60, 15, true))
	if !strings.Contains(view, "MANY") || !strings.Contains(view, "↓ 22 more") || strings.Count(view, " more") != 1 {
		t.Fatalf("the top of a long list should show its heading and a ↓ marker counting topics only:\n%s", view)
	}
	for range 20 {
		m = press(m, "down")
	}
	view = ansi.Strip(m.View(60, 15, true))
	if !strings.Contains(view, "▸ Topic 20") || !strings.Contains(view, "↑ ") || strings.Count(view, " more") != 2 {
		t.Fatalf("the cursor should stay on screen between both markers:\n%s", view)
	}
	m = press(m, "up")
	if !strings.Contains(ansi.Strip(m.View(60, 15, true)), "▸ Topic 19") {
		t.Fatal("up should move the cursor without losing it")
	}
	m = press(m, "end")
	if !strings.Contains(ansi.Strip(m.View(60, 15, true)), "▸ Topic 29") {
		t.Fatal("end should go to the last topic")
	}
	for _, size := range [][2]int{{60, 15}, {40, 10}, {120, 40}} {
		w, h := size[0], size[1]
		m = m.SetSize(w, h)
		out := m.View(w, h, true)
		if got := lipgloss.Height(out); got != h {
			t.Errorf("%dx%d: the list is %d lines, want %d", w, h, got, h)
		}
		for i, line := range strings.Split(out, "\n") {
			if lw := lipgloss.Width(line); lw > w {
				t.Errorf("%dx%d: line %d is %d wide", w, h, i, lw)
			}
		}
	}
}

func TestShortcutsFollowWhatIsOnScreen(t *testing.T) {
	m := newTestModel(t, 80, 30)
	if got := m.Shortcuts(); len(got) != 1 || got[0].Title != "Topics" {
		t.Fatalf("the list's shortcuts = %+v", got)
	}
	if got := press(m, "enter").Shortcuts(); len(got) != 1 || got[0].Title != "Topic" {
		t.Fatalf("an open topic's shortcuts = %+v", got)
	}
}

func TestDocsThatDoNotLoadSayWhy(t *testing.T) {
	m := newModel(fstest.MapFS{}, 80, 20)
	if view := ansi.Strip(m.View(80, 20, true)); !strings.Contains(view, "did not load") {
		t.Fatalf("the pane should say the docs did not load:\n%s", view)
	}
	if m = press(m, "enter", "down", "/", "x", "enter"); m.Reading() != "" {
		t.Fatal("with no topics, nothing should open")
	}
}

// The embedded docs load: a broken index would leave the pane empty.
func TestEmbeddedDocsLoad(t *testing.T) {
	m := New(100, 30)
	if m.err != nil {
		t.Fatal(m.err)
	}
	if len(m.visible()) == 0 {
		t.Fatal("no topics")
	}
}

// A topic's sections are drawn with their icon and name, not glamour's "##",
// and the summary moves next to the title above the box.
func TestOpenTopicIsStyled(t *testing.T) {
	m := press(New(100, 60), "enter")
	view := ansi.Strip(m.View(100, 60, true))
	if strings.Contains(view, "## ") {
		t.Fatalf("no heading should keep glamour's ##:\n%s", view)
	}
	first := templateSections[0]
	if !strings.Contains(view, first.icon+"  "+first.title+" ─") {
		t.Fatalf("%q should be drawn with its icon and a rule:\n%s", first.title, view)
	}
	lines := strings.Split(view, "\n")
	top := strings.Join(lines[:3], "\n")
	g, _ := m.selected()
	if !strings.Contains(top, groupLook(g.group).icon+" "+g.Title) || !strings.Contains(top, g.Summary) {
		t.Fatalf("the header should hold the group's icon, the title and the summary:\n%s", top)
	}
	if strings.Count(view, g.Summary) != 1 {
		t.Fatalf("the summary should show once, above the box:\n%s", view)
	}
}

func TestGroupHeadingsHaveTheirIcon(t *testing.T) {
	view := ansi.Strip(New(100, 60).View(100, 60, true))
	for _, g := range []string{"Getting started", "Data stores", "Secrets"} {
		if !strings.Contains(view, groupLooks[g].icon+" "+strings.ToUpper(g)) {
			t.Errorf("the %s heading should carry its icon:\n%s", g, view)
		}
	}
}

// The restyled headings and the header fit the pane however narrow it is.
func TestStyledTopicFitsThePane(t *testing.T) {
	for _, w := range []int{40, 60, 120} {
		m := press(New(w, 30), "down", "down", "down", "down", "down", "down", "down", "down", "enter")
		for i, line := range strings.Split(m.View(w, 30, true), "\n") {
			if lw := lipgloss.Width(line); lw > w {
				t.Errorf("width %d: line %d is %d wide: %q", w, i, lw, ansi.Strip(line))
			}
		}
	}
}

func TestHeadingLineLeavesOtherLinesAlone(t *testing.T) {
	for _, plain := range []string{"  Some text", "  ## Not a section", "  # What it is", ""} {
		if _, ok := headingLine(plain, 60, true); ok {
			t.Errorf("headingLine restyled %q", plain)
		}
	}
	line, ok := headingLine("  ## Limitations   ", 40, true)
	if !ok || lipgloss.Width(line) > 40 || !strings.HasPrefix(ansi.Strip(line), "  ") {
		t.Fatalf("a section heading should keep its indent and fit 40 columns: %q", ansi.Strip(line))
	}
}

// Every heading of every topic is one of the template's, so none is left as
// glamour's "##".
func TestNoTopicKeepsARawHeading(t *testing.T) {
	m := New(100, 400)
	for i := range m.visible() {
		m.cursor = i
		open := m.openTopic()
		for _, line := range strings.Split(ansi.Strip(open.View(100, 400, true)), "\n") {
			if strings.HasPrefix(strings.TrimSpace(strings.Trim(line, "│ ")), "## ") {
				t.Errorf("%s: %q was not restyled", open.Reading(), line)
			}
		}
	}
}

// Pasted text goes into the search that is open, in the list and in a topic,
// and is dropped when none is.
func TestPasteGoesIntoTheOpenSearch(t *testing.T) {
	m := newTestModel(t, 80, 30)
	m, _ = m.Update(testkeys.Paste("ignored"), true)
	if m.query != "" || m.typing {
		t.Fatalf("a paste with no search open changed the model: %q", m.query)
	}
	m = press(m, "/")
	m, _ = m.Update(testkeys.Paste("pg"), true)
	if !m.typing || m.query != "pg" {
		t.Fatalf("query = %q after a paste into the list search", m.query)
	}
	m, _ = m.Update(testkeys.Paste("pass"), false)
	if m.query != "pg" {
		t.Fatalf("an unfocused pane took a paste: %q", m.query)
	}

	m = press(newTestModel(t, 80, 30), "enter", "/")
	m, _ = m.Update(testkeys.Paste("den"), true)
	if !m.doc.Searching() {
		t.Fatal("a paste into an open topic's search should keep it open")
	}
}

// The list and an open topic switch to the light colours when the terminal
// reports a light background, and back; the dark ones show until then.
func TestSetDarkSwitchesTheColours(t *testing.T) {
	const lightRGB = "107;70;193" // #6B46C1, the light "What it is" and Getting started
	m := newTestModel(t, 80, 30)
	if !m.Dark() || strings.Contains(m.View(80, 30, true), lightRGB) {
		t.Fatal("the dark palette should show until the terminal replies")
	}
	m = m.SetDark(false)
	if m.Dark() || !strings.Contains(m.View(80, 30, true), lightRGB) {
		t.Fatal("the list should be drawn in the light colours")
	}
	m = press(m, "enter")
	if m.Reading() == "" || !strings.Contains(m.View(80, 30, true), lightRGB) {
		t.Fatal("a topic opened on a light background should use the light colours")
	}
	m = m.SetDark(true)
	if m.Reading() == "" || !m.Dark() || strings.Contains(m.View(80, 30, true), lightRGB) {
		t.Fatal("the open topic should go back to the dark colours and stay open")
	}
	m = m.SetDark(false)
	if !strings.Contains(m.View(80, 30, true), lightRGB) {
		t.Fatal("the open topic should switch to the light colours")
	}
}

// zoneOf is the zone under the first cell of text in the list, which is what a
// click there would land on.
func zoneOf(t *testing.T, m Model, text string) string {
	t.Helper()
	clean, zones := components.ScanZones(m.View(80, 30, true))
	for y, line := range strings.Split(ansi.Strip(clean), "\n") {
		if i := strings.Index(line, text); i >= 0 {
			id, _ := zones.At(ansi.StringWidth(line[:i]), y)
			return id
		}
	}
	t.Fatalf("%q is not in the list:\n%s", text, ansi.Strip(clean))
	return ""
}

// A click on a topic selects it; a heading is nothing to click, and while a
// topic is open the list is not on screen to be clicked.
func TestClickSelectsATopic(t *testing.T) {
	m := newTestModel(t, 80, 30)
	if id := zoneOf(t, m, "DATA STORES"); id != "" {
		t.Fatalf("the heading is the zone %q", id)
	}
	redis := zoneOf(t, m, "Redis")
	m = m.Click(redis)
	if !strings.Contains(ansi.Strip(m.View(80, 30, true)), "▸ Redis") {
		t.Fatal("a click on Redis should select it")
	}
	m = press(m, "up", "enter")
	if m = m.Click(redis); m.Reading() != "RDS" || press(m, "esc").cursor != 1 {
		t.Fatal("a click while a topic is open should do nothing")
	}
}
