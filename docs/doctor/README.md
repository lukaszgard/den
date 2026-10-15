# den doctor

## What it is

`den doctor` (also `den check-install`) checks everything den needs outside
itself and tells you how to fix what is missing, before a connection fails
halfway with an error from three tools down. For each program den runs it shows
where it is installed and its version; for anything missing, too old or off
PATH it prints the command that fixes it **for your OS and package manager**.
It only reads: nothing is installed, changed or logged in — den prints install
commands, it never runs them.

| Check | Fails when | Warns when |
|---|---|---|
| config | den.yaml does not load or validate | there is no config file (the fix says where to write one, see [Files and folders](../files/README.md)); one result per warning of [`den config --check`](../config-check/README.md), with its hint as the fix |
| vpn (`--for vpn`) | — | there is no `vpn:` entry (the fix is the smallest one, a gateway); a `config_file` is readable by group or others, or sits inside a git work tree |
| aws | missing, or v1 (SSO sessions need v2) | — |
| session-manager-plugin | missing while services use SSM | — |
| client and secret tools | — | `psql`, `mysql`, `redis-cli`, `mongosh`, `curl`, `docker`, `ssh`, `kubectl`, `sops`, `gpg`, `vault`, `keepassxc-cli`, `openfortivpn`… missing, older than den needs, installed off PATH, or not available on this OS |
| SSO *session* | — | not logged in, or the login expired |
| vpn helper (with a `vpn:` entry, once [installed](../vpn/README.md#passwordless-setup)) | — | the installed helper script is not the one this den would install: an older one's stop cannot clear a hung openfortivpn, so the fix is to re-run `den vpn install-helper` |
| kernel PPP (Linux, with a `vpn:` entry) | with `--for vpn`: the kernel has no PPP driver | the kernel has no PPP driver, which openfortivpn's pppd needs: WSL 3.0.1's kernel, a kernel without `ppp_generic`, or a kernel whose modules were removed since boot |
| local ports | — | a port is already taken (two services sharing a `local_port` is a `config` warning) |
| secrets | — | a secret source cannot be read yet (missing file, expired login, no key to decrypt with); sources failing for the same reason share one line |
| bastion *id* (`--deep`) | its SSM agent is not online | AWS could not be asked (expired login, missing profile, no permission) |

It exits 1 when any check fails, so it also works as a CI or onboarding gate.

What is wrong with den.yaml itself — the files it points at, local ports two services
share, AWS profiles that are not defined, names used twice — is the config check's
(`den config --check`, [Checking den.yaml](../config-check/README.md)). The doctor
shows those as warnings of its `config` check, each with the hint as its fix, so one
command says everything; it adds what only this machine can tell: tools, logins,
ports in use now. A missing AWS profile is a warning, not a failure: it breaks only
the services that use it.

### Tools: installed is not the same as usable

- **Versions.** Some tools only work from a version on: AWS CLI v2 (SSO
  sessions), redis-cli 6 (`--user` for IAM auth), vault 1.11
  (`kv get -mount=`), sops 3.9 (`sops edit`), openfortivpn 1.23 (SAML login).
  An older one gets the upgrade command.
- **An old install that would shadow the new one.** The AWS CLI v2 installer puts
  `aws` in `/usr/local/bin`. A v1 from `pip install --user` (or pipx) sits in
  `~/.local/bin`, which Ubuntu puts first on PATH, so after the install `aws` would
  still be v1. For a v1 there the steps start with `python3 -m pip uninstall -y
  awscli` (or `pipx uninstall awscli`); for Debian's v1 package,
  `sudo apt-get remove -y awscli`.
- **Packages that are themselves too old.** A distribution release keeps the
  version it shipped, so upgrading through apt or dnf changes nothing: Ubuntu
  24.04 and older, Debian 12 and older and RHEL 9 (EPEL) have an openfortivpn
  without SAML. For those, doctor prints the steps to build it from source.
  A fresh install from the package manager can land on such a version, so
  doctor asks you to run it again afterwards.
- **Off PATH.** Homebrew installs `libpq` (psql) and `mysql-client` keg-only,
  so they are on disk but not on PATH; doctor finds them and prints
  `brew link --force …`. `keepassxc-cli` inside `KeePassXC.app` is fine: den
  looks there itself.
- **Not available.** openfortivpn has no Windows build; doctor says what to
  use instead rather than an install command that cannot work.
- **sops keys.** A sops file is encrypted to specific keys. den reads the file's
  plaintext metadata (never decrypting) and checks one of them is usable here:
  the AWS profile and its SSO login for KMS, a matching age key in
  `SOPS_AGE_KEY`, `SOPS_AGE_KEY_FILE` or the default `keys.txt`, the SSH key
  for an SSH recipient, the PGP secret key in gpg's keyring. The Secrets pane
  shows the same reason.

Install commands exist for macOS (Homebrew), Debian/Ubuntu (apt), Fedora/RHEL
(dnf) and Windows (winget, then Scoop); other Linux distributions get the
vendor's install page. When Homebrew or Scoop itself is missing, doctor says
so first. The full list per platform is in [install.md](install.md), generated
from the same catalog doctor uses.

## Configuration

None — it reads the same `den.yaml` den uses (`-c` to pick one), and the `config` result names the file and where it was found. With `--for`
it needs no config at all.

## Prerequisites

Nothing beyond den itself. `--deep` calls `ssm:DescribeInstanceInformation`
with each service's `aws_profile`, so it needs a valid login for those profiles
and that permission.

## Usage

```bash
den doctor                        # everything den.yaml uses — no network
den check-install                 # the same command
den doctor --for sops             # only what sops secrets need, config or not
den doctor --for rds,redis --json # machine-readable, for scripts and agents
den doctor --for sops,vpn --os windows      # another platform's install steps
den doctor --os debian/arm64      # what this den.yaml needs on Ubuntu (arm64)
den doctor --deep                 # also ask SSM whether every bastion is online
den doctor --explain              # ask Claude to explain the problems (see below)
```

`--for` takes the feature names listed in [install.md](install.md#features):
service types (`rds`, `mysql`, `redis`, `documentdb`, `redshift`,
`opensearch`, `neptune`, `docker`), transports (`ssm`, `ssh`, `eice`,
`kubectl`), secrets (`sops`, `vault`, `keepassxc`, `aws_secretsmanager`), and
`tunnel`, `vpn`, `aws`, `tui`, or `all`. Services assume the `ssm` transport
unless another is named. A tool asked for by name that is missing fails, so
`den check-install --for sops || exit 1` works in an onboarding script.

`--os` looks at nothing on this machine: it prints install steps for
`macos`, `debian`, `fedora` or `windows` (optionally `/amd64` or `/arm64`) —
for the `--for` features, or without `--for` for everything den.yaml uses.

```
✓ config                  den.yaml
✓ aws                     /opt/homebrew/bin/aws 2.27.28
✓ session-manager-plugin  /usr/local/bin/session-manager-plugin 1.2.707.0
✓ keepassxc-cli           /Applications/KeePassXC.app/Contents/MacOS/keepassxc-cli 2.7.11 (den finds it there)
⚠ mysql                   installed at /opt/homebrew/opt/mysql-client/bin/mysql 9.3.0, but not on PATH (needed by shop)
                          → brew link --force mysql-client
⚠ sops                    sops not found (needed by team-secrets)
                          → brew install sops
⚠ config                  den.yaml:14: AWS profile "base" (used by EU DEV) is written [base] in ~/.aws/config
                          → write it [profile base]: the AWS config only knows [default] and [profile NAME]
⚠ SSO corp                login expired at Sep 25 16:44
                          → aws sso login --sso-session corp (or Enter on it in the AWS pane)
⚠ secret payments         no age key for age1dy0tdp5d…2f63w8 (looked in ~/Library/Application Support/sops/age/keys.txt) (payments)

5 ok, 5 warnings, 0 failed

To fix the tools above in one go (macOS):

  brew install sops
  brew link --force mysql-client
```

With `--json`, each result carries `install` (the commands) and `docs`.

### `--explain`

Sends the problems (never the checks that passed) to Claude and prints a
plain-language explanation with fix steps. It is the only place den calls a
language model, and it never does so on its own:

1. den prints **exactly** what it would send. Names from den.yaml and the AWS
   config (services, profiles, SSO sessions, secrets, buckets), host names,
   account and instance IDs, IP and e-mail addresses, ARNs and home paths are
   replaced by placeholders such as `profile-1` or `host-2`.
2. It asks `Send it? [y/N]`. Without a terminal it refuses unless you pass `--yes`.
3. The answer comes back in placeholders and den puts your real names back
   locally before printing it.

It needs Anthropic API credentials — `ANTHROPIC_API_KEY`, or a profile from
`ant auth login`. The model defaults to `claude-opus-5`; set another in den.yaml:

```yaml
ai:
  model: claude-opus-5
```
