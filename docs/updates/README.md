# Updates

## What it is

The **Updates** pane keeps den current without leaving it. den checks for a
newer release when it starts; `i` downloads it, verifies it against the
release's `checksums.txt`, and replaces the running binary in place. On macOS
and Linux den then offers to restart into the new version.

When a newer release is found the pane names it, and `s` opens the changelog
with that release's notes at the top, so you can read what changes before you
install.

`v` opens the **version picker**: every published release, to install any of
them, an older one too. That is the way off a bad release, from inside den or
with `den update install VERSION` from a shell. Three things keep den from
offering a bad version back: a person **skips** it on their machine, a team
**pins** den to a version it tested (`updates.pin`), and the maintainers
**yank** a release for everyone. `updates.channel: prerelease` offers release
candidates too, for the people who try a release before it ships.

A restart is a quit followed by a fresh start, so it **closes every open
connection**: tunnels, IAM tokens, Docker Compose environments and the VPN.
Nothing reconnects by itself, and bringing the VPN back means another SAML
login. The prompt lists what will close, and "later" is always one key away;
the new version is installed either way and starts the next time you run den.

## What you need

**Public repository (the default): nothing to configure.**

- No `updates:` block, no token, no AWS.
- HTTPS access to `github.com` and to `release-assets.githubusercontent.com`, the host GitHub redirects release downloads to. Behind a corporate proxy, den honours `HTTPS_PROXY` and `NO_PROXY`.
- Write access to the folder den is installed in.

**Private repository:** export `GITHUB_TOKEN` before starting den. The token needs Contents: read, or the `repo` scope.

**Fork or GitHub Enterprise:** set `updates.github_api`, plus `GITHUB_TOKEN` when the repository is private.

**S3 mirror:** set `updates.s3_bucket`, as described under "Where releases come from". No token and no GitHub access are needed.

When a check fails, the pane says which of these applies.

## Configuration

With no `updates:` block den reads this project's release feed on GitHub, which needs no token,
and offers the newest release. These keys change where it looks and what it offers:

```yaml
updates:
  github_api: "https://api.github.com/repos/you/den/releases/latest"
                                   # a fork on github.com (its own release feed),
                                   # or a GitHub Enterprise server
                                   # (https://ghe.example.com/api/v3/repos/…)
  s3_bucket: "acme-releases"       # an S3 mirror you host; wins over github_api
  s3_prefix: "den/"                # key prefix of latest.json and the archives
  aws_profile: "acme-dev"          # profile `aws s3 cp` runs as
  aws_region: "eu-central-1"       # region of the bucket
  channel: "stable"                # or "prerelease": release candidates too
  pin: "v1.8.0"                    # hold den on this version, older or newer
```

| Key | Required | Default | Meaning |
|-----|----------|---------|---------|
| `github_api` | no | this project's release feed | The `releases/latest` API URL of a fork or a GitHub Enterprise server. |
| `s3_bucket` | no | none | A bucket you mirror the releases to; wins over `github_api`. |
| `s3_prefix` | no | none | Key prefix of `latest.json`, `releases.json` and the release folders, no leading slash. |
| `aws_profile` | no | the default profile | The profile `aws s3 cp` runs as. |
| `aws_region` | no | the profile's | The bucket's region. |
| `channel` | no | `stable` | Which releases the check offers and the picker lists: `stable`, or `prerelease` for release candidates too. |
| `pin` | no | none | A full version (`v1.8.0`, `v1.8.0-rc.1`): the check offers it, older or newer, and nothing else. |

The versions a person skips are not in `den.yaml`: they are that machine's own, in
`updates.json` in den's state directory (`den paths` lists it).

## Prerequisites

- **GitHub (the default):** HTTPS access to `github.com` and to the host it redirects
  release downloads to (`release-assets.githubusercontent.com`), not to `api.github.com`.
  Behind a proxy, set `HTTPS_PROXY`. A private repository or a GitHub Enterprise server
  needs `GITHUB_TOKEN` exported before starting den, with read access to the
  repository's releases; that switches the check to the API (`api.github.com`).
- **S3 mirror:** the AWS CLI v2 and a profile allowed `s3:GetObject` on
  `<prefix>latest.json`, `<prefix>releases.json`, the release archives and `checksums.txt`
  (`s3:ListBucket` too, or a missing `releases.json` answers 403 instead of 404; den treats
  both as "no index"). An expired SSO login shows up as a failed check; log in from the AWS
  pane and press `c`.
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
| `v` | Updates | Open the version picker |
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

### Choosing a version

`v` lists every published release, newest first:

```
  Updates › Versions
  Current 1.8.1  ·  channel stable

    v1.9.0          2026-11-02  ↑ upgrade
  ▸ v1.8.1          2026-10-28  current
    v1.8.0          2026-10-21  ↓ downgrade   ⚠ yanked: SSM reconnect loop — use v1.8.1
    v1.7.2          2026-10-16  ↓ downgrade   skipped
    v1.7.1          2026-10-16  ↓ downgrade   no windows/arm64 build

  ↑↓: move  enter: install  s: notes  x: skip  p: show rc  esc: close
```

| Key | Action |
|-----|--------|
| `↑` `↓` / `k` `j` | Move |
| `enter` | Install the release under the cursor, after a question |
| `s` | Its release notes (`esc` or `s` closes them) |
| `x` | Skip it in update checks on this machine, or stop skipping it |
| `p` | Show or hide release candidates (shown on the prerelease channel) |
| `r` | Load the list again, after it failed |
| `esc` | Close the picker; after an `x`, the check runs again |

`enter` asks before it installs, and only `y` answers yes:

```
  Install v1.7.2?
  ↓ downgrade from v1.8.1

  ⚠ v1.7.2 is older than the version picker (v1.8.0): it offers the latest release
  again, and knows no skip list or updates.pin.

  den.yaml keys and services newer than v1.7.2 are ignored or rejected by it.

  ☑ skip v1.8.1 in update checks

  Install? y: install  n: cancel  x: skip v1.8.1
```

On a downgrade, `x` toggles skipping the version you leave, so the next check does not offer
it straight back; it is saved once the install has worked. The install itself is the same as
`i`: download, verify, replace, and the restart prompt.

The picker opens on the list the startup check read from `releases.json`. Without one (no
release before 1.8.0 published it) it asks the releases API once, which is rate-limited to
60 requests an hour per IP without a token; an S3 mirror without `releases.json` says what to
copy.

### From a shell: `den update`

```console
$ den update
den v1.9.0 is available (you run 1.8.1)
  install it with: den update install

$ den update list
VERSION  DATE        KIND       NOTE
v1.9.0   2026-11-02  upgrade
v1.8.1   2026-10-28  current
v1.8.0   2026-10-21  downgrade  yanked: SSM reconnect loop — use v1.8.1
v1.7.2   2026-10-16  downgrade

$ den update install v1.7.2 --skip-current
Install v1.7.2, a downgrade from 1.8.1, into /usr/local/bin/den
  ⚠ v1.7.2 is older than the version picker (v1.8.0): it offers the latest release again, and knows no skip list or updates.pin
  den.yaml keys and services newer than v1.7.2 are ignored or rejected by it
  v1.8.1 will be skipped in update checks

Install v1.7.2? [y/N] y
✓ Downloaded den_1.7.2_darwin_arm64.tar.gz
✓ Verified SHA-256 against checksums.txt
✓ Extracted the den binary
✓ Replaced /usr/local/bin/den
Installed v1.7.2. The next den you start runs it; den --version says so.
v1.8.1 is skipped in update checks (den update unskip v1.8.1 undoes it)
```

- `den update [--json]`: what the check offers and why.
- `den update list [--all] [--json]`: every release; `--all` adds release candidates.
- `den update install [VERSION] [--yes] [--skip-current]`: VERSION, or the release offered.
  It asks at a terminal and needs `--yes` anywhere else; it restarts nothing.
- `den update skip VERSION` and `den update unskip VERSION`: this machine's skip list.

`den update` reads only the `updates:` block of a `den.yaml` that has errors elsewhere, with a
warning: a release that rejects your config can still install the one that accepts it. It is
not offered over MCP: an agent cannot replace den's binary.

### Skip, pin and yank

What the check offers is the newest release above the one you run, leaving out:

- **yanked** releases: marked bad by the maintainers for everyone. A den running a yanked
  release with nothing newer to go to is offered the newest good release below it.
- **skipped** releases: `x` in the picker, `--skip-current` on a downgrade, or
  `den update skip`. One machine's choice, kept in `updates.json` in the state directory.
- release candidates, unless `updates.channel: prerelease`.

`updates.pin` overrides all three: the check offers the pinned release, older or newer, and
nothing else, until the pin changes. The pane says "pinned to v1.8.0 (updates.pin)", and a
release installed from the picker past the pin is pulled back by the next check.

These work in dens from **1.8.0**, the first with the picker. A downgrade to 1.7.x lands on a
den that reads only `latest.json`: it offers the latest release again, whatever is skipped,
pinned or yanked. The question before such a downgrade says so.

**Yanking a release** (maintainers): a pull request adding it to `.github/yanked.json`,

```json
{"v1.8.0": "SSM reconnect loop — use v1.8.1"}
```

Once it is merged, the `Release index` workflow rebuilds `releases.json`. Dens before 1.8.0
never read it, so a yank of the newest release is followed by a fix release.

## Where releases come from

**GitHub Releases (default).** The check reads the release feed,
`https://github.com/<owner>/<repo>/releases/latest/download/latest.json`, and the download
uses the release's files by their URLs (`…/releases/download/<tag>/<archive>` and
`checksums.txt`). That is the route the install scripts take. It makes no API call, so
GitHub's rate limit of 60 API requests an hour per IP, which a shared office, VPN or CI
address would soon reach, does not apply. A public repository needs no token.

- A release without `latest.json` (one cut before it was published, a fork's workflow without
  that step) is looked up on the releases API instead, as is a private repository, which
  answers the feed with 404.
- `GITHUB_TOKEN` switches the check to the releases API, with the token's own limit of 5000
  requests an hour. That is what a private repository needs: export a personal access token
  before starting den.
- A fork on github.com (`github_api` on `api.github.com`) reads the feed of its own repository.
  A GitHub Enterprise server (any other host) always uses its API.

```bash
export GITHUB_TOKEN=<your-personal-access-token>
den
```

The token needs read access to the repository's contents (`repo`, or a fine-grained token
with Contents: read). Without it, a private repository answers 404 and the check fails
with a hint to set `GITHUB_TOKEN`. A token that cannot read the repository gets the same
404 (GitHub does not say a repository exists to a token that cannot see it), and the check
then says what the token needs: for a fine-grained token, the repository selected under its
owner and Contents: read; for a classic one, the `repo` scope, authorized for SSO when the
organization enforces it. Everything else works.

**The release list.** Every release also carries, on the latest stable one,
`releases.json`: every release from v1.0.0 with its date, notes, whether it is a release
candidate, and why it is yanked. The update check reads it first
(`…/releases/latest/download/releases.json`, again no API call), and the picker lists it.
Releases before 1.8.0 have none; then the check reads `latest.json` as before, and the
picker asks the releases API once.

**S3 mirror.** For a team that hosts its own copy of the releases, set `updates.s3_bucket`:
the check and the download then use `aws s3 cp` with your own AWS profile, and no token is
needed. The bucket holds, under `s3://{bucket}/{prefix}`:

```
latest.json                  {"version": "v1.8.0", "notes": "…"}
releases.json                the release list, for the picker, skip and yank
v1.8.0/den_1.8.0_<os>_<arch>.tar.gz   (and .zip for Windows)
v1.8.0/checksums.txt
```

The release workflow attaches `latest.json` to every stable GitHub release and `releases.json`
to the latest one; copy both with each release's files. A release candidate has no
`latest.json`, so copying its files does not make it everyone's latest. A mirror without
`releases.json` still updates from `latest.json`, but its picker has nothing to list.

## Verification

`i` checks the downloaded archive against the release's `checksums.txt` (SHA-256, published by
GoReleaser) before it touches the installed binary, and stops on a mismatch. On S3 the file is
read from `s3://{bucket}/{prefix}{version}/checksums.txt`. A release that publishes no
`checksums.txt` still installs, with a warning in the install steps saying the download was not
verified.

## Testing a build before it is released

A release goes through three stages, each on its own path to a den:

**A pull request's build.** CI's `snapshot` job builds every pull request's release archives
(version `<last release, patch + 1>-snapshot.<commit>`, a prerelease, so it is not offered
the release it is ahead of) and uploads them as the artifact `den-pr-<n>`, laid out as a
mirror, for 7 days. The job's summary has the commands; they install next to your den, not
over it:

```bash
gh run download <run-id> --repo lukaszgard/den -n den-pr-42 -D den-pr-42
DEN_INSTALL_BASE="file://$PWD/den-pr-42" sh den-pr-42/install.sh --dir den-pr-42/bin
den-pr-42/bin/den --version
```

`make snapshot && scripts/snapshot-mirror.sh dist mirror` builds the same from a checkout.

**A release candidate.** A release worth trying first is tagged `vX.Y.Z-rc.N` from its
release pull request's branch, before it merges (`make release-tag VERSION=X.Y.Z-rc.1`;
`.claude/skills/release` has the steps). It is a GitHub prerelease: `releases/latest` and
the stable channel ignore it. To try it, set

```yaml
updates:
  channel: prerelease
```

and the Updates pane offers it (or `den update install vX.Y.Z-rc.1`). Set the channel back,
or pin the release, to leave the prerelease channel once it ships.

**The release.** Tagged on `main` once the pull request merges; the index job then uploads
`releases.json` to it.

Underneath all three, every pull request runs `scripts/update_e2e_test.sh`: three den
binaries built from the branch, upgraded, downgraded, skipped, pinned and fed a tampered
archive through a fake S3 mirror, each step checked by the binary's own `--version`.
