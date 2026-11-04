# SOPS

Browse and edit sops-encrypted files

## What it is

A file encrypted with sops (with an AWS KMS, age or PGP key), opened in the Secrets panel. den decrypts it through the `sops` command, shows its keys laid out like the file, and edits it in `$EDITOR` through `sops edit`, which encrypts it again when you save. It replaces `AWS_PROFILE=… sops edit file.enc.yaml` in a terminal.

## What you can do

- Open a YAML, JSON, dotenv or INI file encrypted with sops, and browse its keys as a tree or as dotted paths (`db.password`, `hosts[0]`).
- Reveal and copy one value, or reveal all of them.
- Edit the file in `$EDITOR` and come back to the new values; closing without changes says so.
- See whether the file can be opened before you try: den checks that one of the keys it is encrypted to is usable here, without decrypting it.
- For a file in a git working copy, see whether a teammate pushed a newer version (`↓ outdated`), or whether your own change is `not committed` or `not pushed`; fetch with `f` and fast-forward with `u`.
- Keys: press `h` in the Secrets panel.

## Before you start

- `sops` 3.9 or newer (`den doctor --for sops`): editing uses `sops edit`, which 3.9 added.
- Access to one of the file's keys: for AWS KMS a login for `aws_profile` and `kms:Decrypt` (plus `kms:Encrypt` and `kms:GenerateDataKey` to save edits); for age the key in `SOPS_AGE_KEY_FILE` or sops's default `keys.txt`; for PGP the key in gpg's keyring.
- A PGP key with a passphrase needs a GUI pinentry such as `pinentry-mac`: den cannot show a terminal one.
- `$EDITOR` for editing; sops falls back to `vim` or `nano` without it.
- `git`, for a file in a git working copy; `den doctor` checks it when one of your files is in one. A fetch cannot ask for a passphrase or a password: use an SSH agent or a credential helper.

## Configure

An entry of `secrets.sops`; the keys every secret has are in Secrets.

- `file` (required): the encrypted file; `~` is expanded.
- `aws_profile`: exported as `AWS_PROFILE`, for a KMS key; without it sops uses your environment.
- `aws_region`: exported as `AWS_REGION`.
- `git.branch`: compare the file with this branch's upstream, and update from it, whatever branch is checked out; the branch checked out when empty.
- `git.check`: `false` shows no git state and offers no `f` or `u` for this file.

A file in a git working copy needs no `git:` block: den finds the repository, the branch and its upstream by itself.

A file encrypted with a KMS key:

```yaml
secrets:
  sops:
    - name: payments dev
      description: payment provider keys for dev
      file: ~/repos/infra/secrets/payments-dev.enc.yaml
      aws_profile: dev-kms
      aws_region: eu-central-1
```

A dotenv file encrypted with an age key, opened flat and in clear:

```yaml
secrets:
  sops:
    - name: local env
      file: ./secrets/local.enc.env
      view: flat
      auto_reveal: true
```

A production file whose KMS key lives in another account, so it uses that account's profile:

```yaml
secrets:
  sops:
    - name: payments prod
      file: ~/repos/infra/secrets/payments-prod.enc.yaml
      aws_profile: prod-kms
```

A file compared with `main` while you work on a feature branch of the same repository:

```yaml
secrets:
  sops:
    - name: payments stage
      file: ~/repos/infra/secrets/payments-stage.enc.yaml
      aws_profile: dev-kms
      git:
        branch: main
```

A scratch file inside a repository, whose git state you do not want to see:

```yaml
secrets:
  sops:
    - name: scratch keys
      file: ~/repos/infra/secrets/scratch.enc.yaml
      git:
        check: false
```

## Usage

1. Open the Secrets panel: a file in a git working copy shows its state beside `ready`, worked out from what your clone last fetched, and the details box says when that was.
2. Press `f` to fetch the file's upstream and check again; den never fetches on its own.
3. On a file marked `↓ outdated`, press `u`: den fetches, then asks before it fast-forwards the branch.
4. Press `y`: the branch moves forward, every secret is checked again, and an open file shows the new values.

## Limitations

- den only reads the file through `sops`: it never writes a decrypted copy, and edits go through `sops edit` only.
- One file per entry: a folder of encrypted files needs an entry each.
- The status check reads the file's metadata only: a key that exists but is not allowed to decrypt shows as `decrypt failed` when you open the file.
- `u` only fast-forwards the branch checked out: den never merges, rebases, stashes, commits or pushes, and a file changed both here and upstream is left to you.
- `up to date` is as fresh as the last fetch, whoever made it: den, your shell or your editor.
- sops rewrites `lastmodified` and `mac` on every save, so saving without changing a value still makes the file `not committed`.

## When it goes wrong

- `sops not installed`: install it; `den doctor --for sops` prints the command for your OS.
- `no age key for age1…`: the age key is not in `SOPS_AGE_KEY_FILE` or the default `keys.txt`.
- `decrypt failed` with `AccessDeniedException`: the profile may not use the KMS key; it needs `kms:Decrypt` on it.
- `check failed`: the fetch failed, and the details box shows git's last line; a VPN that is down or an SSH key that needs a passphrase are the usual causes. Fix it and press `f`.
- `diverged`: the file changed both in your clone and upstream, and an encrypted file cannot be merged; take the upstream version in git and make your change again with `e`.
- `not updated: you are on feature/x; switch to main to update`: `git.branch` names a branch other than the one checked out.

## See also

- Secrets
- AWS SSO sessions
- `docs/secrets/README.md`
