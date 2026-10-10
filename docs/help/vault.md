# Vault

HashiCorp Vault KV secrets with your own token

## What it is

A secret, or a folder of them, in a HashiCorp Vault KV engine (version 1 or 2), read through the `vault` command with the token your own `vault login` left. den never handles Vault credentials; it can run your login command for you when the token is missing.

## What you can do

- Open a secret and browse its keys; nested JSON values show as a tree.
- Pick from every secret under a folder, subfolders included, with search.
- Show one field only, or an older version of a KV 2 secret.
- Run your `vault login` (OIDC opens your browser) from the panel; den then checks every secret again.
- Use Vault Enterprise or HCP namespaces, and a server with a private CA.
- Keys: press `h` in the Secrets panel.

## Before you start

- The `vault` command, 1.11 or newer (`den doctor --for vault`).
- A token: `VAULT_TOKEN`, `~/.vault-token` from `vault login`, or a token helper.
- A policy allowing `read` on the secrets, and `list` on folders you pick from.

## Configure

An entry of `secrets.vault`; the keys every secret has are in Secrets.

- `path` (required): the secret's path under the mount; ending in `/`, a folder to pick from.
- `address`: exported as `VAULT_ADDR`; your environment's without it.
- `namespace`: exported as `VAULT_NAMESPACE`, for Vault Enterprise and HCP.
- `ca_cert`: exported as `VAULT_CACERT`, for a server with a private CA.
- `mount` (default `secret`): the KV engine's mount.
- `field`: show only this key; one secret only.
- `version`: a KV 2 version number; the latest when unset.
- `login`: the command run to log in, such as `vault login -method=oidc`.
- `cli_path`: where `vault` is, if not on your PATH.

One field of one secret:

```yaml
secrets:
  vault:
    - name: payments db password
      address: https://vault.example.com
      path: payments/db
      field: password
      login: vault login -method=oidc
```

Every secret under a folder, in a namespace and on another mount:

```yaml
secrets:
  vault:
    - name: team apps
      address: https://vault.example.com
      namespace: team-a
      mount: kv
      path: apps/
      login: vault login -method=oidc
```

An older version, from a server with a private CA:

```yaml
secrets:
  vault:
    - name: signing key v3
      address: https://vault.internal.example.com
      ca_cert: ~/certs/internal-ca.pem
      path: signing/key
      version: 3
```

## Limitations

- Read-only, and KV engines only: no `vault kv patch`, and no dynamic secrets such as database credentials.
- A folder lists at most 500 secrets or 10 levels deep; den says so next to the count. Point `path` at a smaller folder.
- An expired token is only noticed when you open a secret: den does not call the server to check.

## When it goes wrong

- `vault login required`: no token found; log in from the panel when `login` is set, or with `vault login` in a terminal.
- `vault: permission denied — token expired or no access`: log in again, or ask for `read` on the path.
- `vault not installed`: install it, or set `cli_path`.

## See also

- Secrets
- `docs/secrets/README.md`
