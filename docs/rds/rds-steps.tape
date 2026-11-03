# The rds recording's steps, shared by rds.tape (the README's GIF) and rds-hd.tape
# (the site video), so the two cannot drift. Sourced after their Output line and
# settings; it has neither of its own.

# Launch den hidden, so the recording opens on the loaded dashboard.
Hide
Type "./den --config docs/demo/den.yaml"
Enter
Wait+Screen@20s /Connect/
Show
Sleep 2s

# ── Filter ────────────────────────────────────────────────────────────────────
# Notice: searching matches on the type, so "rds" hides the redis and docdb rows.
Right
Sleep 3.5s   # Look: the service list
Type "/"
Type "rds"
Sleep 1.5s   # Type: the filter result
Sleep 1s     # Act: enter opens the detail view
Enter
Sleep 3.5s   # Look: the detail view, not connected yet

# ── Connect ───────────────────────────────────────────────────────────────────
# Notice: the tunnel opens, the port is checked, then the IAM token is minted and ~/.pgpass rewritten (the log shows each step). The token bar counts down 15 minutes; den re-mints it at 13.
Sleep 1s     # Act: c connects
Type "c"
Wait+Screen@60s /Status: \S+ CONNECTED/
Sleep 6s     # Read: the log, the connect command, the token bar

# Copy the command
Sleep 1s     # Act
Type "y"
Sleep 2s     # Read: the status bar flashes "copied!"

# Copy the command with the token inline
Sleep 1s     # Act
Type "Y"
Sleep 2s     # Read: the status bar flashes "copied with password!"

# Re-mint the token on demand
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

# ── A password from a secret ──────────────────────────────────────────────────
# Notice: no token bar. The Login section says which secret the password comes from, and t logs in with nothing written to a file. Needs the secret /acme/legacy/dev/db-login in the demo account.
Escape       # clears the filter
Sleep 1s     # Back
Type "/"
Type "password"
Sleep 1.5s   # Type: the filter result
Sleep 1s     # Act: enter opens the detail view
Enter
Sleep 3.5s   # Look: the Login section instead of the token bar
Sleep 1s     # Act: c connects
Type "c"
Wait+Screen@60s /Status: \S+ CONNECTED/
Sleep 4.5s   # Read: the log says the password was read, the Login section when
Sleep 1s     # Act: t opens the client
Type "t"
Wait+Screen@20s /=>/
Sleep 4s     # Read: psql is logged in, nobody typed a password
Escape       # keyboard back to den
Sleep 1s     # Back
Sleep 1s     # Act: d disconnects, and asks first while the client runs
Type "d"
Sleep 1s     # Act: y confirms
Type "y"
Wait+Screen@20s /Status: \S+ IDLE/
Sleep 2s     # Read: idle again
Escape       # detail view -> list
Sleep 1s     # Back

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
