# Keyboard shortcuts

## What it is

In the detail view of any service, in a runbook's view, and in the Connect, Tunnel,
VPN, AWS, Secrets, Runbooks and Help panels, `h` or `?` opens a pop-up in the middle of the screen that lists **every key
that works there and what it does**. `Esc` closes it.

The help line under the detail box only names the keys (`r refresh`,
`a auto-reconnect`) and loses its end on a narrow terminal. The pop-up is where to
look up what a key really does before pressing it on a live connection.

It always fits the terminal. The help the four panels had before was a box as tall
as its text, configuration examples included, and on most terminals its top was cut
off; that text is now in the Help panel, which scrolls.

```
╭──────────────────────────────────────────────────────────────────────────────────────╮
│  rds  orders-int  Status: ● CONNECTED  Local port: 50111  Auto-reconnect: ●          │
│ ──────────────────────────────────────────────────────────────────────────────────── │
│  psql "host=local╭──────────────────────────────────────────────────────╮            │
│ ─────────────────│  Shortcuts — orders-int                              │─────────── │
│─ Logs │ 1 psql ● │                                                      │            │
│Starting session w│  Connection                                          │            │
│Port 50111 opened │    c      Connect: open the tunnel, mint a token     │            │
│Waiting for connec│    d      Disconnect: close tunnel and terminals     │            │
│IAM token generate│    a      SSM port-forward auto-reconnect on/off     │            │
│                  │    a reopens it after a drop (SSM idle timeout).     │            │
│                  │                                                      │            │
│                  │  Token                                               │            │
│                  │    r      Mint a new token now                       │            │
│                  │    p      Copy the token                             │            │
│                  │    Renewed while connected, auto-reconnect or not.   │            │
│                  │                                                      │            │
│                  │  Clipboard                                           │            │
│                  │    y      Copy the connect command                   │            │
│                  │    Y      Copy it with the token inlined             │            │
│                  │                                                      │            │
│                  │  Terminal tabs                                       │            │
│                  │    t      New tab: the client, already logged in     │            │
│                  │    ← →    Previous / next tab (or tab, shift+tab)    │            │
│                  │    1-9    Jump to a terminal and type into it        │            │
│                  │    enter  Type into the selected tab, or restart it  │            │
│                  │    n      Rename the selected tab                    │            │
│                  │    x      Close the selected tab                     │            │
│                  │    While typing, esc or ctrl+t returns to den.       │            │
│                  │                                                      │            │
│                  │  View                                                │            │
│                  │    ↑ ↓    Scroll the logs (pgup, pgdn by page)       │            │
│                  │    h ?    Open or close this help                    │            │
│                  │    esc    Back to the list                           │            │
│                  │                                                      │            │
│                  │  esc: close                                          │            │
│                  ╰──────────────────────────────────────────────────────╯            │
╰──────────────────────────────────────────────────────────────────────────────────────╯
  h: help  c: connect  d: disconnect  t: terminal  r: refresh  a: auto-reconnect  …
```

What is behind the pop-up stays on screen, dimmed, and keeps updating: the logs, the
status and the token bar are still live.

## Configuration

None. There is no `den.yaml` key for this feature: every service has it.

## Prerequisites

None: no tool, no IAM permission, no environment variable. The service does not have
to be connected.

## Usage

1. Open the detail view of a service (`Enter` on it in **Connect**, **Tunnel** or
   **VPN**), or focus the **Connect**, **Tunnel**, **VPN**, **AWS**, **Secrets** or
   **Runbooks** panel.
2. Press `h` or `?`.
3. Read, then `Esc` (or `h`, `?` or `q`). A second `Esc` goes back as it did before:
   out of the detail view, or to the menu.

| Key                  | While the pop-up is open                          |
|----------------------|---------------------------------------------------|
| `Esc`, `h`, `?`, `q` | Close it                                          |
| `↑` `↓` / `k` `j`    | Scroll it, when the terminal is too short for it  |
| `ctrl+c`             | Quit den, where it would without the pop-up       |
| anything else        | Nothing                                           |

**No other key acts while it is open.** `c`, `d`, `r` and `t` are one keypress from
changing a live connection, so over the pop-up they do nothing; close it first.

**In the detail view it lists only what this service has.** The groups follow the
service:

| Group           | Keys                                  | Shown for                                                        |
|-----------------|---------------------------------------|------------------------------------------------------------------|
| Connection      | `c`, `d`                              | every service, worded for its type (tunnel, VPN login, SSO login, docker compose) |
|                 | `a` auto-reconnect                    | services whose tunnel den reopens (every SSM-tunnelled one)      |
|                 | `r` re-read the cached SSO token      | AWS SSO sessions                                                 |
| Token           | `r` mint a new one, `p` copy it       | services holding a token (RDS, Redis/MemoryDB with IAM, Redshift) |
| Runtime toggles | `r` reconnect, `m` monitor, `H` health check | tunnels; a VPN has `r` and `H`                            |
| Clipboard       | `y` copy the command                  | every service but a VPN                                          |
|                 | `Y` copy it with the token inlined    | services whose command can carry it                              |
| Terminal tabs   | `t`, `←` `→`, `1`-`9`, `Enter`, `n`, `x` | services with [terminal tabs](../terminal/README.md): every tunnelled data store |
| View            | `↑` `↓`, `h` `?`, `Esc`               | every service                                                    |

`r` means three different things depending on the service, which is the main reason
the pop-up exists: a new token for RDS, the reconnect toggle for a tunnel or a VPN,
and a re-read of the token cache for an SSO session.

**`a` is auto-reconnect, not token refresh.** The token needs no toggle: den renews
it by itself about two minutes before it expires. `a` decides whether the tunnel is
reopened after it drops.

**It fits the terminal.** On a terminal too short for one column the groups go side
by side, which needs about 110 columns. When that does not fit either, the pop-up
scrolls and its last line reads `↑↓: scroll  esc: close`.

**In a terminal tab the keys are the client's.** While a tab has the keyboard (the
help line reads `TERMINAL`), `h` and `?` are typed into `psql` like any other key.
`Esc` or `ctrl+t` first, then `h`. The same holds for a filter, a password prompt and
a runbook's parameter form: there `h` is text.

**`Tab` stays in a pop-up.** While a Runbooks pop-up is open (a form, a question, the
sources), `Tab` does nothing: it neither leaves for the menu nor submits the form. The one
exception is the new-runbook form with two or more agents, where `Tab` (or `↓` from the
request's last row) moves from the request to the Agent row; on the name it does nothing.
`Enter` and `shift+tab` move between a form's fields.

**In a panel it lists the panel's keys**, then the ones every panel has:

| Panel    | Groups                                                                           |
|----------|----------------------------------------------------------------------------------|
| Connect  | Services (`Enter`, `/`, `c`, `d`, `y`), Token and tunnel (`r`, `a`)              |
| Tunnel   | Tunnels (`c`, `d`, `Enter`, `y`), Runtime toggles (`r`, `m`, `H`, `v`)           |
| VPN      | VPN (`c`, `d`, `Enter`), Runtime toggles (`r`, `H`)                              |
| AWS      | SSO sessions (`Enter` / `c`, `d`, `o`, `/`), AWS files (`e`, `E`)                |
| Secrets  | Secrets (`Enter`, `/`, `Esc`), Values (`r`, `a`, `t`, `y`, `e`), Access (`L`, `l`) |
| Runbooks | Runbooks (`Enter`, `r`, `x`, `n`, `m`, `D`, `s`, `/`, `w`)                        |
| Help     | Topics (`↑` `↓`, `Enter`, `/`, `Esc`), or Topic (scroll, `/`, `n` `N`, `Esc`) in an open one |
| all seven | den (`Tab`, `R`, `h` `?`, `Esc`, `q`)                                           |

A runbook's view lists Runbook (`r`, `x`, `y`, `o`, `t`, `e`, `m`, `D`), Tabs and View.

The pop-up is only about keys. What each feature does and how its part of `den.yaml`
is written is in the Help panel (`?` from the menu), one topic per feature with
examples, and in each feature's own docs: [tunnel](../tunnel/README.md), [vpn](../vpn/README.md),
[secrets](../secrets/README.md), [runbooks](../runbooks/README.md) and the rest.

The Updates panel keeps its own `h` help. In the Config and Logs panels, which have no
pop-up, `h` goes back to the menu.

## Moving around

The keys that work in every panel, and those of the Connect panel, which the pop-up
describes in context. Each other panel's keys are in its own page
([AWS](../aws/README.md#usage), [Secrets](../secrets/README.md), [Runbooks](../runbooks/README.md),
[Updates](../updates/README.md#usage)), and the terminal tabs' in
[Terminal tabs](../terminal/README.md).

| Key                 | Action                                                                  |
|---------------------|-------------------------------------------------------------------------|
| `↑` / `k`           | Move up                                                                 |
| `↓` / `j`           | Move down                                                               |
| `Tab`               | Toggle focus between the left menu and the right panel                  |
| `→` / `l` / `Enter` | Focus the right panel                                                   |
| `←` / `Esc`         | Focus the left menu (`h` too, in a panel without a shortcuts pop-up)    |
| `L`                 | Jump to the Logs panel                                                  |
| `?`                 | Jump to the Help panel: one topic per feature; `/` searches them all    |
| `R`                 | Reload the config file, checked first; see [Editing den.yaml](../config-reload/README.md) |
| `q` / `Ctrl+C`      | Quit, which stops every tunnel                                          |

In the Connect panel:

| Key       | Action                                                                                  |
|-----------|-----------------------------------------------------------------------------------------|
| `c`       | Connect the selected service (asks for confirmation for production)                     |
| `d`       | Disconnect it                                                                           |
| `r`       | Refresh the IAM token or temporary credentials of a connected service                   |
| `a`       | Toggle auto-reconnect of the SSM port-forward: reopen it after a drop, e.g. the SSM idle timeout (any SSM-tunnelled service). Token refresh does not depend on it |
| `y`       | Copy the connect command (never with a credential: `Y` in the detail view)              |
| `/`       | Filter services by name, type or environment (type to search, `Esc` to clear)                        |
| `Enter`   | Open the detail view                                                                    |
| `h` / `?` | The shortcuts pop-up                                                                    |

In the detail view, `y` copies the connect command, `Y` the command with the password or
token inlined, and `p` the raw token.

## Searching the changelog

The changelog (`s` in the Updates panel) is a long document, and searches like a Help
topic does:

| Key            | What it does                                                         |
|----------------|----------------------------------------------------------------------|
| `/`            | Start a search; the view jumps to the first match as you type        |
| `Enter`        | Stop typing and keep the matches                                     |
| `n` / `N`      | Next / previous match, wrapping around                               |
| `Esc`          | While typing: cancel and scroll back. After: clear the search        |
| `Backspace`    | Delete a letter; on an empty search, close it                        |

The search ignores case and starts below where you are reading. The line of the
current match shows in reverse, and the hint under the box reads `2/5` for the second
of five. While you type, every key is a letter of the search: `q`, `h`, `R` and `s`
do not quit, leave, reload or close the changelog. A second `Esc`, with no search on,
closes the changelog as before. The Help panel's own search, across every topic, is in
[docs/help-panel](../help-panel/README.md).
