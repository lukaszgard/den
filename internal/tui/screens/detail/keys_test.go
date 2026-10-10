package detail

import (
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/tui/testkeys"

	tea "charm.land/bubbletea/v2"
)

// flashOf runs cmd and returns the flash it produces, "" for none.
func flashOf(cmd tea.Cmd) string {
	if cmd == nil {
		return ""
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if text := flashOf(c); text != "" {
				return text
			}
		}
		return ""
	}
	if f, ok := msg.(msgs.StatusFlashMsg); ok {
		return f.Text
	}
	return ""
}

// The runtime toggles are keys of the view: each flips the service's own
// setting and says which way it went.
func TestToggleKeysFlipTheServiceSettings(t *testing.T) {
	cases := []struct {
		typ   string
		key   string
		value func(*toggleSvc) bool
		flash string
	}{
		{service.TypeTunnel, "r", func(s *toggleSvc) bool { return s.reconnect }, "reconnect"},
		{service.TypeTunnel, "m", func(s *toggleSvc) bool { return s.monitor }, "monitor"},
		{service.TypeTunnel, "H", func(s *toggleSvc) bool { return s.health }, "health check"},
		{service.TypeVPN, "r", func(s *toggleSvc) bool { return s.reconnect }, "reconnect"},
		{service.TypeVPN, "H", func(s *toggleSvc) bool { return s.health }, "health check"},
	}
	for _, c := range cases {
		svc := &toggleSvc{fakeSvc: &fakeSvc{typ: c.typ, cmd: "ssh -N -D 1080 -L 5432:db:5432 bastion"}}
		m := New(svc, nil, 120, 30)
		m, cmd := m.Update(testkeys.Text(c.key))
		if !c.value(svc) {
			t.Errorf("%s %s: the setting was not turned on", c.typ, c.key)
		}
		if got := flashOf(cmd); !strings.Contains(got, c.flash) {
			t.Errorf("%s %s: flash %q, want it to name %q", c.typ, c.key, got, c.flash)
		}
		if c.typ == service.TypeTunnel && !strings.Contains(m.View(), "1080, 5432") {
			t.Errorf("a tunnel with two forwards should list both ports:\n%s", m.View())
		}
	}
}

func TestDataStoreKeys(t *testing.T) {
	svc := &tokenSvc{fakeSvc: &fakeSvc{typ: service.TypeRDS, cmd: "psql host=localhost"}}
	m := New(svc, nil, 120, 30)

	m, cmd := m.Update(testkeys.Text("a"))
	if !svc.reconnect || !strings.Contains(flashOf(cmd), "auto-reconnect") {
		t.Fatal("a should toggle auto-reconnect")
	}
	m, cmd = m.Update(testkeys.Text("p"))
	if got := flashOf(cmd); !strings.Contains(got, "connect first") {
		t.Fatalf("p without a token: flash %q, want a pointer to connecting first", got)
	}
	if m.ConnectCommand() != "psql host=localhost" || m.Service() != service.Service(svc) || m.Init() != nil {
		t.Fatal("the view does not report its service")
	}
}

// A new log line reaches the view on the next tick.
func TestTickRefreshesTheLogs(t *testing.T) {
	svc := &fakeSvc{cmd: "redis-cli", logs: []string{"first line"}}
	m := New(svc, nil, 120, 30)
	svc.logs = append(svc.logs, "second line")
	m, _ = m.Update(msgs.TickMsg{})
	if !strings.Contains(m.View(), "second line") {
		t.Fatalf("the tick did not bring the new line:\n%s", m.View())
	}
	svc.logs = append(svc.logs, "third line")
	m, _ = m.Update(msgs.ServiceEventMsg{Update: service.StatusUpdate{Info: svc.Info(), State: svc.State()}})
	if !strings.Contains(m.View(), "third line") {
		t.Fatalf("a service event did not bring the new line:\n%s", m.View())
	}
}
