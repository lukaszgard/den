Output docs/docker/docker.gif
Output docs/docker/docker.mp4

Source docs/tape-settings.tape

# Needs Docker with the Compose plugin running, and no AWS: the stack is the
# example next to this tape.

# Launch den hidden on a Compose file, without a den.yaml entry.
Hide
Type "./den create-env -f docs/docker/compose.yml"
Enter
Wait+Screen@20s /Connect/
Show
Sleep 2s

# ── Detail view ───────────────────────────────────────────────────────────────
# Notice: `docker compose up -d` starts the moment den opens; the log shows its
# output, and connected means the command returned.
Right
Sleep 3.5s   # Look: the service list
Sleep 1s     # Act: enter opens the detail view
Enter
Wait+Screen@120s /Status: \S+ CONNECTED/
Sleep 5s     # Read: the compose output in the log

# ── Copy `docker compose ps` ──────────────────────────────────────────────────
Sleep 1s     # Act
Type "y"
Sleep 2s     # Read: the status bar flashes "copied!"

# ── Disconnect ────────────────────────────────────────────────────────────────
# Notice: den stops managing the stack; the containers keep running (docker compose down).
# Docker's Stop sends no status update, so the header may still read CONNECTED: no Wait here.
Sleep 1s     # Act: d disconnects
Type "d"
Sleep 2s     # Read: the stop happens, the containers keep running

Escape       # detail view -> list
Sleep 1s     # Back

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
