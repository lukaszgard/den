# The doctor recording's steps, shared by doctor.tape (the README's GIF) and doctor-
# hd.tape (the site video), so the two cannot drift. Sourced after their Output line
# and settings; it has neither of its own.

Set WaitTimeout 60s

# Local checks need no AWS access. For the --deep part, log in first so the
# bastion checks show their real state rather than the expired-login warning.
# Each command waits for the shell prompt, never a blind hold; "clear" is hidden.

# ── Everything den.yaml uses ──────────────────────────────────────────────────
# Notice: tools with their versions, profiles, logins, ports; ✓ ok, ⚠ warning, ✗ failed.
Type@40ms "den doctor"
Sleep 1s     # Act
Enter
Wait@30s
Sleep 8s     # Read: the checks and the totals line

# ── Only what one feature needs ───────────────────────────────────────────────
# Notice: no den.yaml is needed; only the tools sops and rds ask for.
Hide
Type "clear"
Enter
Show
Sleep 700ms  # Transit
Type@40ms "den check-install --for sops,rds"
Sleep 1s     # Act
Enter
Wait@30s
Sleep 6s     # Read

# ── Another platform's install steps ──────────────────────────────────────────
# Notice: the commands for Windows, for a teammate's onboarding notes.
Hide
Type "clear"
Enter
Show
Sleep 700ms  # Transit
Type@40ms "den doctor --for sops,vpn --os windows"
Sleep 1s     # Act
Enter
Wait@30s
Sleep 6s     # Read

# ── Ask SSM about every bastion ───────────────────────────────────────────────
# Notice: --deep calls AWS, so the command takes its own time.
Hide
Type "clear"
Enter
Show
Sleep 700ms  # Transit
Type@40ms "den doctor --deep"
Sleep 1s     # Act
Enter
Wait@60s
Sleep 8s     # Read: the bastion lines

# ── For scripts and agents ────────────────────────────────────────────────────
# Notice: only the problems, each with the install command.
Hide
Type "clear"
Enter
Show
Sleep 700ms  # Transit
Type@40ms `den doctor --json | jq '.[] | select(.status != "ok") | {check, install}'`
Sleep 1s     # Act
Enter
Wait@30s
Sleep 5s     # Read
