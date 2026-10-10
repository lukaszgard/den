package detail

import "github.com/lukaszgard/den/internal/tui/components"

// The terminal tabs live in components, shared with the runbook view; these
// names keep the detail view's own API.

// Terminal is a client running in a terminal, as a tab shows it.
type Terminal = components.Terminal

// Tabs are the terminals opened for one service, next to its logs: position 0
// is the logs, and terminals 1 to Len follow in the order they were opened.
type Tabs = components.Tabs

const (
	MaxTerminals = components.MaxTerminals
	MaxNameLen   = components.MaxNameLen
)

// NewTabs returns the tabs of a service with no terminal yet.
func NewTabs() *Tabs { return components.NewTabs("Logs") }
