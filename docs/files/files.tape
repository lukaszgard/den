Output docs/files/files.gif
Output docs/files/files.mp4

Source docs/tape-settings.tape

Set WaitTimeout 60s

# Needs nothing: both commands only read. Run from the repo root so that
# the repo den.yaml is the file in use. Each command waits for the shell
# prompt, never a blind hold; "clear" is hidden.

# ── Every file den uses ──────────────────────────────────────────────────────
# Notice: the first line names the den.yaml in use and where it was found;
# the config directory is where your own files belong; exists or missing.
Type@40ms "den paths"
Sleep 1s     # Act
Enter
Wait@30s
Sleep 8s     # Read: the list

# ── The same, for a script ───────────────────────────────────────────────────
Hide
Type "clear"
Enter
Show
Sleep 700ms  # Transit
Type@40ms `den paths --json | jq '.paths[] | select(.exists | not) | .name'`
Sleep 1s     # Act
Enter
Wait@30s
Sleep 4s     # Read: what is missing

# ── A machine with no den.yaml yet ───────────────────────────────────────────
# Notice: doctor says where to write one and what belongs there. An empty
# folder and HOME stand in for a new machine.
Hide
Type "clear"
Enter
Show
Sleep 700ms  # Transit
Type@40ms `(cd "$(mktemp -d)" && HOME=$PWD den doctor)`
Sleep 1s     # Act
Enter
Wait@30s
Sleep 6s     # Read: the first-run guidance

# ── Only what a VPN needs ────────────────────────────────────────────────────
# Notice: openfortivpn and its version, then a warning for each VPN settings
# file that others can read or that sits in a git work tree, with the fix.
# Without a vpn: entry it prints the smallest one instead.
Hide
Type "clear"
Enter
Show
Sleep 700ms  # Transit
Type@40ms "den doctor --for vpn"
Sleep 1s     # Act
Enter
Wait@30s
Sleep 6s     # Read
