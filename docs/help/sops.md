# SOPS

Browse and edit sops-encrypted files

## What it is

A file encrypted with sops (with an AWS KMS, age or PGP key), opened in the Secrets panel. den decrypts it through the `sops` command, shows its keys laid out like the file, and edits it in `$EDITOR` through `sops edit`, which encrypts it again when you save. It replaces `AWS_PROFILE=… sops edit file.enc.yaml` in a terminal.

## What you can do

- Open a YAML, JSON, dotenv or INI file encrypted with sops, and browse its keys as a tree or as dotted paths (`db.password`, `hosts[0]`).
- Reveal and copy one value, or reveal all of them.
- Edit the file in `$EDITOR` and come back to the new values; closing without changes says so.
- See whether the file can be opened before you try: den checks that one of the keys it is encrypted to is usable here, without decrypting it.
- Keys: press `h` in the Secrets panel.

## Before you start

- `sops` 3.9 or newer (`den doctor --for sops`): editing uses `sops edit`, which 3.9 added.
- Access to one of the file's keys: for AWS KMS a login for `aws_profile` and `kms:Decrypt` (plus `kms:Encrypt` and `kms:GenerateDataKey` to save edits); for age the key in `SOPS_AGE_KEY_FILE` or sops's default `keys.txt`; for PGP the key in gpg's keyring.
- A PGP key with a passphrase needs a GUI pinentry such as `pinentry-mac`: den cannot show a terminal one.
- `$EDITOR` for editing; sops falls back to `vim` or `nano` without it.

## Configure

An entry of `secrets.sops`; the keys every secret has are in Secrets.

- `file` (required): the encrypted file; `~` is expanded.
- `aws_profile`: exported as `AWS_PROFILE`, for a KMS key; without it sops uses your environment.
- `aws_region`: exported as `AWS_REGION`.

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

## Limitations

- den only reads the file through `sops`: it never writes a decrypted copy, and edits go through `sops edit` only.
- One file per entry: a folder of encrypted files needs an entry each.
- The status check reads the file's metadata only: a key that exists but is not allowed to decrypt shows as `decrypt failed` when you open the file.

## When it goes wrong

- `sops not installed`: install it; `den doctor --for sops` prints the command for your OS.
- `no age key for age1…`: the age key is not in `SOPS_AGE_KEY_FILE` or the default `keys.txt`.
- `decrypt failed` with `AccessDeniedException`: the profile may not use the KMS key; it needs `kms:Decrypt` on it.

## See also

- Secrets
- AWS SSO sessions
- `docs/secrets/README.md`
