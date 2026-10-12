Output docs/runbooks/agents/drafts/drafts.gif
Output docs/runbooks/agents/drafts/drafts.mp4

Source docs/tape-settings.tape

Env XDG_STATE_HOME "/tmp/den-drafts-demo/state"

# Drafts: what is typed for an agent is kept. esc asks to keep it, the draft is a
# row of the list, n offers it again, D deletes it. Needs an agent CLI installed
# (claude here) so n asks what the agent should write; the request is never sent,
# so it need not be logged in. No AWS. It runs on a copy of this folder's den.yaml
# and one demo runbook in /tmp/den-drafts-demo. Run from the repository root.

# The build, the copy and the launch are hidden; the recording opens on the
# Runbooks panel.
Hide
Type "rm -rf /tmp/den-drafts-demo && mkdir -p /tmp/den-drafts-demo/runbooks && cp docs/runbooks/agents/drafts/den.yaml /tmp/den-drafts-demo/ && cp -R docs/runbooks/demo/env-report /tmp/den-drafts-demo/runbooks/ && rm -rf /tmp/den-drafts-demo/runbooks/*/logs && go build -o ./den ./cmd/den && clear"
Enter
Type "./den -c /tmp/den-drafts-demo/den.yaml"
Enter
Wait+Screen@60s /Runbooks/
Show
Sleep 2s

# ── A request, half written ───────────────────────────────────────────────────
# Notice: esc does not drop what was typed; it asks to keep it.
Type "l"
Sleep 3.5s   # Look: the panel has focus
Sleep 1s     # Act: n starts a new runbook
Type "n"
Sleep 2.5s   # Arrive: the name and the request for the agent
Type "Rotate app password"
Sleep 1.5s   # Type: the name
Enter
Sleep 700ms  # Transit: the request
Type@40ms "rotate the app user's password in Secrets Manager, then print"
Sleep 1s     # Act: esc, before it is finished
Escape
Sleep 2.5s   # Read: Keep as a draft? y keep, n discard
Type "y"
Sleep 3s     # Read: saved as draft, and the draft is the first row

# ── Away and back ─────────────────────────────────────────────────────────────
# Notice: the draft waits while something else has the screen.
Tab
Sleep 700ms  # Transit: the menu
Down
Sleep 2.5s   # Arrive: the Help panel
Up
Sleep 700ms  # Transit: back to Runbooks
Tab
Sleep 1s     # Back: the list, the draft still on top

# ── Go on with it ─────────────────────────────────────────────────────────────
# Notice: n offers the draft beside a new runbook, with its age.
Type "n"
Sleep 3.5s   # Read: New runbook, then the draft
Down
Sleep 1s     # Act: the draft
Enter
Sleep 3.5s   # Look: the form, filled in where it was left
Type@40ms " the new version id"
Sleep 1.5s   # Type: the rest of the request
Escape
Sleep 1s     # Act: keep it again
Type "y"
Sleep 2.5s   # Read: one draft, its request longer

# ── Done with it ──────────────────────────────────────────────────────────────
# Notice: D on a draft asks, then deletes only the draft.
Type "g"
Sleep 1s     # Act: the cursor on the draft, the first row
Type "D"
Sleep 3s     # Read: Delete the draft "Rotate app password"? y/n
Type "y"
Sleep 2.5s   # Read: draft deleted; the runbook list as it was

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
