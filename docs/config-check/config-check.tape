Output docs/config-check/config-check.gif
Output docs/config-check/config-check.mp4

Source docs/tape-settings.tape

# No update source: the startup check must not reach GitHub with your token.
Env GITHUB_TOKEN ""

# Checking den.yaml: den config --check lists every problem with its line and a
# hint, then the Config panel shows the same list with p. den.yaml is a copy of
# docs/config-check/demo/den.yaml, which has five warnings and needs no AWS, a
# network or an editor. Run from the repository root.

# The build, the copy and the launch are hidden; the recording opens on the
# empty prompt.
Hide
Type@40ms "rm -rf /tmp/den-check-demo && mkdir -p /tmp/den-check-demo/bin && touch /tmp/den-check-demo/aws && go build -o /tmp/den-check-demo/bin/den ./cmd/den"
Enter
Type@40ms "cp docs/config-check/demo/den.yaml /tmp/den-check-demo/den.yaml && export PATH=/tmp/den-check-demo/bin:$PATH && cd /tmp/den-check-demo && clear"
Enter
Wait@60s
Show
Sleep 1s

# ── On the command line ───────────────────────────────────────────────────────
# Notice: each problem has its line, and the arrow line under it says how to fix
# it. Two Redis services share a port because neither sets local_port.
Type@40ms "den config --check den.yaml"
Sleep 1s     # Act
Enter
Wait@20s
Sleep 8s     # Read: five problems, each with its hint

# ── In the dashboard ──────────────────────────────────────────────────────────
Sleep 1s     # Act
Type@40ms "clear && den -c den.yaml"
Sleep 1s     # Act
Enter
Wait+Screen@60s /Connect/
Sleep 2s

# Notice: the count beside Config in the menu, and the note in the status bar.
Down@200ms 2
Sleep 2.5s   # Arrive: the Config panel with its problems above the file
Right
Sleep 3.5s   # Look: the panel has focus

# p replaces the file with every problem. Notice: the cursor, and the count.
Sleep 1s     # Act
Type "p"
Sleep 5s     # Read: the list, each problem wrapped with its hint

Down@700ms 2
Sleep 2s     # Look: the cursor moved on

# enter shows the line in the file, at the top, marked with an arrow.
Sleep 1s     # Act
Enter
Sleep 4s     # Read: the line of the problem

# n walks to the next marked line.
Sleep 1s     # Act
Type "n"
Sleep 3.5s   # Look: the next problem's line

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
