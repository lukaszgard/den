---
name: den
description: This skill should be used when the user asks to "connect to the int database", "query the staging Postgres", "open a tunnel to Redis", "check why den can't connect", "run the den runbook", "list den services", or otherwise needs a private cloud database, cache or search cluster (RDS, Aurora, ElastiCache, MemoryDB, DocumentDB, Redshift, OpenSearch, Neptune) that the den CLI reaches through tunnels. It covers using den's MCP tools and JSON CLI safely.
---

# Working with den

den opens tunnels to private cloud data stores (through AWS SSM, SSH, EC2 Instance
Connect or kubectl) and keeps their IAM credentials fresh. Once a tunnel is up, the
store is on `localhost` and any normal client works — `psql`, `mysql`, `redis-cli`,
`mongosh`, `curl`.

## Choose the interface

1. **den's MCP tools** (`list_services`, `connect`, `service_status`, `service_logs`,
   `disconnect`, `runbook_guide`, `validate_runbook`, and — only when enabled —
   `list_runbooks`, `run_runbook`). Use them
   whenever they are available: they are the only way for an agent to open a tunnel.
2. **No MCP tools?** Ask the user to register them once, then continue:
   `claude mcp add den -- den mcp -c ~/.config/den/den.yaml`.
3. **The CLI** answers questions without opening anything: `den list --json`,
   `den doctor --json`, `den run --list --json`, `den config --json`. Do not run
   plain `den`, `den connect` or `den create-env`: they start an interactive
   full-screen UI an agent cannot drive.

## Connect and query

1. Call `list_services`. Pick the service by name, type and environment; every
   entry says whether it is `production`.
2. Call `connect` with the exact name. It waits until the tunnel is usable and returns
   `host` (always `localhost`), `port`, `connect_command` and `credential`.
3. Run the query with the returned command or the equivalent client call, pointed at
   `localhost` and that port. Read-only queries unless the user asked for changes.
4. Call `disconnect` when finished. Tunnels also close when the session ends.

The `credential` field says how the client authenticates, never the secret itself:

| It says | Do |
|---|---|
| `~/.pgpass` (`%APPDATA%\postgresql\pgpass.conf` on Windows, or `$PGPASSFILE` when set) | run `psql` as given; the IAM token is already in that file |
| option file | run the `mysql` command as given; it reads the token from that file |
| AWS profile (DocumentDB) | run `mongosh` as given; it signs with that profile |
| proxy signs requests (OpenSearch, Neptune) | send plain HTTP to `localhost:port` |
| token withheld (Redis with IAM auth, or no `~/.pgpass`) | stop and ask the user to copy the full command from den's TUI detail view (`Y`) or run the query themselves |

## Rules

- **Production needs the user.** Never pick a `production: true` service unless the
  user named it. When den asks the user to confirm, do not retry around a refusal.
- **Never ask for, print or reconstruct credentials** — tokens, passwords, `~/.pgpass`
  contents, AWS keys. den withholds them on purpose.
- **Do not widen den's permissions.** Never edit the `mcp:` block of `den.yaml`
  (`allow`, `allow_production`, `allow_runbooks`) to give yourself access; ask.
- **Runbooks run real scripts.** Run one only when the user asked for that runbook,
  pass parameters exactly as declared (`list_runbooks` shows them), and report its
  exit status and output as they came back.
- **SSO logins and VPNs need a browser.** When `connect` or `den doctor` reports an
  expired or missing login, tell the user the command it suggests
  (e.g. `aws sso login --sso-session NAME`) instead of trying it yourself.

## Writing a runbook

When the user asks for a new runbook — a check, a report, a fix they run from den —
call `runbook_guide` first and follow it: one folder per runbook in den's runbooks
directory, a `runbook.yaml` with only the keys it lists, the environment from its table,
no prompts and no printed credentials. When the files are written, call
`validate_runbook` with the folder until it reports `ok`, then tell the user how to run
it (`den run "<name>"`, or the Runbooks panel). The same guide is in `runbooks.md` next
to this file.

## When something fails

1. Read the error from `connect` — den names the transport that failed and why.
2. Call `service_logs` for the service's recent log (credentials already removed).
3. Run `den doctor --json` for the local picture: AWS CLI and plugin, profiles, SSO
   logins, ports, client tools, secret keys. Each result has `status` (`ok`, `warn`,
   `fail`), `detail`, and when there is something to do a `fix` or `install` (the
   commands for this OS: install, upgrade, or put on PATH) and `docs`.
   `den doctor --for sops,rds --json` checks just what a task needs.
4. Report the failing checks with their `fix` and `install` lines to the user. Do not
   run `install` commands yourself unless the user asks you to: they change the
   user's machine and often need `sudo`. Do not run `den doctor --explain`: it sends
   data to a model and asks the user first.

Common causes: an expired SSO login (the user logs in), a local port held by another
process (`ports in use`), a missing or too old client tool (`psql`, `redis-cli`, …;
doctor gives the install command), a sops file whose key is not on this machine, a
bastion that is not online (`den doctor --deep`, which calls AWS).
