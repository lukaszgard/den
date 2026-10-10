Output docs/runbooks/agents/cli/agent-cli.gif
Output docs/runbooks/agents/cli/agent-cli.mp4

Source docs/tape-settings.tape

Env XDG_STATE_HOME "/tmp/den-demo-state"
Set WaitTimeout 240s

# Way 1: den runbook new --agent. Needs Claude Code (logged in) and cargo; no AWS.
# den makes the folder, starts claude in it with its MCP server and runbook guide,
# then reports whether the result loads. The folder's .claude/settings.json (from
# runbooks.template) pre-allows file writes and the build, so the recording runs
# through; in normal use claude asks before each of them.

# The build is hidden; "built-2" is printed by the shell once it is done (the
# typed command spells it $((1+1))), so the recording opens when den is ready.
Hide
Type "unset CLAUDE_CODE_CHILD_SESSION CLAUDECODE CLAUDE_CODE_ENTRYPOINT"
Enter
Type "rm -rf /tmp/den-demo-state docs/runbooks/agents/cli/runbooks && go build -o ./den ./cmd/den && echo built-$((1+1))"
Enter
Wait+Screen@120s /built-2/
Type "clear"
Enter
Show
Sleep 1s

# ── The request ───────────────────────────────────────────────────────────────
# Notice: a name and what the runbook should do, on the command line.
Type@40ms "./den runbook new 'Env report' 'print AWS_PROFILE, AWS_REGION and DEN_EC2_INSTANCE_ID as a small table, in Rust' --agent claude -c docs/runbooks/agents/cli/den.yaml"
Sleep 1s     # Act
Enter

# claude works in the new folder; the Wait is its own time, not a hold.
Wait+Screen@10m /done [0-9]+:[0-9]+ [AP]M/
Sleep 4s     # Read: what claude wrote
Escape
Sleep 1s     # Act: leave claude
Type@40ms "/exit"
Sleep 1s     # Act
Enter

# ── den checks the folder ─────────────────────────────────────────────────────
# Notice: den says whether the folder loads, then runs it like any runbook.
Wait+Screen@30s /is ready/
Sleep 3s     # Read: the folder is ready, with the command to run it
Type "clear"
Enter
Type@40ms "./den run 'Env report' -c docs/runbooks/agents/cli/den.yaml"
Sleep 1s     # Act
Enter
Wait+Screen@120s /DEN_EC2_INSTANCE_ID/
Sleep 4s     # Read: the table the agent's runbook prints
