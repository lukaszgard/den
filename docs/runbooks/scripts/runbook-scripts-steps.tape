# The runbook-scripts recording's steps, shared by runbook-scripts.tape (the
# README's GIF) and runbook-scripts-hd.tape (the site video), so the two cannot
# drift. Sourced after their Output line and settings; it has neither of its own.

Env XDG_STATE_HOME "/tmp/den-scripts-demo/state"
Env XDG_DATA_HOME "/tmp/den-scripts-demo/data"
Env SHELL "/bin/sh"
Env VISUAL "vi"

# A script with a few `# den:` lines is a runbook: listed, given a form, run in
# the environment it names. m moves it into a folder, keeping its history. No
# AWS: it runs on a copy of docs/runbooks/scripts/den.yaml and its two scripts in
# /tmp/den-scripts-demo, so nothing of the repository moves. Run from the
# repository root.

# The build, the copy and the launch are hidden; the recording opens on the
# Runbooks panel.
Hide
Type "rm -rf /tmp/den-scripts-demo && mkdir -p /tmp/den-scripts-demo/scripts && cp docs/runbooks/scripts/den.yaml /tmp/den-scripts-demo/ && cp docs/runbooks/scripts/*.sh /tmp/den-scripts-demo/scripts/ && go build -o ./den ./cmd/den && clear"
Enter
Type "./den -c /tmp/den-scripts-demo/den.yaml"
Enter
Wait+Screen@60s /Runbooks/
Show
Sleep 2s

# ── Two loose scripts ─────────────────────────────────────────────────────────
# Notice: nothing but the scripts sits in the directory; each is a row.
Type "l"
Sleep 3.5s   # Look: the table, two scripts

# ── Run one ───────────────────────────────────────────────────────────────────
# Notice: the `den:param` line became a form, `den:env` the account it runs in.
Sleep 1s     # Act: enter opens Check bastion health
Enter
Sleep 3.5s   # Look: the view, on Logs
Sleep 1s     # Act: r runs it
Type "r"
Sleep 3s     # Read: the form, with the tier it asks for
Sleep 1s     # Act: enter accepts the default
Enter
Wait+Screen@30s /EXIT 0/
Sleep 3s     # Read: the output, with the environment's instance and profile
Escape
Sleep 1s     # Back: the list

# ── Move it into a folder ─────────────────────────────────────────────────────
# Notice: what the folder gets, and that the script itself goes.
Sleep 1s     # Act: m asks
Type "m"
Wait+Screen@5s /Move into its own folder/
Sleep 5s     # Read: runbook.yaml, run.sh and README.md, made from the script
Sleep 1s     # Act: y moves it
Type "y"
Wait+Screen@10s /moved Check bastion health/
Sleep 2s     # Read: the row stays, now in a folder

# ── It keeps its history ──────────────────────────────────────────────────────
# Notice: the run moved into the folder's logs/, and Files shows the folder.
Sleep 1s     # Act: enter opens it
Enter
Sleep 2.5s   # Arrive: the view
Right
Sleep 3.5s   # Look: History, the run from before the move
Right@200ms 2
Sleep 3.5s   # Read: Files, runbook.yaml, run.sh, README.md and logs/
Escape
Sleep 1s     # Back: the list

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
