# Security model

What den holds, where it writes it, what an AI agent can reach through it and what each
data store needs you to be allowed to do. den runs as you, with your AWS login: it adds no
permission of its own, and the sections below say where it stops short of using all of yours.

## What never leaves den

- **Through `den mcp` and `--json`.** The state of a service (`den list --json`, and what an
  agent is told) is a fixed set of fields, the name, type, status, local port, connect
  command, token expiry and whether the environment is production, and none of them is a
  credential. `connect` tells an agent how its client authenticates (`~/.pgpass`, the MySQL
  option file, the AWS profile, the signing proxy) and never returns a token. A Redis or
  MemoryDB cache using IAM auth therefore cannot be used by an agent directly: its token is
  withheld.
- **In the logs an agent reads.** Before den hands a log to an agent it removes the service's
  current token, signed-URL parameters and any long opaque string, and strips terminal escape
  codes. A runbook's output is redacted the same way, and a runbook parameter marked `secret`
  is masked while typed and redacted from the output and the history on disk.
- **The values of your secrets.** The Secrets panel shows values masked until you reveal them,
  and den never writes one to disk or to its log. Copying one clears the clipboard after 30
  seconds. `den.yaml` only refers to secrets (file paths, secret IDs, profiles), so it is safe
  to commit. Editing a SOPS file hands the file to `sops edit`, which uses its own temporary
  file.
- **To a language model, unless you ask.** `den doctor --explain` is the only place den calls
  one. It first prints exactly what it would send, with names, hosts, IDs and paths replaced by
  placeholders, and sends it only if you agree or pass `--yes`. The placeholders are put back
  locally in the answer.

## Where credentials are written

den mints IAM credentials with your AWS login and puts each where its client looks, with
owner-only permissions:

| Credential | Written to | Lifetime |
|---|---|---|
| RDS / Aurora (PostgreSQL) token, Redshift credentials | `~/.pgpass` (mode 0600; `%APPDATA%\postgresql\pgpass.conf` on Windows, or `$PGPASSFILE` when set), when `update_pgpass` is on | 15 minutes (Redshift 15 minutes to 1 hour), renewed while connected |
| RDS / Aurora (MySQL, MariaDB) token | `~/.config/den/mysql/<local_port>.cnf` (mode 0600, in a 0700 folder) | 15 minutes, renewed while connected |
| ElastiCache / MemoryDB token | nowhere: handed to `redis-cli` in a terminal tab through `REDISCLI_AUTH`, or shown with `Y` / `p` | 15 minutes |
| DocumentDB, OpenSearch, Neptune | no token: `mongosh` signs with the AWS profile, and den's local proxy signs each request | the AWS session |

`Y` and `p` in a service's detail view copy a token to the clipboard on purpose; `y` copies the
connect command, which never contains one. Everything else a credential touches is your own AWS
login (`~/.aws`), which den reads and never writes.

## Production and AI agents

- An agent sees and connects every service not marked production, and runs no runbooks, until
  `mcp:` in `den.yaml` says otherwise. `mcp.allow` narrows the services it can see by name.
- A service is production when its `environment` is `prod`. `mcp.allow_production: true` makes
  connecting one possible, and even then each connect asks you in the MCP client. A client that
  cannot ask is refused.
- Runbooks are off for agents until `mcp.allow_runbooks: true`; one marked `confirm: true` asks you
  first and shows its command. Runbooks from a source stay hidden until `mcp.allow_source_runbooks`
  is also set, and then every run asks you.
- AWS SSO logins and VPNs need a browser and stay in the dashboard. Tunnels an agent opens close
  when it disconnects.

See [den mcp](../mcp/README.md) for the full boundary.

## Someone else's runbooks

A runbook from a [source](../runbook-sources/README.md) is another person's code running with your
AWS credentials. den pins a source to a commit in `den.lock`, so nothing updates on its own, and
asks before the first run of each runbook and again after an update changed its files. A source
runbook cannot change den's context: its `shell`, `editor` and `history` are ignored, and so are
`env_vars` that set `PATH`, `AWS_*` or `DEN_*`. Its `cwd` must stay inside the repository, its name and
README are stripped of terminal escape codes, and `runbooks.allow_sources` limits which repositories a
source may come from.

## The VPN helper

`openfortivpn` needs root. `den vpn install-helper` is optional; without it every connect raises an
admin password prompt. With it, den installs a root-owned wrapper under `/opt/den` and a sudoers rule
pinned to four fixed commands, and the wrapper takes a profile name and nothing else, so no option of
openfortivpn can be injected. den re-checks that the wrapper and every directory above it is root-owned
and not group- or world-writable before each use, and falls back to the prompt if not.

The cost is real: afterwards, anything running as you can become root through the binary the wrapper
runs if your user can rewrite that binary (a Homebrew install can). [VPN › What you are trading
away](../vpn/README.md#what-you-are-trading-away) lists what is written and how to close the hole.

## Updates

den checks the archive it downloads against the release's `checksums.txt` (SHA-256, published with the
release) before it touches the installed binary, and stops on a mismatch. A release that publishes no
`checksums.txt` still installs, with a warning that the download was not verified. `install.sh` and
`install.ps1` check the same file. An older version installed from the version picker or with
`den update install` is checked the same way. Which releases are yanked comes from `releases.json`
on the project's own latest release, built from `.github/yanked.json` in the repository. The update
commands are not offered over MCP: an agent cannot replace den's binary. See
[Updates](../updates/README.md) and [Install](../install/README.md).

## What each data store needs you to be allowed to do

den uses the permissions of the `credential_profile` identity (the one that mints the credential) and, for
the tunnel, the `aws_profile` identity. The store pages have the policies and the database-side setup.

| Store | Permission | Page |
|---|---|---|
| Any SSM tunnel | `aws_profile`: start a port-forwarding session on the bastion (the SSM Session Manager plugin, with the AWS CLI) | [Environments](../environments/README.md#prerequisites) |
| RDS / Aurora | `rds-db:connect` on `arn:aws:rds-db:<region>:<account>:dbuser:<resource-id>/<db_user>`, and IAM authentication enabled with the user set up for it | [RDS](../rds/README.md#prerequisites) |
| ElastiCache | `elasticache:Connect` on the cache **and** the user | [ElastiCache](../elasticache/README.md#prerequisites) |
| MemoryDB | `memorydb:Connect` on the cluster **and** the user | [MemoryDB](../memorydb/README.md#prerequisites) |
| DocumentDB | none in IAM: the SSO role is registered in `$external` on the cluster, and authorization is database-side | [DocumentDB](../documentdb/README.md#prerequisites) |
| Redshift | Serverless: `redshift-serverless:GetCredentials`; provisioned: `redshift:GetClusterCredentialsWithIAM`, or `redshift:GetClusterCredentials` with `db_user` | [Redshift](../redshift/README.md#prerequisites) |
| OpenSearch | domain access policy allowing the signing role (`es:ESHttp*`); Serverless: a data access policy and a network policy for the VPC endpoint | [OpenSearch](../opensearch/README.md#prerequisites) |
| Neptune | `neptune-db:connect` (or the finer `neptune-db:*` data actions) on the cluster resource, with IAM authentication enabled | [Neptune](../neptune/README.md#prerequisites) |
| AWS Secrets Manager secrets | `secretsmanager:GetSecretValue`, read-only | [Secrets](../secrets/README.md#prerequisites) |
| SOPS files with KMS | `kms:Decrypt` (and `kms:Encrypt`, `kms:GenerateDataKey` to save edits) | [Secrets](../secrets/README.md#prerequisites) |
| Updates from S3 | `s3:GetObject` on the release prefix | [Updates](../updates/README.md#prerequisites) |
| `den init` | `Describe*` and `List*` only: SSM, RDS, ElastiCache, MemoryDB, Redshift, OpenSearch | [den init](../init/README.md#prerequisites) |
