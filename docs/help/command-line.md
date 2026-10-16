# Command line

Every den command, and what it is for

## What it is

`den` on its own opens this dashboard. The same binary has commands for what is quicker, or only possible, outside it: checking an install, writing a first config, running a runbook from a script, serving den to an AI agent. Every command takes `-c FILE` to use another `den.yaml`, and `--help` for its flags.

## What you can do

- `den`: the dashboard. `den -c ~/work/den.yaml` uses that config.
- `den list [--json]`: the services the config defines, with their type and status.
- `den config [--json]`: the configuration as den resolved it, environments merged in.
- `den config --check [FILE] [--json] [--strict]`: every problem in `den.yaml` with its line and a hint on how to fix it; exit 1 on an error, and with `--strict` on a warning.
- `den connect NAME`: the dashboard with the service `den.yaml` calls NAME already connecting; `den list` shows the names.
- `den run NAME`: run a runbook or sequence, exiting with the script's own code; `--param key=value` answers a parameter, and `--list` lists them all.
- `den runbook new NAME ["what it should do"] [--agent NAME]`: create a runbook folder, and have an agent CLI (Claude Code, Codex, OpenCode, Copilot CLI, Qwen Code, Droid, Pi or Kimi Code) write it if you like. `den runbook validate [FOLDER] [--json]` checks a runbook folder the way den loads it, and exits 1 while it does not load. `den runbook migrate NAME` moves a header script into a folder, and `den runbook delete NAME` deletes a runbook or sequence for good, with its runs and state folder.
- `den runbook source add|list|update|sync|remove`: manage git repositories of runbooks; `den runbook browse|install|uninstall|fork` work on one source's runbooks.
- `den create-env [-f FILE]`: start a Docker Compose file, `docker-compose.yml` by default.
- `den init --profile P [-o FILE]`: write a starter `den.yaml` from what that AWS profile can see.
- `den paths [--json]`: list every file and folder den uses on this machine, and whether each exists.
- `den schema`: print the JSON Schema of `den.yaml`, for your editor.
- `den doctor [--for F,…] [--os OS] [--deep] [--json] [--explain]`: check what den needs and print how to install what is missing. `den check-install` is the same command.
- `den mcp`: serve den to AI agents over the Model Context Protocol, on stdin and stdout.
- `den vpn install-helper|status|uninstall-helper`: start the VPN without a password prompt, see whether that works, or undo it.
- `den update [--json]`: what the update check offers and why; `den update list [--all]` lists every release, `den update install [VERSION] [--yes] [--skip-current]` installs one, older ones too, and `den update skip|unskip VERSION` keeps a version from being offered on this machine.
- `den completion bash|zsh|fish|powershell`: shell completion; `den --version` prints the version, commit and build date.

## Before you start

- Nothing beyond den itself. Each command needs what its feature needs; `den doctor --for` names it.

## Limitations

- Connections belong to the process that opened them: `den list` from another terminal shows every service idle, even while the dashboard has it connected.
- `den run` has no tunnels of its own, so it exports no `DEN_PORT_*` variables; a runbook that needs a live tunnel has to run from the dashboard.
- `den connect` opens the dashboard: it connects one service and has no mode without it.
- `den doctor --explain` is the only command that calls a language model: it prints exactly what it would send, with your names replaced, and asks first.

## See also

- Getting started
- Runbooks
- Runbook sources
- AI agents (MCP)
- Doctor
- Updates
