package vpn

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
	vpnsvc "github.com/lukaszgard/den/internal/service/vpn"
)

// The pane keeps a states map that only refreshes on a ServiceEventMsg. An idle
// VPN has no goroutine draining its update channel, so a toggle pressed while it
// is down never produces one — which is how the indicator goes stale. These tests
// render with no message delivered at all: the dots must come from the service.

const (
	dotOn  = "●"
	dotOff = "○"
)

func newPane(t *testing.T, spec *config.VPNSpec) (Model, *vpnsvc.Service) {
	t.Helper()
	if spec.Gateway == "" && spec.ConfigFile == "" {
		spec.Gateway = "vpn.example.com"
	}
	svc, err := vpnsvc.New("test", spec)
	if err != nil {
		t.Skipf("vpn.New unsupported on this platform: %v", err)
	}
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
		toggle func(*vpnsvc.Service) bool
	}{
		{"Reconnect", (*vpnsvc.Service).ToggleReconnect},
		{"Health", (*vpnsvc.Service).ToggleHealthCheck},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			m, svc := newPane(t, &config.VPNSpec{HealthCheck: "intranet:443"})

			// reconnect starts off here; a configured health target starts on.
			// Assert the dot inverts rather than a fixed direction.
			before := row(m.View(120, 40, true), tc.label)
			if before == "" {
				t.Fatalf("no %q row in the view", tc.label)
			}
			wasOn := strings.Contains(before, dotOn)

			tc.toggle(svc)

			// No Update call: nothing delivers a ServiceEventMsg for an idle VPN.
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

// VPN deliberately does not implement service.Monitorable: for a VPN the health
// check is the monitor, so a second row would toggle the same loop twice.
func TestNoMonitorRow(t *testing.T) {
	m, _ := newPane(t, &config.VPNSpec{HealthCheck: "intranet:443"})

	if got := row(m.View(120, 40, true), "Monitor"); got != "" {
		t.Errorf("unexpected Monitor row for a VPN: %q", got)
	}
}

func TestHealthRowShowsTargetAndOffState(t *testing.T) {
	m, svc := newPane(t, &config.VPNSpec{HealthCheck: "intranet:443"})

	if got := row(m.View(120, 40, true), "Health"); !strings.Contains(got, "intranet:443") {
		t.Errorf("health row %q does not name the target", got)
	}

	svc.ToggleHealthCheck()

	got := row(m.View(120, 40, true), "Health")
	if !strings.Contains(got, "(off)") {
		t.Errorf("health row %q does not mark the check as off", got)
	}
	if !strings.Contains(got, "intranet:443") {
		t.Errorf("health row %q dropped the target when switched off", got)
	}
	if !strings.Contains(got, dotOff) {
		t.Errorf("health row %q does not show the off dot", got)
	}
}

func TestHealthRowMarkedUnavailableWithoutTarget(t *testing.T) {
	m, _ := newPane(t, &config.VPNSpec{})

	got := row(m.View(120, 40, true), "Health")
	if !strings.Contains(got, "not configured") {
		t.Errorf("health row %q does not say the check is unconfigured", got)
	}
}

// renderStatusBox truncates the left column with leftLines[:innerH], so a box one
// line too short drops the last toggle row without any error. The tallest case is
// a connected VPN: status + uptime + name + both toggles.
func TestStatusBoxFitsEveryToggleRowWhenConnected(t *testing.T) {
	m, _ := newPane(t, &config.VPNSpec{HealthCheck: "intranet:443"})

	started := time.Now().Add(-90 * time.Second)
	m.states["test"] = service.RuntimeState{Status: service.StatusConnected, StartedAt: &started}

	view := m.View(120, 40, true)
	if !strings.Contains(view, "uptime:") {
		t.Fatal("expected the connected view to show uptime")
	}
	for _, label := range []string{"Reconnect", "Health"} {
		if row(view, label) == "" {
			t.Errorf("%q row missing from the connected view — the status box is too short", label)
		}
	}
}

// With no VPN the pane says which file to add one to, and that a gateway is
// all it needs, so a new user does not have to find a VPN file first.
func TestEmptyPaneSaysWhereTheEntryGoes(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("USERPROFILE", "/home/u")
	view := New(nil, 80, 30).SetConfigPath("/home/u/work/den.yaml").View(80, 30, true)
	for _, want := range []string{"No VPNs configured", "vpn: entry", "~/work/den.yaml", "A gateway is enough"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	// Without a den.yaml it names where one belongs.
	view = New(nil, 80, 30).View(80, 30, true)
	if want := filepath.Join("~", ".config", "den", "den.yaml"); !strings.Contains(view, want) {
		t.Errorf("view lacks %q:\n%s", want, view)
	}
}
