package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The rule: every line that names keys is built with components.Hints or
// components.HintLine, so they all read "key: action" with the key in blue.
// den once had three styles at once — "c: connect", "h help", "[y] copy" —
// because each screen wrote its own. This test reads the TUI's source and
// fails on a string that looks like a hint written by hand.

// hintKeyRe is what a key looks like in a hint.
const hintKeyRe = `(?:[a-zA-Z0-9/?]|esc|enter|tab|space|pgup|pgdn|home|end|[←→↑↓]+|ctrl\+\w|shift\+\w|alt\+\w|\d-\d|→/enter|←/esc)`

var (
	// "↑↓ navigate   c: connect": two or more key/action pairs.
	hintPair = regexp.MustCompile(`(?:^|\s{2,})` + hintKeyRe + `:? [a-z]`)
	// "esc: clear": a string that starts with a hint.
	hintStart = regexp.MustCompile(`^\s*` + hintKeyRe + `: \S`)
	// "[y] copy", "[r]efresh".
	hintBracket = regexp.MustCompile(`\[[a-zA-Z]\]\s?[a-z]|\[[a-zA-Z]\][a-z]`)
	// "press → or enter to focus".
	hintPress = regexp.MustCompile(`^\s*press \S+ (?:or \S+ )?to `)
)

func looksLikeHint(s string) bool {
	return len(hintPair.FindAllStringIndex(s, -1)) >= 2 ||
		hintStart.MatchString(s) || hintBracket.MatchString(s) || hintPress.MatchString(s)
}

func TestLooksLikeHint(t *testing.T) {
	for s, want := range map[string]bool{
		"↑↓ navigate   c: connect   d: disconnect":     true,
		"enter open   r run   x cancel":                true,
		"esc: clear":                                   true,
		"[y] yes   [n] no":                             true,
		"[r]efresh":                                    true,
		"press → or enter to focus":                    true,
		"esc ctrl+t":                                   false, // a shortcut's keys
		"Previous / next tab (or tab, shift+tab)":      false,
		"No runbooks match ":                           false,
		"   ↑ %d more":                                 false,
		"%d terminals is the limit — close one with x": false,
		"cancelling %s…":                               false,
	} {
		if got := looksLikeHint(s); got != want {
			t.Errorf("looksLikeHint(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestEveryHintIsBuiltByTheHintsComponent(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			path == filepath.Join("components", "hints.go") {
			return err
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err == nil && looksLikeHint(s) {
				t.Errorf("%s: %q looks like a hint written by hand: build it with components.Hints or components.HintLine", fset.Position(lit.Pos()), s)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
