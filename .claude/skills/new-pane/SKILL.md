---
name: new-pane
description: This skill should be used when the user asks to "add a pane", "add a panel to the TUI", "add a new screen", "add a menu entry", or wants a new left-menu section in den's dashboard.
---

# Add a pane to den's TUI

Each pane is a standalone Bubble Tea model in `internal/tui/screens/<pane>/`. The root
`AppModel` reaches it only through a small adapter implementing the `pane` interface
in `internal/tui/panes.go`, so routing, resizing, the help bar and service keys need
no new switch cases.

## Steps

1. **Screen model** (`internal/tui/screens/<pane>/<pane>.go`) — `New(...)`,
   `Update(msg, focused) (Model, tea.Cmd)`, `View(width, height, focused) string` (a plain string; only the root `AppModel.View()`
   returns `tea.View`), keys as `tea.KeyPressMsg` (`msg.String()`, space is `"space"`),
   `SetSize(width, height) Model` that keeps cursor, filter and scroll state, plus
   `Searching()` / `SelectedService()` when relevant, and `Shortcuts()` listing its
   keys for the shortcuts pop-up (h or ?). Keep help that does not fit a terminal
   out of the pane: what it is for and how its `den.yaml` section is written go into
   its Help topic in `docs/help/`, which scrolls and searches.
2. **Register** in `internal/tui/app.go` — a `pane<Name>` ID, a `paneMetadata` entry
   (label, Nerd Font icon, colour, status-line description), a place in
   `defaultMenuOrder`, a field on `AppModel`, construction in `New`, and rebuilding in
   the `configReloadedMsg` handler if it depends on config.
3. **Adapter** in `internal/tui/panes.go` — embed `basePane` for the defaults and
   override what applies: `searching`, `ownsBack` (esc while a prompt or an open
   item has it), `selected`, `keys` (map keys to `serviceAction`s), `help`,
   `shortcuts`, `serviceEvent`, and `setDark(isDark bool)` (the optional `darkAware`
   interface) when the pane's colours depend on the terminal's light or dark background.
   Add the ID to the `pane()` switch and `allPanes`.
4. **Tests** — a case in `internal/tui/app_test.go`: navigate to the pane, its keys,
   its h/esc ownership. Build keys with `internal/tui/testkeys` (`Press`, `Text`,
   `Paste`); a `tea.KeyPressMsg` literal only for a key `Press` cannot express. Screen
   logic gets its own `_test.go`.
5. **Docs** — the pane ID in `docs/help/menu-and-logs.md`, a Help topic for the pane
   (`docs/help/<pane>.md`, listed in `docs/help/README.md`), the README panel list and
   menu-order IDs, `docs/<pane>/` per the `feature-docs` skill.
6. `make check`, then run `den` and look at it at a small and a large terminal size.
