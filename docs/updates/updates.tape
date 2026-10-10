Output docs/updates/updates.gif
Output docs/updates/updates.mp4

Source docs/tape-settings.tape

# Needs a den build older than the latest release, and access to the release
# source (GitHub Releases, or GITHUB_TOKEN for a private fork). Build one first:
#   go build -ldflags "-X main.version=0.0.1" -o /tmp/den-old/den ./cmd/den
# The install replaces /tmp/den-old/den, not the den on PATH.
#
# Kept under a minute: answering the restart prompt "later" and reopening it
# (n, then r) is in docs/updates/README.md.

# Launch den hidden, so the recording opens on the loaded dashboard.
Hide
Type "/tmp/den-old/den -c docs/demo/den.yaml"
Enter
Wait+Screen@20s /Connect/
Show
Sleep 2s

# ── Open one connection ───────────────────────────────────────────────────────
# Notice: so the restart prompt has something to list.
Right
Sleep 3.5s   # Look: the service list
Sleep 1s     # Act: c connects the first service
Type "c"
# The row shows the token's time left instead of "connected" once it is minted.
Wait+Screen@60s /token: \d+m/
Sleep 2.5s   # Read: the service is connected

# ── To the Updates pane ───────────────────────────────────────────────────────
# Back to the menu. In the demo config's menu order Updates is the ninth entry,
# just before Logs, so eight Downs from Connect; the startup check has already run.
Tab
Sleep 1s     # Back
Down@200ms 8
Sleep 2.5s   # Arrive: the Updates pane
Right
Wait+Screen@30s /available/
Sleep 3.5s   # Look: the new version, the install steps

# ── What is new ───────────────────────────────────────────────────────────────
# Notice: the release's notes, on top of the changelog.
Sleep 1s     # Act: s opens the notes
Type "s"
Sleep 3.5s   # Look: the notes
PageDown
Sleep 2.5s   # Read: the next page
Escape
Sleep 1s     # Back

# ── Install: download, checksum, replace ──────────────────────────────────────
# Notice: the prompt lists the connection a restart would close.
Sleep 1s     # Act: i installs
Type "i"
Wait+Screen@120s /Restart den now/
Sleep 5s     # Read: the connection a restart closes, and that it will not reconnect

# ── Restart into the new version ──────────────────────────────────────────────
Sleep 1s     # Act: only y restarts, not enter
Type "y"
Sleep 3.5s   # Look: den is back, in the new version

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
