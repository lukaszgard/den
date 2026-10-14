# Changelog

## [1.2.1] - 2026-10-14

**`update_pgpass` works on Windows.** den wrote the token to `~/.pgpass`, a file `psql` on
Windows never reads, so `psql` kept asking for a password. den now writes it where `psql`
looks on each system.

### 🔌 Data stores and tunnels

- With `update_pgpass: true`, RDS PostgreSQL and Redshift services write the token to
  `%APPDATA%\postgresql\pgpass.conf` on Windows, creating the `postgresql` folder when it is
  missing. macOS and Linux keep `~/.pgpass`.
- When `PGPASSFILE` is set, den writes to that file on any system: `psql` reads it in place
  of `~/.pgpass`, so the token used to land in a file it skipped.
- The connect log names the file den wrote, and `den mcp`'s `connect` tells an agent the same
  path.
- On Windows, a `~/.pgpass` left by an earlier version was never read; you can delete it.

## [1.2.0] - 2026-10-12

**What you type for an agent is no longer lost.** The request of the Runbooks pane's `n` and
the change `E` asks for lived only in their pop-up: `esc`, a quit or a crash lost it, and a
restarted agent tab started without it. den now keeps them on disk as drafts until you send
them, and keeps the request a runbook was made with, so an agent that failed can get it again.

### 📜 Runbooks

- The `n` and `E` forms save what you type as a draft a second after you stop typing, and when
  den quits, so `ctrl+c`, `q` or a crash loses at most that second. `esc` on a form with
  something in it asks *Keep as a draft?*: `y` keeps it, `n` deletes it, `esc` goes back to the
  form. An empty form closes as before.
- A kept draft is a row at the top of the Runbooks list, TYPE `draft`, with its request's first
  line as the description, so `/` finds it. `enter` opens its form where you left it and `D`
  deletes it after asking; the run keys say it is a draft instead of running it.
- `n` first offers your drafts of new runbooks, newest first, beside a new one; with none it
  opens the form at once, as before. `E` on a runbook with a draft of a change opens that draft.
- `enter` in either form keeps the request with the runbook. Restarting a failed agent's tab
  now asks first: `y` (or `enter`) sends the request again, `n` starts the agent empty, `esc`
  restarts nothing. After den itself restarted, `E` on the runbook opens filled with that
  request, ready to send again or to change. A runbook that cannot be created keeps what was
  typed as a draft.
- Drafts and saved requests are files in den's state directory readable only by you, since a
  request may hold a password; `den mcp` and `--json` never show them. `den paths` lists the
  folder, and deleting a runbook deletes its requests.

### 🔧 Configuration and checks

- `runbooks.drafts.keep_request` says how long a request outlives the runbook it created:
  `until-deleted` (the default) until the runbook is deleted, `until-exit` until its agent exits
  cleanly by itself, `never` once the agent has started. An agent that fails, or whose tab den
  closed, keeps it.
- `runbooks.drafts.days` (default 30) is how long an untouched draft is kept; `0` keeps drafts
  until you delete them.

## [1.1.1] - 2026-10-12

**The `den.yaml` schema and the configuration reference describe every key.** Each key is
documented where den defines it, but the schema generator looked those notes up under the wrong
package name, found none and reported nothing. The schema shipped without a single key
description, and every Description cell of the configuration reference was empty.

### 🔧 Configuration and checks

- `den schema` now carries a description for every key, so an editor with the YAML language
  server shows what a key does when you hover it. If you saved the schema to a file
  (`den schema > ~/.config/den/den.schema.json`), run that again after updating.
- The configuration reference on the documentation site fills its Description column from the
  same text.
- The reference's Required column says yes or no for every key, where it left optional keys
  blank. A key needed only in some setups says when in its description: `aws_region_code` and
  `ec2_instance_id` (in the service's section or from its `env`), the transport's `host`,
  `endpoint_id` and `resource`, Redshift's `cluster_identifier` and `workgroup_name`, the VPN's
  `config_file` and KeePassXC's `key_file`.
- A key with a fixed set of values lists them in its Type (`string, one of ssm, ssh, …`), in
  place of an Allowed values column that stood empty on all but 11 rows.
- The schema marks `rds_host`, `db_user` (RDS), `documentdb_host`, `redshift_host`,
  `opensearch_host`, `neptune_host` and a runbook parameter's `name` as required, as den itself
  always did, so an editor flags them when they are missing.

## [1.1.0] - 2026-10-12

**Six more agent CLIs can write and change your runbooks.** `den runbook new --agent`, the
Runbooks pane's `n` and `E` started only Claude Code and Codex. They now also start OpenCode,
Copilot CLI, Qwen Code, Droid, Pi and Kimi Code, each with den's runbook guide, and without
touching that agent's own configuration.

### 📜 Runbooks

- `kind` in `runbooks.agents`, `runbooks.agent`, a runbook's `agent:` and `--agent` take
  `opencode`, `copilot`, `qwen`, `droid`, `pi` and `kimi` beside `claude` and `codex`. The kind
  is read from the command (`opencode --model …`, `/opt/bin/copilot`) and from
  `ollama launch <kind>`, where `ollama launch copilot-cli` means Copilot CLI. `auto` takes the
  first one installed, in that order after Claude Code and Codex.
- OpenCode and Copilot CLI get den's MCP server for the session, as Claude Code and Codex do:
  OpenCode through a config den writes and names in `OPENCODE_CONFIG`, which it merges with
  yours; Copilot CLI through `--additional-mcp-config`, with an `AGENTS.md` den adds to
  `COPILOT_CUSTOM_INSTRUCTIONS_DIRS` ahead of your own folders.
- Qwen Code, Droid, Pi and Kimi Code cannot take an MCP server for one session, so they check
  their work with the new `den runbook validate` from their shell. Kimi Code takes no first
  prompt: den puts the request in its instructions and tells you to type `go` in its tab.
- `den runbook validate [folder] [--json]` checks a runbook folder the way den loads it and
  reports what the `validate_runbook` MCP tool does, exiting 1 while the folder does not load.
- The setting files den writes for these agents live in `agents/` under den's state directory,
  one folder per start, readable only by you; folders unused for 30 days are removed.

### 🔧 Configuration and checks

- `den doctor --for opencode` (and `copilot`, `qwen`, `droid`, `pi`, `kimi`) shows how to
  install each agent CLI on your OS.

## [1.0.4] - 2026-10-11

**An old SSO login left in the cache no longer hides a fresh one.** `~/.aws/sso/cache` keeps a
file per session name and per start URL, and the files of a renamed session stay behind. den took
the first match in file name order, so an expired leftover that sorted before today's login made a
logged-in session show as expired.

### 🔧 Configuration and checks

- A session counts as logged in when any of its cached logins is still valid. Secrets no longer
  say "SSO login expired — log in from the AWS pane" for a session you have just logged in to,
  and the AWS pane and `den doctor` read the login that runs out last.

## [1.0.3] - 2026-10-10

**`aws.config_path` and `aws.credentials_path` now reach the `aws` CLI and the AWS SDK.** den read
the files named there to find your SSO sessions, but the `aws` commands it runs looked in
`~/.aws/config`, so an SSO login from a custom config file ended in `exit status 255`.

### 🔧 Configuration and checks

- den exports `AWS_CONFIG_FILE` and `AWS_SHARED_CREDENTIALS_FILE` from `aws.config_path` and
  `aws.credentials_path` for everything it starts: SSO logins, RDS tokens, SSM tunnels, credential
  exports and the SDK. A key `den.yaml` leaves out does not override a variable you exported
  yourself, and a config reload that drops a key puts back the value den started with.

### 🔌 Data stores and tunnels

- A failed SSO login says why. The row shows the last line of the `aws` CLI's output after
  `exit status N`, instead of the exit status alone.

## [1.0.2] - 2026-10-10

### 🔄 Updates

- A failed update check with `GITHUB_TOKEN` set no longer ends in a bare "HTTP 404". GitHub
  answers 404 for a repository a valid token cannot read, so the message now says so and what
  the token needs: for a fine-grained token, the repository selected under its owner and
  Contents: read; for a classic one, the `repo` scope, authorized for SSO when the organization
  enforces it. A rejected token (HTTP 401) is reported as invalid, expired or revoked.

## [1.0.1] - 2026-10-10

**Checking for updates no longer depends on GitHub's API rate limit.** den checks on every
start, and without a token GitHub's API allows 60 requests an hour per IP, so an office, a VPN
exit or a CI fleet behind one address got HTTP 403 in the Updates pane.

### 🔄 Updates

- The check reads the release feed on github.com (`latest.json` of the latest release) and the
  download uses the release's files by their URLs: the route `install.sh` and `install.ps1`
  already take, with no API call and no rate limit. A public repository needs nothing
  configured: no token, no `updates:` block. den needs HTTPS access to `github.com` and to
  `release-assets.githubusercontent.com`; behind a proxy it honours `HTTPS_PROXY`.
- `GITHUB_TOKEN` still selects the releases API, which a private repository needs. A release
  without `latest.json` falls back to the API, and a fork whose `github_api` is on
  `api.github.com` reads the feed of its own repository. GitHub Enterprise and the S3 mirror
  work as before.
- A failed check says what to do: that den could not reach github.com (set `HTTPS_PROXY`), that
  the repository is private (set `GITHUB_TOKEN`), or that GitHub's rate limit was reached.
- The Updates pane's help (`h`) shows where releases are read from and what you need, and the
  Updates documentation and Help topic list the same requirements.

## [1.0.0] - 2026-10-10

**den 1.0: reach the private data stores of your AWS accounts from one terminal.** den opens the
tunnel, mints and refreshes the IAM credential the store accepts, and wraps the chores around
them: AWS SSO logins, a VPN, secrets and your own scripts as runbooks. It also serves itself to AI
agents without ever showing them a credential.

### 🔌 Data stores and tunnels

- One tunnel model for every store: RDS and Aurora (PostgreSQL, MySQL, MariaDB), ElastiCache
  (Redis OSS, Valkey), MemoryDB, DocumentDB, Redshift (provisioned and Serverless), OpenSearch
  (domains and Serverless), Neptune and Docker Compose. The tunnel opens, waits until the port
  accepts connections, mints the credential, refreshes it before it expires and reconnects when it
  drops. Kafka (Amazon MSK) is planned: a `kafka` service loads and says so when connected.
- Environments are written once under `environments:` and shared by services and runbooks with
  `env:`. Region and environment are labels you choose; `production: true` (or an environment
  called `prod` or `production`) makes an AI agent ask before it connects. den has no built-in
  knowledge of any account, and `den connect NAME` opens the dashboard with a configured service
  connecting.
- `credential_profile` is the profile the credential is minted as; it defaults to `aws_profile`,
  so an account with one role needs a single key. An empty profile or region is left off the AWS
  CLI calls, so `AWS_PROFILE` and the default chain work.
- [Transports](docs/transports/README.md): SSM (the default), an SSH bastion, an EC2 Instance
  Connect Endpoint or `kubectl port-forward`.
- SSH and SOCKS tunnels with a real data-path health check, and `openfortivpn` with SAML and an
  optional privilege helper so connecting never asks for your admin password.

### 🖥 The dashboard

- A split-pane TUI: Connect, Tunnel, Runbooks, AWS, VPN, Secrets, Create Env, Config, Help, Updates
  and Logs. Terminal tabs run the store's client (`psql`, `mysql`, `redis-cli`, `mongosh`) next to
  the logs, already logged in. A shortcuts pop-up (`h` or `?`) lists the keys of every panel.
- AWS SSO sessions are discovered from your AWS config and can be curated and renamed in
  `den.yaml`.
- Secrets from SOPS, AWS Secrets Manager, KeePassXC and HashiCorp Vault: masked until revealed,
  never written to disk or the log.
- A Help panel with one topic per feature, searchable with `/`.

### 📜 Runbooks

- Your scripts, one folder each with a `runbook.yaml`, run with den's context (AWS profile,
  region, live tunnel ports as `DEN_PORT_*`, `DEN_CREDENTIAL_PROFILE`). Each opens like a service,
  with its logs, run history and a terminal in its folder.
- Runbook sources: your team's runbooks from a git repository, pinned in `den.lock`, updated only
  when you ask, and asking again when a runbook changes.
- Runbooks can be written by an agent: `den runbook new --agent claude`, the Runbooks pane, or
  `den mcp`.

### 🔧 Configuration and checks

- `den.yaml` is found with `-c`, in the current directory or in the config directory; `den paths`
  lists every file den uses. A JSON Schema (`den schema`) gives editors completion and validation.
- `den config --check` lists every problem with its line and a hint; the Config panel shows the
  same list. Editing the file from the Config panel applies it when the editor closes, keeping the
  connections of services that did not change.
- `den init --profile P` writes a starter `den.yaml` from what an AWS profile can see.
- `den doctor` checks the tools, profiles, logins and ports a config needs and prints the install
  command for your OS.

### 🤖 AI agents

- `den mcp` lets Claude Code, Cursor or another MCP client list services, open tunnels and run
  runbooks. Credentials never leave den, production services need your confirmation, and what an
  agent may reach is limited by `mcp:` in `den.yaml`.

### 📦 Install and updates

- Install with one command from the project's GitHub Releases (`install.sh`, `install.ps1`), with
  `go install github.com/lukaszgard/den/cmd/den@latest`, or from a release archive; every download is
  checked against the release's `checksums.txt`.
- The Updates panel checks GitHub Releases and installs a new version in place. `updates.github_api`
  points it at a fork or a GitHub Enterprise server, and `updates.s3_bucket` at a mirror you host.
