# The runbooks recording's steps, shared by runbooks.tape (the README's GIF) and
# runbooks-hd.tape (the site video), so the two cannot drift. Sourced after their
# Output line and settings; it has neither of its own.

Env XDG_STATE_HOME "/tmp/den-demo-state"
Env SHELL "/bin/sh"
Env VISUAL "vi"

# The main idea: runbooks in folders, run with den's context, with a shell and
# an editor where they live. Runs on docs/runbooks/den.yaml (placeholder
# environments, no AWS, nothing connects), so it renders anywhere. Run from the
# repository root; the agent ways have their own tapes under agents/.

# The build and the launch are hidden; the recording opens on the Runbooks panel.
Hide
Type "rm -rf /tmp/den-demo-state docs/runbooks/demo/*/logs && go build -o ./den ./cmd/den && clear"
Enter
Type "./den -c docs/runbooks/den.yaml"
Enter
Wait+Screen@60s /Runbooks/
Show
Sleep 2s

# ── The table ─────────────────────────────────────────────────────────────────
# Notice: what each runbook runs, which account, and its last run.
Type "l"
Sleep 3.5s   # Look: the table, with focus

# ── Open one and run it ───────────────────────────────────────────────────────
# Notice: logs, history, about, files and terminals are tabs of its view.
Down@200ms 4 # past Bastion status, CloudWatch alarms and Disk usage
Sleep 1s     # Act: enter opens Env report
Enter
Sleep 3.5s   # Look: the view, on Logs
Sleep 1s     # Act: r runs it
Type "r"
Sleep 2s     # Read: the parameter form
Sleep 1s     # Act: enter accepts the default
Enter
Wait+Screen@30s /EXIT 0/
Sleep 3s     # Read: the run's output, and the header saying how it ended

# ── Run it again ──────────────────────────────────────────────────────────────
# Notice: each run has a log of its own. History lists the runs with the
# picked one's log under them; enter shows it whole, esc goes back to the runs.
Sleep 1s     # Act
Type "r"
Sleep 700ms  # Transit: the form
Enter
Wait+Screen@30s /EXIT 0/
Sleep 2s     # Read: the second run's log
Right
Sleep 3.5s   # Look: History, two runs
Down
Sleep 1s     # Act: enter opens the log of the other run
Enter
Sleep 3s     # Read: the whole log
Escape
Sleep 1s     # Back: the runs

# ── What den sets for it ──────────────────────────────────────────────────────
Right
Sleep 3.5s   # Read: About, the environment the run gets

# ── Its folder ────────────────────────────────────────────────────────────────
# Notice: the folder as a tree with the runs folded into logs/. enter shows a
# file; e opens it in the editor, in a tab of its own that closes with the
# editor, back on Files.
Right
Sleep 2.5s   # Arrive: Files
Down@200ms 2
Sleep 1s     # Act: enter previews the file
Enter
Sleep 3s     # Look: the preview
Sleep 1s     # Act: e opens the editor
Type "e"
Sleep 2.5s   # Read: the editor tab
Type ":q"
Enter
Sleep 1s     # Back: Files again

# ── A shell where it lives ────────────────────────────────────────────────────
# Notice: the shell starts in the runbook's folder with its environment.
Left@200ms 3 # Transit: back on Logs
Sleep 1s     # Act: t opens a terminal
Type "t"
Sleep 2.5s   # Arrive: the shell
Type@40ms "pwd; echo $AWS_PROFILE $DEN_RUNBOOK"
Sleep 1s     # Act
Enter
Wait+Screen@10s /orders-e2e/
Sleep 3s     # Read: the folder, the profile, the runbook name
Ctrl+T
Sleep 1s     # Back: den has the keys again
Escape
Sleep 1s     # Back: the list

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
