package runbooks

import "testing"

// SetDark reaches the prompts the pane opens later and the one it has open.
func TestSetDarkReachesPrompts(t *testing.T) {
	m := Model{}.SetDark(false)
	if !m.light || !m.flow.light || !m.src.light {
		t.Fatalf("light = %v flow %v sources %v, want all set", m.light, m.flow.light, m.src.light)
	}
	m.naming = &newForm{}
	m = m.SetDark(true)
	if m.light || m.flow.light || m.src.light || m.naming.light {
		t.Fatal("a dark background should clear them all")
	}
}
