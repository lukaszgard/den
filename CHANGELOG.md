# Changelog

## [1.7.1] - 2026-10-16

**Secrets no longer say "SSO login expired" while the AWS pane shows hours left.** With an
`[sso-session]` the access token in `~/.aws/sso/cache` lasts an hour, and the AWS CLI and the SDKs
swap in a new one from the refresh token until the IAM Identity Center session ends. The Secrets
pane and `den doctor` read only that token, so whenever it ran out with nothing using AWS, every
secret on the session was blocked, while the AWS pane, which reads den's own record, counted down
the hours still left.

### 🔧 Configuration and checks

- den renews a session's access token once it runs out, at start and then every minute, and on `r`
  in the Connect list. It never opens a browser, and the renewed file keeps working for secrets,
  `den doctor` and tools outside den.
- The AWS pane, the Secrets pane and `den doctor` decide in one place whether a login is live, so
  they can no longer disagree: a login counts while its token is valid or can still be refreshed.
  After `aws sso logout`, which leaves den's record behind, the AWS pane no longer counts down a
  login that is gone.
- When IAM Identity Center refuses a renewal, because the session ended or was revoked, every pane
  and `den doctor` read the session as expired until the next login, and its log says why. A
  renewal that fails on the way, or that another den or the CLI beat to the refresh token,
  changes nothing.
- A login made outside den that only its refresh token keeps alive reads
  `logged in · token renews on next use` in the AWS pane until den renews it.

## [1.7.0] - 2026-10-16

**Click a link to open it.** Since den took the mouse in 1.3.0, the terminal's own ⌘-click never
reached an address on screen. den now underlines every http(s) address it shows in blue and opens
it in the default browser with one click, and in Ghostty, kitty and foot the pointer turns into a
hand over it.

### 🖥 The dashboard

- Every http(s) address den shows is underlined in blue and opens in the default browser with a
  click: in a panel, the logs, a Help topic, the Updates panel, a bar, a runbook's output or a
  terminal tab, whether den wrote it or a program printed it, plain or as an OSC 8 link.
- A link opens when the button comes up, so a drag that starts on an address selects and copies
  it instead, and a double-click opens it once. Under a pop-up or a question a click opens
  nothing.
- Only `http` and `https` open: a link a program writes to a file or an app is left to the
  terminal (`M`), so a click never starts an app. An address cut with `…` is not a link, and one
  wrapped onto a second line opens only the part on its first line.
- With the mouse over a link the pointer turns into a hand, in a terminal that lets den set it
  (OSC 22): Ghostty, kitty and foot. It turns back off the link, and a key, a quit or a restart
  into a new version gives the terminal its own pointer back, so the shell or a program den
  starts never keeps the hand. Alacritty, GNOME Terminal and Windows Terminal keep their own.
- iTerm2 3.6 and earlier keep their own pointer, an I-beam in a circle, while den has the mouse.
  Hold ⌘ over a link for iTerm2's own hand; ⌘-click opens the link once.
- The Mouse help topic and the shortcuts page cover links and the hand, and say what to do when
  the pointer stays a hand after den was killed.

## [1.6.0] - 2026-10-15

**A VPN left running no longer blocks you.** When den's stop could not clear an openfortivpn hung
in its gateway logout, or the pppd it left behind, den started a second VPN on top of it that
could only fail, and took about 7 s to open because of the dead tunnel. den now kills what ignores
its stop, refuses to connect beside what is left, and on `c` shows what is still running with an
offer to stop it.

### 🖥 The dashboard

- `c` on a VPN that an openfortivpn or an orphaned pppd from an earlier session keeps from
  connecting opens a pop-up naming each process, its pid and how long it has run. `y` stops it
  and connects; `n` connects nothing.
- The pop-up shows the commands to stop it by hand: the kill by pid, and
  `sudo /opt/den/libexec/den-vpn stop <profile>` when the VPN helper is installed. `k` and `s`
  copy them. Without the helper it says that stopping asks for your admin password.
- When den's stop leaves something running, after `y` or after `d`, the pop-up says so and `y`
  tries again. While another VPN in den is up, `c` asks you to disconnect it first.
- A VPN left up by an earlier session is checked in the background: den opens at once and shows
  it as connecting until its `health_check` answers, instead of waiting about 7 s on a dead
  tunnel. A dead one reads "data path appears dead", and `c` offers to stop it and reconnect.

### 🔌 Data stores and tunnels

- den's VPN stop gives openfortivpn 5 seconds to log out of the gateway, then kills it: over a
  dead connection that logout never returns, and the process kept `ppp0` and the VPN's DNS. The
  pppd an openfortivpn leaves behind gets the same 5 seconds, then the same kill. With a
  `gateway` set, the stop behind the admin prompt no longer matches its own shell.
- den no longer starts a second openfortivpn beside one still running, or beside an orphaned
  pppd: it could only time out its login while holding another gateway session. The VPN log
  gives the command to stop what is left yourself.
- The helper's stop fails and names the pid when an openfortivpn or a pppd survives it, instead
  of printing `stopped`.

### 🔧 Configuration and checks

- `den vpn status` says whether the installed VPN helper script is current, and `den doctor`
  warns when it is outdated: an older helper's stop cannot clear a hung openfortivpn or pppd. Run
  `den vpn install-helper` again after updating den.

## [1.5.0] - 2026-10-15

**Copy text from den again.** Since den took the mouse in 1.3.0 a drag selected nothing, and the
Option-drag the docs suggested makes a rectangular selection in Terminal.app. den now selects for
itself: drag over text, let go, and it is copied. `M` hands the mouse back to the terminal when
its own selection is what you need.

### 🖥 The dashboard

- A drag selects text in reverse video, and letting go copies it, with `copied 3 lines` in the
  status bar. A key, a click or the wheel clears the highlight. Clicks, double-clicks and the
  wheel work as before.
- The selection stays in what it started in: the menu, the panel, the inside of a service's
  detail view or a runbook's view, or one bar at the bottom. It never picks up the menu beside a
  panel or a box's border, so a drag over a terminal tab copies only what its program wrote.
- It copies what is on screen: a line cut with `…` stays cut, a wrapped line comes out as several
  lines, and a drag does not scroll. Under a pop-up or a question a drag selects nothing.
- Without a system clipboard (no `xclip` or `wl-copy`, a session over SSH) den asks the terminal
  to set it with OSC 52 and says `(sent to the terminal)`; tmux passes that on only with
  `set -g set-clipboard on`.
- `M` gives the mouse to the terminal, whose own drag-to-select then works in full, and takes it
  back: from the menu, a panel, the detail view and the runbook view, unless a search box, a
  terminal tab or a form is taking the keys. While the terminal has it, the status bar ends with
  `M: mouse to den`. The shortcuts pop-ups list it.
- The Mouse help topic and the shortcuts page give each terminal's modifier for its own
  selection: Option in iTerm2, Fn in Terminal.app (or ⌘R), Shift in Ghostty, kitty, WezTerm,
  Alacritty and Windows Terminal.

## [1.4.0] - 2026-10-15

**The AWS panel says when an SSO login ends.** Each logged-in session's row gave a bar and a
countdown, and the only place the end time showed was the SSO cache, written in UTC, which reads
as local time when it is not. The row now gives the end on your clock, with the zone, so it
cannot be taken for UTC.

### 🖥 The dashboard

- The SSO Login box of the AWS panel ends each logged-in row with the local time the login ends:
  `2h 28m remaining · until 14:59:16 CEST`, or `until Fri 02:15:00 CEST` when that is another
  day. After a den login it is den's record of the login; after a login den did not start, it is
  the current access token's expiry.
- The bar gets shorter to make room. A panel too narrow for the clock leaves it out and keeps the
  countdown, so the row never wraps. The Connect list keeps only its countdown.

## [1.3.0] - 2026-10-14

**den takes the mouse.** A click selects what it lands on, a double-click does what `enter`
does there, and the wheel moves through what is under the pointer, in the menu, the panels,
a service's detail view and a runbook's view. The keys work as before. While den has the
mouse a plain drag no longer selects text: hold Option (iTerm2, Terminal.app) or Shift (most
other terminals) to select. The Help panel's new Mouse topic says what goes where.

### 🖥 The dashboard

- A click on a menu row shows its panel, the menu keeping the focus; a double-click opens it.
- A click on a row of Connect, Tunnel, VPN, AWS, Secrets, Create Env, Runbooks, the Config
  problem list or the Help topics focuses the panel and selects the row; a double-click does
  what `enter` does on it, so it opens a service's detail view or a runbook's view, logs in
  to an AWS session, opens a secret or a Help topic.
- The wheel over a panel moves through it as `↑` and `↓` do, without taking the focus from
  the menu; the wheel over the menu does nothing. The shortcuts pop-up (`h`) scrolls with it.
- Pop-ups, forms and y/n questions stay keyboard-only: a click or the wheel while one is up
  does nothing, so a stray click never answers one.
- In a service's detail view and a runbook's view, a click on a tab shows it (`‹` and `›`
  step to the tabs the bar has no room for), and a click on a terminal's screen types into
  it; picking another tab gives den the keys back. The wheel scrolls the logs, and reaches
  the program of a terminal being typed into as arrow keys.
- The tabs of both views sit between two rules: a line under the tab bar now keeps them apart
  from the first line of the body.

### 📜 Runbooks

- The first tab of a runbook's view is now **Run**, not Logs: it shows the current or last
  run, and no longer shares its name with the Logs panel. The bar reads `Run │ History │ About
  │ Files`.
- A click picks a run in History or a file in Files; a double-click shows the run's whole log
  or the file.

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
