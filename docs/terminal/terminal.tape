Output docs/terminal/terminal.gif
Output docs/terminal/terminal.mp4

Source docs/tape-settings.tape

# Needs: a valid AWS SSO login, an RDS PostgreSQL service in the demo config
# (docs/demo/den.yaml) that the login may connect to, and psql installed.
# Rendered by a person with `make demo`: it drives the real app against the
# demo AWS environment.
#
# Kept to the terminal tab's core (open, query, pager, leave, disconnect) so it
# stays under a minute. The second terminal, the rename, the digit jump, the
# restart of an exited client and closing a tab are in docs/terminal/README.md.

# Launch den hidden, so the recording opens on the loaded dashboard.
Hide
Type "./den --config docs/demo/den.yaml"
Enter
Wait+Screen@20s /Connect/
Show
Sleep 2s

# ── To the RDS service ────────────────────────────────────────────────────────
Right
Sleep 3.5s   # Look: the service list
Type "/"
Type "rds"
Sleep 1.5s   # Type: the filter result
Sleep 1s     # Act: enter opens the detail view
Enter
Sleep 2s     # Look, short: the connect is the point of this scene

# ── Connect ───────────────────────────────────────────────────────────────────
# Notice: the tunnel opens, the port is checked, the IAM token is minted.
Sleep 1s     # Act: c connects
Type "c"
Wait+Screen@60s /Status: \S+ CONNECTED/
Sleep 2s     # Read: the connect command and the logs

# ── t: a terminal tab, logged in ──────────────────────────────────────────────
# Notice: psql starts next to the logs with the token den just minted. The help
# line reads TERMINAL: every key is psql's now.
Type "t"
Wait+Screen@30s /psql \(/
Sleep 3.5s   # Look: psql, no password typed

Type@40ms "select current_user, now();"
Sleep 1s     # Act
Enter
Sleep 2.5s   # Read: the result

# A result longer than the tab goes through psql's pager, which knows the tab's size.
Type@40ms "select * from generate_series(1, 200);"
Sleep 1s     # Act
Enter
Sleep 2.5s   # Read: the pager
Sleep 1s     # Act: q leaves the pager
Type "q"
Sleep 1s     # Back

# ── esc: the keyboard back to den ─────────────────────────────────────────────
# Notice: psql keeps running; the tab stays on screen and the help line lists
# den's keys. Inside the pager a moment ago esc was the pager's: there ctrl+t leaves.
Escape
Sleep 3.5s   # Look: the help line changed

# ── Disconnect stops the clients and keeps the tabs ───────────────────────────
# Notice: d asks first, because a terminal is still running.
Sleep 1s     # Act: d disconnects
Type "d"
Wait+Screen@10s /it stops \d+ running/
Sleep 2.5s   # Read: the question
Sleep 1s     # Act: y confirms
Type "y"
Wait+Screen@20s /Status: \S+ IDLE/
Sleep 2.5s   # Read: idle, the terminal tab still there

Escape       # detail view -> list
Sleep 1s     # Back

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
