package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfigEdit changes den.yaml in place, line by line, so that everything it
// does not touch stays exactly as written: comments, their alignment, blank
// lines, a list indented by one space. yaml.v3 can keep comments through a
// round trip, but it re-indents the whole file, which would turn adding one
// source into a diff of every line of a file people curate by hand.
//
// Each change finds its place through yaml.v3's node positions, then inserts
// or deletes whole lines; the new lines are rendered by yaml.v3 and indented
// to match their neighbours. A block written in flow style ({…} or […]) is
// refused rather than rewritten.
type ConfigEdit struct {
	path  string
	lines []string // without their line endings
	eol   string   // "\n", or "\r\n" for a file written that way
	final bool     // the text ends with a line ending
}

// EditConfig opens the den.yaml at path for editing. A symlinked den.yaml is
// edited at its target, so the link survives the save.
func EditConfig(path string) (*ConfigEdit, error) {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(data)
	e := &ConfigEdit{path: path, eol: "\n"}
	if strings.Contains(text, "\r\n") {
		e.eol = "\r\n"
	}
	if text != "" {
		e.final = strings.HasSuffix(text, "\n")
		text = strings.TrimSuffix(text, "\n")
		for line := range strings.SplitSeq(text, "\n") {
			e.lines = append(e.lines, strings.TrimSuffix(line, "\r"))
		}
	}
	return e, nil
}

// Bytes is the edited text.
func (e *ConfigEdit) Bytes() []byte {
	if len(e.lines) == 0 {
		return nil
	}
	out := strings.Join(e.lines, e.eol)
	if e.final {
		out += e.eol
	}
	return []byte(out)
}

// Save writes the edit over den.yaml, once the result loads and validates
// the way den will read it, and returns that config. Nothing is written
// otherwise: a failed save leaves den.yaml as it was.
func (e *ConfigEdit) Save() (*Config, error) {
	mode := os.FileMode(0o600)
	if info, err := os.Stat(e.path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(e.path), ".den.yaml.*")
	if err != nil {
		return nil, err
	}
	name := tmp.Name()
	fail := func(err error) (*Config, error) {
		_ = os.Remove(name)
		return nil, errors.New(strings.ReplaceAll(err.Error(), name, e.path))
	}
	if _, err := tmp.Write(e.Bytes()); err != nil {
		_ = tmp.Close()
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}
	if err := os.Chmod(name, mode); err != nil {
		return fail(err)
	}
	cfg, err := Load(name)
	if err == nil {
		err = Validate(cfg)
	}
	if err != nil {
		return fail(err)
	}
	if err := os.Rename(name, e.path); err != nil {
		return fail(err)
	}
	return cfg, nil
}

// AddSource appends src to runbooks.sources, creating the runbooks: block
// and its sources: key as needed.
func (e *ConfigEdit) AddSource(src RunbookSource) error {
	root, err := e.root()
	if err != nil {
		return err
	}
	item, err := renderYAML(src)
	if err != nil {
		return err
	}
	ri := mapIndex(root, "runbooks")
	if ri < 0 {
		// No runbooks: block at all: one goes at the end of the file.
		block := append([]string{"runbooks:", "  sources:"}, seqItem(item, "    ")...)
		if n := len(e.lines); n > 0 && strings.TrimSpace(e.lines[n-1]) != "" {
			block = append([]string{""}, block...)
		}
		e.insert(len(e.lines), block)
		return nil
	}
	runbooks := root.Content[2*ri+1]
	rbEnd := e.keyEnd(root, ri, len(e.lines), 0)
	switch {
	case isNull(runbooks):
		// runbooks: with nothing under it yet.
		key := root.Content[2*ri]
		indent := strings.Repeat(" ", key.Column-1+2)
		e.insert(key.Line, append([]string{indent + "sources:"}, seqItem(item, indent+"  ")...))
		return nil
	case runbooks.Kind != yaml.MappingNode || runbooks.Style&yaml.FlowStyle != 0:
		return errFlow("runbooks")
	}
	indent := strings.Repeat(" ", runbooks.Content[0].Column-1)
	si := mapIndex(runbooks, "sources")
	if si < 0 {
		e.insert(rbEnd, append([]string{indent + "sources:"}, seqItem(item, indent+"  ")...))
		return nil
	}
	sources := runbooks.Content[2*si+1]
	sEnd := e.keyEnd(runbooks, si, rbEnd, len(indent))
	switch {
	case isNull(sources):
		e.insert(sEnd, seqItem(item, indent+"  "))
		return nil
	case sources.Kind != yaml.SequenceNode || sources.Style&yaml.FlowStyle != 0:
		return errFlow("runbooks.sources")
	}
	if nameIndex(sources, src.Name) >= 0 {
		return fmt.Errorf("a source named %q exists already", src.Name)
	}
	e.insert(sEnd, seqItem(item, strings.Repeat(" ", sources.Column-1)))
	return nil
}

// RemoveSource deletes the source called name, and sources: with it when it
// was the last one.
func (e *ConfigEdit) RemoveSource(name string) error {
	return e.removeEntry("sources", "source", name)
}

// RemoveRunbook deletes the runbook called name from runbooks.items, and
// items: with it when it was the last one.
func (e *ConfigEdit) RemoveRunbook(name string) error {
	return e.removeEntry("items", "runbook", name)
}

// RemoveSequence deletes the sequence called name from runbooks.sequences,
// and sequences: with it when it was the last one.
func (e *ConfigEdit) RemoveSequence(name string) error {
	return e.removeEntry("sequences", "sequence", name)
}

// removeEntry deletes the entry called name from the list runbooks.<list>,
// and the list's key with it when it was the last one: an empty key would
// read as null, which den.yaml's lists are not.
func (e *ConfigEdit) removeEntry(list, what, name string) error {
	at, err := e.findEntry(list, what, name)
	if err != nil {
		return err
	}
	if len(at.seq.Content) == 1 {
		e.delete(at.runbooks.Content[2*at.si].Line-1, at.seqEnd)
		return nil
	}
	e.delete(at.item.Line-1, at.itemEnd)
	return nil
}

// SetSourceList sets the include or exclude list of the source called name;
// an empty list removes the key.
func (e *ConfigEdit) SetSourceList(name, key string, values []string) error {
	if key != "include" && key != "exclude" {
		return fmt.Errorf("%s is not a list den edits (include, exclude)", key)
	}
	at, err := e.findEntry("sources", "source", name)
	if err != nil {
		return err
	}
	var block []string
	indent := strings.Repeat(" ", at.item.Column-1)
	if len(values) > 0 {
		lines, err := renderYAML(map[string][]string{key: values})
		if err != nil {
			return err
		}
		for _, l := range lines {
			block = append(block, indent+l)
		}
	}
	ki := mapIndex(at.item, key)
	if ki < 0 {
		e.insert(at.itemEnd, block)
		return nil
	}
	start := at.item.Content[2*ki].Line - 1
	e.delete(start, e.keyEnd(at.item, ki, at.itemEnd, len(indent)))
	e.insert(start, block)
	return nil
}

// entryAt is where one named entry of a list under runbooks: — a source, a
// runbook of items:, a sequence — sits in den.yaml.
type entryAt struct {
	runbooks *yaml.Node // the runbooks: mapping
	si       int        // index of the list's key in it
	seq      *yaml.Node // the list
	seqEnd   int        // the line after the list's last
	item     *yaml.Node // the entry's mapping
	itemEnd  int        // the line after its last
}

// findEntry finds the entry called name in runbooks.<list>; what names such
// an entry in errors.
func (e *ConfigEdit) findEntry(list, what, name string) (entryAt, error) {
	missing := fmt.Errorf("no %s %q in %s", what, name, e.path)
	root, err := e.root()
	if err != nil {
		return entryAt{}, err
	}
	ri := mapIndex(root, "runbooks")
	if ri < 0 {
		return entryAt{}, missing
	}
	runbooks := root.Content[2*ri+1]
	if runbooks.Kind != yaml.MappingNode || runbooks.Style&yaml.FlowStyle != 0 {
		if isNull(runbooks) {
			return entryAt{}, missing
		}
		return entryAt{}, errFlow("runbooks")
	}
	si := mapIndex(runbooks, list)
	if si < 0 {
		return entryAt{}, missing
	}
	seq := runbooks.Content[2*si+1]
	if seq.Kind != yaml.SequenceNode || seq.Style&yaml.FlowStyle != 0 {
		if isNull(seq) {
			return entryAt{}, missing
		}
		return entryAt{}, errFlow("runbooks." + list)
	}
	i := nameIndex(seq, name)
	if i < 0 {
		return entryAt{}, missing
	}
	item := seq.Content[i]
	if item.Kind != yaml.MappingNode || item.Style&yaml.FlowStyle != 0 {
		return entryAt{}, errFlow(fmt.Sprintf("the %s %q", what, name))
	}
	rbEnd := e.keyEnd(root, ri, len(e.lines), 0)
	keyIndent := runbooks.Content[0].Column - 1
	seqEnd := e.keyEnd(runbooks, si, rbEnd, keyIndent)
	itemEnd := seqEnd
	if i+1 < len(seq.Content) {
		itemEnd = seq.Content[i+1].Line - 1
	}
	itemEnd = e.trimTail(item.Line-1, itemEnd, seq.Column-1)
	return entryAt{runbooks: runbooks, si: si, seq: seq, seqEnd: seqEnd, item: item, itemEnd: itemEnd}, nil
}

func (e *ConfigEdit) root() (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(e.lines, "\n")), &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", e.path, err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode}, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode || root.Style&yaml.FlowStyle != 0 {
		return nil, fmt.Errorf("%s is not a block of keys den can edit", e.path)
	}
	return root, nil
}

// keyEnd is the line after the last one that belongs to the i-th key of m:
// the next key's line, or limit for the last key, less the blank lines and
// the comments heading what follows. indent is the key's own indentation.
func (e *ConfigEdit) keyEnd(m *yaml.Node, i, limit, indent int) int {
	end := limit
	if 2*(i+1) < len(m.Content) {
		end = m.Content[2*(i+1)].Line - 1
	}
	return e.trimTail(m.Content[2*i].Line-1, end, indent)
}

// trimTail moves end back over trailing blank lines and over comments
// indented no deeper than indent, which head the next node. Deeper ones may
// be the content of a block scalar, a script's comment, and stay.
func (e *ConfigEdit) trimTail(start, end, indent int) int {
	for end > start+1 {
		line := e.lines[end-1]
		trimmed := strings.TrimSpace(line)
		lead := len(line) - len(strings.TrimLeft(line, " "))
		if trimmed != "" && (!strings.HasPrefix(trimmed, "#") || lead > indent) {
			break
		}
		end--
	}
	return end
}

func (e *ConfigEdit) insert(at int, lines []string) {
	if at >= len(e.lines) {
		e.final = true
	}
	e.lines = append(e.lines[:at], append(append([]string{}, lines...), e.lines[at:]...)...)
}

func (e *ConfigEdit) delete(start, end int) {
	e.lines = append(e.lines[:start], e.lines[end:]...)
}

// mapIndex is the index of key among m's keys, or -1.
func mapIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i / 2
		}
	}
	return -1
}

// nameIndex is the index of the entry called name in seq, or -1.
func nameIndex(seq *yaml.Node, name string) int {
	for i, item := range seq.Content {
		if k := mapIndex(item, "name"); k >= 0 && item.Content[2*k+1].Value == name {
			return i
		}
	}
	return -1
}

func isNull(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Tag == "!!null" }

func errFlow(what string) error {
	return fmt.Errorf("edit %s in den.yaml by hand: it is written in flow style ({…} or […]), which den does not rewrite", what)
}

// renderYAML renders v the way den.yaml is written, two spaces deep.
func renderYAML(v any) ([]string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n"), nil
}

// seqItem turns rendered lines into one item of a block sequence whose dash
// sits at indent.
func seqItem(lines []string, indent string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if i == 0 {
			out[i] = indent + "- " + l
		} else {
			out[i] = indent + "  " + l
		}
	}
	return out
}
