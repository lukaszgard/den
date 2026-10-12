# The aws recording's steps, shared by aws.tape (the README's GIF) and aws-hd.tape
# (the site video), so the two cannot drift. Sourced after their Output line and
# settings; it has neither of its own.

# Needs: an AWS config with at least one SSO session and a few profiles (the
# demo config reads ~/.aws/config). Nothing is logged in: the recording only
# browses. Rendered by a person with `make demo`.

# Launch den hidden, so the recording opens on the loaded dashboard.
Hide
Type "./den --config docs/demo/den.yaml"
Enter
Wait+Screen@20s /Connect/
Show
Sleep 2s

# ── To the AWS pane ───────────────────────────────────────────────────────────
# The demo config's menu order: connect, runbooks, tunnel, secrets, aws, ...
Down@200ms 4
Sleep 2.5s   # Arrive: the AWS pane

# ── Sessions and profiles ─────────────────────────────────────────────────────
# Notice: SSO sessions on top with their token TTL, profiles below.
Right
Sleep 3.5s   # Look: the pane has focus

# The cursor starts on the first session; its details show on the right.
Sleep 4s     # Read: the session, its sso_start_url, the token state

# ── Move down into the profile list ───────────────────────────────────────────
# Notice: each profile shows its raw keys from ~/.aws/config.
Down@200ms 3
Sleep 3.5s   # Look: a profile and its keys

# ── Filter the profiles ───────────────────────────────────────────────────────
# Notice: the sessions stay visible while the profile list narrows.
Type "/"
Type "prod"
Sleep 1.5s   # Type: the filter result
Sleep 2s     # Read: the narrowed list
Sleep 1s     # Act: esc clears the filter
Escape
Sleep 1s     # Back

# ── Back up to a session ──────────────────────────────────────────────────────
Up@200ms 4
Sleep 3.5s   # Look: back on a session row

# Enter would run `aws sso login --sso-session <name>` for this row, and `o`
# would open its own SSO start page. Both are left out of the recording because
# they hand off to a browser.

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
