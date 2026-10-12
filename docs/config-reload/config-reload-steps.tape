# The config-reload recording's steps, shared by config-reload.tape (the README's
# GIF) and config-reload-hd.tape (the site video), so the two cannot drift. Sourced
# after their Output line and settings; it has neither of its own.

# No update source: the startup check must not reach GitHub with your token.
Env GITHUB_TOKEN ""

# Editing den.yaml from den: an edit with an error is not applied and says why,
# the fix is applied when the editor closes, and a panel added to menu_order
# shows up at once. den.yaml is a copy of docs/config-reload/demo/den.yaml and
# the "editor" is demo/edit.sh, which makes one edit each time it is opened, so
# nothing needs AWS, a network or a real editor. Run from the repository root.

# The build, the copy and the launch are hidden; the recording opens on the
# loaded dashboard.
Hide
Type@40ms "rm -rf /tmp/den-reload-demo && mkdir -p /tmp/den-reload-demo/bin && touch /tmp/den-reload-demo/aws && go build -o /tmp/den-reload-demo/bin/den ./cmd/den"
Enter
Type@40ms "cp docs/config-reload/demo/den.yaml /tmp/den-reload-demo/den.yaml && export EDITOR=$PWD/docs/config-reload/demo/edit.sh VISUAL= PATH=/tmp/den-reload-demo/bin:$PATH && cd /tmp/den-reload-demo && clear"
Enter
Type@40ms "den -c den.yaml"
Enter
Wait+Screen@60s /Connect/
Show
Sleep 2s

# ── The Config panel ──────────────────────────────────────────────────────────
# Notice: the file den runs, with line numbers. The menu order is connect,
# tunnel, config, logs, help.
Down@200ms 2
Sleep 2.5s   # Arrive: the Config panel
Right
Sleep 3.5s   # Look: the panel has focus

# ── An edit with errors ───────────────────────────────────────────────────────
# e opens the editor. This edit adds a tunnel without its command, and a
# misspelt key: den keeps the running config and lists both, with their lines.
Sleep 1s     # Act: e opens the editor
Type "e"
Wait+Screen@30s /den.yaml has/
Sleep 4s     # Read: the pop-up, the problems and their lines

# esc: the problems stay in the Config panel, the status bar and the menu.
Sleep 1s     # Act
Escape
Sleep 3s     # Read: the status bar and the Config panel

# ── The fix ───────────────────────────────────────────────────────────────────
# Edit again: the fix is applied as soon as the editor closes.
Sleep 1s     # Act
Type "e"
Wait+Screen@30s /config reloaded/
Sleep 2.5s   # Read: the status bar says the config was reloaded

# ── A new panel ───────────────────────────────────────────────────────────────
# Add runbooks to menu_order: the menu has it at once, no restart.
Sleep 1s     # Act
Type "e"
Wait+Screen@30s /Runbooks/
Sleep 3.5s   # Read: Runbooks in the menu

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
