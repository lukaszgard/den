Output docs/runbooks/agents/mcp/agent-mcp.gif
Output docs/runbooks/agents/mcp/agent-mcp.mp4

Source docs/tape-settings.tape

Env XDG_STATE_HOME "/tmp/den-demo-state"
Set WaitTimeout 240s

# Way 3: den registered with your agent once, then den's prompt. Needs Claude Code
# (logged in) and go; no AWS. den mcp is a server the agent starts itself: it is
# registered, not piped in. The folder's .claude/settings.json pre-allows the
# writes and den's tools so the recording runs through; in normal use claude asks.

# The build is hidden; "built-2" is printed by the shell once it is done (the
# typed command spells it $((1+1))), so the recording opens when den is ready.
Hide
Type "unset CLAUDE_CODE_CHILD_SESSION CLAUDECODE CLAUDE_CODE_ENTRYPOINT"
Enter
Type "rm -rf /tmp/den-demo-state docs/runbooks/agents/mcp/runbooks && go build -o ./den ./cmd/den && mkdir -p docs/runbooks/agents/mcp/runbooks && cp -r docs/runbooks/agents/template/.claude docs/runbooks/agents/mcp/runbooks/ && echo built-$((1+1))"
Enter
Wait+Screen@120s /built-2/
Type "clear"
Enter
Show
Sleep 1s

# ── Register den once ─────────────────────────────────────────────────────────
# Notice: local scope, this folder only. den mcp is a server claude starts.
Type@40ms "cd docs/runbooks/agents/mcp/runbooks"
Sleep 1s     # Act
Enter
Type@40ms "claude mcp add den -- $OLDPWD/den mcp -c $OLDPWD/docs/runbooks/agents/mcp/den.yaml"
Sleep 1s     # Act
Enter
Sleep 3s     # Read: claude says den was added
Type "clear"
Enter

# ── Ask with den's prompt ─────────────────────────────────────────────────────
# Notice: the slash command is den's own prompt, offered by the MCP server.
Type "claude"
Sleep 1s     # Act
Enter
Wait+Screen@60s /for shortcuts/
Sleep 2s     # Look: claude is ready
Type@40ms "/mcp__den__new_runbook the current UTC time and this env's region, in Go. Then run it."
Sleep 1s     # Act
Enter
Wait+Screen@10m /done [0-9]+:[0-9]+ [AP]M/
Sleep 4s     # Read: the runbook claude wrote and ran
Type@40ms "/exit"
Sleep 1s     # Act
Enter
Sleep 1s     # Back: the shell

# ── Leave nothing registered ──────────────────────────────────────────────────
Type "clear"
Enter
Type@40ms "claude mcp remove den"
Sleep 1s     # Act
Enter
Sleep 2.5s   # Read: claude says den was removed
