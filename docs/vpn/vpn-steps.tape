# The vpn recording's steps, shared by vpn.tape (the README's GIF) and vpn-hd.tape
# (the site video), so the two cannot drift. Sourced after their Output line and
# settings; it has neither of its own.

# Needs: the demo config with its "Office VPN" entry's gateway set to a real
# FortiGate (openfortivpn + SAML; do not commit it) and a person to finish the
# SAML login in the browser. Rendered by a person with `make demo`:
# the connect waits up to three minutes for that login, which is not part of
# the recording's pace.

# ── One-time setup: the privilege helper ─────────────────────────────────────
# With it installed, connect and disconnect never ask for a password. Without
# it, both show the OS admin dialog. `den vpn status` says which applies.
Type@40ms "./den vpn status"
Sleep 1s     # Act
Enter
Wait@10s
Sleep 4s     # Read: whether the helper is installed

# Launch den hidden, so the recording moves on to the loaded dashboard.
Hide
Type "clear"
Enter
Type "./den --config docs/demo/den.yaml"
Enter
Wait+Screen@20s /Connect/
Show
Sleep 2s

# ── To the VPN service in the Connect panel ──────────────────────────────────
# Connect is the default panel; the filter matches on the type, so "vpn" leaves
# the VPN row alone however long the list is.
Right
Sleep 3.5s   # Look: the service list
Type "/"
Type "vpn"
Sleep 1.5s   # Type: the filter result
Sleep 1s     # Act: enter opens the detail view
Enter
Sleep 3.5s   # Look: the detail view, not connected yet

# ── Runtime toggles ──────────────────────────────────────────────────────────
# Notice: reconnect and the health check flip on the still-idle VPN (r and H work
# in the detail view too). The dots read from the service itself, so they move
# even before anything is connected.
Sleep 1s     # Act: r flips reconnect
Type "r"
Sleep 1s     # Act: H flips the health check
Type "H"
Sleep 3s     # Read: both dots flipped

# Put them back the way den.yaml had them
Type "r"
Sleep 700ms  # Transit
Type "H"
Sleep 1s     # Back

# ── Connect ──────────────────────────────────────────────────────────────────
# With the helper installed this starts straight away. Without it, the OS shows
# its native admin password dialog outside the TUI first.
Sleep 1s     # Act: c connects
Type "c"

# ── SAML browser handoff ─────────────────────────────────────────────────────
# Notice: the log shows "Authenticate at 'https://...'" and "opening SAML login
# URL in browser".
Wait+Screen@60s /opening SAML login URL/
Sleep 4s     # Read: the log

# ── Connected state ──────────────────────────────────────────────────────────
# After the SAML login completes, "Tunnel is up and running." appears and the
# status turns green. The wait covers the person finishing the login.
Wait+Screen@180s /Status: \S+ CONNECTED/
Sleep 3s     # Read: connected

# ── Disconnect — also prompt-free with the helper installed ─────────────────
Sleep 1s     # Act: d disconnects
Type "d"
Wait+Screen@30s /Status: \S+ IDLE/
Sleep 2s     # Read: idle again

Escape       # detail view -> list
Sleep 1s     # Back

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
