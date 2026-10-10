package process

import "testing"

func TestKeepColours(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"plain":            {"plain text\twith a tab", "plain text\twith a tab"},
		"colour":           {"\x1b[32m✔\x1b[0m ok", "\x1b[32m✔\x1b[0m ok"},
		"bold and reset":   {"\x1b[1;31mfail\x1b[m", "\x1b[1;31mfail\x1b[m"},
		"truecolour":       {"\x1b[38;2;10;20;30mx\x1b[39m", "\x1b[38;2;10;20;30mx\x1b[39m"},
		"colon form":       {"\x1b[38:2::10:20:30mx\x1b[0m", "\x1b[38:2::10:20:30mx\x1b[0m"},
		"erase line":       {"\x1b[2Kcleared", "cleared"},
		"cursor up":        {"a\x1b[1Ab", "ab"},
		"cursor position":  {"\x1b[10;5Hx", "x"},
		"title (OSC, BEL)": {"\x1b]0;owned\x07text", "text"},
		"clipboard (ST)":   {"\x1b]52;c;aGk=\x1b\\text", "text"},
		"bell and BS":      {"a\x07b\x08c", "abc"},
		"private mode":     {"\x1b[?25lhidden", "hidden"},
		"colour inside":    {"\x1b[2K\x1b[31mred\x1b[0m\x1b[1A", "\x1b[31mred\x1b[0m"},
		"two-byte escape":  {"a\x1bcb", "ab"},
		"empty":            {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := KeepColours(tc.in); got != tc.want {
				t.Errorf("KeepColours(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
