package help

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

// The Help pane is a list of topics, one markdown document per feature, all
// in docs/help/. README.md there is the table of contents for GitHub readers
// and for this pane alike, so the order and grouping are written once:
//
//	## Group
//
//	- [Title](file.md)
//
// A topic starts with "# Title" and a one-line summary, which the list shows
// next to the title. topics_test.go holds every topic to the shared template.

// indexFile is the table of contents in the help docs.
const indexFile = "README.md"

// Topic is one document of the Help pane.
type Topic struct {
	File    string // the file name in docs/help
	Title   string // the document's H1, which the index links with
	Summary string // the line under the H1
	Source  string // the whole markdown document
}

// Group is a heading of the topic list and the topics under it.
type Group struct {
	Title  string
	Topics []Topic
}

var indexEntry = regexp.MustCompile(`^- \[([^\]]+)\]\(([^)/]+\.md)\)$`)

// Load reads the index and every topic it lists. It fails on anything the
// index does not expect, naming the file and line, rather than drop a topic
// from the list without a word.
func Load(docs fs.FS) ([]Group, error) {
	index, err := fs.ReadFile(docs, indexFile)
	if err != nil {
		return nil, err
	}
	var groups []Group
	for i, line := range strings.Split(string(index), "\n") {
		line = strings.TrimRight(line, " \r")
		switch {
		case strings.HasPrefix(line, "## "):
			groups = append(groups, Group{Title: strings.TrimSpace(line[3:])})
		case len(groups) == 0 || line == "":
			// The title and introduction above the first group are for
			// GitHub readers.
		default:
			m := indexEntry.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("%s:%d: want a group's \"- [Title](file.md)\" line, got %q", indexFile, i+1, line)
			}
			topic, err := loadTopic(docs, m[2])
			if err != nil {
				return nil, err
			}
			if topic.Title != m[1] {
				return nil, fmt.Errorf("%s:%d: %s is titled %q, the index calls it %q", indexFile, i+1, m[2], topic.Title, m[1])
			}
			g := &groups[len(groups)-1]
			g.Topics = append(g.Topics, topic)
		}
	}
	for _, g := range groups {
		if len(g.Topics) == 0 {
			return nil, fmt.Errorf("%s: group %q lists no topics", indexFile, g.Title)
		}
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("%s has no groups", indexFile)
	}
	return groups, nil
}

// loadTopic reads one topic: its H1 is the first line and its summary the
// next line with text.
func loadTopic(docs fs.FS, file string) (Topic, error) {
	src, err := fs.ReadFile(docs, file)
	if err != nil {
		return Topic{}, err
	}
	t := Topic{File: file, Source: string(src)}
	lines := strings.Split(t.Source, "\n")
	if !strings.HasPrefix(lines[0], "# ") {
		return Topic{}, fmt.Errorf("%s: the first line must be the \"# Title\"", file)
	}
	t.Title = strings.TrimSpace(lines[0][2:])
	for _, line := range lines[1:] {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			return Topic{}, fmt.Errorf("%s: a one-line summary must follow the title", file)
		}
		t.Summary = line
		break
	}
	if t.Summary == "" {
		return Topic{}, fmt.Errorf("%s: a one-line summary must follow the title", file)
	}
	return t, nil
}

// Body is the document without its H1 and summary: the pane shows both above
// the box already.
func (t Topic) Body() string {
	_, rest, _ := strings.Cut(t.Source, "\n")
	_, body, _ := strings.Cut(strings.TrimLeft(rest, "\n"), "\n")
	return body
}

// Matches is how many lines of the topic hold query, case aside: what the
// list shows next to a topic while a search is on. Code fences are left out,
// as the rendered document does not show them.
func (t Topic) Matches(query string) int {
	q := strings.ToLower(query)
	if q == "" {
		return 0
	}
	n := 0
	for line := range strings.SplitSeq(t.Source, "\n") {
		if !strings.HasPrefix(line, "```") && strings.Contains(strings.ToLower(line), q) {
			n++
		}
	}
	return n
}
