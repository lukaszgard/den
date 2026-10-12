# The redshift recording's steps, shared by redshift.tape (the README's GIF) and
# redshift-hd.tape (the site video), so the two cannot drift. Sourced after their
# Output line and settings; it has neither of its own.

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
# Notice: searching matches on the type, so "redshift" isolates the redshift rows.
Right
Sleep 3.5s   # Look: the service list
Type "/"
Type "redshift"
Sleep 1.5s   # Type: the filter result
Sleep 1s     # Act: enter opens the detail view
Enter
Sleep 3.5s   # Look: the detail view, not connected yet

# ── Connect ───────────────────────────────────────────────────────────────────
# Notice: the tunnel opens, then temporary credentials are minted and written to ~/.pgpass. The log names the IAMR:/IAM: user Redshift chose. The bar is scaled to credential_ttl, not the RDS 15 minutes.
Sleep 1s     # Act: c connects
Type "c"
Wait+Screen@60s /Status: \S+ CONNECTED/
Sleep 6s     # Read: the log, the psql command, the TTL bar

# Copy the psql command
Sleep 1s     # Act
Type "y"
Sleep 2s     # Read: the status bar flashes "copied!"

# Copy the command with the password inline
Sleep 1s     # Act
Type "Y"
Sleep 2s     # Read: the status bar flashes "copied with password!"

# Re-mint on demand
Sleep 1s     # Act
Type "r"
Sleep 2s     # Read: the bar fills up again

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
