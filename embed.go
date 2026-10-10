// Package den embeds the documentation that ships inside the binary: the
// changelog rendered by the updates pane, the help topics rendered by the help
// pane, and the den.yaml JSON Schema that `den schema` prints.
//
// They live here rather than next to the screens that render them because the
// embed directive cannot reach above its own directory, and the files belong at
// the repo root and in docs/ where people actually read them. Embedding
// docs/help keeps the in-app help and the published help from drifting apart,
// which they previously did: the help screen carried its own copy as a const.
package den

import (
	"embed"
	"io/fs"
)

//go:embed CHANGELOG.md
var Changelog string

// helpDocs is only the markdown of docs/help: the tape and the GIF it
// renders stay out of the binary.
//
//go:embed docs/help/*.md
var helpDocs embed.FS

// HelpDocs are the Help pane's topics, rooted at docs/help: README.md is the
// table of contents and every other file one topic.
func HelpDocs() fs.FS {
	docs, err := fs.Sub(helpDocs, "docs/help")
	if err != nil {
		panic(err) // a constant, valid path
	}
	return docs
}

// RunbookGuide is how to write a runbook, for agents: den mcp serves it with
// runbook_guide, den runbook new --agent hands it over, and the Agent Skill
// ships the same file.
//
//go:embed skills/den/runbooks.md
var RunbookGuide string

// Schema is the JSON Schema for den.yaml, generated from the config structs.
// docs/config/README.md, the reference page, is generated from it, so its
// directive comes second.
//
//go:generate go run ./internal/tools/schemagen -o schema/den.schema.json
//go:generate go run ./internal/tools/configdoc -i schema/den.schema.json -o docs/config/README.md
//go:embed schema/den.schema.json
var Schema []byte
