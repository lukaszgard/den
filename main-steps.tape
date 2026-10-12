# The home recording's steps, shared by main.tape (README GIF) and main-hd.tape
# (site video), so the two cannot drift. Sourced after their Output lines and docs/tape-settings.tape.
#
# Paced for a person watching: every hold names its tier. The tiers and their
# numbers are in AGENTS.md (Feature documentation, the tape); tune them there and here.
#
# The menu order is the demo config's (docs/demo/den.yaml): connect, runbooks,
# tunnel, secrets, aws, vpn, config, help, updates, logs. The scenes: eight EU DEV
# services connected from the list (PostgreSQL, Valkey, DocumentDB, Redshift,
# OpenSearch, Neptune, MySQL, MemoryDB), a psql session on EU DEV Orders DB,
# check-rds-status (bash) and S3 buckets (Go) run, orders - EU STAGING decrypted.
# Then a short tour of the rest of the menu: AWS, VPN and Config pass by, the
# Help topic Runbooks opens, Updates is shown, and the video ends on the
# services list, still connected.
#
# Before rendering (make demo and make tape do neither), in one terminal:
#   - source the demo environment (AWS_CONFIG_FILE and the tokens the tape inherits):
#       source ../den-infra/out/den.env
#   - log in to AWS SSO; the tape launches den already logged in:
#       aws sso login --sso-session acme-sso
#   - build the binary the tape runs, from the repo root:
#       go build -o den ./cmd/den
#   - run S3 buckets once, so go has fetched and compiled the SDK and the
#     recording does not wait on it:
#       ./den run "S3 buckets" --config docs/demo/den.yaml

# ── Open ──────────────────────────────────────────────────────────────────────
# Launch den hidden, so the video opens on the loaded dashboard, not on the shell.
Hide
Type "./den --config docs/demo/den.yaml"
Enter
Wait+Screen@20s /Connect/
Show
Sleep 2s

# ── Connect ───────────────────────────────────────────────────────────────────
# Notice: one key per service, and the dots going yellow, then green, one engine
# after another. Then the database client opening in a tab of its own, already
# logged in, on data that is really there.
Right
Sleep 2s     # Look, short: the service list, all idle
Type "c"     # EU DEV Orders DB, PostgreSQL
Sleep 450ms
Down@150ms 3 # to EU DEV Cache
Sleep 250ms
Type "c"     # Valkey
Sleep 450ms
Down
Sleep 250ms
Type "c"     # EU DEV Documents, DocumentDB
Sleep 450ms
Down
Sleep 250ms
Type "c"     # EU DEV Analytics, Redshift Serverless
Sleep 450ms
Down
Sleep 250ms
Type "c"     # EU DEV Logs, OpenSearch
Sleep 450ms
Down
Sleep 250ms
Type "c"     # EU DEV Graph, Neptune
Sleep 450ms
Down
Sleep 250ms
Type "c"     # EU DEV MySQL
Sleep 450ms
Down
Sleep 250ms
Type "c"     # EU DEV MemoryDB, the last one pressed
Wait+Screen@60s /MemoryDB.*token: \d+m/
Sleep 3.5s   # Look: eight tunnels up, a token each
Up@80ms 9    # Transit: back to EU DEV Orders DB
Sleep 1s     # Act: enter opens the detail view
Enter
Sleep 1.5s   # Look, short: status, local port, how to connect
Type "t"     # a terminal tab: the client, already logged in
Wait+Screen@30s /psql \(/
Sleep 1s     # Act
Type@40ms "select count(*) from orders;"
Sleep 1s     # Act: enter runs it
Enter
Wait+Screen@10s /\(1 row\)/
Sleep 3s     # Read: the rows counted, no password typed
Ctrl+T       # back to den, the tab stays
Sleep 1s     # Back
Escape       # detail view -> list
Sleep 1s     # Back
Escape       # list -> menu
Sleep 1s     # Back

# ── Runbooks ──────────────────────────────────────────────────────────────────
# Notice: the runbooks are scripts in any language, and the table says which:
# bash, uv run, go run, cargo run. Two of them run from den, one in bash and one
# in Go, each with its result and the same keys. The first one also shows what
# else a runbook's view holds: its past runs, what it is, its folder. The list is
# the demo config's two folders (docs/demo/runbooks, docs/runbooks/demo):
# check-rds-status, Bastion status, CloudWatch alarms, Disk usage, Env report,
# S3 buckets, Legacy check.
Down
Sleep 700ms  # Transit
Right
Sleep 3.5s   # Look: the runbooks, and the command each one runs
Sleep 1s     # Act: enter opens check-rds-status, the first row, in bash
Enter
Sleep 1.5s   # Look, short: its Logs tab, nothing run yet
Type "r"
Wait+Screen@60s /\d+ instances:/
Sleep 4s     # Read: the table of instances
Right
Sleep 3s     # Look: History, the runs kept, each with its own log
Right
Sleep 3s     # Read: About, the environment the run gets
Right
Sleep 2.5s   # Arrive: Files, the runbook's folder as a tree
Escape       # runbook -> list
Sleep 1s     # Back
Down@150ms 5 # past Bastion status, CloudWatch alarms, Disk usage and Env report to S3 buckets
Sleep 1s     # Act: enter opens S3 buckets, in Go
Enter
Sleep 700ms  # Transit: its Logs tab
Type "r"
Wait+Screen@60s /\d+ buckets, newest first/
Sleep 4s     # Read: the newest buckets of the account
Escape       # runbook -> list
Sleep 1s     # Back
Tab
Sleep 1s     # Back

# ── Secrets ───────────────────────────────────────────────────────────────────
# Notice: a SOPS file decrypted with your AWS login, values masked.
Down         # past Tunnel
Sleep 700ms  # Transit
Down
Sleep 700ms  # Transit
Right
Sleep 3s     # Look, short: the secrets
Type "/"
Type@60ms "orders - eu staging"
Sleep 1.5s   # Type: the filter result
Sleep 1s     # Act: enter decrypts it
Enter
Wait+Screen@60s /••••/
Sleep 4s     # Read: the keys, values masked
Escape       # secret -> list, its values are dropped
Sleep 1s     # Back
Tab
Sleep 1s     # Back

# ── The rest of the menu ──────────────────────────────────────────────────────
# Notice: there is more than was shown: AWS, VPN and Config pass by, the Help
# opens on a topic, Updates says whether this is the latest, and the video ends
# where it began, on the services, all still connected.
Down@700ms 4 # Transit: AWS, VPN, Config, then Help
Sleep 700ms  # Transit
Right
Sleep 1.5s   # Look, short: the topics, one per feature
Down@80ms 25 # Transit: past every group, down to Runbooks
Sleep 1s     # Act: enter opens the topic
Enter
Sleep 4s     # Read: what runbooks are and what you can do with them
Escape       # topic -> list
Sleep 1s     # Back
Tab
Sleep 1s     # Back
Down         # Updates; the check ran at launch
Sleep 2.5s   # Arrive: the version, and whether it is the latest
Up@80ms 8    # Transit: back to Connect
Sleep 1s     # Act
Right
Sleep 3s     # Look, short: the services, the eight tunnels still up

# ── End ───────────────────────────────────────────────────────────────────────
# Hide the quit, so the loop never shows the shell.
Hide
Escape
Sleep 300ms
Type "q"
Sleep 500ms
