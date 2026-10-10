package detail

import (
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/service"

	"charm.land/lipgloss/v2"
)

// tokenSvc is a service as RDS is one: it holds a token, can inline it into
// its connect command and reopens its tunnel.
type tokenSvc struct {
	*fakeSvc
	reconnect bool
}

func (s *tokenSvc) HoldsToken() bool                 { return true }
func (s *tokenSvc) HasSecretCommand() bool           { return true }
func (s *tokenSvc) ConnectCommandWithSecret() string { return s.cmd + " password=tok" }
func (s *tokenSvc) ToggleReconnect() bool            { s.reconnect = !s.reconnect; return s.reconnect }
func (s *tokenSvc) ReconnectEnabled() bool           { return s.reconnect }

// toggleSvc has the runtime toggles of a tunnel or a VPN.
type toggleSvc struct {
	*fakeSvc
	reconnect, monitor, health bool
}

func (s *toggleSvc) ToggleReconnect() bool     { s.reconnect = !s.reconnect; return s.reconnect }
func (s *toggleSvc) ReconnectEnabled() bool    { return s.reconnect }
func (s *toggleSvc) ToggleMonitor() bool       { s.monitor = !s.monitor; return s.monitor }
func (s *toggleSvc) Monitoring() bool          { return s.monitor }
func (s *toggleSvc) ToggleHealthCheck() bool   { s.health = !s.health; return s.health }
func (s *toggleSvc) HealthCheckEnabled() bool  { return s.health }
func (s *toggleSvc) HealthCheckTarget() string { return "intranet.example.com:443" }

// shortcutKeys is every key the pop-up offers for svc, with its text.
func shortcutKeys(svc service.Service, tabs bool) map[string]string {
	keys := map[string]string{}
	for _, g := range shortcutGroups(svc, tabs) {
		for _, k := range g.Keys {
			for key := range strings.FieldsSeq(k.Keys) {
				keys[key] = k.Text
			}
		}
	}
	return keys
}

// Every service type has keys to list: the root model opens the pop-up for
// whatever Shortcuts returns, and an empty list would be a pop-up that never
// opens.
func TestEveryServiceTypeHasShortcuts(t *testing.T) {
	types := []string{
		service.TypeRDS, service.TypeRedis, service.TypeDocumentDB, service.TypeRedshift,
		service.TypeOpenSearch, service.TypeNeptune, service.TypeKafka, service.TypeDocker,
		service.TypeTunnel, service.TypeVPN, service.TypeAWS,
	}
	for _, typ := range types {
		m := New(&fakeSvc{typ: typ, cmd: "client --port 50111"}, nil, 120, 40)
		groups := m.Shortcuts()
		if len(groups) < 2 || groups[0].Title != "Connection" || groups[len(groups)-1].Title != "View" {
			t.Fatalf("%s: groups = %+v, want Connection first and View last", typ, groups)
		}
		keys := shortcutKeys(m.svc, false)
		for _, k := range []string{"c", "d", "h", "?", "esc"} {
			if keys[k] == "" {
				t.Errorf("%s: the pop-up does not explain %q", typ, k)
			}
		}
	}
}

// Two columns of shortcuts fit a 120-column terminal only while the texts stay
// short, and that is what keeps the pop-up whole on a short terminal.
func TestShortcutTextsStayShort(t *testing.T) {
	services := []service.Service{
		&tokenSvc{fakeSvc: &fakeSvc{typ: service.TypeRDS}},
		&fakeSvc{typ: service.TypeOpenSearch},
		&fakeSvc{typ: service.TypeNeptune},
		&toggleSvc{fakeSvc: &fakeSvc{typ: service.TypeTunnel}},
		&toggleSvc{fakeSvc: &fakeSvc{typ: service.TypeVPN}},
		&fakeSvc{typ: service.TypeAWS},
		&fakeSvc{typ: service.TypeDocker},
		&fakeSvc{typ: service.TypeKafka},
	}
	for _, svc := range services {
		for _, g := range shortcutGroups(svc, true) {
			for _, k := range g.Keys {
				if w := lipgloss.Width(k.Text); w > 41 {
					t.Errorf("%s %q: %q is %d columns, want at most 41", svc.Info().Type, k.Keys, k.Text, w)
				}
			}
			if w := lipgloss.Width(g.Note); w > 48 {
				t.Errorf("%s: the note %q is %d columns, want at most 48", svc.Info().Type, g.Note, w)
			}
		}
	}
}

func TestShortcutsFollowTheService(t *testing.T) {
	has := func(t *testing.T, keys map[string]string, want ...string) {
		t.Helper()
		for _, k := range want {
			if _, ok := keys[k]; !ok {
				t.Fatalf("the pop-up does not offer %q: %v", k, keys)
			}
		}
	}
	lacks := func(t *testing.T, keys map[string]string, unwanted ...string) {
		t.Helper()
		for _, k := range unwanted {
			if text, ok := keys[k]; ok {
				t.Fatalf("the pop-up offers %q (%s), which does nothing here", k, text)
			}
		}
	}

	t.Run("a service without a token or terminals", func(t *testing.T) {
		keys := shortcutKeys(&fakeSvc{typ: service.TypeDocumentDB}, false)
		has(t, keys, "c", "d", "y", "h", "?", "esc")
		lacks(t, keys, "r", "p", "a", "Y", "t", "n", "x", "m", "H")
	})
	t.Run("a token service with terminals", func(t *testing.T) {
		keys := shortcutKeys(&tokenSvc{fakeSvc: &fakeSvc{typ: service.TypeRDS}}, true)
		has(t, keys, "c", "d", "a", "r", "p", "y", "Y", "t", "←", "→", "1-9", "enter", "n", "x")
		lacks(t, keys, "m", "H")
		if !strings.Contains(keys["c"], "token") || !strings.Contains(keys["d"], "terminals") {
			t.Fatalf("c and d should say what they do here: %q, %q", keys["c"], keys["d"])
		}
		if !strings.Contains(keys["r"], "token") {
			t.Fatalf("r refreshes the token of a token service, not %q", keys["r"])
		}
		// "Auto-reconnect" alone left users guessing what it reconnects, and
		// whether it governed the token too.
		if !strings.Contains(keys["a"], "SSM port-forward") {
			t.Fatalf("a should name the SSM port-forward it reconnects, not %q", keys["a"])
		}
		notes := map[string]string{}
		for _, g := range shortcutGroups(&tokenSvc{fakeSvc: &fakeSvc{typ: service.TypeRDS}}, true) {
			notes[g.Title] = g.Note
		}
		if !strings.Contains(notes["Connection"], "drop") || !strings.Contains(notes["Connection"], "SSM idle timeout") {
			t.Fatalf("the Connection note should say when a reopens the forward, not %q", notes["Connection"])
		}
		if !strings.Contains(notes["Token"], "auto-reconnect or not") {
			t.Fatalf("the Token note should say renewal does not follow auto-reconnect, not %q", notes["Token"])
		}
	})
	// Behind the signing proxy there is no client to log in: the tab is a
	// shell, and a pop-up promising a logged-in client would be wrong.
	t.Run("a store behind the signing proxy", func(t *testing.T) {
		for _, typ := range []string{service.TypeOpenSearch, service.TypeNeptune} {
			keys := shortcutKeys(&fakeSvc{typ: typ}, true)
			has(t, keys, "c", "d", "y", "t", "←", "→", "1-9", "enter", "n", "x")
			if !strings.Contains(keys["t"], "shell") || !strings.Contains(keys["t"], "$DEN_URL") {
				t.Fatalf("%s: t opens a shell that knows $DEN_URL, not %q", typ, keys["t"])
			}
		}
		if rds := shortcutKeys(&tokenSvc{fakeSvc: &fakeSvc{typ: service.TypeRDS}}, true)["t"]; !strings.Contains(rds, "logged in") {
			t.Fatalf("t logs the client of a database in, not %q", rds)
		}
	})
	t.Run("a tunnel", func(t *testing.T) {
		keys := shortcutKeys(&toggleSvc{fakeSvc: &fakeSvc{typ: service.TypeTunnel}}, false)
		has(t, keys, "c", "d", "r", "m", "H", "y")
		lacks(t, keys, "a", "p", "Y", "t")
		if !strings.Contains(keys["r"], "Reconnect") {
			t.Fatalf("r is the reconnect toggle of a tunnel, not %q", keys["r"])
		}
	})
	t.Run("a VPN", func(t *testing.T) {
		keys := shortcutKeys(&toggleSvc{fakeSvc: &fakeSvc{typ: service.TypeVPN}}, false)
		has(t, keys, "c", "d", "r", "H")
		// The view of a VPN handles neither the monitor toggle nor the clipboard.
		lacks(t, keys, "m", "y", "Y", "p", "a")
	})
	t.Run("an AWS SSO session", func(t *testing.T) {
		keys := shortcutKeys(&fakeSvc{typ: service.TypeAWS}, false)
		has(t, keys, "c", "d", "r", "y")
		if !strings.Contains(keys["c"], "Log in") || !strings.Contains(keys["r"], "SSO") {
			t.Fatalf("c and r should describe an SSO session: %q, %q", keys["c"], keys["r"])
		}
	})
}

// The help line and the pop-up are two lists of the same keys. A key named on
// the line and missing from the pop-up would be one nobody can look up.
func TestHelpLineKeysAreInTheShortcuts(t *testing.T) {
	services := map[string]service.Service{
		"plain":  &fakeSvc{typ: service.TypeRedis},
		"token":  &tokenSvc{fakeSvc: &fakeSvc{typ: service.TypeRDS}},
		"tunnel": &toggleSvc{fakeSvc: &fakeSvc{typ: service.TypeTunnel}},
		"vpn":    &toggleSvc{fakeSvc: &fakeSvc{typ: service.TypeVPN}},
		"docker": &fakeSvc{typ: service.TypeDocker},
	}
	for name, svc := range services {
		keys := shortcutKeys(svc, false)
		for _, k := range serviceKeys(svc) {
			if _, ok := keys[k.k]; !ok {
				t.Errorf("%s: the help line names %q (%s), the pop-up does not", name, k.k, k.v)
			}
		}
	}
}
