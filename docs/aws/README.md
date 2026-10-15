# AWS SSO

## What it is

The ☁ AWS panel lists your AWS SSO sessions and every profile in `~/.aws/config`,
shows how long each session's token has left, and logs in without leaving den.

den creates **one login entry per `[sso-session ...]` section**, not per profile.
That mirrors how AWS SSO actually works: `aws sso login --sso-session X`
authenticates the session, and every profile with `sso_session = X` is usable
afterwards. A config with 60 profiles sharing one session therefore shows one
entry — logging in 60 times would run the same command 60 times.

Sessions appear in the Connect list too, with their token TTL, so an expired
login is visible next to the services that are about to fail because of it.

## Configuration

Nothing is required. den discovers sessions from your AWS config:

```ini
# ~/.aws/config
[sso-session acme-sso]
sso_start_url = https://example.awsapps.com/start
sso_region = eu-central-1
sso_registration_scopes = sso:account:access

[profile acme-dev]
sso_session = acme-sso
sso_account_id = 123456789012
sso_role_name = Developer
region = eu-central-1
```

`aws.config_path` and `aws.credentials_path` are not only read by den: when they are
set, den passes them to the `aws` CLI and the AWS SDK it runs as `AWS_CONFIG_FILE` and
`AWS_SHARED_CREDENTIALS_FILE`, so `aws sso login` finds the sessions den listed. Left out,
a variable you export yourself keeps applying.

Use `aws.sessions` in `den.yaml` only to curate that list:

```yaml
aws:
  config_path: "~/.aws/config"         # default
  credentials_path: "~/.aws/credentials"
  sessions:
    - session: acme-sso               # must match an [sso-session ...] section
      name: "Acme SSO"                  # display name (default: the session name)
      description: "Main corporate SSO"
    - session: other-org
      name: "Other Org"
```

| Key           | Required | Description                                                      |
|---------------|----------|------------------------------------------------------------------|
| `session`     | yes      | The `[sso-session X]` name. Must already exist in the AWS config |
| `name`        | no       | Display name in the TUI and `den list`                           |
| `description` | no       | Shown as a subtitle                                              |

Entries appear in the order listed, and sessions left out are hidden.

Curation is a filter, not a registration list — a session shows up because it is
in `~/.aws/config`, whether or not `aws.sessions` mentions it:

| `sessions`     | Result                                                 |
|----------------|---------------------------------------------------------|
| key absent     | Every discovered session, in discovery order (default) |
| `sessions: []` | No sessions at all                                     |
| entries listed | Only those, in the order listed                        |

Commenting the block out is the same as leaving it absent, so every discovered
session still appears. To show none while keeping the pane, write `sessions: []`;
to hide the pane entirely, use `disabled_panes: [aws]`.

### What curation cannot do

It cannot define a session. `aws sso login --sso-session X` resolves X from the
AWS config and the CLI has no flag for a start URL, so **den never writes to
`~/.aws/config`** — add the `[sso-session]` section there yourself. An entry
naming a session that is not in the AWS config is skipped, with a warning in the
📄 Logs panel naming both the entry and the session.

## Prerequisites

- **AWS CLI v2** — `aws sso login` is run as a subprocess
- At least one `[sso-session ...]` section in `~/.aws/config`
- A browser, for the SSO authorisation step

## Usage

| Key           | Action                                                     |
|---------------|-------------------------------------------------------------|
| `Enter` / `c` | `aws sso login` for the selected session                   |
| `d`           | Cancel a login in progress                                 |
| `o`           | Open the selected session's SSO start page in a browser    |
| `e`           | Edit `~/.aws/config` in `$EDITOR`                          |
| `E`           | Edit `~/.aws/credentials` in `$EDITOR`                     |
| `/`           | Filter profiles — SSO sessions stay visible                |
| `↑`/`k`, `↓`/`j` | Move between sessions and profiles                      |

Sessions are listed first, then profiles. Acting on a profile acts on the session
it references through `sso_session`, so you can log in from whichever row you
happen to be on. A profile with no `sso_session`, or one whose session you have
curated away, says so rather than logging in somewhere else.

After a login, den records the precise expiry via `aws configure
export-credentials` (when a profile references the session) and falls back to the
AWS CLI's own token cache otherwise. The token bar is scaled to the token's real
lifetime, typically 8-12 hours.

Next to the bar, the row gives the time the login ends on the local clock, with
the zone's abbreviation so it cannot be taken for the UTC the caches hold:
`until 14:59:16 CEST`, or `until Fri 02:15:00 CEST` when that is another day.
After a den login it is den's record; after a login den did not start, it is the
current access token's expiry, which moves on at each renewal.
When the pane is too narrow for the clock, the row keeps only the countdown.

From the command line, the session is an ordinary service:

```bash
den list                 # sessions appear with type 'aws' and their token status
```
