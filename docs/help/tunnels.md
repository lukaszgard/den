# Tunnels

Your own SSH forwards and SOCKS proxies

## What it is

A tunnel is a long-running command of your own, usually an `ssh` with `-D` and `-L` flags, that den starts, checks, watches and restarts. Use it for the corporate SOCKS proxy or the port-forwards you keep open all day, instead of an `ssh` in a terminal tab that dies unnoticed.

## What you can do

- Start and stop each tunnel from the Tunnel panel; its `-D` and `-L` ports are listed as `:5555 SOCKS proxy` and `:5432 → db.internal:5432`.
- Count a tunnel as connected only once it works: every port must be free before it starts and accept connections after, and a `health_check` host must answer through the SOCKS proxy.
- Restart a tunnel when it drops (`reconnect`), backing off from 5 to 60 seconds.
- Re-check a running tunnel every 15 seconds (`monitor`): ssh can stay alive long after its connection died, and two misses in a row count as a drop.
- Start a tunnel when a VPN comes up and stop it when the VPN goes down (`auto_connect`).
- Switch reconnect, monitor, health check and auto-connect on and off while den runs, without touching `den.yaml`.
- Copy the command, and read ssh's output in the detail view.
- Keys: press `h` in the Tunnel panel.

## Before you start

- `ssh` on your PATH (`den doctor --for tunnel`), or whatever your command runs.
- A login that needs no typing: a key or ssh-agent. den cannot answer a password prompt.
- No AWS login: tunnels run your command as it is written.

## Configure

A `tunnels:` list.

- `name` (required): shown in the panel.
- `command` (required): the command, run through `sh -c`.
- `description`: shown in the detail view.
- `reconnect` (default `false`): restart the tunnel after it drops.
- `monitor` (default `false`): re-check it every 15 seconds while connected.
- `health_check`: a `host:port` dialled through the command's `-D` proxy before the tunnel counts as connected; it needs a `-D` port in `command`.
- `auto_connect.vpn`: the `name` of the entry in `vpn:` the tunnel runs over.
- `auto_connect.enabled` (default `true`): `false` keeps the link but stops following the VPN.

A SOCKS proxy that proves it reaches the far side, is watched and restarts by itself:

```yaml
tunnels:
  - name: office socks
    description: SOCKS proxy into the office network
    command: ssh -N -T -o BatchMode=yes -o ExitOnForwardFailure=yes -o ServerAliveInterval=15 -D 5555 user@bastion.example.com
    reconnect: true
    monitor: true
    health_check: intranet.example.com:443
```

Two local forwards to internal services in one command:

```yaml
tunnels:
  - name: dev forwards
    command: ssh -N -T -L 8080:app.internal:80 -L 15432:db.internal:5432 user@jump.dev.example.com
```

A proxy that only works over the office VPN, so it follows that VPN instead of retrying while it is down:

```yaml
tunnels:
  - name: office proxy (vpn)
    command: ssh -N -T -D 5556 user@bastion.office.example.com
    reconnect: true
    auto_connect:
      vpn: office
```

## Limitations

- The ports den shows and checks come from the `-D` and `-L` flags in `command`; a command without them is connected once it has stayed up for 2 seconds.
- A port that is already taken stops the tunnel at once, with `port … is already in use`: often an ssh left over from a terminal.
- Toggles last until den exits; `den.yaml` is never rewritten.
- `auto_connect` works in the dashboard only: `den connect`, `den mcp` and the other commands do not follow VPNs.
- A tunnel never feeds the data stores: they open their own forwards through their environment's transport.

## When it goes wrong

- `Permission denied (publickey)` in the detail view: ssh has no usable key; add it to your agent.
- The tunnel stays connecting and then fails after 30 seconds: a port never opened or the `health_check` host never answered through the proxy.
- `connection check failed 2 times in a row`: the monitor found the tunnel dead and stopped it; `reconnect` brings it back.

## See also

- VPN
- Transports
- `docs/tunnel/README.md`
