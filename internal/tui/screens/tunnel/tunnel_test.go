package tunnel

import (
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
	tunnelsvc "github.com/lukaszgard/den/internal/service/tunnel"
)

// The pane keeps a states map that only refreshes on a ServiceEventMsg. A stopped
// tunnel has nobody draining its update channel, so a toggle pressed while it is
// down never produces one — which is exactly how the indicator used to go stale.
// These tests render the view with no message delivered at all: the dots have to
// come from the service itself.

const (
	dotOn  = "●"
	dotOff = "○"
)

func newPane(spec config.TunnelSpec) (Model, *tunnelsvc.Service) {
	svc := tunnelsvc.New(spec)
	return New([]service.Service{svc}, 120, 40), svc
}

// row returns the rendered line containing label, or "" when there is none.
func row(view, label string) string {
	for line := range strings.SplitSeq(view, "\n") {
		if strings.Contains(line, label) {
			return line
		}
	}
	return ""
}

func TestToggleDotsFollowTheServiceWhileStopped(t *testing.T) {
	cases := []struct {
		label  string
		toggle func(*tunnelsvc.Service) bool
	}{
		{"Reconnect", (*tunnelsvc.Service).ToggleReconnect},
		{"Monitor", (*tunnelsvc.Service).ToggleMonitor},
		{"Health", (*tunnelsvc.Service).ToggleHealthCheck},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			m, svc := newPane(config.TunnelSpec{
				Name:        "t",
				Command:     "ssh -N -D 5555 host",
				HealthCheck: "intranet:443",
			})

			// reconnect and monitor start off here; a configured health target
			// starts on. Assert the dot inverts rather than a fixed direction.
			before := row(m.View(120, 40, true), tc.label)
			if before == "" {
				t.Fatalf("no %q row in the view", tc.label)
			}
			wasOn := strings.Contains(before, dotOn)

			tc.toggle(svc)

			// No Update call: nothing delivers a ServiceEventMsg for a stopped tunnel.
			after := row(m.View(120, 40, true), tc.label)
			if isOn := strings.Contains(after, dotOn); isOn == wasOn {
				t.Errorf("%q dot did not change: %q then %q", tc.label, before, after)
			}

			tc.toggle(svc)

			back := row(m.View(120, 40, true), tc.label)
			if isOn := strings.Contains(back, dotOn); isOn != wasOn {
				t.Errorf("%q dot did not return: %q then %q", tc.label, before, back)
			}
		})
	}
}

func TestHealthRowShowsTargetAndOffState(t *testing.T) {
	m, svc := newPane(config.TunnelSpec{
		Name:        "t",
		Command:     "ssh -N -D 5555 host",
		HealthCheck: "intranet:443",
	})

	// A configured target starts enabled and is shown next to the dot.
	if got := row(m.View(120, 40, true), "Health"); !strings.Contains(got, "intranet:443") {
		t.Errorf("health row %q does not name the target", got)
	}

	svc.ToggleHealthCheck()

	got := row(m.View(120, 40, true), "Health")
	if !strings.Contains(got, "(off)") {
		t.Errorf("health row %q does not mark the check as off", got)
	}
	// The target is configuration, so it stays visible while the check is off.
	if !strings.Contains(got, "intranet:443") {
		t.Errorf("health row %q dropped the target when switched off", got)
	}
}

// renderStatusBox truncates the left column with leftLines[:innerH], so a box that
// is one line too short drops the last toggle row without any error. The tallest
// case is a connected tunnel: status + uptime + name + three toggles.
func TestStatusBoxFitsEveryToggleRowWhenConnected(t *testing.T) {
	m, svc := newPane(config.TunnelSpec{
		Name:        "t",
		Command:     "ssh -N -D 5555 host",
		HealthCheck: "intranet:443",
	})

	started := time.Now().Add(-90 * time.Second)
	m.states["t"] = service.RuntimeState{Status: service.StatusConnected, StartedAt: &started}

	view := m.View(120, 40, true)
	if !strings.Contains(view, "uptime:") {
		t.Fatal("expected the connected view to show uptime")
	}
	for _, label := range []string{"Reconnect", "Monitor", "Health"} {
		if row(view, label) == "" {
			t.Errorf("%q row missing from the connected view — the status box is too short", label)
		}
	}
	_ = svc
}

func TestHealthRowMarkedUnavailableWithoutTarget(t *testing.T) {
	m, _ := newPane(config.TunnelSpec{Name: "t", Command: "ssh -N -D 5555 host"})

	got := row(m.View(120, 40, true), "Health")
	if !strings.Contains(got, "not configured") {
		t.Errorf("health row %q does not say the check is unconfigured", got)
	}
}
