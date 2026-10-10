Output docs/secrets/secrets.gif
Output docs/secrets/secrets.mp4

Source docs/tape-settings.tape

Env SOPS_AGE_KEY_FILE "/tmp/den-secrets-demo/demo-age.key"
Env EDITOR "vi"
Env XDG_STATE_HOME "/tmp/den-secrets-demo/state"

# Secrets without leaving den: a SOPS file (decrypt, reveal, copy, edit) and a
# KeePassXC database (master password, then an entry). Needs sops 3.9 or newer and
# keepassxc-cli; no AWS. It runs on docs/secrets/den.yaml and a copy of
# docs/secrets/demo in /tmp/den-secrets-demo (fake values, master password "demo"),
# so the edit below never changes the repository. Run from the repository root:
# make tape TAPE=secrets. The shortcuts pop-up (h) is in docs/shortcuts.

# The build, the copy and the launch are hidden; the recording opens on the
# Secrets pane.
Hide
Type "rm -rf /tmp/den-secrets-demo && mkdir -p /tmp/den-secrets-demo && cp docs/secrets/demo/demo-age.key docs/secrets/demo/demo.enc.yaml docs/secrets/demo/team.kdbx /tmp/den-secrets-demo/ && go build -o ./den ./cmd/den && clear"
Enter
Type "./den -c docs/secrets/den.yaml"
Enter
Wait+Screen@60s /Secrets/
Show
Sleep 2s

# ── The list ──────────────────────────────────────────────────────────────────
# Notice: a SOPS file and a KeePassXC database, each with its status.
Enter
Sleep 3.5s   # Look: the pane has focus

# ── Decrypt: keys laid out as in the file, values masked ─────────────────────
Sleep 1s     # Act: enter decrypts Demo
Enter
Wait+Screen@30s /••••/
Sleep 4s     # Read: the keys as in the file, every value masked

# ── Reveal one value, then move on (it is masked again) ──────────────────────
Sleep 1s     # Act
Down
Sleep 700ms  # Transit
Type "r"
Sleep 2.5s   # Read: one value shown
Down
Sleep 2s     # Read: it is masked again

# ── Reveal everything at once (auto_reveal: true opens files this way) ───────
Sleep 1s     # Act
Type "a"
Sleep 2.5s   # Read: every value shown
Sleep 1s     # Act
Type "a"
Sleep 1s     # Back

# ── Copy a value: the clipboard is cleared after 30 s ────────────────────────
Sleep 1s     # Act
Type "y"
Wait+Screen@5s /clipboard clears in/
Sleep 1s     # Read: the status bar flash, shown for a second

# ── Edit through sops: add a key, save, see it decrypted again ───────────────
Sleep 1s     # Act: e opens the decrypted file in the editor
Type "e"
Sleep 3.5s   # Look: the editor
Type "G"
Type "o"
Type@40ms "feature_flag: on"
Sleep 1s     # Act
Escape
Type ":wq"
Sleep 1s     # Act
Enter
Wait+Screen@30s / saved/
Sleep 3s     # Read: the new key, decrypted again

# ── Close the file: decrypted values are dropped ─────────────────────────────
Sleep 1s     # Act
Escape
Sleep 1s     # Back: the list

# ── KeePassXC: the master password, then an entry ────────────────────────────
# Notice: den asks for the password and hands it to keepassxc-cli on stdin; it
# keeps it for ten minutes here (remember_password), so the next entry opens
# without asking.
Down
Sleep 1s     # Act: enter opens Team vault
Enter
Wait+Screen@10s /Master password for/
Sleep 2.5s   # Read: the masked password field
Type@40ms "demo"
Sleep 1s     # Act
Enter
Wait+Screen@30s /Monitoring\/Grafana/
Sleep 3.5s   # Look: every entry of the database, by path
Sleep 1s     # Act: enter opens CI/Jenkins
Enter
Wait+Screen@30s /UserName/
Sleep 2.5s   # Read: the entry's fields, values masked
Down
Sleep 700ms  # Transit
Type "r"
Sleep 3s     # Read: the password shown
Escape
Sleep 1s     # Back: the entries
Escape
Sleep 1s     # Back: the list

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
