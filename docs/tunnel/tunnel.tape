Output docs/tunnel/tunnel.gif
Output docs/tunnel/tunnel.mp4

Source docs/tape-settings.tape

# Needs: the demo config's "Office Tunnel" (an ssh -D tunnel with a health check
# and auto_connect to the VPN) and a reachable bastion. Rendered by a person
# with `make demo`: it connects a real tunnel.

# Launch den hidden, so the recording opens on the loaded dashboard.
Hide
Type "./den --config docs/demo/den.yaml"
Enter
Wait+Screen@20s /Connect/
Show
Sleep 2s

# ── To the Tunnel pane ────────────────────────────────────────────────────────
# The demo config's menu order: connect, runbooks, tunnel, ...
# Notice: the tunnel list with its idle state and port mappings.
Down@200ms 2
Sleep 2.5s   # Arrive: the Tunnel pane
Right
Sleep 3.5s   # Look: the pane has focus

# ── Runtime toggles ───────────────────────────────────────────────────────────
# Notice: each key flips one dot in the status box, on the still-stopped tunnel.
# The dots track the service itself, so they move even now.
Sleep 1s     # Act: r flips reconnect
Type "r"
Sleep 1s     # Act: m flips the monitor
Type "m"
Sleep 1s     # Act: H flips the health check
Type "H"
Sleep 3s     # Read: all three dots flipped

# Put them back the way den.yaml had them
Type "r"
Sleep 700ms  # Transit
Type "m"
Sleep 700ms  # Transit
Type "H"
Sleep 1s     # Back

# ── Auto-connect with the VPN ─────────────────────────────────────────────────
# Needs auto_connect: {vpn: "Office VPN"} on the tunnel. v flips it; the
# "Auto-connect Office VPN" dot follows.
Sleep 1s     # Act
Type "v"
Sleep 2.5s   # Read: the Auto-connect dot
Sleep 1s     # Act: v flips it back
Type "v"
Sleep 1s     # Back

# ── Connect a tunnel ──────────────────────────────────────────────────────────
# Notice: "connecting" while den checks the ports (and the health check) are
# live; the dot turns green, uptime starts, the port mappings stay in view.
Sleep 1s     # Act: c connects
Type "c"
# Not /connected/: the VPN warning "Office VPN is not connected" matches it too.
Wait+Screen@60s /uptime: \d\d:/
Sleep 3s     # Read: connected, uptime, port mappings

# ── Open the detail view ──────────────────────────────────────────────────────
# Notice: the log shows the connection checks: "waiting for :5555…", "verified: …".
Sleep 1s     # Act: enter opens the detail view
Enter
Sleep 4s     # Read: the log
Escape
Sleep 1s     # Back

# ── Disconnect ────────────────────────────────────────────────────────────────
Sleep 1s     # Act: d disconnects
Type "d"
Wait+Screen@20s /idle/
Sleep 2s     # Read: idle again

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
