// Command configdoc writes docs/config/README.md, the den.yaml reference, from
// the JSON Schema that schemagen makes from the config structs: every key, its
// type, whether it is required, the values it allows and the comment on its
// field. Run it through `go generate` (see embed.go, after schemagen) or
// `make generate`; CI fails when the file is stale.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

func main() {
	in := flag.String("i", "schema/den.schema.json", "the schema to read")
	out := flag.String("o", "docs/config/README.md", "output file")
	flag.Parse()
	if err := run(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, "configdoc:", err)
		os.Exit(1)
	}
}

func run(in, out string) error {
	data, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	var root schema
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("%s: %w", in, err)
	}
	doc, err := render(&root)
	if err != nil {
		return err
	}
	return os.WriteFile(out, doc, 0o600)
}

// guides links a block of the schema to the page that explains the feature, as
// GitHub and the site read it from docs/config/. Each file must exist: a page
// that moves fails the generator instead of leaving a dead link.
var guides = []struct {
	page string
	defs []string
}{
	{"../aws/README.md", []string{"AWSSpec", "AWSSessionSpec"}},
	{"../docker/README.md", []string{"DockerSpec"}},
	{"../documentdb/README.md", []string{"DocumentDBSpec"}},
	{"../elasticache/README.md", []string{"RedisSpec"}},
	{"../mcp/README.md", []string{"MCPConfig"}},
	{"../neptune/README.md", []string{"NeptuneSpec"}},
	{"../opensearch/README.md", []string{"OpenSearchSpec"}},
	{"../rds/README.md", []string{"RDSSpec"}},
	{"../redshift/README.md", []string{"RedshiftSpec"}},
	{"../runbook-sources/README.md", []string{"RunbookSource"}},
	{"../runbooks/README.md", []string{"RunbookSettings", "RunbookSpec", "RunbooksConfig", "SequenceSpec"}},
	{"../runbooks/agents/cli/README.md", []string{"AgentSpec"}},
	{"../secrets/README.md", []string{"AWSSecretsManagerSecretSpec", "KeePassXCSecretSpec", "SOPSSecretSpec", "SecretsConfig", "VaultSecretSpec"}},
	{"../transports/README.md", []string{"TransportSpec"}},
	{"../tunnel/README.md", []string{"TunnelAutoConnect", "TunnelSpec"}},
	{"../updates/README.md", []string{"UpdatesConfig"}},
	{"../vpn/README.md", []string{"VPNSpec"}},
}

// guideOf is the page that explains a definition, or "".
func guideOf(def string) string {
	for _, g := range guides {
		if slices.Contains(g.defs, def) {
			return g.page
		}
	}
	return ""
}

// schema is the part of JSON Schema that schemagen emits.
type schema struct {
	Ref         string   `json:"$ref"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []any    `json:"enum"`
	Required    []string `json:"required"`
	Items       *schema  `json:"items"`
	Properties  props    `json:"properties"`
	// AdditionalProperties is false, or the schema of a map's values.
	AdditionalProperties *schema            `json:"-"`
	Defs                 map[string]*schema `json:"$defs"`
}

type prop struct {
	Name string
	S    *schema
}

// props keeps the order of the keys in the file, which is the order of the
// fields in the struct and the one people read den.yaml in.
type props []prop

func (p *props) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	if _, err := dec.Token(); err != nil { // {
		return err
	}
	for dec.More() {
		name, err := dec.Token()
		if err != nil {
			return err
		}
		var s schema
		if err := dec.Decode(&s); err != nil {
			return err
		}
		*p = append(*p, prop{Name: name.(string), S: &s}) //nolint:forcetypeassert // an object key is a string
	}
	return nil
}

// UnmarshalJSON reads additionalProperties, which is false or a schema.
func (s *schema) UnmarshalJSON(b []byte) error {
	type plain schema
	var aux struct {
		plain
		Additional json.RawMessage `json:"additionalProperties"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	*s = schema(aux.plain)
	if len(aux.Additional) > 0 && string(aux.Additional) != "false" && string(aux.Additional) != "true" {
		s.AdditionalProperties = new(schema)
		return json.Unmarshal(aux.Additional, s.AdditionalProperties)
	}
	return nil
}

// block is one object of den.yaml: where it sits (services[].rds), its schema.
type block struct {
	def  string
	path string
	s    *schema
}

func defName(ref string) string { return strings.TrimPrefix(ref, "#/$defs/") }

// collect finds the blocks in the order a reader meets them: depth first
// through the keys, each definition at the first path that reaches it.
func collect(root *schema) ([]block, error) {
	var blocks []block
	seen := map[string]bool{}
	var visit func(s *schema, path string) error
	visit = func(s *schema, path string) error {
		switch {
		case s.Ref != "":
			name := defName(s.Ref)
			def := root.Defs[name]
			if def == nil {
				return fmt.Errorf("%s refers to %s, which the schema does not define", path, s.Ref)
			}
			if seen[name] {
				return nil
			}
			seen[name] = true
			blocks = append(blocks, block{def: name, path: path, s: def})
			return visit(def, path)
		case s.Items != nil:
			return visit(s.Items, path+"[]")
		case s.AdditionalProperties != nil:
			return visit(s.AdditionalProperties, path+".<name>")
		}
		for _, p := range s.Properties {
			if err := visit(p.S, join(path, p.Name)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root, ""); err != nil {
		return nil, err
	}
	// Definitions nothing refers to still belong in the reference.
	var rest []string
	for name := range root.Defs {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	for _, name := range rest {
		blocks = append(blocks, block{def: name, path: name, s: root.Defs[name]})
	}
	return blocks, nil
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// slug is the anchor GitHub and Hugo's Markdown (the same rule) give a heading.
func slug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

func render(root *schema) ([]byte, error) {
	blocks, err := collect(root)
	if err != nil {
		return nil, err
	}
	anchor := map[string]string{}
	for _, bl := range blocks {
		anchor[bl.def] = slug(bl.path)
	}
	paths := map[string]string{}
	for _, bl := range blocks {
		paths[bl.def] = bl.path
	}

	var b bytes.Buffer
	b.WriteString(`<!-- Generated by internal/tools/configdoc from schema/den.schema.json; do not edit. Run: make generate -->

# Configuration reference

Every key of ` + "`den.yaml`" + `, generated from the config structs den itself reads, so it
cannot drift from what den accepts. A key that is not listed here is a mistake: den
reports it as a warning when it loads the file.

den reads ` + "`-c FILE`" + `, then ` + "`./den.yaml`" + `, then ` + "`~/.config/den/den.yaml`" + ` (the config directory follows ` + "`XDG_CONFIG_HOME`" + `; [Files and folders](../files/README.md)). The repository's
annotated [den.yaml](../../den.yaml) is a working example, ` + "`den init --profile P`" + ` writes
one for your account, and [the JSON Schema](../schema/README.md) gives your editor
completion and inline errors. Each feature's page has the keys in context, with
examples; this page is the complete list.

` + "**Required** says whether den refuses the file when the key is missing from its entry. A key marked **no** is optional, or comes from somewhere else, such as the named environment (`environments`) a service picks with `env`, or is needed only in some setups, which its description names.\n\n")

	b.WriteString("## Top-level keys\n\n")
	if err := table(&b, root, "", anchor, paths); err != nil {
		return nil, err
	}

	for _, bl := range blocks {
		fmt.Fprintf(&b, "\n## `%s`\n\n", bl.path)
		if d := blockLede(bl.def, bl.path, oneLine(bl.s.Description)); d != "" {
			b.WriteString(d + "\n\n")
		}
		if g := guideOf(bl.def); g != "" {
			if _, err := os.Stat(filepath.Join("docs/config", g)); err != nil {
				return nil, fmt.Errorf("guide of %s: %w", bl.def, err)
			}
			fmt.Fprintf(&b, "Guide: [%s](%s)\n\n", guideTitle(g), g)
		}
		if err := table(&b, bl.s, bl.path, anchor, paths); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

// blockLede turns a Go doc comment ("RDSSpec holds RDS-specific config.") into a
// sentence about the block, by swapping the type name it starts with for words.
func blockLede(def, path, doc string) string {
	rest, ok := strings.CutPrefix(doc, def+" ")
	if !ok || rest == "" {
		return doc
	}
	subject := "This block"
	if strings.HasSuffix(path, "[]") || strings.HasSuffix(path, ".<name>") {
		subject = "Each entry"
	}
	if strings.HasPrefix(rest, "are ") {
		subject = "These"
	}
	return subject + " " + rest
}

// keyLede does the same for a key's comment ("Dirs are scanned…"): the Go field
// name it starts with is not what the file calls the key.
func keyLede(key, doc string) string {
	word, rest, ok := strings.Cut(doc, " ")
	if !ok || !strings.EqualFold(word, strings.ReplaceAll(key, "_", "")) {
		return doc
	}
	return "`" + key + "` " + rest
}

// guideTitle names a guide by its directory: ../rds/README.md → docs/rds.
func guideTitle(g string) string {
	return "docs/" + strings.TrimSuffix(strings.TrimPrefix(g, "../"), "/README.md")
}

// table writes the keys of one object.
func table(b *bytes.Buffer, s *schema, path string, anchor, paths map[string]string) error {
	if len(s.Properties) == 0 {
		return fmt.Errorf("%q has no keys to document", path)
	}
	required := map[string]bool{}
	for _, r := range s.Required {
		required[r] = true
	}
	b.WriteString("| Key | Type | Required | Description |\n|---|---|---|---|\n")
	for _, p := range s.Properties {
		req := "no"
		if required[p.Name] {
			req = "yes"
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n", p.Name, typeOf(p.S, anchor, paths), req,
			cell(keyLede(p.Name, oneLine(p.S.Description))))
	}
	return nil
}

// enumOf is the allowed values of a key, or of the items of a list key.
func enumOf(s *schema) []any {
	if len(s.Enum) == 0 && s.Items != nil {
		return s.Items.Enum
	}
	return s.Enum
}

// typeOf writes a key's type, linking an object to the table of its keys.
// A key with a fixed set of values lists them here: they are a handful of
// keys, and a column of their own stood empty on every other row.
func typeOf(s *schema, anchor, paths map[string]string) string {
	link := func(ref string) string {
		name := defName(ref)
		return fmt.Sprintf("[`%s`](#%s)", paths[name], anchor[name])
	}
	if enum := enumOf(s); len(enum) > 0 {
		vals := make([]string, len(enum))
		for i, e := range enum {
			vals[i] = fmt.Sprintf("`%v`", e)
		}
		if s.Items != nil {
			return "list of " + s.Items.Type + ", each one of " + strings.Join(vals, ", ")
		}
		return s.Type + ", one of " + strings.Join(vals, ", ")
	}
	switch {
	case s.Ref != "":
		return "object, keys in " + link(s.Ref)
	case s.Items != nil && s.Items.Ref != "":
		return "list of objects, keys in " + link(s.Items.Ref)
	case s.Items != nil:
		return "list of " + s.Items.Type
	case s.AdditionalProperties != nil && s.AdditionalProperties.Ref != "":
		return "map of name → object, keys in " + link(s.AdditionalProperties.Ref)
	case s.AdditionalProperties != nil:
		return "map of string → " + s.AdditionalProperties.Type
	}
	return s.Type
}

// oneLine joins a comment's lines: a table cell cannot hold a line break.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// cell escapes what would end a table cell early.
func cell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
