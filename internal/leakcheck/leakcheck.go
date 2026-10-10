// Package leakcheck fails a package's tests when they leave goroutines behind
// that can never run again.
//
// den runs a goroutine per service to copy its events, one to wait for the
// next event, one per terminal tab to wait for its output, and more inside the
// tunnels. One of them blocked for good is invisible until den has piled up
// enough of them to matter. Go's goroutineleak profile finds them after the
// fact: goroutines blocked on a channel, mutex or condition that nothing still
// running can reach, so nothing can ever wake them.
package leakcheck

import (
	"bytes"
	"fmt"
	"os"
	"runtime/pprof"
	"testing"
)

// Main runs the package's tests and then fails the test binary if they leaked
// goroutines, printing where each one is stuck. Use it as the TestMain:
//
//	func TestMain(m *testing.M) { leakcheck.Main(m) }
func Main(m *testing.M) {
	os.Exit(Run(m))
}

// Run is Main for a TestMain with cleanup of its own to do before it exits:
// it returns the exit code instead of exiting.
func Run(m *testing.M) int {
	code := m.Run()
	if code == 0 {
		if report := Leaks(); report != "" {
			fmt.Fprintf(os.Stderr, "FAIL: these goroutines are blocked for good once the tests end\n\n%s\n", report)
			code = 1
		}
	}
	return code
}

// Leaks returns the goroutineleak profile when it lists any goroutine, with
// the stack each one is stuck in, and "" when there are none.
func Leaks() string {
	p := pprof.Lookup("goroutineleak")
	if p == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := p.WriteTo(&buf, 1); err != nil {
		return "goroutineleak profile: " + err.Error()
	}
	if p.Count() == 0 {
		return ""
	}
	return buf.String()
}
