package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/msgs"
)

// Services bound to a VPN (tunnels with auto_connect) follow it while their
// auto-connect setting is on: den connects them when the VPN comes up and stops
// them when it goes down, rather than letting them retry against a network they
// cannot reach. Only transitions count, so a tunnel the user disconnected stays
// down until the VPN next comes up, and one they connected by hand is left alone
// until the VPN next goes down.

// vpnsAdoptedMsg lists the VPNs reconcileRunningVPNs took over at startup
// already connected. Adopting one sends no status event — its connected state
// is seeded when the service is built — so this message stands in for "came up".
type vpnsAdoptedMsg struct{ names []string }

// vpnChanged records VPN name's new status and returns the commands that bring
// its bound services in line: started when it has just come up, stopped when
// it has just gone down.
func (m *AppModel) vpnChanged(name string, status service.Status) []tea.Cmd {
	up := status == service.StatusConnected
	if m.vpnUp[name] == up {
		return nil
	}
	m.vpnUp[name] = up
	var cmds []tea.Cmd
	for _, svc := range m.manager.Services() {
		bound, ok := svc.(service.VPNBound)
		if !ok || bound.BoundVPN() != name || !bound.AutoConnect() {
			continue
		}
		svcName := svc.Info().Name
		switch running := isRunning(svc); {
		case up && !running:
			text := fmt.Sprintf("auto-connect: %s (%s is up)", svcName, name)
			m.logger.Info(text)
			cmds = append(cmds, startService(m.ctx, m.manager, svc),
				func() tea.Msg { return msgs.StatusFlashMsg{Text: text} })
		case !up && running:
			text := fmt.Sprintf("auto-connect: stopping %s (%s is %s)", svcName, name, status)
			m.logger.Info(text)
			cmds = append(cmds, stopService(m.ctx, svc),
				func() tea.Msg { return warnFlash(text) })
		}
	}
	return cmds
}

// vpnDownWarning is what to say when the user connects svc by hand while the
// VPN it runs over is not connected, or "" when that is not the case. den
// connects anyway: the user asked, and auto-connect only governs what den
// does on its own.
func (m *AppModel) vpnDownWarning(svc service.Service) string {
	bound, ok := svc.(service.VPNBound)
	if !ok || bound.BoundVPN() == "" {
		return ""
	}
	name := bound.BoundVPN()
	for _, s := range m.manager.Services() {
		if info := s.Info(); info.Type == service.TypeVPN && info.Name == name && s.State().Status == service.StatusConnected {
			return ""
		}
	}
	return fmt.Sprintf("%s is not connected — %s may not reach its host", name, svc.Info().Name)
}
