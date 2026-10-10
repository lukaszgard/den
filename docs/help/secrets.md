# Secrets

Read secrets from four stores in one panel

## What it is

The Secrets panel opens the secrets you work with without leaving den: SOPS-encrypted files, AWS Secrets Manager, KeePassXC databases and HashiCorp Vault. `den.yaml` only says where each secret lives, never its value, so the file stays safe to commit. Use it instead of a `sops edit`, an `aws secretsmanager get-secret-value` or a KeePassXC window kept open on the side.

## What you can do

- See every secret with a status worked out without decrypting: ready, file not found, tool missing, SSO login expired, login required.
- Open a secret and browse its keys, as a tree indented like the YAML it came from or as a flat list of dotted keys.
- Reveal one value, or all of them, and copy one; the clipboard is cleared 30 seconds later.
- Filter the list by name, description or location, and an open secret by key name (never by value, which would give masked values away).
- Edit a SOPS file in `$EDITOR`; sops encrypts it again when you save. The other providers are read-only.
- Log in from den: an AWS SSO login turns the secrets waiting for it ready, and a Vault secret runs its own `login` command.
- Keys: press `h` in the Secrets panel.

## Before you start

- The tool of each provider you use: `sops`, `keepassxc-cli` or `vault`; Secrets Manager needs none. `den doctor --for sops` (or `keepassxc`, `vault`, `aws_secretsmanager`) checks it.
- Access to the secret itself: the KMS, age or PGP key of a SOPS file, an AWS login with read access, the KeePassXC master password, a Vault token.
- Each provider's topic lists exactly what it needs.

## Configure

A `secrets:` section with one group per provider: `sops`, `aws_secretsmanager`, `keepassxc` and `vault`. The keys below work in every group; each provider's topic has its own.

- `name` (required): shown in the panel; unique across all groups.
- `description`: shown in the box under the list.
- `auto_reveal` (default `false`): open the secret with every value in clear. Use it only where nobody can see your screen.
- `view` (default `secrets.view`, else `tree`): `tree` or `flat`, for a secret with nested values.
- `secrets.view` (default `tree`): the view of every secret that does not set its own.

One secret of each provider, the smallest entry each takes:

```yaml
secrets:
  sops:
    - name: app dev
      file: ~/repos/infra/secrets/dev.enc.yaml
      aws_profile: dev-data         # for a KMS key
  aws_secretsmanager:
    - name: oidc key dev
      secret_id: /apps/oidc/dev/private-key
      aws_profile: dev-data
  keepassxc:
    - name: team vault
      database: ~/secrets/team.kdbx
  vault:
    - name: grafana admin
      address: https://vault.example.com
      path: grafana/admin
      login: vault login -method=oidc
```

Every secret opens as a flat list of dotted keys, except one that keeps the tree; a local dotenv file opens with its values in clear, with a description to tell it apart:

```yaml
secrets:
  view: flat
  sops:
    - name: compose env
      description: dotenv for docker compose, age key
      file: ./secrets/compose.enc.env
      auto_reveal: true
    - name: helm values dev
      description: nested values, easier to read as a tree
      file: ~/repos/charts/values-dev.enc.yaml
      aws_profile: dev-data
      view: tree
```

## Limitations

- Read-only except SOPS: Secrets Manager, KeePassXC and Vault values cannot be changed from den.
- Four providers. SSM Parameter Store (`aws_ssm:`) and a generic command provider are planned; a planned group stops den loading with `unsupported provider` rather than being ignored.
- Values are shown on screen once revealed, and in any screen share or recording.
- den drops its copy of decrypted values when you leave a secret, but cannot promise no other copy lingers in memory until it is reused.
- If den quits within 30 seconds of a copy, the clipboard is not cleared.
- Over SSH, or without `xclip` or `wl-copy`, a copy is sent to the terminal (OSC 52); den cannot read that clipboard, so its clear empties it without checking what you copied since.
- Services and runbooks cannot read a value from a secret yet.

## When it goes wrong

- `decrypt failed — enter for details`: the status checks passed but the read did not, usually a missing IAM or KMS permission. Open the secret for the full error.
- `SSO login expired`: log in from the AWS SSO panel; the secret turns ready by itself, without a reload.
- `unknown AWS profile`: `aws_profile` is not in the AWS config den reads (`aws.config_path`, default `~/.aws/config`).

## See also

- SOPS
- AWS Secrets Manager
- KeePassXC
- Vault
- AWS SSO sessions
- `docs/secrets/README.md`
