# The recording of SOPS files kept in git, shared by secrets-git.tape (the README's
# GIF) and secrets-git-hd.tape (the site video). Sourced after their Output line and
# settings; it has neither of its own.

Env SOPS_AGE_KEY_FILE "/tmp/den-secrets-git/demo-age.key"
Env XDG_STATE_HOME "/tmp/den-secrets-git/state"

# A teammate rotated a password and pushed; den shows the file up to date with what
# your clone knows, f fetches and finds the newer version, u fast-forwards after
# asking, and the open file shows the new value. Needs git and sops; no AWS and no
# network: docs/secrets/demo/git-demo.sh makes the repositories in
# /tmp/den-secrets-git, with a bare one as the remote. Run from the repository root:
# make tape TAPE=secrets-git.

# The repositories, the build and the launch are hidden; the recording opens on the
# Secrets pane.
Hide
Type "docs/secrets/demo/git-demo.sh && cp docs/secrets/demo/demo-age.key docs/secrets/demo/demo.enc.yaml /tmp/den-secrets-git/ && mv /tmp/den-secrets-git/demo.enc.yaml /tmp/den-secrets-git/local.enc.yaml && go build -o ./den ./cmd/den && clear"
Enter
Type "./den -c docs/secrets/git-den.yaml"
Enter
Wait+Screen@60s /Secrets/
Show
Sleep 2s

# ── The list: ready, and up to date with what the clone knows ────────────────
# Notice: a second status beside ready; the file outside a repository has none,
# and the details box says the clone never fetched.
Enter
Sleep 3.5s   # Look: the pane has focus
Sleep 4s     # Read: ✓ up to date, Git infra · main → origin/main · never fetched

# ── f: fetch, and the teammate's change shows up ─────────────────────────────
Sleep 1s     # Act
Type "f"
Wait+Screen@30s /outdated/
Sleep 5s     # Read: ↓ outdated, 1 newer commit, its subject and author

# ── Open it: the values are the local ones, and the banner says so ───────────
Sleep 1s     # Act
Enter
Wait+Screen@30s /newer version of this file/
Sleep 4s     # Read: the banner over the masked values

# ── u: fetch, then ask before the fast-forward ───────────────────────────────
Sleep 1s     # Act
Type "u"
Wait+Screen@30s /Fast-forward infra/
Sleep 4.5s   # Read: the repository, the branch, the commits, and that the whole branch moves

# ── y: the branch moves, the file is read again ──────────────────────────────
Sleep 1s     # Act
Type "y"
Wait+Screen@30s /\(1 commit\)/
Sleep 3s     # Read: the status bar names the move
Down
Sleep 700ms  # Transit
Type "r"
Sleep 3.5s   # Read: the rotated password
Escape
Sleep 1s     # Back: the list
Sleep 3s     # Read: ✓ up to date again

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
