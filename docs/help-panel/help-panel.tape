Output docs/help-panel/help-panel.gif
Output docs/help-panel/help-panel.mp4

Source docs/tape-settings.tape

# Needs nothing but a built ./den: the config below has no services and an empty
# AWS config (so none of yours shows up), and the fake aws first on PATH makes the
# startup update check fail at once instead of calling S3. No AWS login, no network.
#
# The setup and the launch are hidden; the recording opens on the loaded dashboard.

Hide
Type@40ms `mkdir -p /tmp/den-help-demo/bin && printf '#!/bin/sh\nexit 1\n' > /tmp/den-help-demo/bin/aws && chmod +x /tmp/den-help-demo/bin/aws`
Enter
Type@40ms `: > /tmp/den-help-demo/aws-config && : > /tmp/den-help-demo/aws-credentials`
Enter
Type@40ms `printf 'version: "1"\naws:\n  config_path: /tmp/den-help-demo/aws-config\n  credentials_path: /tmp/den-help-demo/aws-credentials\nupdates:\n  s3_bucket: den-help-demo\n' > /tmp/den-help-demo/den.yaml`
Enter
Type@40ms "PATH=/tmp/den-help-demo/bin:$PATH ./den -c /tmp/den-help-demo/den.yaml"
Enter
Wait+Screen@20s /Connect/
Show
Sleep 2s

# ── The topic list ────────────────────────────────────────────────────────────
# Notice: ? jumps from the menu to Help; topics grouped under headings.
Type "?"
Sleep 3.5s   # Look: the topic list
Down@200ms 9
Sleep 1s     # Act: enter opens RDS and Aurora

# ── One topic ─────────────────────────────────────────────────────────────────
# Notice: RDS and Aurora: what it is, what you can do, before you start, configure…
Enter
Sleep 3.5s   # Look: the topic
PageDown
Sleep 2.5s   # Read: the next page
PageDown
Sleep 2.5s   # Read: the next page
PageDown
Sleep 2.5s   # Read: the next page
Escape
Sleep 1s     # Back

# ── Search every topic ────────────────────────────────────────────────────────
# Notice: the list narrows to the topics that mention it, with their number of matches.
Type "/"
Type "auto_reveal"
Sleep 1.5s   # Type: the narrowed list
Sleep 1s     # Act: enter opens the selected topic at its first match
Enter
Sleep 3.5s   # Look: the topic, on the match
Type "n"
Sleep 2.5s   # Read: the next match
Type "n"
Sleep 2.5s   # Read: the next match

# One step back at a time: the topic's search, the topic, the list's search
Escape
Sleep 1s     # Back
Escape
Sleep 1s     # Back
Escape
Sleep 1s     # Back

# ── How to create a runbook ───────────────────────────────────────────────────
# Notice: searching for a phrase of the Runbooks topic opens it on its Usage section.
Type "/"
Type@40ms "Ask an agent to write it"
Sleep 1.5s   # Type: the narrowed list
Sleep 1s     # Act
Enter
Sleep 5s     # Read: the Usage section
Escape
Sleep 1s     # Back
Escape
Sleep 1s     # Back
Escape
Sleep 1s     # Back

# ── The panel's keys ──────────────────────────────────────────────────────────
Sleep 1s     # Act: h opens the pop-up
Type "h"
Sleep 4s     # Read: the panel's keys
Escape
Sleep 1s     # Back

# ── End ───────────────────────────────────────────────────────────────────────
Hide
Type "q"
Sleep 500ms
