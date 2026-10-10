# Doctor

Check what den needs, and how to install it

## What it is

`den doctor` (also `den check-install`) checks everything den needs outside itself, before a connection fails halfway with an error from three tools down: the config and the problems in it, the tools it runs and their versions, AWS logins, ports in use and secrets. For anything missing or too old it prints the command that fixes it for your OS and package manager. It only reads: it never installs, changes or logs in to anything.

## What you can do

- Check everything your `den.yaml` uses, with no network calls.
- See which `den.yaml` it used and where from, and where to write one when there is none (see Files and folders).
- Get a warning for a VPN settings file that others can read or that sits in a git work tree.
- Check one feature without a config at all: `den doctor --for vault`, or several with `--for rds,sops`.
- See another platform's install steps, for a teammate or a new machine: `--os windows`, `--os debian/arm64`.
- Ask SSM whether every bastion is online with `--deep`.
- Get the results as JSON for scripts and agents, with each fix's commands.
- Have Claude explain the problems in plain language with `--explain`: den shows exactly what it would send, with your names replaced by placeholders, and asks first.
- Use it as an onboarding or CI gate: it exits 1 when a check fails.

## Before you start

- Nothing beyond den itself.
- `--deep` needs a login for each service's `aws_profile`, allowed `ssm:DescribeInstanceInformation`.
- `--explain` needs Anthropic API credentials: `ANTHROPIC_API_KEY`, or a profile from `ant auth login`.

## Configure

Doctor reads the `den.yaml` den uses, and needs none when you name features. The only key of its own is the model `--explain` asks:

- `ai.model` (default `claude-opus-5`): the Claude model that explains the report.

```yaml
ai:
  model: claude-opus-5
```

Typical runs:

```
den doctor
den doctor --for sops,vault
den doctor --for rds --os windows
den doctor --deep
den doctor --json
den doctor --explain
```

Feature names for `--for`: `rds`, `mysql`, `redis`, `documentdb`, `redshift`, `opensearch`, `neptune`, `docker`, `ssm`, `ssh`, `eice`, `kubectl`, `sops`, `vault`, `keepassxc`, `aws_secretsmanager`, `tunnel`, `vpn`, `aws`, `tui`, `runbook-sources`, `claude`, `codex`, or `all`.

## Limitations

- It prints install commands; it never runs a package manager.
- Install commands exist for macOS (Homebrew), Debian and Ubuntu (apt), Fedora and RHEL (dnf) and Windows (winget, then Scoop); other Linux distributions get the vendor's page.
- It checks that a login exists and has not expired, not that it grants the permissions a store needs.
- `--explain` is the only place den calls a language model, and only after you say yes.

## When it goes wrong

- A tool is installed but reported off PATH: Homebrew installs `libpq` and `mysql-client` keg-only; run the `brew link --force …` it prints.
- A tool is too old and its package manager has nothing newer, like openfortivpn without SAML on Ubuntu 24.04 and older, Debian 12 and older and RHEL 9: doctor prints the steps to build it from source instead. After a fresh install from such a package, run doctor again: only then does it know the version.
- `aws` stays at v1 after the v2 install: pip put v1 in `~/.local/bin`, which Ubuntu puts first on PATH. Doctor's steps for a v1 there start with `python3 -m pip uninstall -y awscli` (or `pipx uninstall awscli`); run them all, then `hash -r`.
- `kernel PPP` fails on Linux: the VPN's pppd has no PPP driver to use. On WSL 3.0.1 the kernel is built without it; roll WSL back to 2.7.14 or use FortiClient on Windows. On other kernels, load it with `sudo modprobe ppp_generic`, or reboot when the running kernel's modules were removed by an upgrade.
- A `config` warning says `written [base]`: in the AWS config file a profile section must read `[profile base]`. The same warnings come from `den config --check`.
- An SSO session is reported expired: log in from the AWS SSO panel, or with the `aws sso login` command it prints.

## See also

- Getting started
- Checking den.yaml
- Command line
- AI agents (MCP)
- `docs/doctor/README.md`
