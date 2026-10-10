package applogs

import (
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/tui/applog"
	"github.com/lukaszgard/den/internal/tui/msgs"
)

// BenchmarkRefreshFull is a refresh of a full panel, den's lines and a run's,
// as every tick pays it while a run prints.
func BenchmarkRefreshFull(b *testing.B) {
	l := applog.New()
	for range 1000 {
		l.Warn("den.yaml: line 2: unknown key \"bogus_key\", ignored")
		l.Output("deploy", "\x1b[32mok\x1b[0m migrations applied to the orders database")
	}
	m := New(l, 120, 40)
	for b.Loop() {
		l.Info("x")
		m, _ = m.Update(msgs.TickMsg{Time: time.Now()}, true)
	}
}
