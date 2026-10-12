# The elasticache recording's steps, shared by elasticache.tape (the README's GIF)
# and elasticache-hd.tape (the site video), so the two cannot drift. Sourced after
# their Output line and settings; it has neither of its own.

# Launch den hidden, so the recording opens on the loaded dashboard.
Hide
Type "./den --config docs/demo/den.yaml"
Enter
Wait+Screen@20s /Connect/
Show
Sleep 2s

# ── Filter ────────────────────────────────────────────────────────────────────
# Notice: searching matches on the type: "redis" shows the ElastiCache caches (and any MemoryDB, which is the same type).
Right
Sleep 3.5s   # Look: the service list
Type "/"
Type "redis"
Sleep 1.5s   # Type: the filter result
Sleep 1s     # Act: enter opens the detail view
Enter
Sleep 3.5s   # Look: the detail view, not connected yet

# ── Connect ───────────────────────────────────────────────────────────────────
# Notice: the tunnel opens, then an IAM token signed for elasticache is minted. The token bar counts down 15 minutes; den re-mints it at 13.
Sleep 1s     # Act: c connects
Type "c"
Wait+Screen@60s /Status: \S+ CONNECTED/
Sleep 6s     # Read: the log, the redis-cli command, the token bar

# Copy the redis-cli command; with a token it carries it as the password
Sleep 1s     # Act
Type "y"
Sleep 2s     # Read: the status bar flashes "copied with password!"

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

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
