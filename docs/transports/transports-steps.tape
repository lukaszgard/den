# The transports recording's steps, shared by transports.tape (the README's GIF) and
# transports-hd.tape (the site video), so the two cannot drift. Sourced after their
# Output line and settings; it has neither of its own.

# Needs the demo config's ssh-transport environment (eu-lab) and a reachable
# bastion (see docs/transports/README.md), and a logged-in profile for the credential.
# Rendered by a person with `make demo`.

# Launch den hidden, so the recording opens on the loaded dashboard.
Hide
Type "./den -c docs/demo/den.yaml"
Enter
Wait+Screen@20s /Connect/
Show
Sleep 2s

# ── The ssh-transport service ─────────────────────────────────────────────────
# Connect pane: filter to the ssh-transport service.
Right
Sleep 3.5s   # Look: the service list
Type "/"
Type "lab"
Sleep 1.5s   # Type: the filter result
Sleep 1s     # Act: enter opens the detail view
Enter
Sleep 3.5s   # Look: the detail view, not connected yet

# ── Connect ───────────────────────────────────────────────────────────────────
# Notice: the log shows the ssh transport starting and the port opening.
Sleep 1s     # Act: c connects
Type "c"
Wait+Screen@60s /Status: \S+ CONNECTED/
Sleep 5s     # Read: the log, the ssh command, the connect command

# ── Disconnect ────────────────────────────────────────────────────────────────
Sleep 1s     # Act: d disconnects
Type "d"
Wait+Screen@20s /Status: \S+ IDLE/
Sleep 2s     # Read: idle again

Escape       # detail view -> list
Sleep 1s     # Back

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
