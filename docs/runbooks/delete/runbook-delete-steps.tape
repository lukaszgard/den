# The runbook-delete recording's steps, shared by runbook-delete.tape (the README's
# GIF) and runbook-delete-hd.tape (the site video), so the two cannot drift. Sourced
# after their Output line and settings; it has neither of its own.

Env XDG_STATE_HOME "/tmp/den-delete-demo/state"
Env XDG_DATA_HOME "/tmp/den-delete-demo/data"
Env SHELL "/bin/sh"

# D deletes a runbook for good: its folder, its runs and its state folder, after
# a question listing all of it, which only y answers. No AWS: it runs on a copy
# of docs/runbooks/delete/den.yaml and two demo runbooks in /tmp/den-delete-demo,
# so nothing of the repository is deleted. Run from the repository root.

# The build, the copy and the launch are hidden; the recording opens on the
# Runbooks panel.
Hide
Type "rm -rf /tmp/den-delete-demo && mkdir -p /tmp/den-delete-demo/runbooks && cp docs/runbooks/delete/den.yaml /tmp/den-delete-demo/ && cp -R docs/runbooks/demo/env-report docs/runbooks/demo/disk-usage /tmp/den-delete-demo/runbooks/ && rm -rf /tmp/den-delete-demo/runbooks/*/logs && go build -o ./den ./cmd/den && clear"
Enter
Type "./den -c /tmp/den-delete-demo/den.yaml"
Enter
Wait+Screen@60s /Runbooks/
Show
Sleep 2s

# ── Give Env report something to lose ─────────────────────────────────────────
# A run, a log and a state folder, and a shell open in its folder.
Type "l"
Sleep 3s     # Look: the list; Morning check is a sequence of Env report and Disk usage
Down@200ms 2
Sleep 1s     # Act: enter opens Env report
Enter
Sleep 2.5s   # Arrive: its view
Sleep 1s     # Act: r runs it
Type "r"
Sleep 700ms  # Transit: the form
Enter
Wait+Screen@30s /EXIT 0/
Sleep 2s     # Read: the run
Sleep 1s     # Act: t opens a shell in its folder
Type "t"
Sleep 2s     # Arrive: the shell, stopped before the folder goes
Ctrl+T
Sleep 1s     # Back: den has the keys again

# ── The question ──────────────────────────────────────────────────────────────
# Notice: what goes, what stays, and the sequence that stops loading without
# it. enter is not an answer: only y deletes.
Sleep 1s     # Act: D asks
Type "D"
Sleep 6s     # Read: Deletes, Keeps and the warnings
Sleep 1s     # Act: enter is not an answer
Enter
Wait+Screen@5s /kept Env report/
Sleep 700ms  # Read: the flash, which lasts a second

Sleep 1s     # Act
Type "D"
Sleep 700ms  # Transit: the same question
Sleep 1s     # Act: y deletes
Type "y"
Wait+Screen@10s /deleted Env report/
Sleep 3s     # Read: back on the list, Env report gone

# Morning check goes with it, saying why.
Sleep 1s     # Act: w says why
Type "w"
Sleep 3.5s   # Read: Morning check cannot load without Env report
Escape
Sleep 1s     # Back

# ── From a shell ──────────────────────────────────────────────────────────────
# The same from a shell; outside a terminal it needs --yes.
Hide
Type "q"
Sleep 500ms
Type "clear"
Enter
Show
Type@40ms `./den -c /tmp/den-delete-demo/den.yaml runbook delete "Morning check"`
Sleep 1s     # Act
Enter
Wait+Screen@10s /\[y\/N\]/
Sleep 3s     # Read: what goes and what stays
Sleep 1s     # Act: y answers
Type "y"
Enter
Wait+Screen@10s /deleted Morning check/
Sleep 2.5s   # Read: deleted
