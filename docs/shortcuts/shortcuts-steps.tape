# The shortcuts recording's steps, shared by shortcuts.tape (the README's GIF) and
# shortcuts-hd.tape (the site video), so the two cannot drift. Sourced after their
# Output line and settings; it has neither of its own.

# Needs: the demo config (docs/demo/den.yaml) with its RDS services and tunnel.
# Nothing is connected, so no AWS login is required, but den runs its startup
# update check: rendered by a person with `make demo`.
#
# Kept under a minute: the pop-up's other entry point (?) and the Help panel's
# search are in docs/shortcuts/README.md and docs/help-panel.

# Launch den hidden, so the recording opens on the loaded dashboard.
Hide
Type "./den --config docs/demo/den.yaml"
Enter
Wait+Screen@20s /Connect/
Show
Sleep 2s

# ── The Connect panel's own keys ─────────────────────────────────────────────
# Connect is the first left-menu item; h lists what its keys do
Right
Sleep 3.5s   # Look: the service list
Sleep 1s     # Act: h opens the pop-up
Type "h"
Sleep 6s     # Read: the panel's keys
Escape
Sleep 1s     # Back

# ── To an RDS service ─────────────────────────────────────────────────────────
# Filter the list down to the RDS rows
Type "/"
Type "rds"
Sleep 1.5s   # Type: the filter result
Sleep 1s     # Act: enter opens the detail view
Enter
Sleep 3.5s   # Look: the detail view. The help line under the box only names the keys.

# ── h: the shortcuts pop-up ───────────────────────────────────────────────────
# Notice: centred over the view, which stays visible behind it. Every key this
# service has, with what it does: the token keys and the terminal tabs are there
# because this is an RDS service.
Sleep 1s     # Act
Type "h"
Sleep 7s     # Read: the service's keys

# esc closes the pop-up and leaves the detail view open
Escape
Sleep 1s     # Back

# ── The Tunnel panel has the pop-up too ───────────────────────────────────────
# Back to the menu, down to Tunnel (the demo config's menu order: connect,
# runbooks, tunnel): h lists the panel's keys, then den's own
Escape
Sleep 1s     # Back
Escape
Sleep 1s     # Back
Down
Sleep 700ms  # Transit
Down
Sleep 2.5s   # Arrive: the Tunnel pane
Right
Sleep 3.5s   # Look: the pane has focus
Sleep 1s     # Act
Type "h"
Sleep 5s     # Read: the panel's keys
Escape
Sleep 1s     # Back

# ── A tunnel has other keys than a database ───────────────────────────────────
# Into the first tunnel's detail view
Sleep 1s     # Act: enter opens the detail view
Enter
Sleep 3.5s   # Look: the detail view

# Notice: here r is the reconnect toggle, with m and H next to it, and there is
# no token.
Sleep 1s     # Act
Type "h"
Sleep 6s     # Read: the tunnel's keys
Escape
Sleep 1s     # Back

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
