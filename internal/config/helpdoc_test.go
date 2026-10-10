package config

import (
	"bytes"
	"io/fs"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	den "github.com/lukaszgard/den"

	"gopkg.in/yaml.v3"
)

// The Help pane's topics (docs/help) are where people copy den.yaml snippets
// from, so a snippet with a misspelt or renamed key would be copied into real
// configs — where den ignores it, because loading is not strict. These tests
// hold the snippets to the config structs, and fail when a new section,
// service type, transport or secrets provider has no snippet yet.

// helpSnippet is one ```yaml block of a help topic and where it is: the file
// and the heading above it, which is how a failure names it.
type helpSnippet struct {
	heading, src string
}

func helpSnippets(t *testing.T) []helpSnippet {
	t.Helper()
	docs := den.HelpDocs()
	files, err := fs.Glob(docs, "*.md")
	if err != nil {
		t.Fatal(err)
	}
	var out []helpSnippet
	for _, file := range files {
		src, err := fs.ReadFile(docs, file)
		if err != nil {
			t.Fatal(err)
		}
		var (
			heading string
			block   *strings.Builder
		)
		for line := range strings.SplitSeq(string(src), "\n") {
			switch {
			case block != nil && strings.HasPrefix(line, "```"):
				out = append(out, helpSnippet{file + " › " + heading, block.String()})
				block = nil
			case block != nil:
				block.WriteString(line + "\n")
			case strings.HasPrefix(line, "```yaml"):
				block = &strings.Builder{}
			case strings.HasPrefix(line, "#"):
				heading = strings.TrimSpace(strings.TrimLeft(line, "#"))
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("docs/help has no yaml snippets")
	}
	return out
}

func TestHelpSnippetsUseRealKeys(t *testing.T) {
	for _, s := range helpSnippets(t) {
		dec := yaml.NewDecoder(bytes.NewReader([]byte(s.src)))
		dec.KnownFields(true)
		var cfg Config
		if err := dec.Decode(&cfg); err != nil {
			t.Errorf("snippet under %q: %v", s.heading, err)
			continue
		}
		// secrets: catches unknown groups inline, so strict decoding lets a
		// misspelt provider through.
		if cfg.Secrets != nil && len(cfg.Secrets.Other) > 0 {
			t.Errorf("snippet under %q: unknown secrets provider %v", s.heading, slices.Sorted(maps.Keys(cfg.Secrets.Other)))
		}
	}
}

// Put together, the snippets are one config that loads and validates: every
// env: a service names is defined, a tunnel's auto_connect names a VPN, and so on.
func TestHelpSnippetsMakeAValidConfig(t *testing.T) {
	merged := map[string]any{}
	for _, s := range helpSnippets(t) {
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
	cfg, err := parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("the help snippets together do not validate: %v", err)
	}
}

func TestHelpCoversEveryConfigSection(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range helpSnippets(t) {
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(s.src), &doc); err != nil {
			t.Fatalf("snippet under %q: %v", s.heading, err)
		}
		for key, val := range doc {
			seen[key] = true
			if key == "secrets" {
				for provider := range asMap(val) {
					seen["secrets."+provider] = true
				}
			}
		}
		for _, svc := range asList(doc["services"]) {
			seen["service type "+asString(asMap(svc)["type"])] = true
		}
		walkYAML(doc, func(key string, val any) {
			if key == "transport" {
				seen["transport "+asString(asMap(val)["type"])] = true
			}
		})
	}

	var want []string
	for _, tag := range yamlKeys(reflect.TypeFor[Config]()) {
		if tag != "version" {
			want = append(want, tag)
		}
	}
	for _, tag := range yamlKeys(reflect.TypeFor[SecretsConfig]()) {
		if tag != "view" {
			want = append(want, "secrets."+tag)
		}
	}
	for _, typ := range schemaEnum(t, reflect.TypeFor[ServiceSpec](), "Type") {
		// Kafka is planned: connecting one only says it is not implemented,
		// so the help does not offer it.
		if typ != string(ServiceTypeKafka) {
			want = append(want, "service type "+typ)
		}
	}
	for _, typ := range schemaEnum(t, reflect.TypeFor[TransportSpec](), "Type") {
		want = append(want, "transport "+typ)
	}

	for _, w := range want {
		if !seen[w] {
			t.Errorf("no topic in docs/help has a snippet for %s", w)
		}
	}
}

// mergeYAML folds src into dst: maps merge key by key and lists append, so
// snippets that each show one services: entry add up to one list.
func mergeYAML(dst, src map[string]any) {
	for k, v := range src {
		switch sv := v.(type) {
		case map[string]any:
			if dv, ok := dst[k].(map[string]any); ok {
				mergeYAML(dv, sv)
				continue
			}
		case []any:
			if dv, ok := dst[k].([]any); ok {
				dst[k] = append(dv, sv...)
				continue
			}
		}
		dst[k] = v
	}
}

// walkYAML calls fn for every key of every map in v, at any depth.
func walkYAML(v any, fn func(key string, val any)) {
	switch v := v.(type) {
	case map[string]any:
		for k, val := range v {
			fn(k, val)
			walkYAML(val, fn)
		}
	case []any:
		for _, val := range v {
			walkYAML(val, fn)
		}
	}
}

// yamlKeys lists the yaml keys of a struct's fields, skipping inline ones.
func yamlKeys(typ reflect.Type) []string {
	var keys []string
	for f := range typ.Fields() {
		name, opts, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" || strings.Contains(opts, "inline") {
			continue
		}
		keys = append(keys, name)
	}
	return keys
}

// schemaEnum reads the enum= values of a field's jsonschema tag: the list of
// types den.schema.json offers, kept next to the field.
func schemaEnum(t *testing.T, typ reflect.Type, field string) []string {
	t.Helper()
	f, ok := typ.FieldByName(field)
	if !ok {
		t.Fatalf("%s has no field %s", typ, field)
	}
	var values []string
	for part := range strings.SplitSeq(f.Tag.Get("jsonschema"), ",") {
		if v, ok := strings.CutPrefix(part, "enum="); ok {
			values = append(values, v)
		}
	}
	if len(values) == 0 {
		t.Fatalf("%s.%s has no jsonschema enum", typ, field)
	}
	return values
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func asList(v any) []any         { l, _ := v.([]any); return l }
func asString(v any) string      { s, _ := v.(string); return s }
