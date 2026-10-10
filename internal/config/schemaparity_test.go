package config

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	den "github.com/lukaszgard/den"
)

// The schema den ships (schema/den.schema.json) is generated from the config
// structs, and gives editors completion and red squiggles. den's own checks
// (Check) are the only validator: there is no JSON Schema validator in den, as
// it would have to agree with Validate on every rule. What does have to agree
// is the two things the schema promises an editor and Check must therefore keep:
// a key that is `required` is demanded, and a value outside an `enum` is
// refused. These tests take a config that validates, break each such key in
// turn, and ask Check to notice, on the key or the entry it belongs to.

// parityNotEnforced are the keys the schema calls required or enumerated that
// Check deliberately treats differently, each with the reason.
var parityNotEnforced = map[string]string{
	"UpdatesConfig.s3_bucket": "an updates: block without s3_bucket falls back to the GitHub Enterprise release API",
}

// parityWarningOnly are the enums whose wrong value is a warning, not an
// error: den has always ignored the value and used the default.
var parityWarningOnly = map[string]string{
	"LogsConfig.level":      "an unknown level is a warning, info is used",
	"root.menu_order[]":     "an unknown panel is a warning, ignored",
	"root.disabled_panes[]": "an unknown panel is a warning, ignored",
}

// paritySite is one enum or required key the base config has, to break.
type paritySite struct {
	kind string // enum | required
	def  string // the schema definition it belongs to
	key  string // its name there
	path string // where in the base config
}

func (s paritySite) id() string { return s.def + "." + s.key }

type schemaDoc struct {
	Defs map[string]map[string]any `json:"$defs"`
	root map[string]any
}

func loadSchemaDoc(t *testing.T) schemaDoc {
	t.Helper()
	var sd schemaDoc
	if err := json.Unmarshal(den.Schema, &sd); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(den.Schema, &sd.root); err != nil {
		t.Fatal(err)
	}
	return sd
}

// resolve follows a $ref, giving the definition and its name; the root is "root".
func (sd schemaDoc) resolve(s map[string]any) (string, map[string]any) {
	if ref, ok := s["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/$defs/")
		return name, sd.Defs[name]
	}
	return "", s
}

func asObject(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// sites walks the base config along the schema and lists the first
// occurrence of every enum and required key it has.
func (sd schemaDoc) sites(n *yaml.Node, schema map[string]any, name, path string, seen map[string]bool, out *[]paritySite) {
	if n == nil {
		return
	}
	if defName, def := sd.resolve(schema); def != nil {
		schema = def
		if defName != "" {
			name = defName
		}
	}
	add := func(kind, key, at string) {
		s := paritySite{kind: kind, def: name, key: key, path: at}
		if !seen[kind+" "+s.id()] {
			seen[kind+" "+s.id()] = true
			*out = append(*out, s)
		}
	}
	join := func(p, k string) string {
		if p == "" {
			return k
		}
		return p + "." + k
	}
	switch n.Kind {
	case yaml.MappingNode:
		props := asObject(schema["properties"])
		have := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i].Value, n.Content[i+1]
			have[key] = true
			sub := asObject(props[key])
			if sub == nil {
				sub = asObject(schema["additionalProperties"]) // a map: environments
			}
			if sub == nil {
				continue
			}
			_, subDef := sd.resolve(sub)
			if _, ok := sub["enum"]; ok && val.Kind == yaml.ScalarNode {
				add("enum", key, join(path, key))
			} else if _, ok := subDef["enum"]; ok && val.Kind == yaml.ScalarNode {
				add("enum", key, join(path, key))
			}
			sd.sites(val, sub, name, join(path, key), seen, out)
		}
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if key := r.(string); have[key] {
					add("required", key, join(path, key))
				}
			}
		}
	case yaml.SequenceNode:
		items := asObject(schema["items"])
		if items == nil {
			return
		}
		for i, c := range n.Content {
			at := fmt.Sprintf("%s[%d]", path, i)
			if _, ok := items["enum"]; ok && c.Kind == yaml.ScalarNode {
				key := path[strings.LastIndex(path, ".")+1:] + "[]"
				s := paritySite{kind: "enum", def: name, key: key, path: at}
				if !seen["enum "+s.id()] {
					seen["enum "+s.id()] = true
					*out = append(*out, s)
				}
			}
			sd.sites(c, items, name, at, seen, out)
		}
	}
}

// pathSegments splits "services[2].rds.engine" into names and indexes.
func pathSegments(path string) []any {
	var segs []any
	for part := range strings.SplitSeq(path, ".") {
		name, rest, _ := strings.Cut(part, "[")
		if name != "" {
			segs = append(segs, name)
		}
		for rest != "" {
			idx, after, _ := strings.Cut(rest, "]")
			i, _ := strconv.Atoi(idx)
			segs = append(segs, i)
			rest = strings.TrimPrefix(after, "[")
		}
	}
	return segs
}

// at is the node at path in root, and the mapping that holds it when it is a
// value there.
func at(root *yaml.Node, path string) (node, parent *yaml.Node, keyIndex int) {
	n := root.Content[0]
	parent, keyIndex = nil, -1
	for _, seg := range pathSegments(path) {
		switch seg := seg.(type) {
		case string:
			found := false
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == seg {
					parent, keyIndex, n, found = n, i, n.Content[i+1], true
					break
				}
			}
			if !found {
				return nil, nil, -1
			}
		case int:
			parent, keyIndex = nil, -1
			n = n.Content[seg]
		}
	}
	return n, parent, keyIndex
}

// paritySupplement is what the Help snippets do not use, so the keys in it
// are tested too.
const paritySupplement = `
secrets:
  vault:
    - name: parity vault
      path: kv/parity
      view: flat
`

// paritySource is a config that validates, built from the snippets of the
// Help topics, which between them use every section, and paritySupplement.
func paritySource(t *testing.T) []byte {
	t.Helper()
	merged := map[string]any{}
	snippets := append(helpSnippets(t), helpSnippet{"parity supplement", paritySupplement})
	for _, s := range snippets {
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(s.src), &doc); err != nil {
			t.Fatalf("snippet under %q: %v", s.heading, err)
		}
		mergeYAML(merged, doc)
	}
	data, err := yaml.Marshal(merged)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// reasons Check gave for a broken copy of the base: the lines of the path and
// of everything above it, which is where an entry's problem is blamed.
func parityLines(root *yaml.Node, path string) []int {
	var lines []int
	segs := pathSegments(path)
	prefix := ""
	for _, seg := range segs {
		switch seg := seg.(type) {
		case string:
			if prefix != "" {
				prefix += "."
			}
			prefix += seg
		case int:
			prefix += fmt.Sprintf("[%d]", seg)
		}
		if l := lineAt(root, prefix); l > 0 {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestCheckEnforcesTheSchemasEnumsAndRequiredKeys(t *testing.T) {
	sd := loadSchemaDoc(t)
	base := paritySource(t)

	var baseRoot yaml.Node
	if err := yaml.Unmarshal(base, &baseRoot); err != nil {
		t.Fatal(err)
	}
	var sites []paritySite
	sd.sites(baseRoot.Content[0], sd.root, "root", "", map[string]bool{}, &sites)
	if len(sites) < 10 {
		t.Fatalf("found only %d enum and required keys in the base config: %v", len(sites), sites)
	}

	exercised := map[string]bool{}
	for _, s := range sites {
		exercised[s.kind+" "+s.id()] = true
		t.Run(s.kind+" "+s.id(), func(t *testing.T) {
			var root yaml.Node
			if err := yaml.Unmarshal(base, &root); err != nil {
				t.Fatal(err)
			}
			n, parent, keyIndex := at(&root, s.path)
			if n == nil {
				t.Fatalf("no %s in the base config", s.path)
			}
			switch s.kind {
			case "enum":
				n.Value, n.Tag, n.Style = "not-a-value", "!!str", 0
			case "required":
				parent.Content = slices.Delete(parent.Content, keyIndex, keyIndex+2)
			}
			broken, err := yaml.Marshal(&root)
			if err != nil {
				t.Fatal(err)
			}
			_, ps := Check(broken)

			var again yaml.Node
			if err := yaml.Unmarshal(broken, &again); err != nil {
				t.Fatal(err)
			}
			lines := parityLines(&again, s.path)
			// A removed key has no line of its own: its entry's is the nearest.
			found := func(sev Severity) bool {
				for _, p := range ps {
					if p.Severity == sev && slices.Contains(lines, p.Line) {
						return true
					}
				}
				return false
			}
			switch reason, listed := parityNotEnforced[s.id()]; {
			case listed:
				if found(SeverityError) {
					t.Errorf("Check enforces %s now: remove it from parityNotEnforced (%s)", s.id(), reason)
				}
			case parityWarningOnly[s.id()] != "":
				if found(SeverityError) {
					t.Errorf("Check refuses %s now: remove it from parityWarningOnly", s.id())
				}
				if !found(SeverityWarning) {
					t.Errorf("%s: a wrong value should be a warning on one of lines %v, got:\n%v", s.id(), lines, ps)
				}
			default:
				if !found(SeverityError) {
					t.Errorf("%s (%s %s): the schema says so, but Check reports no error on one of lines %v:\n%v\nbroken config:\n%s",
						s.id(), s.kind, s.path, lines, ps, broken)
				}
			}
		})
	}

	// Every enum and required key of the schema is exercised, or listed as
	// one the base config cannot reach: a key added to a struct tag without a
	// snippet that uses it would otherwise go untested.
	var missing []string
	collect := func(name string, def map[string]any) {
		if req, ok := def["required"].([]any); ok {
			for _, r := range req {
				if id := name + "." + r.(string); !exercised["required "+id] {
					missing = append(missing, "required "+id)
				}
			}
		}
		for key, p := range asObject(def["properties"]) {
			if _, ok := asObject(p)["enum"]; ok && !exercised["enum "+name+"."+key] {
				missing = append(missing, "enum "+name+"."+key)
			}
			if items := asObject(asObject(p)["items"]); items != nil {
				if _, ok := items["enum"]; ok && !exercised["enum "+name+"."+key+"[]"] {
					missing = append(missing, "enum "+name+"."+key+"[]")
				}
			}
		}
	}
	collect("root", sd.root)
	for name, def := range sd.Defs {
		collect(name, def)
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Errorf("the base config (the Help snippets) never uses these, so they are not tested; add a snippet that does:\n  %s", strings.Join(missing, "\n  "))
	}
}
