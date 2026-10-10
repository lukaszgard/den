Output docs/runbooks/agents/panel/agent-panel.gif
Output docs/runbooks/agents/panel/agent-panel.mp4

Source docs/tape-settings.tape

Env XDG_STATE_HOME "/tmp/den-demo-state"
Set WaitTimeout 240s

# Way 2: n in the Runbooks panel. Needs Claude Code (logged in); no AWS. n asks for
# a name and, because an agent is installed, what the runbook should do; claude then
# works in a terminal tab of the new runbook's view, in its folder. Its
# .claude/settings.json (from runbooks.template) pre-allows the writes and commands
# so the recording runs through; in normal use claude asks. In that tab one esc
# is claude's; esc twice or ctrl+t gives den the keys back.

# The build and the launch are hidden; the recording opens on the Runbooks panel.
Hide
Type "unset CLAUDE_CODE_CHILD_SESSION CLAUDECODE CLAUDE_CODE_ENTRYPOINT"
Enter
Type "rm -rf /tmp/den-demo-state docs/runbooks/agents/panel/runbooks && go build -o ./den ./cmd/den && clear"
Enter
Type "./den -c docs/runbooks/agents/panel/den.yaml"
Enter
Wait+Screen@120s /Runbooks/
Show
Sleep 2s

# ── A new runbook ─────────────────────────────────────────────────────────────
# Notice: n asks for a name, then, because an agent is installed, what it
# should do.
Type "l"
Sleep 3s     # Look: the panel has focus
Sleep 1s     # Act: n starts a new runbook
Type "n"
Sleep 2s     # Read: the name pop-up, "Goes into <folder>"
Type "Disk report"
Sleep 1.5s   # Type: the name
Enter
Sleep 700ms  # Transit: the second question
Type@40ms "the 5 largest files directly in /usr/bin, in Python, as a small table (a quick check, no deep scan)"
Sleep 1s     # Act
Enter

# claude works in a terminal tab of the new runbook's view; the Wait is its own
# time, not a hold.
Wait+Screen@10m /done [0-9]+:[0-9]+ [AP]M/
Sleep 4s     # Read: what claude wrote
Ctrl+T
Sleep 1s     # Act: den has the keys again; r runs the new runbook
Type "r"
Wait+Screen@120s /EXIT 0/

# ── Its output ────────────────────────────────────────────────────────────────
# Logs is the first tab: the new runbook's output.
Left@200ms 4
Sleep 6s     # Read: the table the runbook prints
Escape
Sleep 1s     # Back: the list

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
