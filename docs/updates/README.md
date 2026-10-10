# Updates

## What it is

The **Updates** pane keeps den current without leaving it. den checks for a
newer release when it starts; `i` downloads it, verifies it against the
release's `checksums.txt`, and replaces the running binary in place. On macOS
and Linux den then offers to restart into the new version.

When a newer release is found the pane names it, and `s` opens the changelog
with that release's notes at the top, so you can read what changes before you
install.

A restart is a quit followed by a fresh start, so it **closes every open
connection**: tunnels, IAM tokens, Docker Compose environments and the VPN.
Nothing reconnects by itself, and bringing the VPN back means another SAML
login. The prompt lists what will close, and "later" is always one key away;
the new version is installed either way and starts the next time you run den.

## Configuration

With no `updates:` block den reads this project's GitHub Releases, which need no token.
Three keys change where it looks:

```yaml
updates:
  github_api: "https://api.github.com/repos/you/den/releases/latest"
                                   # a fork, or a GitHub Enterprise server
                                   # (https://ghe.example.com/api/v3/repos/…)
  s3_bucket: "acme-releases"       # an S3 mirror you host; wins over github_api
  s3_prefix: "den/"                # key prefix of latest.json and the archives
  aws_profile: "acme-dev"          # profile `aws s3 cp` runs as
  aws_region: "eu-central-1"       # region of the bucket
```

## Prerequisites

- **GitHub (the default):** network access to `api.github.com`. A private fork or a
  GitHub Enterprise server needs `GITHUB_TOKEN` exported before starting den, with
  read access to the repository's releases.
- **S3 mirror:** the AWS CLI v2 and a profile allowed `s3:GetObject` on
  `<prefix>latest.json`, the release archives and `checksums.txt`. An expired
  SSO login shows up as a failed check; log in from the AWS pane and press `c`.
- **Install:** write access to the directory den is installed in. den swaps
  the binary with a rename next to it, so a den under a root-owned directory
  needs a manual install (the pane shows the commands).

## Usage

| Key | Where | Action |
|-----|-------|--------|
| `c` | Updates | Check for a new version |
| `i` | Updates | Download, verify and install it |
| `y` | restart prompt | Restart into the installed version now |
| `n` / `esc` | restart prompt | Later: keep running the old version |
| `r` | after an install | Open the restart prompt again |
| `s` | Updates | Show the changelog; the notes of a newer release come first |
| `h` | Updates | Help overlay |

The changelog opens in a box that scrolls (`↑` `↓`, `PgUp` `PgDn`); `esc` or `s`
closes it. It is the changelog of the den you are running, which cannot list a
release newer than itself, so the newer release's notes are put above it under
"new, not installed yet".

The pane also lists the commands for installing by hand: `aws s3 cp` with your
profile and region when releases come from an S3 mirror.

The prompt appears in the Updates pane when the install finishes. If you moved
to another pane while it downloaded, a status message says it is waiting there:
den never pops a question over a pane you are typing in, since a stray `y`
would drop every connection. `enter` does not answer the prompt for the same
reason.

A restart keeps the arguments den was started with (`-c den.yaml`,
`den connect NAME`), its working directory and environment, so it comes back
with the same config. AWS SSO sessions survive it: their token stays in the AWS
CLI's cache.

On Windows den cannot replace itself in place: after an install, quit and
start den again.

## Where releases come from

**GitHub Releases (default).** The check reads the repository's `releases/latest` API and
the download uses the release's assets. A public repository needs no token. Set
`github_api` to read a fork or a GitHub Enterprise server instead; for a private
repository export a personal access token before starting den:

```bash
export GITHUB_TOKEN=<your-personal-access-token>
den
```

The token needs read access to the repository's contents (`repo`, or a fine-grained token
with Contents: read). Without it, a private repository answers 404 and the check fails
with a hint to set `GITHUB_TOKEN`. Everything else works.

**S3 mirror.** For a team that hosts its own copy of the releases, set `updates.s3_bucket`:
the check and the download then use `aws s3 cp` with your own AWS profile, and no token is
needed. Copy each release's files there, and a `latest.json` must exist at
`s3://{bucket}/{prefix}latest.json`:

```json
{"version": "1.0.0"}
```

The release workflow attaches `latest.json` to every GitHub release for this purpose.

## Verification

`i` checks the downloaded archive against the release's `checksums.txt` (SHA-256, published by
GoReleaser) before it touches the installed binary, and stops on a mismatch. On S3 the file is
read from `s3://{bucket}/{prefix}{version}/checksums.txt`. A release that publishes no
`checksums.txt` still installs, with a warning in the install steps saying the download was not
verified.

