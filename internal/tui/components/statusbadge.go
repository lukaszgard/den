package components

import (
	"strings"

	"github.com/lukaszgard/den/internal/service"

	"charm.land/lipgloss/v2"
)

var (
	dotConnected    = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575")).Render("●")
	dotIdle         = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Render("○")
	dotError        = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Render("✗")
	dotConnecting   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65")).Render("◐")
	dotStopping     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65")).Render("◑")
	dotReconnecting = lipgloss.NewStyle().Foreground(lipgloss.Color("#F5A623")).Render("↻")
)

// StatusDot returns the coloured status dot for the given status.
func StatusDot(s service.Status) string {
	switch s {
	case service.StatusConnected:
		return dotConnected
	case service.StatusConnecting:
		return dotConnecting
	case service.StatusError:
		return dotError
	case service.StatusStopping:
		return dotStopping
	case service.StatusReconnecting:
		return dotReconnecting
	default:
		return dotIdle
	}
}

// dotUnavailable marks a toggle that has nothing to act on, e.g. a health check
// with no target configured. It is dimmer than the "off" dot so the row reads as
// inert rather than merely switched off.
var dotUnavailable = lipgloss.NewStyle().Foreground(lipgloss.Color("#3A3A3A")).Render("○")

// ToggleDot returns the dot for a runtime toggle: green when on, grey when off.
// Callers render it from the service's live accessor, never from a cached status
// snapshot, so it cannot go stale.
func ToggleDot(on bool) string {
	if on {
		return dotConnected
	}
	return dotIdle
}

// ToggleDotUnavailable returns the dot for a toggle that cannot be used because
// nothing is configured for it.
func ToggleDotUnavailable() string { return dotUnavailable }

// ToggleRows renders the runtime toggle rows — reconnect (r), monitor (m) and
// health check (H) — for a service. Each setting is read live from the service
// through its capability interface, never from a cached RuntimeState, so the dots
// update the moment a toggle is pressed even while the service is stopped. A
// service that does not implement a capability contributes no row.
func ToggleRows(svc service.Service) []string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	var rows []string
	if rc, ok := svc.(service.Reconnectable); ok {
		rows = append(rows, ToggleDot(rc.ReconnectEnabled())+" Reconnect")
	}
	if mo, ok := svc.(service.Monitorable); ok {
		rows = append(rows, ToggleDot(mo.Monitoring())+" Monitor")
	}
	if hc, ok := svc.(service.HealthCheckable); ok {
		switch target := hc.HealthCheckTarget(); {
		case target == "":
			rows = append(rows, ToggleDotUnavailable()+" Health "+dim.Render("(not configured)"))
		case hc.HealthCheckEnabled():
			rows = append(rows, ToggleDot(true)+" Health "+dim.Render(target))
		default:
			rows = append(rows, ToggleDot(false)+" Health "+dim.Render(target+" (off)"))
		}
	}
	// Only services that name a VPN get the row: the others have nothing to follow.
	if vb, ok := svc.(service.VPNBound); ok && vb.BoundVPN() != "" {
		label := vb.BoundVPN()
		if !vb.AutoConnect() {
			label += " (off)"
		}
		rows = append(rows, ToggleDot(vb.AutoConnect())+" Auto-connect "+dim.Render(label))
	}
	return rows
}

// TypeBadge returns a coloured type icon for the given service type.
func TypeBadge(svcType string) string {
	var icon string
	var style lipgloss.Style
	switch svcType {
	case service.TypeRDS:
		icon = "\uf1c0 "
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C91BF")).Bold(true)
	case service.TypeKafka:
		icon = "\uf538 "
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("#F1C40F")).Bold(true)
	case service.TypeRedis:
		icon = "\ue76d "
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Bold(true)
	case service.TypeDocumentDB:
		icon = "\ue7a4 " // nf-dev-mongodb \u2014 DocumentDB is MongoDB-compatible
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("#4DB6AC")).Bold(true)
	case service.TypeRedshift:
		icon = "\uf1c0 " // nf-fa-database — Redshift speaks the PostgreSQL protocol
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("#E8743B")).Bold(true)
	case service.TypeOpenSearch:
		icon = "\uf002 " // nf-fa-search
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("#005EB8")).Bold(true)
	case service.TypeNeptune:
		icon = "\uf1e0 " // nf-fa-share_alt — a small graph
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("#3F8CD8")).Bold(true)
	case service.TypeDocker:
		icon = "\uf308 "
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("#2ECC71")).Bold(true)
	case service.TypeTunnel:
		icon = "\uf0c1 "
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA")).Bold(true)
	case service.TypeVPN:
		icon = "\uf084 "
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("#674EFF")).Bold(true)
	case service.TypeAWS:
		icon = "\ue7ad "
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF9900")).Bold(true)
	default:
		icon = "\uf233 "
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
	}
	const badgeWidth = 6 // longest label: "tunnel"/"docker"/"docdb "
	label := badgeLabel(svcType)
	pad := badgeWidth - len(label)
	if pad < 0 {
		// A label longer than the column would make strings.Repeat panic, which
		// takes the whole TUI down. Truncate instead — a squeezed badge is a
		// cosmetic problem, a crash is not.
		label = label[:badgeWidth]
		pad = 0
	}
	return style.Render(icon + label + strings.Repeat(" ", pad))
}

// badgeLabel shortens type names that would otherwise blow out the badge column.
// The service type itself keeps its full name everywhere else.
func badgeLabel(svcType string) string {
	switch svcType {
	case service.TypeDocumentDB:
		return "docdb"
	case service.TypeRedshift:
		return "rshift"
	case service.TypeOpenSearch:
		return "search"
	case service.TypeNeptune:
		return "graph"
	}
	return svcType
}
