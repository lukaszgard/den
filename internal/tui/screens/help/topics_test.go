package help

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"unicode/utf8"

	den "github.com/lukaszgard/den"
)

// Every topic follows one template, so a reader finds the same thing in the
// same place whichever feature they open. These tests hold the real documents
// in docs/help to it.

// summaryMax keeps the summary readable next to the title in an 80-column
// terminal's list.
const summaryMax = 50

func topics(t *testing.T) []Topic {
	t.Helper()
	groups, err := Load(den.HelpDocs())
	if err != nil {
		t.Fatal(err)
	}
	var out []Topic
	for _, g := range groups {
		out = append(out, g.Topics...)
	}
	return out
}

func TestEveryTopicIsInTheIndex(t *testing.T) {
	files, err := fs.Glob(den.HelpDocs(), "*.md")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]int{}
	for _, topic := range topics(t) {
		listed[topic.File]++
	}
	for _, f := range files {
		if f == indexFile {
			continue
		}
		if listed[f] != 1 {
			t.Errorf("README.md lists %s %d times, want once", f, listed[f])
		}
	}
}

func TestTopicsFollowTheTemplate(t *testing.T) {
	titles := map[string]bool{}
	for _, topic := range topics(t) {
		titles[topic.Title] = true
	}
	for _, topic := range topics(t) {
		t.Run(topic.File, func(t *testing.T) {
			if n := utf8.RuneCountInString(topic.Summary); n > summaryMax {
				t.Errorf("the summary is %d characters, at most %d fit the list: %q", n, summaryMax, topic.Summary)
			}
			checkSections(t, topic)
			checkSourceLines(t, topic)
			checkSeeAlso(t, topic, titles)
		})
	}
}

// checkSections: the template's sections, in its order, the required ones
// present, and a yaml example under Configure.
func checkSections(t *testing.T, topic Topic) {
	t.Helper()
	for _, p := range sectionProblems(topic) {
		t.Error(p)
	}
}

func sectionProblems(topic Topic) []string {
	var problems []string
	got := topicSections(topic)
	var titles []string
	for _, s := range got {
		titles = append(titles, s.title)
	}
	next := 0
	for _, title := range titles {
		i := slices.IndexFunc(templateSections, func(s templateSection) bool { return s.title == title })
		if i < 0 {
			problems = append(problems, fmt.Sprintf("## %s is not a section of the template", title))
			continue
		}
		if i < next {
			problems = append(problems, fmt.Sprintf("## %s is out of order: the template has %v", title, sectionTitles()))
		}
		next = i + 1
	}
	for _, s := range templateSections {
		if s.required && !slices.Contains(titles, s.title) {
			problems = append(problems, fmt.Sprintf("## %s is missing", s.title))
		}
	}
	for _, s := range got {
		if s.title == "Configure" && !strings.Contains(s.body, "```yaml") {
			problems = append(problems, "## Configure has no ```yaml example")
		}
	}
	return problems
}

func sectionTitles() []string {
	var out []string
	for _, s := range templateSections {
		out = append(out, s.title)
	}
	return out
}

type section struct{ title, body string }

func topicSections(topic Topic) []section {
	var out []section
	for line := range strings.SplitSeq(topic.Source, "\n") {
		if title, ok := strings.CutPrefix(line, "## "); ok {
			out = append(out, section{title: strings.TrimSpace(title)})
			continue
		}
		if len(out) > 0 {
			out[len(out)-1].body += line + "\n"
		}
	}
	return out
}

var (
	markdownLink  = regexp.MustCompile(`\[[^\]]+\]\([^)]*\)`)
	markdownImage = regexp.MustCompile(`!\[`)
	numbered      = regexp.MustCompile(`^\d+\. `)
)

// checkSourceLines: glamour keeps a source line break inside a paragraph or
// a bullet, and cuts table cells with …; links print as their text and their
// path run together, and images not at all.
func checkSourceLines(t *testing.T, topic Topic) {
	t.Helper()
	inCode := false
	prev := ""
	for i, line := range strings.Split(topic.Source, "\n") {
		n := i + 1
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
			prev = ""
			continue
		}
		if inCode {
			continue
		}
		switch {
		case strings.HasPrefix(strings.TrimSpace(line), "|"):
			t.Errorf("line %d: a table; write bullets instead", n)
		case markdownImage.MatchString(line):
			t.Errorf("line %d: an image", n)
		case markdownLink.MatchString(line):
			t.Errorf("line %d: a link; name the topic or the docs path instead", n)
		case line != "" && strings.HasPrefix(line, " ") && !strings.HasPrefix(strings.TrimLeft(line, " "), "- "):
			t.Errorf("line %d: an indented continuation line; keep the bullet on one line", n)
		case line != "" && prev != "" && !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "  - ") &&
			!numbered.MatchString(line) && !strings.HasPrefix(prev, "#") && !strings.HasPrefix(line, "#"):
			t.Errorf("line %d: a paragraph spread over two source lines; keep it on one", n)
		}
		prev = line
	}
	if inCode {
		t.Error("a code block is never closed")
	}
}

// checkSeeAlso: every entry is another topic, by its title, or a docs path
// that exists, so the pointers do not rot when a topic is renamed.
func checkSeeAlso(t *testing.T, topic Topic, titles map[string]bool) {
	t.Helper()
	for _, s := range topicSections(topic) {
		if s.title != "See also" {
			continue
		}
		for line := range strings.SplitSeq(strings.TrimSpace(s.body), "\n") {
			entry, ok := strings.CutPrefix(line, "- ")
			switch {
			case !ok:
				t.Errorf("See also: %q is not a bullet", line)
			case strings.HasPrefix(entry, "`docs/"):
				path := strings.Trim(entry, "`")
				if _, err := os.Stat(filepath.Join("..", "..", "..", "..", path)); err != nil {
					t.Errorf("See also: %s does not exist", path)
				}
			case entry == topic.Title:
				t.Errorf("See also: %q is this topic", entry)
			case !titles[entry]:
				t.Errorf("See also: %q is not a topic's title", entry)
			}
		}
	}
}

func TestLoadRejectsWhatTheIndexDoesNotExpect(t *testing.T) {
	topic := "# A\n\nThe summary\n\n## What it is\n"
	for name, docs := range map[string]fstest.MapFS{
		"a stray line": {
			"README.md": {Data: []byte("# Help\n\n## Group\n\n- [A](a.md)\nsee also\n")},
			"a.md":      {Data: []byte(topic)},
		},
		"a link into a folder": {
			"README.md": {Data: []byte("## Group\n\n- [A](sub/a.md)\n")},
		},
		"a title the topic does not have": {
			"README.md": {Data: []byte("## Group\n\n- [B](a.md)\n")},
			"a.md":      {Data: []byte(topic)},
		},
		"a missing topic": {
			"README.md": {Data: []byte("## Group\n\n- [A](a.md)\n")},
		},
		"a topic without a summary": {
			"README.md": {Data: []byte("## Group\n\n- [A](a.md)\n")},
			"a.md":      {Data: []byte("# A\n\n## What it is\n")},
		},
		"a topic without a title": {
			"README.md": {Data: []byte("## Group\n\n- [A](a.md)\n")},
			"a.md":      {Data: []byte("The summary\n")},
		},
		"an empty group": {
			"README.md": {Data: []byte("## Group\n\n- [A](a.md)\n\n## Empty\n")},
			"a.md":      {Data: []byte(topic)},
		},
		"no groups": {
			"README.md": {Data: []byte("# Help\n")},
		},
	} {
		if _, err := Load(docs); err == nil {
			t.Errorf("%s: Load should fail", name)
		}
	}
}

func TestMatchesCountsLinesOutsideCodeFences(t *testing.T) {
	topic := Topic{Source: "# Vault\n\nRead Vault secrets\n\n```yaml\nvault:\n```\nvault login\n"}
	if got := topic.Matches("VAULT"); got != 4 {
		t.Fatalf("Matches = %d, want 4: the title, the summary, the yaml line and the last line", got)
	}
	if got := topic.Matches(""); got != 0 {
		t.Fatalf("an empty query matches nothing, got %d", got)
	}
}

// Every section and every group the index uses has an icon and a colour: one
// without would fall back to plain text, or to the Help purple, unnoticed.
func TestEverySectionAndGroupHasALook(t *testing.T) {
	for _, s := range templateSections {
		if s.icon == "" || s.color.Dark == "" || s.color.Light == "" {
			t.Errorf("section %q has no icon or colour", s.title)
		}
	}
	groups, err := Load(den.HelpDocs())
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		if _, ok := groupLooks[g.Title]; !ok {
			t.Errorf("group %q has no entry in groupLooks", g.Title)
		}
	}
}

// Usage is optional and sits between Configure and Limitations.
func TestUsageComesAfterConfigure(t *testing.T) {
	topic := func(sections ...string) Topic {
		src := "# T\n\nSummary\n"
		for _, s := range sections {
			src += "\n## " + s + "\n\n```yaml\nlogs: {}\n```\n"
		}
		return Topic{File: "t.md", Title: "T", Summary: "Summary", Source: src}
	}
	if p := sectionProblems(topic("What it is", "What you can do", "Before you start", "Configure", "Usage", "Limitations", "See also")); len(p) > 0 {
		t.Errorf("Usage after Configure should pass: %v", p)
	}
	if p := sectionProblems(topic("What it is", "What you can do", "Before you start", "Usage", "Configure", "Limitations", "See also")); len(p) == 0 {
		t.Error("Usage before Configure should fail")
	}
	if p := sectionProblems(topic("What it is", "What you can do", "Before you start", "Limitations", "Usage", "See also")); len(p) == 0 {
		t.Error("Usage after Limitations should fail")
	}
}
