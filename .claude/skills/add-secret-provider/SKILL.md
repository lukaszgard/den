---
name: add-secret-provider
description: This skill should be used when the user asks to "add a secret provider", "support 1Password/Bitwarden/pass/GCP Secret Manager/Azure Key Vault in the Secrets pane", "add a secrets backend", or wants a new group under the secrets block of den.yaml.
---

# Add a secret provider to den

The Secrets pane lists `secrets.Item`s grouped by provider. A provider shells out to
the tool the user already logs in with (or uses its SDK) and never writes a secret to
disk or the log.

## Steps

1. **Config** (`internal/config/schema.go`) — a `<Name>SecretSpec` struct with `name`
   (`jsonschema:"required"`), `description`, the locator keys, `auto_reveal`, and
   `view` (`jsonschema:"enum=tree,enum=flat"`) when values can nest; add the group to
   `SecretsConfig` above `Other`.
2. **Validation** (`validateSecrets` in `internal/config/config.go`) — names are unique
   across groups via `checkName`, and `view` via `checkView`; update the "supported:"
   list in the unsupported-provider error.
3. **Provider** (`internal/secrets/<name>/`) — implement `secrets.Item` (`Name`,
   `Description`, `Provider`, `Location`, `Details`, `AutoReveal`, `Probe`, `Read`) and
   optionally `Editor`, the unlocker pattern (`keepassxc`), or folder browsing (`vault`).
   `Probe` must be cheap and explain what is missing ("SSO login expired — log in from
   the AWS pane"): the pane and `den doctor` both show it. When the CLI itself is
   missing, return `secrets.NotInstalled("<cli>")`, which adds this OS's install
   command; implement `secrets.ToolUser` (`Tools() []string`) so `den doctor` checks
   the CLI's presence and version, and add the CLI to the `internal/prereq` catalog
   plus a `Features` entry named after the `den.yaml` group.
   A provider read through an AWS profile takes the shared `*secrets.AWSLogins` in its
   constructor, checks the profile with `logins.Check(profile, time.Now())` in `Probe`
   (never its own parse of `~/.aws/config`), and implements `secrets.AWSUser`
   (`UsesAWS() bool`): the pane then probes it again after an SSO login or expiry.
   Turn nested values into fields with `secrets.FlattenJSON`: it keeps each value's
   path, which the tree view needs. Such a provider implements `secrets.Viewer`
   (`TreeView() bool { return it.spec.View != config.SecretViewFlat }`); one with flat
   values (KeePassXC attributes) leaves it out and opens flat.
4. **Wiring** — `internal/secrets/providers/providers.go` builds the items and fills an
   empty `view` from `secrets.view` (`cmp.Or`); nothing in the TUI or doctor needs
   changing.
5. **Tests** — a fake CLI on `PATH` (see `internal/secrets/vault/vault_test.go`) or a
   fake SDK client (`awssm`): parsing, error handling, and that errors never contain a
   secret value.
6. **Docs** — `docs/secrets/README.md` provider table, the README "Supported Secret
   Providers" table, a commented example in `den.yaml`, a Help topic
   `docs/help/<provider>.md` listed under Secrets in `docs/help/README.md` (and the
   provider in `docs/help/secrets.md`), `make generate`, `make check`.
