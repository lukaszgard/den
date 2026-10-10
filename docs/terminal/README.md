# Terminal tabs

## What it is

In the detail view of a connected data store, `t` runs its client — `psql` for an
RDS PostgreSQL service, say — **inside den, in a tab next to the logs, already
logged in**. There is no command to copy into another terminal and no token to
paste: den starts the client against the local end of the tunnel and hands it the
current credential.

```
╭──────────────────────────────────────────────────────────────────────────╮
│  rds  orders-int  Status: ● CONNECTED  Local port: 50111  Reconnect: ●   │
│ ──────────────────────────────────────────────────────────────────────── │
│  psql "host=localhost port=50111 dbname=orders user=app_iam …"   y: copy │
│ ──────────────────────────────────────────────────────────────────────── │
│─ Logs │ 1 psql ● │ 2 migration ● │ 3 psql ○                              │
│orders=> select count(*) from orders;                                     │
│ count                                                                    │
│-------                                                                   │
│  1284                                                                    │
│orders=> █                                                                │
╰──────────────────────────────────────────────────────────────────────────╯
  TERMINAL  esc/ctrl+t: den keys
```

A service can have up to nine terminals. Each is a real terminal: the client's
pager, `\e` into your editor, colours, line editing and `ctrl+c` behave as they do
outside den. Tabs can be renamed and closed, and they keep running while you look
at the logs or leave the detail view.

What `t` runs depends on the service:

| Service                               | `t` runs                                                        |
|---------------------------------------|-----------------------------------------------------------------|
| RDS and Aurora                        | `psql`, or `mysql` for `engine: mysql`                          |
| Redshift (provisioned and Serverless) | `psql`                                                          |
| ElastiCache, Valkey, MemoryDB         | `redis-cli`                                                     |
| DocumentDB                            | `mongosh`                                                       |
| OpenSearch, Neptune                   | your own shell, with `DEN_URL` set: see [Shell tabs](#shell-tabs) |

Docker environments, tunnels, VPNs and SSO sessions have no tabs: there is nothing
to log in to. A [runbook's view](../runbooks/README.md#the-runbook-view) has the same
tabs, opening a shell in the runbook's folder or its script in the editor. `y`/`Y` copy the connect command everywhere, as before.

## Configuration

None. There is no `den.yaml` key for this feature: every service in the table has it.

How the client gets the credential, which is not something to configure but worth
knowing:

| Service             | Runs                                   | Credential reaches the client through                               |
|---------------------|----------------------------------------|---------------------------------------------------------------------|
| RDS `postgres`      | `psql "<the shown connection string>"` | `PGPASSWORD` in the client's environment, so it works with `update_pgpass` off |
| RDS `mysql`         | the shown `mysql …` command            | den's option file `~/.config/den/mysql/<local_port>.cnf`, which every token refresh rewrites |
| Redshift            | `psql "<the shown connection string>"` | `PGPASSWORD` in the client's environment, so it works with `update_pgpass` off |
| ElastiCache, MemoryDB | the shown `redis-cli …` command      | `REDISCLI_AUTH` in the client's environment. Not set for a cache without `user_id`, which has no token |
| DocumentDB          | the shown `mongosh …` command          | Nothing to hand over: DocumentDB has no token. `AWS_PROFILE` is set in the client's environment and the driver signs the login with that profile |
| OpenSearch, Neptune | your shell                             | Nothing: the signing proxy holds the credentials and the shell gets none |

A token or password is never put on the client's command line, where other users of
the machine could read it from the process list.

## Prerequisites

- **The client**: `psql`, the MySQL 8+ client, `redis-cli` 6+ or `mongosh` 2.5+,
  whichever the service uses. den finds it the way `den doctor` does, which includes
  Homebrew's keg-only `libpq` and `mysql-client` that are not on `PATH`. A missing
  client is reported with its install command; `den doctor --for <type>` (`rds`,
  `mysql`, `redshift`, `redis`, `documentdb`) prints every step. A shell tab needs
  no client.
- **A connected service**: `t` needs the tunnel up and the credential minted. On a
  service that is not connected it says so and does nothing.
- Everything the service itself needs ([RDS](../rds/README.md),
  [Redshift](../redshift/README.md), [MemoryDB](../memorydb/README.md),
  [DocumentDB](../documentdb/README.md), [OpenSearch](../opensearch/README.md),
  [Neptune](../neptune/README.md)). Nothing else: no extra IAM permission and no
  environment variable.

| Platform              | How the terminal is made                                    | Status                                  |
|-----------------------|-------------------------------------------------------------|-----------------------------------------|
| macOS, Linux          | A pseudo-terminal; the client gets a controlling terminal   | Tested, including an end-to-end test of den itself on a pty |
| Windows 10 1809+ / Server 2019+ | ConPTY, the API Windows Terminal uses             | **Built, not yet tried.** den's CI has no Windows runner |

On Windows a failure stays inside the tab: a client that cannot start is a message
in the status bar, and closing the tab stops the client.

## Usage

1. Open **Connect**, select the service, press `Enter` for the detail view and
   `c` to connect.
2. Press `t`. A tab opens and the client logs in. For an RDS PostgreSQL service:

   ```
   psql (16.4, server 15.5)
   SSL connection (protocol: TLSv1.3, cipher: TLS_AES_256_GCM_SHA384)
   Type "help" for help.

   orders=>
   ```

   The help line under the box reads `TERMINAL`, and the tab with the keyboard is
   highlighted: from here **the keys go to the client**, including `q`, `Tab` and
   `ctrl+c` (which cancels the running query, not den).
3. `Esc` gives the keyboard back to den and leaves the terminal on screen, as "back"
   does everywhere else in den. A second `Esc` closes the detail view; the client
   keeps running either way.

   While an editor (`\e`), the pager or an agent has the tab, the first `Esc` is
   theirs: vim cannot be used without it, and Claude Code stops a reply with it. A
   second `Esc` within half a second of the first gives den the keys; the editor has
   had its one `Esc` by then. The help line reads `esc esc/ctrl+t: den keys` instead of
   `esc/ctrl+t: den keys`. A key between the two (`Esc` `i` `Esc` in vim) makes them
   two single ones, which both stay the editor's. `ctrl+t` is the key that always
   works: the one key the client never sees.

### Keys

While den has the keyboard (after `Esc` or `ctrl+t`, or on the Logs tab):

| Key                          | Action                                                                  |
|------------------------------|-------------------------------------------------------------------------|
| `t`                          | Open a new terminal, logged in, and type into it                        |
| `←` `→`, `Tab` `Shift+Tab`   | Select the previous / next tab; Logs is the first                       |
| `1`–`9`                      | Jump to that terminal and type into it (`ctrl+t` `2` switches mid-work) |
| `Enter`                      | Type into the selected terminal; restart it if its client has exited    |
| `n`                          | Rename the selected tab: `Enter` saves, `Esc` cancels                   |
| `x`                          | Close the selected tab; asks `y` first while its client is still running |
| `d`                          | Disconnect; asks `y` first while terminals are running, since it stops them |
| `c` `r` `a` `y` `Y` `p` `Esc` | As before                                                              |

`y`, `Y` and `p` copy to the system clipboard. Where there is none (a Linux machine without
`xclip`/`wl-copy`, a session over SSH) den asks the terminal to set it (OSC 52), which most
terminals accept; the status line then says "sent to the terminal". Over tmux, set
`set-clipboard on`.

### What the dots mean, and when a client stops

`●` on a tab is a running client, `○` one that has exited. An exited tab keeps its
last screen, so the reason a login failed stays readable, until you close it or
press `Enter` to start the client again in the same tab with the current token.

| When                                        | What happens to the clients                                       |
|---------------------------------------------|-------------------------------------------------------------------|
| You leave the detail view                   | Nothing. They keep running; the tabs are there when you come back |
| den is reconnecting the tunnel              | Nothing. The client keeps running and can reconnect once the tunnel is back |
| The service is disconnected (`d`) or fails  | They are stopped; the tabs and their names stay, to restart after reconnecting |
| A config reload (`R`) removes the service   | They are stopped and the tabs are gone                            |
| den quits or restarts after an update       | They are stopped. The restart prompt lists them                   |

If a client dies while you are typing into it, den keeps ignoring keys until `Esc`,
`ctrl+t` or `Enter`. The rest of the line you were typing is therefore not read as
den's keys, where `x` would close the tab.

The same care is behind the question on `d`. `Esc` is easy to press out of habit,
and the keys typed after it are den's: without the question, the `d` of a `delete`
typed a moment too late would disconnect the service and stop every terminal.

### How den knows an editor has the tab

Editors and pagers switch the terminal to its alternate screen, or at least put the
arrow keys into application mode (`less -X`); den sees both. `vim`, `vi`, `nano`,
`less` and `less -X` are recognised.

Some prompts switch the arrow keys too: zsh with oh-my-zsh does it at every prompt.
den therefore asks the terminal which program is in front. When it is the client or
shell the tab started, at its own prompt, `Esc` is den's whatever the arrow keys do. A
pager started from that shell is a program of its own, so it keeps its first `Esc`. On
Windows that question has no answer, and a prompt that switches the arrow keys is
treated like an editor: it keeps its first `Esc`, and `Esc` twice or `ctrl+t` leaves.

Two `Esc`s typed so fast that they arrive together (over a slow ssh, say) count as two:
the editor gets one, den the other. A terminal that reports keys unambiguously (the kitty
keyboard protocol: kitty, Ghostty, WezTerm, iTerm2 and others, not Apple Terminal, and
tmux only when it is set up to pass it on) never merges them, so there `alt`+`Esc` is the
key you pressed and goes to the client.

Two things follow from `Esc` being den's at the prompt:

- `Esc` as a Meta prefix (`Esc` `b` for back-a-word) does not reach the client when
  typed as two separate keys. `alt`+`b` and `ctrl`+arrows do.
- readline's vi mode needs `Esc` to leave insert mode, and at the prompt den takes
  it (`ctrl+[` is the same key). vi mode at the prompt is therefore not usable in a
  tab. The editor behind `\e` is unaffected.

### Shift+Enter, the cursor, the title

Besides the keys above, den uses what the terminal it runs in can do. Each of these falls
back to what a plain terminal does, so nothing breaks where it is missing, but nothing
changes either: if you see no difference, your terminal is probably not reporting it.

| What                         | You see                                                                   | Needs a terminal that |
|------------------------------|---------------------------------------------------------------------------|-----------------------|
| `Shift`+`Enter`              | A new line, not a submit, in an agent's prompt (Claude Code, Codex). At a shell or `psql` prompt it is `Enter` | reports keys unambiguously (the kitty keyboard protocol: kitty, Ghostty, WezTerm, iTerm2; not Apple Terminal; tmux only if set up to pass it on) |
| `Alt`+`Esc`                  | Reaches the client as the key you pressed, instead of counting as two `Esc`s at once | the same |
| The cursor                   | The terminal's own, in the shape the client asks for (vim's bar in insert mode), blinking as it asks. It shows while you type into a tab and is hidden while den has the keyboard | shows a cursor shape (nearly all do) |
| The window or tab title      | `den · <pane> · N connected`, with the count left out when nothing is connected | sets titles (nearly all do) |
| The tab's progress bar       | Indeterminate while a runbook or sequence runs, a percentage while an update downloads | draws one (Windows Terminal, Ghostty, iTerm2, ...) |
| Copy over SSH                | `y`, `Y` and `p` reach your local clipboard when den has none; the status line says "sent to the terminal" | accepts OSC 52 (most do; tmux needs `set-clipboard on`) |

A den built before these changes shows none of them. `go version -m $(which den)` prints the
commit it was built from.

To see them in a terminal that has them: open a shell tab (`t`) and run `vim`; in insert
mode the cursor is a bar. Start a runbook and watch the tab's progress bar and the title.
In an agent tab, `Shift`+`Enter` adds a line.

### The token and long sessions

The IAM token is only checked when the client connects, so an open session outlives
the token's 15 minutes. A client reconnecting by itself later (`\c`, or psql's
automatic reset after a dropped connection) reuses the token it started with and is
rejected once that has expired. Restart the tab instead: `Enter` on the exited tab,
or `x` and `t`.

The same goes for Redshift's temporary password and for the IAM token of a cache.
DocumentDB has no token to go stale: `mongosh` signs every login with what the
profile resolves at that moment.

### Shell tabs

OpenSearch and Neptune are HTTP endpoints behind den's signing proxy. No client logs
in to them, so their `t` opens **your own shell** in the tab — `$SHELL`, or
`%COMSPEC%` on Windows — with `DEN_URL` set to the proxy:

```
$ curl -s "$DEN_URL/_cat/indices?v"      # OpenSearch
$ curl -s "$DEN_URL/status"              # Neptune
```

A request to `$DEN_URL` is signed by den, exactly as the connect command shown above
the tabs is. The tab is named `shell` until you rename it.

It is your shell with one more variable: it starts in the directory den was started
in, with den's environment and your own startup files. den adds no credential to it.
In `cmd.exe` the variable is written `%DEN_URL%`.

### Limits

- **Tried against live stores: none yet.** The tabs are tested with a shell standing
  in for the client, and each client is started with the command den already shows.
  If a login fails in a tab while the copied command works in your own terminal,
  that is a bug in den.
- **No scrollback inside den.** Long results go through the client's pager, which
  sees the real size of the tab. Output that has scrolled off the top of a tab
  cannot be scrolled back to.
- **Tab names are not saved.** The clients stop when den does, so there is nothing
  to restore a name onto.
- **Selecting text** uses your terminal's own selection, which is line-based across
  den's frame. For a large result, `\o file` in psql or `tee` in mysql is easier.
- **`ctrl+t`** is taken by den, so readline's transpose-characters is not available
  in a tab.
