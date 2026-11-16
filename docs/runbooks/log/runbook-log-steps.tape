# The runbook-log recording's steps, shared by runbook-log.tape (the README's GIF)
# and runbook-log-hd.tape (the site video), so the two cannot drift. Sourced after
# their Output line and settings; it has neither of its own.

Env XDG_STATE_HOME "/tmp/den-log-demo/state"
Env SHELL "/bin/sh"

# A run's log read in place, as in k9s: / lights up the matches, n goes from one
# to the next, f shows the log alone, T hides the times, esc steps back, c copies
# it whole. No AWS: it runs on docs/runbooks/log/den.yaml, whose one runbook
# prints a long log of its own. Run from the repository root.

# The build and the launch are hidden; the recording opens on the Runbooks panel.
Hide
Type "rm -rf /tmp/den-log-demo && go build -o ./den ./cmd/den && clear"
Enter
Type "./den -c docs/runbooks/log/den.yaml"
Enter
Wait+Screen@60s /Runbooks/
Show
Sleep 2s

# ── A long run ────────────────────────────────────────────────────────────────
# Notice: the log follows the run as it goes, too long for the screen.
Type "l"
Sleep 1s     # Act: enter opens Migrate orders
Enter
Sleep 3.5s   # Look: its view, on Run
Sleep 1s     # Act: r runs it
Type "r"
Wait+Screen@30s /EXIT 0/
Sleep 2.5s   # Read: the end of the log, and the header saying how it ended

# ── Search it ─────────────────────────────────────────────────────────────────
# Notice: every match lights up as the query is typed; n jumps to the next one,
# and the log's line under the box counts them.
Sleep 1s     # Act: / starts a search
Type "/"
Type "warn"
Sleep 1.5s   # Type: the warnings light up
Enter
Sleep 1s     # Act: n goes to the next match
Type "n"
Sleep 2.5s   # Read: the match, and which of the four it is on the log's line

# ── Full screen, without the times ────────────────────────────────────────────
# Notice: f leaves only the log, the search still on; T hides the times.
Sleep 1s     # Act: f shows the log alone
Type "f"
Sleep 3.5s   # Look: the log fills the box
Sleep 1s     # Act: T hides the timestamps
Type "T"
Sleep 3s     # Read: the lines without their times, and T: show time to bring them back

# ── Back, and a copy ──────────────────────────────────────────────────────────
# Notice: esc ends the search, then the full screen; c copies the whole log.
Escape
Sleep 1s     # Back: the search ends
Escape
Sleep 1s     # Back: the header and the tabs
Sleep 1s     # Act: c copies the log
Type "c"
Wait+Screen@5s /copied \d+ lines/
Sleep 2s     # Read: the flash

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
