# KeePassXC

Entries of a .kdbx database, without the app open

## What it is

An entry, a group or a whole KeePassXC database (`.kdbx`), read through `keepassxc-cli` and shown in the Secrets panel. den asks for the master password in the panel and hands it to `keepassxc-cli` on stdin only. Use it for the team logins kept in KeePass, without unlocking the app and searching.

## What you can do

- Open one entry by its path, or pick from every entry of a group or of the whole database, with search.
- Show the attributes you choose, custom ones included, and the current TOTP code.
- Reveal and copy a value; multi-line notes are copied with their line breaks.
- Keep the master password for a while, so every entry of that database opens without asking; forget all remembered passwords at once.
- Open a database protected by a key file, with or without a password.
- Keys: press `h` in the Secrets panel.

## Before you start

- `keepassxc-cli` (`den doctor --for keepassxc`). On macOS installing the KeePassXC app is enough: den finds the CLI inside it. On Linux the `keepassxc` package puts it on your PATH.
- The database's master password, key file, or both.

## Configure

An entry of `secrets.keepassxc`; the keys every secret has are in Secrets (`view` aside: entries are always flat).

- `database` (required): the `.kdbx` file; `~` is expanded.
- `entry`: one entry, by its path without the root group, such as `CI/Jenkins`. Without it, den lists entries to pick from.
- `group`: with no `entry`, list only the entries under this group, subgroups included.
- `key_file`: a key file, used with or instead of the password.
- `no_password` (default `false`): `true` for a database opened by `key_file` alone; den never asks.
- `attributes` (default `UserName`, `Password`, `URL`, `Notes`): the attributes to show, in order.
- `totp` (default `false`): add a `TOTP` row with the current code.
- `remember_password`: how long den keeps the master password, such as `10m`; asked on every open when unset.
- `cli_path`: where `keepassxc-cli` is, if den does not find it.

One entry, with its TOTP code, the password kept for ten minutes:

```yaml
secrets:
  keepassxc:
    - name: jenkins service account
      database: ~/secrets/team.kdbx
      entry: CI/Jenkins
      attributes: [UserName, Password, URL]
      totp: true
      remember_password: 10m
```

Every entry under one group, to pick from:

```yaml
secrets:
  keepassxc:
    - name: payments logins
      database: ~/secrets/team.kdbx
      group: Payments
      remember_password: 10m
```

A database opened by a key file alone:

```yaml
secrets:
  keepassxc:
    - name: build machine keys
      database: ~/secrets/build.kdbx
      key_file: ~/.keys/build.keyx
      no_password: true
```

## Limitations

- Read-only: den cannot add or change an entry.
- Databases protected by a YubiKey challenge-response are not supported yet.
- Every read re-derives the database key, which is slow on purpose: about a second, more for an entry with multi-line notes.
- A remembered password is held in den's memory until it times out, you forget it, or den quits; den cannot promise no other copy lingers until the memory is reused.

## When it goes wrong

- `keepassxc-cli not installed`: install KeePassXC, or set `cli_path`.
- `wrong password — try again`: the master password did not open the database; it asks again.
- The entry is not found: list every path with `keepassxc-cli ls -R -f DATABASE`; lines ending in `/` are groups.

## See also

- Secrets
- `docs/secrets/README.md`
