package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/lukaszgard/den"
)

// Severity says whether a problem keeps den from using a config.
type Severity int

const (
	// SeverityError is a mistake den cannot run with: the file is not used.
	SeverityError Severity = iota
	// SeverityWarning is something den ignores, and runs without.
	SeverityWarning
)

func (s Severity) String() string {
	if s == SeverityError {
		return "error"
	}
	return "warning"
}

// Problem is one mistake in den.yaml.
type Problem struct {
	Severity Severity
	// Line is where in the file, from 1; 0 when no one line is to blame.
	Line int
	// Key is the YAML path the problem is about ("services[2].local_port");
	// empty when no one key is to blame.
	Key string
	// Code names the kind of problem, stable and kebab-case ("unknown-key",
	// "port-shared"): --json and tests rely on it, the message may be reworded.
	Code string
	Msg  string
	// Hint says how to fix it, in one sentence; empty when the message does.
	Hint string
}

func (p Problem) String() string {
	if p.Line > 0 {
		return fmt.Sprintf("line %d: %s", p.Line, p.Msg)
	}
	return p.Msg
}

// Lines is the problem as it is printed: the message, then its hint after an
// arrow.
func (p Problem) Lines() []string {
	if p.Hint == "" {
		return []string{p.Msg}
	}
	return []string{p.Msg, "→ " + p.Hint}
}

// Problems are what Check found, errors first, each kind in file order.
type Problems []Problem

// Errors is the number of problems den cannot run with.
func (ps Problems) Errors() int {
	n := 0
	for _, p := range ps {
		if p.Severity == SeverityError {
			n++
		}
	}
	return n
}

// Warnings is the number of problems den runs with.
func (ps Problems) Warnings() int { return len(ps) - ps.Errors() }

// Summary counts the problems: "2 errors, 1 warning".
func (ps Problems) Summary() string {
	var parts []string
	if n := ps.Errors(); n > 0 {
		parts = append(parts, count(n, "error", "errors"))
	}
	if n := ps.Warnings(); n > 0 {
		parts = append(parts, count(n, "warning", "warnings"))
	}
	return strings.Join(parts, ", ")
}

// Err is the errors as one error, a "path:line: message" line each, the form
// editors and terminals jump to; nil without errors.
func (ps Problems) Err(path string) error {
	if ps.Errors() == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("invalid config:")
	for _, p := range ps {
		if p.Severity != SeverityError {
			continue
		}
		b.WriteString("\n  " + p.Located(path))
	}
	return errors.New(b.String())
}

// Located is the problem as "path:line: message".
func (p Problem) Located(path string) string {
	if p.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", path, p.Line, p.Msg)
	}
	return fmt.Sprintf("%s: %s", path, p.Msg)
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// PaneNames are the panels menu_order and disabled_panes can name, in the
// menu's default order.
var PaneNames = []string{"connect", "tunnel", "runbooks", "vpn", "aws", "secrets", "create-env", "config", "logs", "help", "updates"}

// logLevels are the logs.level values the logger knows, lower case.
var logLevels = []string{"trace", "debug", "info", "warn", "warning", "error"}

// LoadChecked reads path and checks it. err is only for a file that cannot be
// read; a file den cannot use comes back as a nil config and its problems.
func LoadChecked(path string) (*Config, Problems, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg, ps := Check(data)
	if cfg != nil {
		base := configDir(path)
		cfg.setBaseDir(base)
		ps = sorted(append(ps, Inspect(cfg, rootNode(data), base)...))
	}
	return cfg, ps, nil
}

// Check parses and validates the contents of a den.yaml and lists every
// problem with its line, rather than the first: someone fixing the file sees
// them all at once. The config is nil when any problem is an error.
//
// Keys den does not know are warnings, not errors: den has always ignored
// them, and a config that ran yesterday must still load. A misspelt key is
// still the commonest mistake that does nothing at all, so each one is named,
// with the key it most likely meant.
func Check(data []byte) (*Config, Problems) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, Problems{yamlProblem(err)}
	}
	ps := unknownKeys(data)
	cfg, err := parse(data)
	if err != nil {
		ps = append(ps, decodeProblems(err, &root)...)
		return nil, sorted(ps)
	}
	for _, err := range unjoin(Validate(cfg)) {
		key := headKey(err.Error())
		ps = append(ps, Problem{Severity: SeverityError, Line: lineAt(&root, key), Key: key, Code: "invalid", Msg: err.Error()})
	}
	ps = append(ps, settingWarnings(cfg, &root)...)
	ps = sorted(ps)
	if ps.Errors() > 0 {
		return nil, ps
	}
	return cfg, ps
}

// rootNode is data as a YAML tree, for the line of a key; nil when it is not YAML.
func rootNode(data []byte) *yaml.Node {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil
	}
	return &root
}

func sorted(ps Problems) Problems {
	slices.SortStableFunc(ps, func(a, b Problem) int {
		if a.Severity != b.Severity {
			return int(a.Severity) - int(b.Severity)
		}
		// A problem with no line goes after those with one.
		la, lb := a.Line, b.Line
		if la == 0 {
			la = 1 << 30
		}
		if lb == 0 {
			lb = 1 << 30
		}
		return la - lb
	})
	return ps
}

// unjoin splits what errors.Join put together.
func unjoin(err error) []error {
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	return []error{err}
}

var yamlLineRe = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

// yamlProblem is a syntax error: the file is not YAML at all.
func yamlProblem(err error) Problem {
	msg := err.Error()
	if m := yamlLineRe.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		return Problem{Severity: SeverityError, Line: line, Code: "yaml-syntax", Msg: "not valid YAML: " + m[2]}
	}
	return Problem{Severity: SeverityError, Code: "yaml-syntax", Msg: "not valid YAML: " + strings.TrimPrefix(msg, "yaml: ")}
}

var typeErrorRe = regexp.MustCompile(`^line (\d+): (.*)$`)

// decodeProblems are what parse returned: values of the wrong kind, keys given
// twice, or a service naming an env that is not there.
func decodeProblems(err error, root *yaml.Node) Problems {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		msg := strings.TrimPrefix(err.Error(), "resolve config: ")
		key := headKey(msg)
		return Problems{{Severity: SeverityError, Line: lineAt(root, key), Key: key, Code: "invalid", Msg: msg}}
	}
	var ps Problems
	for _, e := range te.Errors {
		p := Problem{Severity: SeverityError, Code: "type", Msg: e}
		if m := typeErrorRe.FindStringSubmatch(e); m != nil {
			p.Line, _ = strconv.Atoi(m[1])
			p.Msg = m[2]
		}
		if unknownFieldRe.MatchString(e) {
			continue // a strict decode reports these, as warnings
		}
		p.Msg = plainTypeError(p.Msg)
		ps = append(ps, p)
	}
	return ps
}

var cannotUnmarshalRe = regexp.MustCompile("^cannot unmarshal !!(\\w+) (`.*` )?into (.+)$")

// plainTypeError says yaml's "cannot unmarshal !!str `abc` into int" in the
// words of someone writing YAML rather than Go.
func plainTypeError(msg string) string {
	m := cannotUnmarshalRe.FindStringSubmatch(msg)
	if m == nil {
		return msg
	}
	got := map[string]string{"seq": "a list", "map": "a mapping", "bool": "true/false", "null": "nothing"}[m[1]]
	if got == "" {
		got = strings.TrimSpace(m[2])
	}
	want := m[3]
	switch {
	case strings.HasPrefix(want, "[]"):
		want = "a list"
	case strings.HasPrefix(want, "map["), strings.Contains(want, "."):
		want = "a mapping (key: value)"
	case strings.HasPrefix(want, "int"), strings.HasPrefix(want, "uint"), strings.HasPrefix(want, "float"):
		want = "a number"
	case want == "bool":
		want = "true or false"
	case want == "string":
		want = "a single value"
	}
	if got == "" {
		return "expected " + want
	}
	return fmt.Sprintf("expected %s, got %s", want, got)
}

var unknownFieldRe = regexp.MustCompile(`^line (\d+): field (.+) not found in type (\S+)$`)

// unknownKeys decodes data strictly and names every key no config struct
// has, as a warning. Keys an inline map catches (secrets: providers) are not
// among them: Validate names those.
func unknownKeys(data []byte) Problems {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var cfg Config
	var te *yaml.TypeError
	if err := dec.Decode(&cfg); !errors.As(err, &te) {
		return nil
	}
	var ps Problems
	for _, e := range te.Errors {
		m := unknownFieldRe.FindStringSubmatch(e)
		if m == nil {
			continue
		}
		line, _ := strconv.Atoi(m[1])
		p := Problem{Severity: SeverityWarning, Line: line, Code: "unknown-key", Msg: fmt.Sprintf("unknown key %q, ignored", m[2])}
		if s := suggest(m[2], schemaKeys()[strings.TrimPrefix(strings.TrimPrefix(m[3], "*"), "config.")]); s != "" {
			p.Hint = fmt.Sprintf("did you mean %q?", s)
		}
		ps = append(ps, p)
	}
	return ps
}

// schemaKeys are the keys of each config struct, by type name, read from the
// schema den ships: it is generated from these structs, inline sections
// included, so it lists what a strict decode would accept.
var schemaKeys = sync.OnceValue(func() map[string][]string {
	type object struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	var s struct {
		object
		Defs map[string]object `json:"$defs"`
	}
	if err := json.Unmarshal(den.Schema, &s); err != nil {
		return nil
	}
	keys := map[string][]string{"Config": slices.Sorted(maps.Keys(s.Properties))}
	for name, def := range s.Defs {
		keys[name] = slices.Sorted(maps.Keys(def.Properties))
	}
	return keys
})

// suggest is the candidate closest to key, if one is close enough to be the
// key someone meant to type.
func suggest(key string, candidates []string) string {
	best, bestDist := "", max(2, len(key)/3)+1
	for _, c := range candidates {
		if d := editDistance(strings.ToLower(key), c); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// settingWarnings are values den ignores without failing: panel names it does
// not have, and a log level it does not know.
func settingWarnings(cfg *Config, root *yaml.Node) Problems {
	var ps Problems
	for _, key := range []string{"menu_order", "disabled_panes"} {
		names := cfg.MenuOrder
		if key == "disabled_panes" {
			names = cfg.DisabledPanes
		}
		for i, name := range names {
			if slices.Contains(PaneNames, name) {
				continue
			}
			at := fmt.Sprintf("%s[%d]", key, i)
			p := Problem{
				Severity: SeverityWarning, Line: lineAt(root, at), Key: at, Code: "unknown-panel",
				Msg: fmt.Sprintf("%s: unknown panel %q, ignored", key, name),
			}
			if s := suggest(name, PaneNames); s != "" {
				p.Hint = fmt.Sprintf("did you mean %q?", s)
			} else {
				p.Hint = "panels: " + strings.Join(PaneNames, ", ")
			}
			ps = append(ps, p)
		}
	}
	if l := cfg.Logs; l != nil && l.Level != "" && !slices.Contains(logLevels, strings.ToLower(l.Level)) {
		ps = append(ps, Problem{
			Severity: SeverityWarning, Line: lineAt(root, "logs.level"), Key: "logs.level", Code: "log-level",
			Msg: fmt.Sprintf("logs.level: %q is not a level (trace, debug, info, warn or error), info is used", l.Level),
		})
	}
	return ps
}

// pathHeadRe is the path a validation message starts with: service[0],
// environments.eu-dev, secrets.sops[1], runbooks.agent.
var pathHeadRe = regexp.MustCompile(`^([a-z_]+(?:\[\d+\])?(?:\.[A-Za-z0-9_-]+(?:\[\d+\])?)*)[: ]`)

// pathAliases are the singular names Validate's messages use for lists.
var pathAliases = map[string]string{
	"service":  "services",
	"tunnel":   "tunnels",
	"runbook":  "runbooks.items",
	"sequence": "runbooks.sequences",
}

var segmentRe = regexp.MustCompile(`^(.+?)(?:\[(\d+)\])?$`)

// headKey is the path a validation message starts with, written as the file
// has it ("services[0]" for "service[0] ..."); empty when it starts with none.
func headKey(msg string) string {
	m := pathHeadRe.FindStringSubmatch(msg)
	if m == nil {
		return ""
	}
	segments := strings.Split(m[1], ".")
	if alias, ok := pathAliases[strings.SplitN(segments[0], "[", 2)[0]]; ok {
		first := strings.Split(alias, ".")
		if i := strings.IndexByte(segments[0], '['); i >= 0 {
			first[len(first)-1] += segments[0][i:]
		}
		segments = append(first, segments[1:]...)
	}
	return strings.Join(segments, ".")
}

// lineAt is the line of the value at key ("services[2].local_port"); 0 when
// key is empty. A key that goes deeper than the file gives the line of the
// deepest part that is there.
func lineAt(root *yaml.Node, key string) int {
	line, _ := locate(root, key)
	return line
}

// hasKey reports whether the file writes the value at key.
func hasKey(root *yaml.Node, key string) bool {
	_, full := locate(root, key)
	return full
}

// firstKey is the first of keys the file writes, or the last when it writes
// none: a value that may come from the entry itself or from the environment it
// names is blamed where it was set.
func firstKey(root *yaml.Node, keys ...string) string {
	for _, k := range keys {
		if hasKey(root, k) {
			return k
		}
	}
	return keys[len(keys)-1]
}

// locate walks key down the file: the line of the deepest part found, and
// whether all of key was.
func locate(root *yaml.Node, key string) (line int, full bool) {
	if key == "" || root == nil || len(root.Content) == 0 {
		return 0, false
	}
	n := root.Content[0]
	for _, seg := range strings.Split(key, ".") {
		sm := segmentRe.FindStringSubmatch(seg)
		if n.Kind != yaml.MappingNode {
			return line, false
		}
		var value *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == sm[1] {
				line, value = n.Content[i].Line, n.Content[i+1]
				break
			}
		}
		if value == nil {
			return line, false
		}
		n = value
		if sm[2] == "" {
			continue
		}
		idx, _ := strconv.Atoi(sm[2])
		if n.Kind != yaml.SequenceNode || idx >= len(n.Content) {
			return line, false
		}
		n = n.Content[idx]
		line = n.Line
	}
	return line, true
}
