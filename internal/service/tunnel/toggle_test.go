package tunnel

import (
	"testing"

	"github.com/lukaszgard/den/internal/config"
)

// The three runtime toggles are the only tunnel state the TUI can change without
// restarting anything, and a regression is invisible in the UI: the dot keeps
// rendering whatever the last good value was. These tests pin the flip, what the
// probe paths see afterwards, and the fact that none of it reaches the spec.

func TestNewSeedsTogglesFromSpec(t *testing.T) {
	cases := []struct {
		name                                  string
		spec                                  config.TunnelSpec
		reconnect, monitor, health, hasTarget bool
	}{
		{
			name: "all off",
			spec: config.TunnelSpec{Command: "ssh -N -D 5555 host"},
		},
		{
			name:      "all on",
			spec:      config.TunnelSpec{Command: "ssh -N -D 5555 host", Reconnect: true, Monitor: true, HealthCheck: "intranet:443"},
			reconnect: true, monitor: true, health: true, hasTarget: true,
		},
		{
			// A configured target starts enabled even when monitor is off: the
			// check still runs during connect.
			name:   "health target without monitor",
			spec:   config.TunnelSpec{Command: "ssh -N -D 5555 host", HealthCheck: "intranet:443"},
			health: true, hasTarget: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.spec)
			if got := s.ReconnectEnabled(); got != tc.reconnect {
				t.Errorf("ReconnectEnabled() = %v, want %v", got, tc.reconnect)
			}
			if got := s.Monitoring(); got != tc.monitor {
				t.Errorf("Monitoring() = %v, want %v", got, tc.monitor)
			}
			if got := s.HealthCheckEnabled(); got != tc.health {
				t.Errorf("HealthCheckEnabled() = %v, want %v", got, tc.health)
			}
			if got := s.HealthCheckTarget() != ""; got != tc.hasTarget {
				t.Errorf("HealthCheckTarget() non-empty = %v, want %v", got, tc.hasTarget)
			}
		})
	}
}

func TestTogglesFlipAndReportNewValue(t *testing.T) {
	s := New(config.TunnelSpec{Command: "ssh -N -D 5555 host", HealthCheck: "intranet:443"})

	cases := []struct {
		name   string
		toggle func() bool
		read   func() bool
	}{
		{"reconnect", s.ToggleReconnect, s.ReconnectEnabled},
		{"monitor", s.ToggleMonitor, s.Monitoring},
		{"health check", s.ToggleHealthCheck, s.HealthCheckEnabled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.read()
			// The returned value is what the status flash prints, so it has to be
			// the new setting, not the old one.
			if got := tc.toggle(); got != !before {
				t.Fatalf("toggle returned %v, want %v", got, !before)
			}
			if got := tc.read(); got != !before {
				t.Fatalf("after toggle read %v, want %v", got, !before)
			}
			if got := tc.toggle(); got != before {
				t.Fatalf("second toggle returned %v, want %v", got, before)
			}
			if got := tc.read(); got != before {
				t.Fatalf("after second toggle read %v, want %v", got, before)
			}
		})
	}
}

func TestToggleHealthCheckInertWithoutTarget(t *testing.T) {
	s := New(config.TunnelSpec{Command: "ssh -N -D 5555 host"})

	if got := s.ToggleHealthCheck(); got {
		t.Errorf("ToggleHealthCheck() = true with no target configured, want false")
	}
	if s.HealthCheckEnabled() {
		t.Error("HealthCheckEnabled() = true with no target configured")
	}
	// Repeated presses must not flip it on by accident.
	s.ToggleHealthCheck()
	if s.HealthCheckEnabled() {
		t.Error("HealthCheckEnabled() = true after a second press with no target")
	}
}

func TestActiveHealthCheckGatesTheProbe(t *testing.T) {
	s := New(config.TunnelSpec{Command: "ssh -N -D 5555 host", HealthCheck: "intranet:443"})

	if got := s.activeHealthCheck(); got != "intranet:443" {
		t.Errorf("activeHealthCheck() = %q, want %q", got, "intranet:443")
	}

	s.ToggleHealthCheck()
	if got := s.activeHealthCheck(); got != "" {
		t.Errorf("activeHealthCheck() = %q while disabled, want %q", got, "")
	}
	// The target itself is configuration and survives the toggle, so the TUI can
	// keep showing what would be probed.
	if got := s.HealthCheckTarget(); got != "intranet:443" {
		t.Errorf("HealthCheckTarget() = %q while disabled, want %q", got, "intranet:443")
	}

	// A disabled check must not dial: checkHealth returns nil rather than
	// errNoSocks even though the forwards carry no SOCKS proxy.
	if err := s.checkHealth([]Forward{{Port: 8080, Target: "app:80"}}); err != nil {
		t.Errorf("checkHealth() while disabled = %v, want nil", err)
	}

	s.ToggleHealthCheck()
	if err := s.checkHealth([]Forward{{Port: 8080, Target: "app:80"}}); err != errNoSocks {
		t.Errorf("checkHealth() while enabled = %v, want errNoSocks", err)
	}
}

func TestTogglesDoNotWriteBackToSpec(t *testing.T) {
	spec := config.TunnelSpec{
		Command:     "ssh -N -D 5555 host",
		Reconnect:   true,
		Monitor:     true,
		HealthCheck: "intranet:443",
	}
	s := New(spec)
	s.ToggleReconnect()
	s.ToggleMonitor()
	s.ToggleHealthCheck()

	// den.yaml is never rewritten: a restart has to restore the file's values.
	if !s.spec.Reconnect || !s.spec.Monitor || s.spec.HealthCheck != "intranet:443" {
		t.Errorf("toggles mutated the spec: %+v", s.spec)
	}
}

func TestAutoConnectToggle(t *testing.T) {
	off := false
	cases := []struct {
		name string
		ac   *config.TunnelAutoConnect
		vpn  string
		on   bool
	}{
		{"no block", nil, "", false},
		{"enabled by default", &config.TunnelAutoConnect{VPN: "Office VPN"}, "Office VPN", true},
		{"enabled: false", &config.TunnelAutoConnect{VPN: "Office VPN", Enabled: &off}, "Office VPN", false},
	}
	for _, tc := range cases {
		s := New(config.TunnelSpec{Command: "ssh -N -D 5555 host", AutoConnect: tc.ac})
		if s.BoundVPN() != tc.vpn || s.AutoConnect() != tc.on {
			t.Errorf("%s: BoundVPN %q AutoConnect %v, want %q %v", tc.name, s.BoundVPN(), s.AutoConnect(), tc.vpn, tc.on)
		}
	}

	s := New(config.TunnelSpec{Command: "ssh -N -D 5555 host", AutoConnect: &config.TunnelAutoConnect{VPN: "Office VPN"}})
	updates := s.updates
	if got := s.ToggleAutoConnect(); got || s.AutoConnect() {
		t.Fatalf("toggle returned %v, want off", got)
	}
	select {
	case <-updates:
	default:
		t.Error("a toggle must send an update so the pane redraws its dot")
	}
	if !s.ToggleAutoConnect() || !s.AutoConnect() {
		t.Fatal("second toggle must switch it back on")
	}
	if !s.spec.AutoConnect.On() {
		t.Error("the toggle mutated the spec")
	}

	unbound := New(config.TunnelSpec{Command: "ssh -N -D 5555 host"})
	if unbound.ToggleAutoConnect() || unbound.AutoConnect() {
		t.Error("without a bound VPN there is nothing to follow")
	}
}
