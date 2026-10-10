package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"
)

// serviceAction is what a service key does. Panes map their keys to actions
// (pane.keys); runAction carries them out through the service's capability
// interfaces, so a new service type gets the keys its capabilities allow
// without any pane knowing its concrete type.
type serviceAction int

const (
	actConnect           serviceAction = iota + 1 // start it, or say it is already running
	actStart                                      // start it without that check (docker compose)
	actDisconnect                                 // stop it
	actRefresh                                    // mint a new credential, for services holding one
	actToggleReconnect                            // flip automatic reconnect (tunnel, VPN)
	actAutoReconnect                              // the same, labelled as on the Connect pane
	actToggleMonitor                              // flip the liveness monitor
	actToggleHealth                               // flip the health check, when a target exists
	actToggleAutoConnect                          // flip following the bound VPN (tunnel auto_connect)
	actCopy                                       // copy the connect command, never with a secret in it
	actDetail                                     // open the detail view
	actLogin                                      // AWS SSO login
	actCancelLogin                                // cancel an SSO login in progress
)

// runAction performs act on svc and returns the commands that do it.
func (m *AppModel) runAction(act serviceAction, svc service.Service) []tea.Cmd {
	flash := func(text string) tea.Cmd { return func() tea.Msg { return msgs.StatusFlashMsg{Text: text} } }
	switch act {
	case actConnect, actLogin:
		return m.connectCmds(svc)
	case actStart:
		return []tea.Cmd{startService(m.ctx, m.manager, svc), flash("starting...")}
	case actDisconnect:
		return []tea.Cmd{stopService(m.ctx, svc), flash("disconnecting...")}
	case actCancelLogin:
		return []tea.Cmd{stopService(m.ctx, svc), flash("cancelling SSO login...")}
	case actRefresh:
		if !refreshesToken(svc) {
			return nil
		}
		return []tea.Cmd{refreshToken(m.ctx, svc), flash("refreshing token...")}
	case actToggleReconnect, actAutoReconnect:
		r, ok := svc.(service.Reconnectable)
		if !ok {
			return nil
		}
		label := "reconnect"
		if act == actAutoReconnect {
			label = "auto-reconnect"
		}
		return []tea.Cmd{msgs.FlashToggle(label, r.ToggleReconnect())}
	case actToggleMonitor:
		if mon, ok := svc.(service.Monitorable); ok {
			return []tea.Cmd{msgs.FlashToggle("monitor", mon.ToggleMonitor())}
		}
	case actToggleHealth:
		h, ok := svc.(service.HealthCheckable)
		if !ok {
			return nil
		}
		if h.HealthCheckTarget() == "" {
			return []tea.Cmd{flash("no health_check configured")}
		}
		return []tea.Cmd{msgs.FlashToggle("health check", h.ToggleHealthCheck())}
	case actToggleAutoConnect:
		b, ok := svc.(service.VPNBound)
		if !ok {
			return nil
		}
		if b.BoundVPN() == "" {
			return []tea.Cmd{flash("no auto_connect vpn configured")}
		}
		return []tea.Cmd{msgs.FlashToggle("auto-connect with "+b.BoundVPN(), b.ToggleAutoConnect())}
	case actCopy:
		cmd := svc.ConnectCommand()
		return []tea.Cmd{components.Copy(cmd, "copied!")}
	case actDetail:
		info := svc.Info()
		return []tea.Cmd{func() tea.Msg { return msgs.OpenDetailMsg{ServiceName: info.Name, ServiceType: info.Type} }}
	}
	return nil
}

// paneServiceKey runs the service action p maps msg to, if any. A key with an
// action but no service under the cursor does nothing, except on the AWS pane,
// which explains why its selected row cannot log in.
func (m *AppModel) paneServiceKey(p pane, msg tea.KeyPressMsg) []tea.Cmd {
	act, ok := p.keys()[msg.String()]
	if !ok {
		return nil
	}
	if svc, ok := p.selected(); ok {
		return m.runAction(act, svc)
	}
	if act == actLogin {
		if hint := m.awsPane.SelectionHint(); hint != "" {
			return []tea.Cmd{func() tea.Msg { return msgs.StatusFlashMsg{Text: hint} }}
		}
	}
	return nil
}
