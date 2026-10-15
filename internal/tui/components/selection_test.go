package components

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A selection reads as a terminal's does: within a row the cells between the
// two ends, whichever comes first; over several rows from the first end to
// the end of the area, whole rows, then up to the second end. Neither end
// leaves the area.
func TestSelectionSpans(t *testing.T) {
	area := Rect{X0: 2, Y0: 1, X1: 10, Y1: 6}
	for _, tc := range []struct {
		name           string
		ax, ay, bx, by int
		want           []Span
	}{
		{"one row forwards", 3, 2, 6, 2, []Span{{Y: 2, X0: 3, X1: 7}}},
		{"one row backwards", 6, 2, 3, 2, []Span{{Y: 2, X0: 3, X1: 7}}},
		{"one cell", 4, 2, 4, 2, []Span{{Y: 2, X0: 4, X1: 5}}},
		{"several rows", 5, 1, 4, 3, []Span{{Y: 1, X0: 5, X1: 10}, {Y: 2, X0: 2, X1: 10}, {Y: 3, X0: 2, X1: 5}}},
		{"several rows backwards", 4, 3, 5, 1, []Span{{Y: 1, X0: 5, X1: 10}, {Y: 2, X0: 2, X1: 10}, {Y: 3, X0: 2, X1: 5}}},
		{"head right of the area", 3, 2, 40, 2, []Span{{Y: 2, X0: 3, X1: 10}}},
		{"head below and left of it", 3, 4, 0, 20, []Span{{Y: 4, X0: 3, X1: 10}, {Y: 5, X0: 2, X1: 3}}},
		{"head above it", 3, 2, 5, 0, []Span{{Y: 1, X0: 5, X1: 10}, {Y: 2, X0: 2, X1: 4}}},
	} {
		if got := SelectionSpans(area, tc.ax, tc.ay, tc.bx, tc.by); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: spans = %v, want %v", tc.name, got, tc.want)
		}
	}
	if got := SelectionSpans(Rect{}, 1, 1, 2, 2); got != nil {
		t.Errorf("an empty area selected %v", got)
	}
}

func TestRect(t *testing.T) {
	r := Rect{X0: 2, Y0: 1, X1: 5, Y1: 3}
	if !r.Contains(2, 1) || !r.Contains(4, 2) || r.Contains(5, 2) || r.Contains(2, 3) || r.Contains(1, 1) {
		t.Fatal("Contains should take [X0, X1) of [Y0, Y1)")
	}
	if x, y := r.Clamp(-3, 9); x != 2 || y != 2 {
		t.Fatalf("Clamp(-3, 9) = %d, %d, want 2, 2", x, y)
	}
	if (Rect{}).Contains(0, 0) || !(Rect{}).Empty() || r.Empty() {
		t.Fatal("the zero Rect, and only it, should be empty")
	}
}

// What is copied is the text on screen: no colours, no padding at the end of
// a line or of the selection, and a wide character cut in half by an end is
// a space, so nothing past the selection comes with it.
func TestSelectedText(t *testing.T) {
	frame := strings.Join([]string{
		"menu │ \x1b[1;31morders-db\x1b[m   connected   ",
		"menu │ billing-db  idle        ",
		"menu │                         ",
		"menu │ a日b                    ",
	}, "\n")
	spans := SelectionSpans(Rect{X0: 7, Y0: 0, X1: 32, Y1: 4}, 7, 0, 20, 2)
	if got, want := SelectedText(frame, spans), "orders-db   connected\nbilling-db  idle"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	// 日 is the cells 8 and 9 of the last line.
	if got := SelectedText(frame, []Span{{Y: 3, X0: 9, X1: 11}}); got != " b" {
		t.Fatalf("from the second half of 日: %q, want \" b\"", got)
	}
	if got := SelectedText(frame, []Span{{Y: 3, X0: 7, X1: 9}}); got != "a" {
		t.Fatalf("up to the first half of 日: %q, want \"a\"", got)
	}
}

// The highlight swaps the colours of the cells selected and nothing else: no
// line changes width, and a coloured run the selection cuts keeps its colour
// after it.
func TestHighlight(t *testing.T) {
	frame := strings.Join([]string{
		"ab\x1b[31mcdefgh\x1b[m ij",
		"x日yz",
		"short",
	}, "\n")
	spans := []Span{{Y: 0, X0: 3, X1: 5}, {Y: 1, X0: 2, X1: 4}, {Y: 2, X0: 0, X1: 20}}
	out := Highlight(frame, spans)
	got, want := strings.Split(out, "\n"), strings.Split(frame, "\n")
	for i := range want {
		if ansi.StringWidth(got[i]) != ansi.StringWidth(want[i]) {
			t.Errorf("line %d is %d cells wide, was %d: %q", i, ansi.StringWidth(got[i]), ansi.StringWidth(want[i]), got[i])
		}
	}
	if plain := ansi.Strip(got[0]); plain != "abcdefgh ij" {
		t.Fatalf("the text changed: %q", plain)
	}
	if !strings.Contains(got[0], "\x1b[7mde\x1b[m") {
		t.Fatalf("de should be in reverse video: %q", got[0])
	}
	// The last style set before fgh is red again.
	before := got[0][:strings.Index(got[0], "fgh")]
	if last := before[strings.LastIndex(before, "\x1b["):]; last != "\x1b[31m" {
		t.Fatalf("fgh is drawn after %q, want red: %q", last, got[0])
	}
	if plain := ansi.Strip(got[1]); plain != "x  yz" {
		t.Fatalf("日 cut in half should leave a space for each half: %q", plain)
	}
	if Highlight(frame, nil) != frame {
		t.Fatal("no spans should leave the frame as it was")
	}
}
