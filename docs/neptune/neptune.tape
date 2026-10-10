Output docs/neptune/neptune.gif
Output docs/neptune/neptune.mp4

Source docs/tape-settings.tape

# Needs: a valid AWS SSO login and a reachable bastion, as in the demo config
# (docs/demo/den.yaml). Rendered by a person with `make demo`: it drives the
# real app against the demo AWS environment.

# Launch den hidden, so the recording opens on the loaded dashboard.
Hide
Type "./den --config docs/demo/den.yaml"
Enter
Wait+Screen@20s /Connect/
Show
Sleep 2s

# ── Filter ────────────────────────────────────────────────────────────────────
# Notice: searching matches on the type, so "neptune" isolates its rows.
Right
Sleep 3.5s   # Look: the service list
Type "/"
Type "neptune"
Sleep 1.5s   # Type: the filter result
Sleep 1s     # Act: enter opens the detail view
Enter
Sleep 3.5s   # Look: the detail view, not connected yet

# ── Connect ───────────────────────────────────────────────────────────────────
# Notice: the tunnel opens on a private port, the signing identity's credentials are checked, then the signing proxy starts on local_port. No token bar: every request is signed on the fly, so there is nothing to mint or refresh.
Sleep 1s     # Act: c connects
Type "c"
Wait+Screen@60s /Status: \S+ CONNECTED/
Sleep 6s     # Read: the log and the command, no token bar

# Copy the command
Sleep 1s     # Act
Type "y"
Sleep 2s     # Read: the status bar flashes "copied!"

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
