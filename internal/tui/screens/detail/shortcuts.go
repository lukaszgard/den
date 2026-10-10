package detail

import (
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/components"
)

// The shortcuts pop-up (h or ?) says what every key of the view does for the
// service on screen. The help line under the box only names the keys, and
// loses its end on a narrow terminal. The root model shows the pop-up; the
// view says what goes into it.

type (
	shortcut      = components.Shortcut
	shortcutGroup = components.ShortcutGroup
)

// Shortcuts lists the keys of the view for the service it shows.
func (m Model) Shortcuts() []components.ShortcutGroup {
	return shortcutGroups(m.svc, m.strip.Tabs() != nil)
}

// connectWords says what c and d do for the service types that are not a
// tunnelled data store.
var connectWords = map[string]struct{ connect, disconnect string }{
	service.TypeTunnel: {"Start the tunnel command", "Stop the tunnel"},
	service.TypeVPN:    {"Connect: log in through the browser", "Disconnect the VPN"},
	service.TypeAWS:    {"Log in: aws sso login, in the browser", "Cancel a login in progress"},
	service.TypeDocker: {"Start: docker compose up -d", "Mark it stopped; containers keep running"},
	service.TypeKafka:  {"Not available yet: Kafka is planned", "Clear the error"},
}

// shellTabWords says what t opens for the service types reached through the
// signing proxy: nothing logs in to an HTTP endpoint, so their tab is a shell.
var shellTabWords = map[string]string{
	service.TypeOpenSearch: "New tab: a shell with $DEN_URL set",
	service.TypeNeptune:    "New tab: a shell with $DEN_URL set",
}

// shortcutGroups lists the keys that do something for svc, following the same
// capability checks as Update and the help line: a key that would do nothing
// is not offered.
func shortcutGroups(svc service.Service, tabs bool) []shortcutGroup {
	typ := svc.Info().Type
	holdsToken := service.HoldsToken(svc)
	_, reconnects := svc.(service.Reconnectable)

	words, ok := connectWords[typ]
	if !ok {
		words.connect, words.disconnect = "Connect: open the tunnel", "Disconnect: close the tunnel"
		if holdsToken {
			words.connect = "Connect: open the tunnel, mint a token"
		}
		if tabs {
			words.disconnect = "Disconnect: close tunnel and terminals"
		}
	}
	connection := shortcutGroup{Title: "Connection", Keys: []shortcut{{Keys: "c", Text: words.connect}, {Keys: "d", Text: words.disconnect}}}
	view := shortcutGroup{Title: "View", Keys: []shortcut{
		{Keys: "↑ ↓", Text: "Scroll the logs (pgup, pgdn by page)"},
		{Keys: "h ?", Text: "Open or close this help"},
		{Keys: "esc", Text: "Back to the list"},
	}}

	var groups []shortcutGroup
	switch typ {
	case service.TypeTunnel, service.TypeVPN:
		toggles := shortcutGroup{Title: "Runtime toggles", Note: "Toggles last until den exits."}
		if reconnects {
			toggles.Keys = append(toggles.Keys, shortcut{Keys: "r", Text: "Reconnect on/off: restart after a drop"})
		}
		if _, ok := svc.(service.Monitorable); ok && typ == service.TypeTunnel {
			toggles.Keys = append(toggles.Keys, shortcut{Keys: "m", Text: "Monitor on/off: re-check ports every 15 s"})
		}
		if _, ok := svc.(service.HealthCheckable); ok {
			toggles.Keys = append(toggles.Keys, shortcut{Keys: "H", Text: "Health check on/off: probe the target"})
		}
		groups = append(groups, connection)
		if len(toggles.Keys) > 0 {
			groups = append(groups, toggles)
		}
		// The view of a VPN has no clipboard keys.
		if typ == service.TypeVPN {
			return append(groups, view)
		}
	default:
		if reconnects {
			// Say what a reconnects: "auto-reconnect" alone read as if it
			// could also stop the token refresh.
			connection.Keys = append(connection.Keys, shortcut{Keys: "a", Text: "SSM port-forward auto-reconnect on/off"})
			connection.Note = "a reopens it after a drop (SSM idle timeout)."
		}
		if typ == service.TypeAWS {
			connection.Keys = append(connection.Keys, shortcut{Keys: "r", Text: "Re-read the cached SSO token (no login)"})
		}
		groups = append(groups, connection)
		if holdsToken {
			groups = append(groups, shortcutGroup{
				Title: "Token",
				Keys:  []shortcut{{Keys: "r", Text: "Mint a new token now"}, {Keys: "p", Text: "Copy the token"}},
				Note:  "Renewed while connected, auto-reconnect or not.",
			})
		}
	}

	command := "connect"
	switch typ {
	case service.TypeTunnel:
		command = "tunnel"
	case service.TypeAWS:
		command = "login"
	}
	clipboard := shortcutGroup{Title: "Clipboard", Keys: []shortcut{{Keys: "y", Text: "Copy the " + command + " command"}}}
	if _, ok := service.SecretCommand(svc); ok {
		clipboard.Keys = append(clipboard.Keys, shortcut{Keys: "Y", Text: "Copy it with the token inlined"})
	}
	groups = append(groups, clipboard)

	if tabs {
		newTab, ok := shellTabWords[typ]
		if !ok {
			newTab = "New tab: the client, already logged in"
		}
		groups = append(groups, shortcutGroup{
			Title: "Terminal tabs",
			Keys: []shortcut{
				{Keys: "t", Text: newTab},
				{Keys: "← →", Text: "Previous / next tab (or tab, shift+tab)"},
				{Keys: "1-9", Text: "Jump to a terminal and type into it"},
				{Keys: "enter", Text: "Type into the selected tab, or restart it"},
				{Keys: "n", Text: "Rename the selected tab"},
				{Keys: "x", Text: "Close the selected tab"},
			},
			Note: "Back to den: esc (twice in an editor) or ctrl+t",
		})
	}
	return append(groups, view)
}
