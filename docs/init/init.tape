Output docs/init/init.gif
Output docs/init/init.mp4

Source docs/tape-settings.tape

Set WaitTimeout 90s

# Needs a logged-in, non-production profile: `aws sso login --profile dev`.
# Record against a sandbox account — the output shows real endpoints.
# Each command waits for the shell prompt, never a blind hold; "clear" is hidden.

# The output file must not exist: den init refuses to overwrite it.
Hide
Type "rm -f /tmp/den-init.yaml"
Enter
Type "clear"
Enter
Show
Sleep 700ms  # Transit

# ── Look at the account ───────────────────────────────────────────────────────
# Notice: den finds the bastion and the data stores, and writes a config.
Type@40ms "den init --profile dev --region eu-central-1 -o /tmp/den-init.yaml"
Sleep 1s     # Act
Enter
Wait@90s
Sleep 4s     # Read: what was found

# ── The generated config ──────────────────────────────────────────────────────
# Notice: one service per store, ready to edit.
Hide
Type "clear"
Enter
Show
Sleep 700ms  # Transit
Type@40ms "head -40 /tmp/den-init.yaml"
Sleep 1s     # Act
Enter
Wait@10s
Sleep 8s     # Read: the first services

# ── It loads ──────────────────────────────────────────────────────────────────
# Notice: den doctor checks the new file.
Hide
Type "clear"
Enter
Show
Sleep 700ms  # Transit
Type@40ms "den doctor -c /tmp/den-init.yaml"
Sleep 1s     # Act
Enter
Wait@30s
Sleep 6s     # Read: the checks and the totals line
