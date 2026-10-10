package process

import (
	"reflect"
	"testing"
)

func TestRingBufferKeepsTheNewestLinesInOrder(t *testing.T) {
	r := NewRingBuffer(3)
	if r.Lines() != nil || r.LastN(2) != nil || r.Seq() != 0 {
		t.Fatal("an empty buffer has no lines and a zero Seq")
	}
	for _, l := range []string{"a", "b"} {
		r.Add(l)
	}
	if got := r.Lines(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("Lines = %q", got)
	}
	for _, l := range []string{"c", "d", "e"} {
		r.Add(l)
	}
	if got := r.Lines(); !reflect.DeepEqual(got, []string{"c", "d", "e"}) {
		t.Fatalf("after wrapping, Lines = %q", got)
	}
	if got := r.LastN(2); !reflect.DeepEqual(got, []string{"d", "e"}) {
		t.Fatalf("LastN(2) = %q", got)
	}
	if got := r.LastN(10); len(got) != 3 {
		t.Fatalf("LastN beyond the size = %q", got)
	}
	// Seq keeps counting once the length stops changing: that is what lets a
	// reader notice new lines in a full buffer.
	if r.Seq() != 5 {
		t.Fatalf("Seq = %d, want 5", r.Seq())
	}
}

func TestRingBufferResetEmptiesItAndMovesSeq(t *testing.T) {
	r := NewRingBuffer(2)
	for _, l := range []string{"a", "b", "c"} {
		r.Add(l)
	}
	before := r.Seq()
	r.Reset()
	if r.Lines() != nil || r.LastN(1) != nil {
		t.Fatalf("after Reset, Lines = %q", r.Lines())
	}
	// A reader that skips work while Seq stands still must see the buffer empty.
	if r.Seq() == before {
		t.Fatal("Reset left Seq unchanged")
	}
	r.Add("d")
	if got := r.Lines(); !reflect.DeepEqual(got, []string{"d"}) {
		t.Fatalf("after Reset and Add, Lines = %q", got)
	}
}
