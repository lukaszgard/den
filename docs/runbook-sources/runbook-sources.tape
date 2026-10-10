Output docs/runbook-sources/runbook-sources.gif
Output docs/runbook-sources/runbook-sources.mp4

Source docs/tape-settings.tape

Env XDG_DATA_HOME "/tmp/den-sources-demo/data"
Env XDG_STATE_HOME "/tmp/den-sources-demo/state"
Env SHELL "/bin/sh"
Env GIT_CONFIG_GLOBAL "/dev/null"

# Runbooks from a git repository: add the source, browse it, install and
# uninstall from the panel, and the question a new runbook asks before its
# first run. The repository is made from docs/runbook-sources/demo in /tmp and
# den.yaml is a copy, so nothing needs AWS, a network or a real account. Run
# from the repository root, with git installed.

# The build, the repository and the copy of den.yaml are hidden; the recording
# opens on a clean shell in the demo folder.
Hide
Type "rm -rf /tmp/den-sources-demo && mkdir -p /tmp/den-sources-demo && go build -o ./den ./cmd/den"
Enter
Type "cp -R docs/runbook-sources/demo/upstream /tmp/den-sources-demo/den-runbooks && cp docs/runbook-sources/den.yaml /tmp/den-sources-demo/den.yaml"
Enter
Type "git -C /tmp/den-sources-demo/den-runbooks init -q -b main && git -C /tmp/den-sources-demo/den-runbooks add -A && git -C /tmp/den-sources-demo/den-runbooks -c user.name=demo -c user.email=demo@example.com commit -qm runbooks"
Enter
Type "export PATH=$PWD:$PATH; cd /tmp/den-sources-demo && clear"
Enter
Show
Sleep 1s

# ── Add the source ────────────────────────────────────────────────────────────
# Notice: den writes den.yaml, checks the repository out and pins the commit in
# den.lock.
Type@40ms "den runbook source add /tmp/den-sources-demo/den-runbooks --name platform-ops --path runbooks --env-map prod=demo-prod -c den.yaml"
Sleep 1s     # Act
Enter
Wait+Screen@60s /Its runbooks are listed as/
Sleep 3s     # Read: what den did, and that new runbooks ask before their first run
Type@40ms "tail -n 9 den.yaml"
Sleep 1s     # Act
Enter
Sleep 4s     # Read: the source den wrote into den.yaml

# ── Browse it ─────────────────────────────────────────────────────────────────
# Notice: every runbook the repository holds, installed or not.
Type "clear"
Enter
Type@40ms "den runbook browse platform-ops -c den.yaml"
Sleep 1s     # Act
Enter
Wait+Screen@20s /installed · den runbook install/
Sleep 4s     # Read: the list, and the installed marks

# ── The panel ─────────────────────────────────────────────────────────────────
# Notice: s opens the sources, enter its runbooks, space uninstalls one.
Type "clear"
Enter
Type@40ms "den -c den.yaml"
Sleep 1s     # Act
Enter
Wait+Screen@20s /Runbooks/
Sleep 2s     # Look, short: the loaded panel
Type "l"
Sleep 3.5s   # Look: the table, source runbooks listed as platform-ops/<name>
Type "s"
Sleep 2.5s   # Arrive: the sources
Sleep 1s     # Act: enter opens platform-ops
Enter
Sleep 3.5s   # Look: its runbooks
Down
Sleep 1s     # Act: space uninstalls it
Type " "
Sleep 2.5s   # Read: the runbook is no longer installed
Escape
Sleep 1s     # Back
Escape
Sleep 1s     # Back: the table

# ── The question before a first run ───────────────────────────────────────────
# Notice: where the runbook comes from, and why it asks.
Type "g"
Sleep 700ms  # Transit: the first runbook
Type "r"
Sleep 4s     # Read: the confirmation
Sleep 1s     # Act: y runs it
Type "y"
Wait+Screen@30s /exit 0/
Sleep 2.5s   # Read: the table, with its last run

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
