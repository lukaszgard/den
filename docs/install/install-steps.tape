# The install recording's steps, shared by install.tape (the README's GIF) and
# install-hd.tape (the site video), so the two cannot drift. Sourced after their
# Output line and settings; it has neither of its own.

Set WaitTimeout 90s

# Needs the network: it installs from the project's GitHub Releases.
# It installs into a scratch folder, not over the den on PATH.
# Each command waits for the shell prompt, never a blind hold.

Hide
Type "rm -rf /tmp/den-demo"
Enter
Type "clear"
Enter
Show
Sleep 700ms  # Transit

# ── Install from GitHub Releases ──────────────────────────────────────────────
# Notice: one line downloads the installer and runs it into the scratch folder;
# the installer checks the archive against the release's checksums.txt.
Type@40ms "curl -fsSL https://github.com/lukaszgard/den/releases/latest/download/install.sh | sh -s -- --dir /tmp/den-demo/bin"
Sleep 1s     # Act
Enter
Wait@90s
Sleep 6s     # Read: where den was installed

# ── It runs ───────────────────────────────────────────────────────────────────
Type@40ms "/tmp/den-demo/bin/den --version"
Sleep 1s     # Act
Enter
Wait@30s
Sleep 3s     # Read: the version
