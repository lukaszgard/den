# AWS SSO sessions

Log in to AWS without leaving den

## What it is

The AWS panel lists the SSO sessions of your AWS config, how long each login has left, and the profiles that use them. `enter` runs `aws sso login` for a session, so every profile on it works again. den shows one entry per `[sso-session]`, not per profile, because that is what a login unlocks.

## What you can do

- See every SSO session with its remaining time and the local time its login ends, and the remaining time again in the Connect list next to the services that will fail without it.
- Log in to a session, from its row or from any profile that uses it; cancel a login that hangs.
- Open the session's SSO start page in your browser.
- Filter the profiles; the sessions stay visible.
- Edit your AWS config and credentials files in `$EDITOR`.
- Choose which sessions den shows, in which order and under which names, with `aws.sessions`.
- Watch secrets and other panels follow a login by themselves: those waiting for it turn ready without a reload.
- Stay logged in for as long as your IAM Identity Center session lasts: den renews a session's one-hour access token once it runs out, without a browser, so secrets, `den doctor` and tools outside den keep finding a valid login.
- Keys: press `h` in the AWS panel.

## Before you start

- The AWS CLI v2 (`den doctor --for aws`).
- At least one `[sso-session …]` section in your AWS config, and profiles that name it with `sso_session`.
- A browser for the SSO approval step.

## Configure

Nothing is required: den finds the sessions in your AWS config. The optional `aws:` section says where that config is and curates what den shows.

- `config_path` (default `~/.aws/config`): the AWS config file.
- `credentials_path` (default `~/.aws/credentials`): the credentials file.
- When either is set, den hands it to the `aws` CLI and the SDK as `AWS_CONFIG_FILE` and `AWS_SHARED_CREDENTIALS_FILE`, so logins and tokens use the same file den reads.
- `sessions`: the sessions to show, in this order. Left out, den shows every session; `sessions: []` shows none.
- `sessions[].session` (required): the name of an `[sso-session …]` in the AWS config.
- `sessions[].name`: the name den shows; the session's own name otherwise.
- `sessions[].description`: shown under the name.

Show two sessions, in this order and with friendlier names:

```yaml
aws:
  sessions:
    - session: corp
      name: Company SSO
      description: every work account
    - session: lab-org
      name: Lab
```

Read the AWS files from another place, and show every session found there:

```yaml
aws:
  config_path: ~/work/aws/config
  credentials_path: ~/work/aws/credentials
```

Keep the AWS panel but list no sessions:

```yaml
aws:
  sessions: []
```

## Limitations

- `sessions` only filters: it cannot add a session your AWS config lacks, and den never writes to that file.
- A profile without `sso_session` (static keys, `credential_process`) is listed, but there is nothing to log in to.
- A login needs a browser; it cannot happen in `den mcp` or a headless shell.
- An entry naming a session that is not in the AWS config is skipped, with a warning in the Logs panel.
- den cannot see when the IAM Identity Center session ends. The AWS panel, secrets and `den doctor` count a login as live while it can be refreshed; when IAM Identity Center refuses a renewal, the session's log says why and the session reads as expired everywhere until you log in again.
- After a login made outside den, the token bar shows the current access token's hour and starts over at each renewal. Until den first renews it, the row says `logged in · token renews on next use`.

## When it goes wrong

- Only one entry although you have many profiles: your AWS config defines one `[sso-session]`; that is expected.
- A profile says it has no session: it sets no `sso_session`, or its session was curated away with `aws.sessions`.
- `unknown AWS profile` elsewhere in den: the profile is not in the file at `config_path`.
- A session reads as expired while its login had time left: IAM Identity Center refused to renew it, because the session was ended early or revoked. The session's log names the refusal; log in again.

## See also

- Environments
- Secrets
- `docs/aws/README.md`
