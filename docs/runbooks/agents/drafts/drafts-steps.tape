# The drafts recording's steps, shared by drafts.tape (the README's GIF) and
# drafts-hd.tape (the site video), so the two cannot drift. Sourced after their
# Output line and settings; it has neither of its own.

# The state folder holds the drafts; it must outlive den between the two runs.
Env XDG_STATE_HOME "/tmp/den-drafts-demo/state"

# Drafts: what is typed for an agent survives quitting den. esc asks to keep it,
# the draft is a file in den's state folder, and after a restart n offers it
# again, filled in where it was left. Needs an agent CLI installed (claude here)
# so n asks what the agent should write; the request is never sent, so it need
# not be logged in. No AWS. It runs on a copy of this folder's den.yaml and one
# demo runbook in /tmp/den-drafts-demo. Run from the repository root.

# The build, the copy and the first launch are hidden; den is built into the
# demo folder and put first on PATH, so the visible restart is a plain den,
# which finds ./den.yaml itself. The recording opens on the Runbooks panel.
Hide
Type "rm -rf /tmp/den-drafts-demo && mkdir -p /tmp/den-drafts-demo/runbooks && cp docs/runbooks/agents/drafts/den.yaml /tmp/den-drafts-demo/ && cp -R docs/runbooks/demo/env-report /tmp/den-drafts-demo/runbooks/ && rm -rf /tmp/den-drafts-demo/runbooks/*/logs && go build -o /tmp/den-drafts-demo/den ./cmd/den && cd /tmp/den-drafts-demo && export PATH=$PWD:$PATH && clear"
Enter
Type "den"
Enter
Wait+Screen@60s /Runbooks/
Show
Sleep 2s

# ── A request, half written ───────────────────────────────────────────────────
# Notice: a name and a request for the agent, not finished yet.
Type "l"
Sleep 3.5s   # Look: the panel has focus
Sleep 1s     # Act: n starts a new runbook
Type "n"
Sleep 2.5s   # Arrive: the name and the request for the agent
Type "Rotate app password"
Sleep 1.5s   # Type: the name
Enter
Sleep 700ms  # Transit: the request
Type@40ms "rotate the app user's password in Secrets Manager"
Sleep 1s     # Act: esc, before it is finished

# ── Keep it ───────────────────────────────────────────────────────────────────
# Notice: esc does not drop what was typed; it asks to keep it.
Escape
Sleep 4s     # Read: Keep as a draft? y keep, n discard
Type "y"
Sleep 4.5s   # Read: saved as draft, and the draft is the first row

# ── Quit: the draft is a file ─────────────────────────────────────────────────
# Notice: den is gone, and den paths says where the drafts are kept.
Sleep 1s     # Act: q quits den
Type "q"
Wait@10s
Sleep 700ms  # Transit: the shell
Type@40ms "den paths | grep runbook-drafts"
Sleep 1s     # Act
Enter
Wait@10s
Sleep 4s     # Read: the drafts folder, exists, may hold secrets

# ── Start den again ───────────────────────────────────────────────────────────
# Notice: the draft is still the first row.
Type@40ms "den"
Sleep 1s     # Act
Enter
Wait+Screen@20s /Rotate app password/
Sleep 4s     # Read: the draft row after the restart

# ── Pick it up ────────────────────────────────────────────────────────────────
# Notice: n offers the draft beside a new runbook, with its age, and opens it
# where it was left.
Type "l"
Sleep 700ms  # Transit: the panel
Sleep 1s     # Act: n
Type "n"
Sleep 4s     # Read: New runbook, then the draft and its age
Down
Sleep 1s     # Act: the draft
Enter
Sleep 3.5s   # Look: the form, filled in where it was left
Type@40ms ", then print the new version id"
Sleep 4s     # Read: the whole request

# ── End ───────────────────────────────────────────────────────────────────────
# The form is still open, so q alone would be typed into it: esc first, and n
# answers the Keep as a draft? question.
Hide
Escape
Sleep 500ms
Type "n"
Type "q"
Sleep 500ms
