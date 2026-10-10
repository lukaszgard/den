package leakcheck

import (
	"strings"
	"testing"
	"time"
)

// A check that never finds anything would pass every package, so this leaks
// a goroutine on purpose — which is also why this package has no TestMain.
func TestLeaksReportsABlockedGoroutine(t *testing.T) {
	func() {
		never := make(chan struct{})
		go func() { <-never }() // unreachable once this function returns
	}()
	time.Sleep(20 * time.Millisecond) // let it block

	report := Leaks()
	if !strings.Contains(report, "TestLeaksReportsABlockedGoroutine") {
		t.Fatalf("the blocked goroutine is not in the report:\n%s", report)
	}
}
