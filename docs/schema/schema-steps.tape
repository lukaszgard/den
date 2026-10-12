# The schema recording's steps, shared by schema.tape (the README's GIF) and schema-
# hd.tape (the site video), so the two cannot drift. Sourced after their Output line
# and settings; it has neither of its own.

Set WaitTimeout 30s

# den schema needs no AWS access: this tape can be rendered anywhere.
# Each command waits for the shell prompt, never a blind hold; "clear" is hidden.

# ── The schema ────────────────────────────────────────────────────────────────
# Notice: a JSON Schema printed by den itself.
Type@40ms "den schema | head -20"
Sleep 1s     # Act
Enter
Wait@10s
Sleep 7s     # Read

# ── Save it where an editor can find it ───────────────────────────────────────
Hide
Type "clear"
Enter
Show
Sleep 700ms  # Transit
Type@40ms "den schema > /tmp/den.schema.json"
Sleep 1s     # Act
Enter
Wait@10s
Sleep 1s     # Back

# ── What an editor checks against ─────────────────────────────────────────────
# Notice: the service types den knows.
Hide
Type "clear"
Enter
Show
Sleep 700ms  # Transit
Type@40ms `jq '."$defs".ServiceSpec.properties.type' /tmp/den.schema.json`
Sleep 1s     # Act
Enter
Wait@10s
Sleep 5s     # Read

# ── The editor hint in den.yaml ───────────────────────────────────────────────
# Notice: the first line points the editor at the schema.
Hide
Type "clear"
Enter
Show
Sleep 700ms  # Transit
Type@40ms "head -1 den.yaml"
Sleep 1s     # Act
Enter
Wait@10s
Sleep 4s     # Read
