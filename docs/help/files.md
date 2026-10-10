# Files and folders

Where den's files live, and what stays out of git

## What it is

den reads a few files and writes a few more. The ones you write yourself (`den.yaml`, VPN settings, your runbooks) belong in one folder, the config directory, `~/.config/den` on Linux and macOS and `%APPDATA%\den` on Windows. What den makes for itself goes to two others: run history in the state directory, downloaded data in the data directory. `den paths` lists every one for this machine.

## What you can do

- See every file and folder den uses, and whether each exists, with `den paths`; `--json` prints it for scripts.
- Find out which `den.yaml` den loaded and where from: the `-c` flag, the current directory or the config directory.
- Move the folders with `XDG_CONFIG_HOME`, `XDG_STATE_HOME` and `XDG_DATA_HOME`.
- Be warned by `den doctor` about a VPN settings file that others can read or that sits inside a git work tree.
- Keep a personal `den.yaml` out of a shared repository while a team shares its own.

## Before you start

- Nothing: `den paths` only reads, and creates no folder.
- A VPN needs no settings file for the usual case. `gateway: vpn.example.com` is enough; write a file only for openfortivpn settings den has no key for.

## Configure

den uses the first `den.yaml` that exists: the file named by `-c`, then `./den.yaml`, then the config directory, then `~/.config/den/den.yaml` where that differs. Your own files go in the config directory.

- `den.yaml`: references to your stores, never secret values; a team may share one in a repository.
- `vpn/*.conf`: openfortivpn settings, in its `key = value` format; they can hold a password, so `chmod 600` them and never commit them.
- `runbooks/`: the folders you list in `runbooks.dirs`; `den runbook new` makes one.
- `global-bundle.pem`: the RDS CA bundle den downloads for DocumentDB.

A VPN with just a gateway:

```yaml
vpn:
  - name: files office
    gateway: vpn.example.com
```

A VPN with a settings file in the config directory:

```yaml
vpn:
  - name: files lab
    gateway: lab-vpn.example.com
    config_file: ~/.config/den/vpn/lab.conf
```

Runbooks in the config directory:

```yaml
runbooks:
  dirs:
    - ~/.config/den/runbooks
```

Check where things are:

```
den paths
den paths --json
den doctor
```

## Limitations

- `den paths` reports; it never creates or moves a file.
- The VPN helper keeps a root-owned copy of each VPN's settings in `/opt/den/etc`; `den paths` lists it, but you cannot read it without sudo.
- Deleting a committed file does not remove it from git's history. Rotate a password that was committed.

## When it goes wrong

- `no config file found (tried …)`: none of the places listed exists. Run `den init -o ~/.config/den/den.yaml`, or copy `den.yaml` from the release archive there.
- A VPN settings file `can be read by other users`: run `chmod 600` on it.
- A VPN settings file `is inside the git work tree`: move it to `~/.config/den/vpn/`, or git-ignore it.
- den used a different `den.yaml` than you expected: `den paths` says which one and where from. A `./den.yaml` in the current directory wins over the config directory.

## See also

- Getting started
- VPN
- Doctor
