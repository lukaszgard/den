# Changelog

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
