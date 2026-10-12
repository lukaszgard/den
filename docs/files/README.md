# Files and folders

## What it is

den reads and writes a handful of files. The ones you write yourself (`den.yaml`, VPN
settings, your runbooks) belong in one folder, the **config directory**, and den keeps
what it makes for itself in two others. Putting your own files there means they survive
`git pull` in a checkout of den, are found from any directory, and never end up in a
repository by accident.

`den paths` prints every one of these for this machine and whether it exists. `den doctor`
says which `den.yaml` it used and warns about a personal file that others can read or that
sits in a git work tree.

| What | Where (Linux, macOS) | You write it | Safe to commit |
|---|---|---|---|
| `den.yaml` | `./den.yaml`, else `~/.config/den/den.yaml` | yes | a team's shared one, yes: it holds references, never secret values |
| VPN settings, `vpn/*.conf` | `~/.config/den/vpn/` | yes, only for openfortivpn settings den has no key for | **never**: it can hold a password |
| Runbooks, `runbooks/` | `~/.config/den/runbooks/`, or any folder in `runbooks.dirs` | yes, or `den runbook new` | a team's runbooks, yes |
| RDS CA bundle, `global-bundle.pem` | `~/.config/den/` | no, den downloads it for DocumentDB | pointless: it is public and den fetches it again |
| State: run history, runbook trust, runbook drafts | `~/.local/state/den/` | no | no: a draft may hold a secret |
| Data: runbook source checkouts, runbook state | `~/.local/share/den/` | no | no |
| The VPN helper's copy of the settings | `/opt/den/etc/<name>.conf` | no, `den vpn install-helper` writes it as root | never |

## Configuration

None of its own: the locations follow the platform's conventions, and den.yaml names the
rest (`vpn[].config_file`, `runbooks.dirs`, `documentdb.ca_file`).

den looks for `den.yaml` in this order and uses the first that exists:

1. the file named by `-c` or `--config` (an error if it does not exist)
2. `./den.yaml`, in the directory den runs in
3. `den.yaml` in the config directory
4. `~/.config/den/den.yaml`, when the config directory is somewhere else (setups made
   before den followed `XDG_CONFIG_HOME` keep loading)

Where the three directories are:

| Directory | Linux, macOS | Windows |
|---|---|---|
| Config | `$XDG_CONFIG_HOME/den`, else `~/.config/den` | `$XDG_CONFIG_HOME\den`, else `%APPDATA%\den` |
| State | `$XDG_STATE_HOME/den`, else `~/.local/state/den` | `$XDG_STATE_HOME\den`, else `%LOCALAPPDATA%\den` |
| Data | `$XDG_DATA_HOME/den`, else `~/.local/share/den` | `$XDG_DATA_HOME\den`, else `%LOCALAPPDATA%\den` |

A relative `XDG_*` value is ignored, as the XDG specification says.

The rules:

- Personal files live in the config directory. A team may share a `den.yaml` in a
  repository, but never a VPN settings file or anything that holds a credential.
- A VPN needs no settings file for the usual case: `gateway: vpn.example.com` is enough, and
  den adds port 443 and the SAML login itself. Write a file only for openfortivpn settings
  den has no key for (`set-dns`, `trusted-cert`, `persistent`), in openfortivpn's
  `key = value` format with a `.conf` name.
- Keep a VPN settings file readable by you alone: `chmod 600`. Doctor warns when it is not.
- If a file with a password was committed once, deleting it does not remove it from git's
  history. Rotate the password, and rewrite history (`git filter-repo`) before the
  repository is shared.

## Prerequisites

None. `den paths` and `den doctor` read files and create nothing.

## Usage

Where is everything on this machine, and which `den.yaml` is in use:

```
$ den paths
den.yaml                   exists   ~/.config/den/den.yaml  (from the config directory)
config directory           exists   ~/.config/den           (your own files belong here)
runbooks                   exists   ~/.config/den/runbooks  (the usual place for runbooks you write)
RDS CA bundle              missing  ~/.config/den/global-bundle.pem  (downloaded for DocumentDB when needed)
state directory            exists   ~/.local/state/den      (run history, runbook trust)
runbook drafts             exists   ~/.local/state/den/runbook-drafts  (requests typed for an agent; may hold secrets)
data directory             missing  ~/.local/share/den      (runbook source checkouts, runbook state)
vpn office                 exists   ~/.config/den/vpn/office.conf  (config_file)
vpn office helper copy     missing  /opt/den/etc/office.conf  (root-owned, made by `den vpn install-helper`)
```

`den paths --json` prints the same for scripts. A machine with no `den.yaml` yet:

```
$ den doctor
⚠ config  no den.yaml found; den shows an empty dashboard
          → write one with `den init -o ~/.config/den/den.yaml`, or copy den.yaml from the release archive there;
            your own files (den.yaml, vpn/*.conf, runbooks/) belong in ~/.config/den; `den paths` lists every place den looks
```

Set a VPN up from nothing: add the smallest entry to the `den.yaml` that `den paths`
names, then check it.

```yaml
vpn:
  - name: office
    gateway: vpn.example.com
```

```
den doctor --for vpn
```

A file for extra openfortivpn settings:

```
mkdir -p ~/.config/den/vpn
printf 'set-dns = 0\n' > ~/.config/den/vpn/office.conf
chmod 600 ~/.config/den/vpn/office.conf
```

and in `den.yaml`: `config_file: ~/.config/den/vpn/office.conf`. See [VPN](../vpn/README.md).
