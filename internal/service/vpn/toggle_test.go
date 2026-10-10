package vpn

import (
	"testing"

	"github.com/lukaszgard/den/internal/config"
)

// The two runtime toggles are the only VPN state the TUI can change without
// reconnecting, and a regression is invisible in the UI: the dot keeps rendering
// whatever the last good value was. nextRetry carries the safety rules that keep
// auto-reconnect from spraying browser tabs or burning gateway session slots, so
// it is pinned here rather than left to a manual test against a real gateway.

func newSvc(t *testing.T, spec *config.VPNSpec) *Service {
	t.Helper()
	if spec.Gateway == "" && spec.ConfigFile == "" {
		spec.Gateway = "vpn.example.com"
	}
	// New() probes the host (openfortivpn on PATH, a live ppp0) to decide its
	// seed state, which we don't care about here — only the toggle fields, which
	// are set before any of that.
	svc, err := New("test", spec)
	if err != nil {
		t.Skipf("vpn.New unsupported on this platform: %v", err)
	}
	return svc
}

func TestNewSeedsTogglesFromSpec(t *testing.T) {
	cases := []struct {
		name                       string
		spec                       config.VPNSpec
		reconnect, health, hasHost bool
	}{
		{
			name: "all off",
			spec: config.VPNSpec{},
		},
		{
			name:      "all on",
			spec:      config.VPNSpec{Reconnect: true, HealthCheck: "intranet:443"},
			reconnect: true, health: true, hasHost: true,
		},
		{
			// A configured target starts enabled even without reconnect.
			name:   "health target only",
			spec:   config.VPNSpec{HealthCheck: "intranet:443"},
			health: true, hasHost: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := tc.spec
			s := newSvc(t, &spec)
			if got := s.ReconnectEnabled(); got != tc.reconnect {
				t.Errorf("ReconnectEnabled() = %v, want %v", got, tc.reconnect)
			}
			if got := s.HealthCheckEnabled(); got != tc.health {
				t.Errorf("HealthCheckEnabled() = %v, want %v", got, tc.health)
			}
			if got := s.HealthCheckTarget() != ""; got != tc.hasHost {
				t.Errorf("HealthCheckTarget() non-empty = %v, want %v", got, tc.hasHost)
			}
		})
	}
}

func TestTogglesFlipAndReportNewValue(t *testing.T) {
	s := newSvc(t, &config.VPNSpec{HealthCheck: "intranet:443"})

	cases := []struct {
		name   string
		toggle func() bool
		read   func() bool
	}{
		{"reconnect", s.ToggleReconnect, s.ReconnectEnabled},
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
		})
	}
}

func TestToggleHealthCheckInertWithoutTarget(t *testing.T) {
	s := newSvc(t, &config.VPNSpec{})

	if got := s.ToggleHealthCheck(); got {
		t.Errorf("ToggleHealthCheck() = true with no target configured, want false")
	}
	if s.HealthCheckEnabled() {
		t.Error("HealthCheckEnabled() = true with no target configured")
	}
}

func TestActiveHealthCheckGatesTheProbe(t *testing.T) {
	s := newSvc(t, &config.VPNSpec{HealthCheck: "intranet:443"})

	if got := s.activeHealthCheck(); got != "intranet:443" {
		t.Errorf("activeHealthCheck() = %q, want %q", got, "intranet:443")
	}

	s.ToggleHealthCheck()
	if got := s.activeHealthCheck(); got != "" {
		t.Errorf("activeHealthCheck() = %q while disabled, want %q", got, "")
	}
	// The target is configuration and survives the toggle, so the TUI can keep
	// showing what would be probed.
	if got := s.HealthCheckTarget(); got != "intranet:443" {
		t.Errorf("HealthCheckTarget() = %q while disabled, want %q", got, "intranet:443")
	}
	// dataPathAlive must report "nothing to verify" rather than probing a target
	// the user switched off.
	if !s.dataPathAlive() {
		t.Error("dataPathAlive() = false while the check is disabled, want true")
	}
}

func TestTogglesDoNotWriteBackToSpec(t *testing.T) {
	spec := &config.VPNSpec{Reconnect: true, HealthCheck: "intranet:443"}
	s := newSvc(t, spec)
	s.ToggleReconnect()
	s.ToggleHealthCheck()

	// den.yaml is never rewritten: a restart has to restore the file's values.
	if !spec.Reconnect || spec.HealthCheck != "intranet:443" {
		t.Errorf("toggles mutated the spec: %+v", spec)
	}
}

// nextRetry is the safety gate on auto-reconnect. Each of these rules exists
// because a VPN connect is interactive: it opens a browser tab for SAML, and a
// half-finished login holds a gateway session slot until the gateway starts
// refusing every new login.
func TestNextRetryRefusesUnsafeReconnects(t *testing.T) {
	cases := []struct {
		name      string
		reconnect bool
		outcome   attemptOutcome
		attempt   int
		wantOK    bool
	}{
		{name: "reconnect off", reconnect: false, outcome: outcomeConnected, attempt: 0},
		{name: "login timed out", reconnect: true, outcome: outcomeLoginTimeout, attempt: 0},
		{name: "machine cannot run it", reconnect: true, outcome: outcomeFatal, attempt: 0},
		{name: "attempts exhausted", reconnect: true, outcome: outcomeConnected, attempt: len(reconnectDelays)},
		{name: "past the cap", reconnect: true, outcome: outcomeConnected, attempt: len(reconnectDelays) + 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSvc(t, &config.VPNSpec{Reconnect: tc.reconnect, HealthCheck: "intranet:443"})
			s.mu.Lock()
			s.reconnect, s.outcome = tc.reconnect, tc.outcome
			s.mu.Unlock()

			if _, ok := s.nextRetry(t.Context(), tc.attempt); ok != tc.wantOK {
				t.Errorf("nextRetry(attempt=%d) ok = %v, want %v", tc.attempt, ok, tc.wantOK)
			}
		})
	}
}

// The delays grow, so a flapping tunnel backs off instead of hammering the
// gateway, and the schedule is finite.
func TestReconnectDelaysAreBoundedAndIncreasing(t *testing.T) {
	if len(reconnectDelays) == 0 {
		t.Fatal("reconnectDelays is empty — auto-reconnect would never retry")
	}
	for i := 1; i < len(reconnectDelays); i++ {
		if reconnectDelays[i] <= reconnectDelays[i-1] {
			t.Errorf("reconnectDelays[%d]=%v is not longer than [%d]=%v",
				i, reconnectDelays[i], i-1, reconnectDelays[i-1])
		}
	}
}
