# Menu and logs

Choose the panels, and where den writes its log

## What it is

The left menu lists den's panels; `menu_order` and `disabled_panes` choose which appear and in what order. The Logs panel shows what den itself did (connections, token renewals, warnings), each line's level in its own colour, and the output of the runbooks you run from the dashboard, tagged with their names; `logs:` sets how much it records and whether it also writes to a file. The Config panel shows the `den.yaml` den loaded.

## What you can do

- Order the menu, and hide the panels you do not use.
- Read den's own log in the Logs panel, newest at the bottom: warnings in orange, errors in red.
- Follow your runbook runs there too, with their colours, and hide them with `o` when you want den's own lines alone.
- Write the log to a file as well, for a bug report or a long session.
- Record more (`debug`, `trace`) while you chase a problem, or less (`warn`, `error`).
- See the loaded `den.yaml` with its line numbers and the problems den found in it, filter its lines, and open it in your editor from the Config panel.
- Change any of these keys and have it applied on reload, without a restart.
- Keys: the Logs and Config panels list theirs on the line at the bottom of the screen.

## Before you start

- Nothing beyond den itself.

## Configure

Top-level keys of `den.yaml`.

- `title`: the header line above the dashboard; `den — Developer Environment Navigator` when unset.
- `menu_order`: the panels to show, in this order; panels left out are hidden. Every panel, in den's order, when unset.
- `disabled_panes`: panels to hide whatever the order says.
- Panel names: `connect`, `tunnel`, `runbooks`, `vpn`, `aws`, `secrets`, `create-env`, `config`, `logs`, `help`, `updates`.
- `logs.level` (default `info`): `trace`, `debug`, `info`, `warn` or `error`.
- `logs.file_location`: a file the log is appended to as well; `~` is expanded. No file when unset.

Your own header line:

```yaml
title: "Acme platform — data access"
```

Your own order, without the panels you never use:

```yaml
menu_order: [connect, secrets, runbooks, aws, tunnel, vpn, config, logs, help, updates]
```

Every panel in den's order, minus two:

```yaml
disabled_panes: [create-env, vpn]
```

A detailed log, written to a file too:

```yaml
logs:
  level: debug
  file_location: ~/.cache/den/den.log
```

## Limitations

- With `menu_order` set, a panel you leave out of it is hidden, including any panel a later den version adds.
- An unknown panel name is ignored, with a warning in the Config panel.
- The log file is appended to and never rotated.
- The panel keeps den's last 1000 lines and, apart from them, the last 1000 lines of runbook output.
- Runbook output counts as `info`: `warn` or `error` leaves it out. The log file gets it without its colours.
- Only runs started from the dashboard show; `den run` and `den mcp` runs are other processes.
- Secrets and tokens are redacted from the log; with `trace`, check a file before you share it anyway.

## When it goes wrong

- A panel is missing: it is not in `menu_order`, or it is in `disabled_panes`; a misspelt name is ignored, and the Config panel names it with the panel it most likely is.
- No log file appears: den could not create its folder or open the file, and does not say so; check the path and its permissions.

## See also

- Getting started
- Editing den.yaml
- Updates
- `docs/updates/README.md`
