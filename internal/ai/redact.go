// Package ai asks Claude to explain den's diagnostics. It is opt-in and never
// automatic: the caller shows the user exactly what would be sent, redacted,
// and sends it only when they agree.
package ai

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Redactor replaces identifying values — names from den.yaml, host names,
// account and instance IDs, addresses, e-mail addresses, ARNs, home paths —
// with stable placeholders such as host-1, and can put them back into text
// that uses the placeholders. The explanation then refers to the user's real
// names on screen while none of them left the machine.
type Redactor struct {
	terms        []term            // explicit values, longest first
	placeholders map[string]string // original → placeholder
	originals    map[string]string // placeholder → original
	counts       map[string]int    // kind → last number used
}

type term struct{ value, kind string }

// NewRedactor returns a redactor for the given sensitive values, by kind
// (e.g. "profile" → the AWS profile names). Empty values are ignored.
func NewRedactor(sensitive map[string][]string) *Redactor {
	r := &Redactor{placeholders: map[string]string{}, originals: map[string]string{}, counts: map[string]int{}}
	seen := map[string]bool{}
	for kind, values := range sensitive {
		for _, v := range values {
			if len(strings.TrimSpace(v)) < 2 || seen[v] {
				continue
			}
			seen[v] = true
			r.terms = append(r.terms, term{value: v, kind: kind})
		}
	}
	// Longest first, so "orders-prod" is replaced before "orders".
	sort.Slice(r.terms, func(i, j int) bool {
		if len(r.terms[i].value) != len(r.terms[j].value) {
			return len(r.terms[i].value) > len(r.terms[j].value)
		}
		return r.terms[i].value < r.terms[j].value
	})
	return r
}

var patterns = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"arn", regexp.MustCompile(`arn:aws[a-z-]*:[^\s"',;)]+`)},
	{"email", regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)},
	{"path", regexp.MustCompile(`(?:/Users|/home)/[^/\s"']+`)},
	{"instance", regexp.MustCompile(`\b(?:i|mi|eice)-[0-9a-f]{8,17}\b`)},
	{"account", regexp.MustCompile(`\b\d{12}\b`)},
	{"ip", regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)},
	// Host names: at least three labels ending in an alphabetic TLD, so
	// version numbers (2.27.28) and file names (den.yaml) are left alone.
	{"host", regexp.MustCompile(`\b(?:[A-Za-z0-9-]+\.){2,}[A-Za-z]{2,}\b`)},
}

// publicHosts are documentation sites den itself links to; they identify
// nothing about the user.
var publicHosts = []string{"docs.aws.amazon.com", "modelcontextprotocol.io", "json-schema.org"}

// Redact returns s with every sensitive value replaced by its placeholder.
func (r *Redactor) Redact(s string) string {
	for _, t := range r.terms {
		if strings.Contains(s, t.value) {
			s = strings.ReplaceAll(s, t.value, r.placeholder(t.kind, t.value))
		}
	}
	for _, p := range patterns {
		s = p.re.ReplaceAllStringFunc(s, func(match string) string {
			if p.kind == "host" && (isPlaceholder(match) || isPublic(match)) {
				return match
			}
			if p.kind == "path" {
				return "~"
			}
			return r.placeholder(p.kind, match)
		})
	}
	return s
}

// Restore puts the original values back into text written with placeholders.
func (r *Redactor) Restore(s string) string {
	keys := make([]string, 0, len(r.originals))
	for ph := range r.originals {
		keys = append(keys, ph)
	}
	// Longest first, so host-12 is not read as host-1 followed by "2".
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, ph := range keys {
		s = strings.ReplaceAll(s, ph, r.originals[ph])
	}
	return s
}

func (r *Redactor) placeholder(kind, original string) string {
	if ph, ok := r.placeholders[original]; ok {
		return ph
	}
	r.counts[kind]++
	ph := fmt.Sprintf("%s-%d", kind, r.counts[kind])
	r.placeholders[original] = ph
	r.originals[ph] = original
	return ph
}

var placeholderRe = regexp.MustCompile(`^[a-z]+-\d+$`)

func isPlaceholder(s string) bool { return placeholderRe.MatchString(s) }

func isPublic(host string) bool {
	for _, h := range publicHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}
