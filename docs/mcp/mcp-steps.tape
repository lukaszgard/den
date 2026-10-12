# The mcp recording's steps, shared by mcp.tape (the README's GIF) and mcp-hd.tape
# (the site video), so the two cannot drift. Sourced after their Output line and
# settings; it has neither of its own.

Set WaitTimeout 180s

# Needs Claude Code, a logged-in non-production profile and the demo config
# (docs/demo/den.yaml); its production services stay behind mcp.allow_production.
#
# It registers an MCP server named den in your Claude Code setup. If you already
# have one, remove it first (`claude mcp remove den`): `claude mcp add` stops at
# "already exists". The tape removes the registration it made when it ends.

Hide
Type "clear"
Enter
Show
Sleep 700ms  # Transit

# ── Register den with the agent ───────────────────────────────────────────────
# Notice: den is added as an MCP server, one command.
Type@40ms "claude mcp add den -- den mcp -c $PWD/docs/demo/den.yaml"
Sleep 1s     # Act
Enter
Wait@30s
Sleep 2s     # Read

# ── Ask in plain words ────────────────────────────────────────────────────────
Type@40ms "claude"
Sleep 1s     # Act
Enter
# The input box shows "? for shortcuts" only once claude is ready for the question.
Wait+Screen@30s /\? for shortcuts/
Sleep 1s     # Act
Type@40ms "Which databases can den reach? Connect to the DEV one and show its Postgres version."
Sleep 1s     # Act
Enter

# Notice: the agent lists the services, connects, and reports the version. The
# Wait is the agent's own time. "PostgreSQL <n>" is in its answer, not in the question.
Wait+Screen@180s /PostgreSQL [0-9]+/
Sleep 6s     # Read: the answer
Type@40ms "/exit"
Sleep 1s     # Act
Enter
Sleep 1s     # Back

# ── End ───────────────────────────────────────────────────────────────────────
# Take the registration back out, so a re-run starts clean.
Hide
Type "claude mcp remove den >/dev/null 2>&1"
Enter
Sleep 500ms
