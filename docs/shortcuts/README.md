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
│ ─────────────────│  Connection                                          │─────────── │
│Starting session w│    c      Connect: open the tunnel, mint a token     │            │
│Port 50111 opened │    d      Disconnect: close tunnel and terminals     │            │
│Waiting for connec│    a      SSM port-forward auto-reconnect on/off     │            │
│IAM token generate│    a reopens it after a drop (SSM idle timeout).     │            │
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
| den             | `M` the mouse to the terminal         | every service                                                    |

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
| all seven | den (`Tab`, `R`, `M`, `h` `?`, `Esc`, `q`)                                      |

A runbook's view lists Runbook (`r`, `x`, `y`, `o`, `t`, `e`, `m`, `D`), Tabs, View and den (`M`).

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
| `M`                 | Give the mouse to the terminal, whose own drag-to-select then works; `M` again gives it back to den (also in the detail and runbook views; text in a search box, a terminal tab or a form) |
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

## Mouse

den reads clicks, drags and the wheel in every panel and view, and the keys keep working
as before. A click selects, a double-click does what `Enter` does, a drag selects text
and copies it, the wheel is `↑` or `↓` sent to what is under the pointer, and a click
on a link opens it in the browser.

| Where                              | Click                                       | Double-click                                 | Wheel                                        |
|------------------------------------|---------------------------------------------|----------------------------------------------|----------------------------------------------|
| The menu                           | Show the panel; the menu keeps the focus    | Open the panel                               | Nothing                                      |
| A panel's rows                     | Focus the panel and select the row          | What `Enter` does on the row                 | Move through the panel without focusing it   |
| A view's tabs, `‹` `›`             | Show the tab; leaves a terminal typed into  | On a terminal: type into it, or restart it   | —                                            |
| A terminal's screen                | Type into it (not into an exited one)       | —                                            | `↑` `↓` to its program while you type into it |
| History, Files                     | Pick a run or a file                        | The run's whole log, or the file             | Move through the runs or the tree            |
| Run, About, a file shown, the logs | —                                           | —                                            | Scroll                                       |
| The shortcuts pop-up               | Nothing                                     | Nothing                                      | Scroll it                                    |
| A link, anywhere outside a pop-up  | Open it in the browser                      | Open it once                                 | —                                            |
| Drag: any text, outside a pop-up   | Press and drag: select it; let go: copy it  | —                                            | Clears the selection                         |

- **Selecting text.** A drag selects in reverse video, and letting go copies the text,
  with `copied N lines` in the status bar; a key, a click or the wheel clears it. The
  selection stays in what it started in (the menu, the panel, the inside of a view's
  box, or one bar), so no border or menu text comes with it. It copies what is on
  screen: a line cut with `…` stays cut, a wrapped line comes out as several lines, and
  a drag does not scroll. For the terminal's own selection, press `M` (the status bar
  then ends with `M: mouse to den`; `M` again gives the mouse back to den), or hold the
  terminal's modifier while you drag: `Option` in iTerm2, `Fn` in Terminal.app (or `⌘R`,
  View → Allow Mouse Reporting), `Shift` in Ghostty, kitty, WezTerm, Alacritty and
  Windows Terminal.
- **Links.** Every http(s) address den shows is underlined in blue and opens in the
  default browser with a click: in a panel, the logs, a Help topic, the Updates panel,
  a bar, a runbook's output or a terminal tab, whether den wrote it or a program
  printed it, plain or as an OSC 8 link. It opens when the button comes up, so a drag over it
  selects and copies it instead. Only `http` and `https` open: a program's link to a
  file or an app is left to the terminal, so a click never starts an app. An address
  cut with `…` is not a link, and one wrapped onto a second line opens only the part
  on its first line. With `M`, the terminal handles links itself (⌘-click or
  Ctrl-click in most).
- **The hand.** With the mouse over a link the pointer turns into a hand, in a
  terminal that lets den set it (OSC 22): Ghostty, kitty, foot, iTerm2 3.5 and xterm.
  Alacritty, GNOME Terminal and Windows Terminal cannot, and keep their own pointer.
  Terminals name the shapes differently (CSS names in Ghostty, kitty and foot, X11
  ones in iTerm2 and xterm), so den sends both. It changes as the mouse moves, and a
  key gives the terminal its own pointer back until the next move, so a program den
  starts, or the shell after it, never gets the hand.
- **Keyboard-only.** Pop-ups, forms and y/n questions take no click, no drag and no
  wheel, so a stray click never answers one; hint lines and the bars at the bottom are
  not buttons, but for a link in them.
- **Terminal tabs.** A click hands a tab the keyboard, but the program in it never sees
  the mouse: `vim` or `less` get the wheel as arrow keys, and no clicks.

The Help panel has the same as a topic: Mouse, under Getting started.

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
