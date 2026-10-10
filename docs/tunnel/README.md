# SSH & SOCKS tunnels

## What it is

The Tunnel feature lets you manage persistent SSH tunnels (or any long-running shell command) directly from the `den` TUI. You define your tunnel commands in `den.yaml` and `den` takes care of starting, monitoring, and restarting them — showing real-time status, uptime, and parsed port mappings in the dashboard.

It supports any shell command, making it suitable for:
- **SOCKS proxies** (`ssh -D`)
- **Local port forwards** (`ssh -L`)
- **Combined tunnels** (multiple `-D` and `-L` flags in a single command)

## Configuration (`den.yaml`)

Add a `tunnels` section with one entry per tunnel:

```yaml
tunnels:
  - name: "Corp VPN Tunnel"
    description: "SOCKS proxy + internal service forwards"
    command: "ssh -N -T -D 5555 -L 5432:internal-db.corp:5432 user@bastion.corp.net"
    reconnect: true
    health_check: "intranet.corp.net:443"
    monitor: true

  - name: "Dev Tunnel"
    description: "Forward local dev ports"
    command: "ssh -N -T -L 8080:app.internal:80 user@jump.dev.net"
```

| Field         | Required | Description                                 |
|---------------|----------|---------------------------------------------|
| `name`        | yes      | Display name shown in the TUI               |
| `description` | no       | Optional subtitle shown in the detail panel |
| `command`     | yes      | Full shell command executed via `sh -c`     |
| `reconnect`   | no       | Restart the tunnel after it drops (backoff 5 s → 60 s) |
| `health_check`| no       | `host:port` dialled **through** the `-D` SOCKS proxy before the tunnel counts as connected. Requires a `-D` port in `command` |
| `monitor`     | no       | Re-run the connection checks every 15 s while connected; two misses in a row count as a drop |
| `auto_connect.vpn` | yes, in the block | The VPN the tunnel runs over — the `name` of an entry in the `vpn:` section. See [Auto-connect with a VPN](#auto-connect-with-a-vpn) |
| `auto_connect.enabled` | no | Follow that VPN (default `true`); `false` keeps the link but turns the behaviour off |

### SSH flags parsed by the TUI

The tunnel screen automatically parses the command and displays port mappings:

| Flag                         | Displayed as                 |
|------------------------------|------------------------------|
| `-D <port>`                  | `:<port>  SOCKS proxy`       |
| `-L <local>:<host>:<remote>` | `:<local> → <host>:<remote>` |

## Connection checks

`den` only shows a tunnel as **connected** once it has proven that the tunnel works. Until then it stays **connecting** (yellow):

1. **Before starting**, every `-D`/`-L` port must be free. If something already listens there (often an ssh left over from a terminal), the tunnel fails right away with `port 5555 is already in use`. Without this check, the next step would pass because of the other process.
2. **After starting**, every `-D`/`-L` port must accept connections. ssh only opens these ports *after* it has logged in, so a listening port means the session is really up.
3. **If `health_check` is set**, `den` then asks the `-D` SOCKS proxy to connect to that `host:port`. This proves traffic reaches the far side, not just that ssh is listening locally. Pick something internal and always on.

If the checks don't pass within **30 s**, or the command exits first, the tunnel shows an error quoting ssh's last output (e.g. `Permission denied (publickey)`). With `reconnect: true`, `den` then retries with backoff.

A command with no `-D`/`-L` ports and no `health_check` has nothing to check. It counts as connected once it has stayed up for 2 s.

**While connected**, `monitor: true` re-runs the checks every 15 s. ssh can stay alive long after its connection has died. After two misses in a row, `den` stops it and reports `connection check failed 2 times in a row`, and `reconnect: true` brings it back.

## Auto-connect with a VPN

A tunnel whose host is only reachable over a VPN can follow that VPN instead of being
started by hand once the VPN is up — and instead of retrying uselessly every 5–60 s
(`reconnect: true`) while the VPN is down:

```yaml
tunnels:
  - name: "Corp Tunnel"
    command: "ssh -N -T -D 5555 user@bastion.corp.net"
    reconnect: true
    auto_connect:
      vpn: "Corp VPN"   # the name of an entry in the vpn: section
      enabled: true     # optional, default true

vpn:
  - name: "Corp VPN"
    config_file: ~/.config/den/corp-vpn.conf
```

What den does, in the TUI:

| When | den |
|---|---|
| The VPN becomes **connected** — you connect it, its own `reconnect` brings it back, or den adopts a VPN left up by a previous session at startup | connects the tunnel, unless it is already running |
| The VPN stops being connected — disconnected, error, reconnecting | stops the tunnel |
| You press `d` on the tunnel while the VPN stays up | nothing: it stays down until the VPN next comes up |
| You press `c` on the tunnel while the VPN is down | connects it anyway and warns `Corp VPN is not connected — Corp Tunnel may not reach its host` |

Only changes of the VPN's state count, so den never fights what you just did by hand.
`v` on the Tunnel panel switches auto-connect off and on at runtime, like the other
toggles; with `enabled: false` (or after `v`) the VPN no longer starts or stops the
tunnel, but the warning on `c` stays. `den.yaml` is validated: `auto_connect.vpn` must
name a configured VPN.

This is TUI behaviour: `den connect`, `den mcp` and the other commands do not follow
VPNs.

Recommended ssh options, so ssh itself fails fast instead of hanging:

```yaml
command: "ssh -N -T -o BatchMode=yes -o ExitOnForwardFailure=yes -o ServerAliveInterval=15 -D 5555 user@bastion"
```

- `BatchMode=yes`: fail instead of waiting for a password prompt that `den` can't answer.
- `ExitOnForwardFailure=yes`: exit if a port can't be bound, instead of running on without it.
- `ServerAliveInterval=15`: notice a dead connection from the ssh side too.

## Prerequisites

- **SSH client** — `ssh` must be in `$PATH`
- **SSH access** — valid credentials to the target host. Login must be non-interactive (key-based auth / ssh-agent), because `den` can't answer a password prompt
- **Network reachability** — the bastion/jump host must be reachable from your machine

No AWS credentials or SSM plugin required — the Tunnel feature runs arbitrary shell commands, not AWS-managed sessions.

## Usage

### TUI (recommended)

1. Run `den` — the **Tunnel** panel is accessible from the left menu
2. Navigate tunnels with `↑` / `↓` (or `j` / `k`)
3. Use the keyboard shortcuts below

| Key       | Action                                |
|-----------|---------------------------------------|
| `c`       | Connect (start) the selected tunnel   |
| `d`       | Disconnect (stop) the selected tunnel |
| `r`       | Toggle `reconnect` for the selected tunnel |
| `m`       | Toggle `monitor` for the selected tunnel   |
| `H`       | Toggle the `health_check` probe            |
| `v`       | Toggle `auto_connect` (following the tunnel's VPN) |
| `y`       | Copy the raw command to clipboard     |
| `Enter`   | Open detail view                      |
| `h` / `?` | [Shortcuts pop-up](../shortcuts/README.md) |
| `↑` / `k` | Move cursor up                        |
| `↓` / `j` | Move cursor down                      |

`r`, `m` and `H` also work in the detail view (`Enter`). Lowercase `h` opens the
shortcuts pop-up, here and in the detail view, which is why the health-check toggle
is the uppercase `H`.

### Runtime toggles

`reconnect`, `monitor`, `health_check` and `auto_connect` can be flipped while `den`
runs. The status box shows one dot per setting, read from the tunnel itself, so it
tracks the current value whether the tunnel is running or stopped:

```
● Reconnect
● Monitor
○ Health  intranet.corp:443 (off)
● Auto-connect  Corp VPN
```

The auto-connect row only appears on tunnels with an `auto_connect` block.

Green means on, grey means off, and a dimmed dot with `(not configured)` means there is
nothing to toggle — pressing `H` on a tunnel without a `health_check` target does nothing
but say so.

Toggles are **runtime-only**: they last until `den` exits and are never written back to
`den.yaml`. Restart and the file's values apply again.

When each one takes effect:

| Toggle         | Takes effect                                                                  |
|----------------|-------------------------------------------------------------------------------|
| `reconnect`    | At the next drop. A retry already waiting out its backoff still runs           |
| `monitor`      | At the next 15 s tick                                                          |
| `health_check` | At the next check — the next monitor tick while connected, else the next connect |
| `auto_connect` | At the VPN's next change                                                       |

Switching `monitor` on mid-connection works without restarting the tunnel: the check loop
runs whenever there is something to probe and reads the setting on every tick.

The status dot shows the current state:

| Color  | State                 |
|--------|-----------------------|
| Green  | Connected             |
| Yellow | Connecting (includes the connection checks) / Stopping |
| Red    | Error                 |
| Gray   | Idle                  |

Uptime is displayed for active tunnels (HH:MM:SS). If a tunnel exits unexpectedly the error output is shown in the detail panel.

In the **Connect** (Services) list, a tunnel shows its first `-D`/`-L` port next to its type, e.g. `Office Tunnel  tunnel :5555  connected`. The detail header lists every port. Pressing `c` on a tunnel that is already up shows a short `⚠  already running` warning instead of starting it again.

### Quitting

Pressing `q` or `Ctrl+C` stops all active tunnels before exiting.
