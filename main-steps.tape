# The home recording's steps, shared by main.tape (README GIF) and main-hd.tape
# (site video), so the two cannot drift. Sourced after their Output lines and docs/tape-settings.tape.
#
# Paced for a person watching: every hold names its tier. The tiers and their
# numbers are in AGENTS.md (Feature documentation, the tape); tune them there and here.
#
# The menu order is the demo config's (docs/demo/den.yaml): connect, runbooks,
# tunnel, secrets, aws, vpn, config, help, updates, logs. It needs an SSO login
# first; it connects EU DEV Orders DB, runs check-rds-status and decrypts
# orders - EU STAGING.

# ── Open ──────────────────────────────────────────────────────────────────────
# Launch den hidden, so the video opens on the loaded dashboard, not on the shell.
Hide
Type "./den --config docs/demo/den.yaml"
Enter
Wait+Screen@20s /Connect/
Show
Sleep 2s

# ── Connect ───────────────────────────────────────────────────────────────────
# Notice: the services, the filter narrowing them, one tunnel coming up, and the
# database client opening in a tab of its own, already logged in.
Right
Sleep 3.5s   # Look: the service list
Type "/"
Type "eu dev"
Sleep 1.5s   # Type: the filter result
Sleep 1s     # Act: enter opens the first match, EU DEV Orders DB
Enter
Sleep 1.5s   # Look, short: the connect is the point of this scene
Type "c"
Wait+Screen@60s /Status: \S+ CONNECTED/
Sleep 3s     # Read: status, local port, how to connect
Type "t"     # a terminal tab: the client, already logged in
Wait+Screen@30s /psql \(/
Sleep 5s     # Read: psql, no password typed
Ctrl+T       # back to den, the tab stays
Sleep 1s     # Back
Escape       # detail view -> list
Sleep 1s     # Back
Escape       # list -> menu
Sleep 1s     # Back

# ── Runbooks ──────────────────────────────────────────────────────────────────
# Notice: a runbook run from den, its result, and the tabs around it.
Down
Sleep 2.5s   # Arrive
Right
Sleep 3.5s   # Look: the runbooks
Type "/"
Type "check-rds"
Sleep 1.5s   # Type: the filter result
Enter        # ends the filter, keeps it
Sleep 1s     # Act: enter opens the runbook
Enter
Sleep 2s     # Look: its Logs tab, nothing run yet
Sleep 1s     # Act: r runs it
Type "r"
Wait+Screen@3m /\d+ instances:/
Sleep 4s     # Read: the table of instances
Right
Sleep 2.5s   # Arrive: History
Right
Sleep 2.5s   # Arrive: About
Right
Sleep 2.5s   # Arrive: Files
Left@700ms 3 # Transit: back to Logs
Sleep 2s     # Look: the result again
Escape       # runbook -> list
Sleep 1s     # Back
Tab
Sleep 1s     # Back

# ── Secrets ───────────────────────────────────────────────────────────────────
# Notice: a SOPS file decrypted with your AWS login, values masked.
Down         # past Tunnel
Sleep 700ms  # Transit
Down
Sleep 2.5s   # Arrive
Right
Sleep 3.5s   # Look: the secrets
Type "/"
Type "orders - eu staging"
Sleep 1.5s   # Type: the filter result
Sleep 1s     # Act: enter decrypts it
Enter
Wait+Screen@60s /••••/
Sleep 4s     # Read: the keys, values masked
Escape       # close the secret
Sleep 1s     # Back
Tab
Sleep 1s     # Back

# ── AWS ───────────────────────────────────────────────────────────────────────
Down
Sleep 2.5s   # Arrive
Right
Sleep 3.5s   # Look
Tab
Sleep 1s     # Back

# ── Help ──────────────────────────────────────────────────────────────────────
# Notice: a topic per feature, and a topic is a page you can scroll.
Down         # past VPN
Sleep 700ms  # Transit
Down         # past Config
Sleep 700ms  # Transit
Down
Sleep 2.5s   # Arrive
Right
Sleep 3.5s   # Look: the topic list
Down@200ms 9 # Scroll: from Getting started down to RDS and Aurora
Sleep 1s     # Act: enter opens the topic
Enter
Sleep 3.5s   # Look: What it is
PageDown
Sleep 3.5s   # Read: What you can do
PageDown
Sleep 3.5s   # Read: Before you start
PageDown
Sleep 3.5s   # Read: Configure

# ── End ───────────────────────────────────────────────────────────────────────
# Hide the quit, so the loop never shows the shell.
Hide
Type "q"
Sleep 500ms
