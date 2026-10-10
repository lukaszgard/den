package main

import (
	"os"
	"testing"

	"github.com/lukaszgard/den/internal/leakcheck"
)

func TestMain(m *testing.M) {
	// den run and den mcp keep runbook history in the state directory; the
	// tests keep theirs out of the developer's.
	state, err := os.MkdirTemp("", "den-test-state-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_STATE_HOME", state); err != nil {
		panic(err)
	}
	code := leakcheck.Run(m)
	_ = os.RemoveAll(state)
	os.Exit(code)
}
