package process

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// KeepColours drops from line every escape sequence but colour and style
// (SGR, ESC [ … m) and every control character but tab. den shows a process's
// output inside its own screen: a cursor move or an erase there would draw
// over the pane, and an OSC would set den's window title or write the
// clipboard (OSC 52). Colour only paints the text it wraps.
func KeepColours(line string) string {
	if !strings.ContainsFunc(line, isControl) {
		return line
	}
	var b strings.Builder
	b.Grow(len(line))
	var state byte
	for rest := line; rest != ""; {
		seq, _, n, newState := ansi.DecodeSequence(rest, state, nil)
		if n <= 0 {
			break
		}
		state = newState
		rest = rest[n:]
		switch {
		case isSGR(seq):
			b.WriteString(seq)
		case strings.HasPrefix(seq, "\x1b"):
			// Any other sequence: dropped whole, its parameters with it.
		default:
			b.WriteString(strings.Map(func(r rune) rune {
				if isControl(r) {
					return -1
				}
				return r
			}, seq))
		}
	}
	return b.String()
}

// isControl reports the characters that act on a terminal rather than show:
// C0, DEL and C1. Tab only moves to the next column.
func isControl(r rune) bool {
	return r != '\t' && unicode.IsControl(r)
}

// isSGR reports whether seq is ESC [ params m, where params are digits and
// the separators of SGR: ';' between attributes, ':' inside one (38:2::r:g:b).
func isSGR(seq string) bool {
	if len(seq) < 3 || !strings.HasPrefix(seq, "\x1b[") || seq[len(seq)-1] != 'm' {
		return false
	}
	for _, c := range []byte(seq[2 : len(seq)-1]) {
		if (c < '0' || c > '9') && c != ';' && c != ':' {
			return false
		}
	}
	return true
}
