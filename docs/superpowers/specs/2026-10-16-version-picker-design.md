# Version picker and release testing — design

Status: implemented (POC) · Date: 2026-10-16 · Scope: picking any published version in
the Updates pane and with `den update`, keeping a bad version from being offered (skip,
pin, yank), and the process that gets a build in front of testers before it is released.

## Why

The updater knew one release: `latest.json`. Someone hit by a bad release could not go
back from inside den; the only way was the install script with `--version`. Nothing
tested a build before everyone got it either: CI built nothing installable, a release was
a tag, and no test ran the updater itself on a real binary.

## Goals

1. Install any published version, newer or older, from the Updates pane and from a shell.
2. Keep den from offering a bad version back: per machine (skip), per team (pin), for
   everyone (yank).
3. Get builds in front of testers before a release: a pull request's own build, and
   release candidates on an opt-in channel.
4. Exercise upgrade and downgrade of a real den binary in CI.
5. Keep the startup check as cheap as it is: no API call per start.

## Non-goals (follow-ups)

- Keeping the previous binary (`den.prev`) for an offline rollback.
- Replacing a running `den.exe` on Windows.
- Release signing and attestations.
- Update tools over MCP: never. An agent must not replace den's binary.

## The index: `releases.json`

```json
{"schema": 1, "releases": [
  {"version": "v1.8.0-rc.1", "date": "2026-10-20T10:00:00Z", "prerelease": true,  "notes": "…"},
  {"version": "v1.7.2",      "date": "2026-10-16T11:28:55Z", "prerelease": false, "notes": "…"},
  {"version": "v1.7.0",      "date": "…", "prerelease": false, "yanked": "SSM reconnect loop — use v1.7.1", "notes": "…"}]}
```

- Built by `scripts/release-index.sh OWNER/REPO .github/yanked.json` from the releases API:
  no drafts, tags `vX.Y.Z` or `vX.Y.Z-rc.N`, from v1.0.0 (the first with `checksums.txt`),
  newest first by semver, with the reasons of `.github/yanked.json`
  (`{"v1.7.0": "reason"}`).
- Uploaded (`--clobber`) to the **latest stable** release by
  `.github/workflows/release-index.yaml`, so it reads at
  `releases/latest/download/releases.json` with no API call, like `latest.json`. The
  workflow runs after every release (`release.yaml` calls it: a release made with
  `GITHUB_TOKEN` fires no `release` event), on a push to `main` that changes
  `yanked.json`, and by hand.
- Readers (`internal/update/releases.go`): the release feed reads the URL above; an S3
  mirror `<prefix>releases.json` with `aws s3 cp` (a 404, or a 403 to a caller that may
  not list the bucket, is "no index"); a token or GitHub Enterprise lists the releases API
  (`…/releases?per_page=100`) and takes the yanked marks from a `releases.json` asset.
- Without an index (every release before 1.8.0, or the seconds before its upload) the
  **startup check** reads `latest.json` as before, plus the pinned release by name; the
  **picker**, which a person opens, makes one unauthenticated API list call.

## What is offered: `update.Offer`

A pure function of the releases, the running version and a `Policy{Channel, Pin, Skipped}`:

- a pin offers the pinned release, older or newer; an unpublished pin is an error;
- otherwise the newest release above the running one that is not yanked, not skipped and
  admitted by the channel (`stable`: no release candidates; `prerelease`: them too);
- with nothing newer and the running release yanked, the newest eligible one below it —
  the way off a bad release;
- a running version that is not one (`dev`) is below every release.

`update.Why` says in one line why the offer is not simply the newest release. Versions are
ordered by `golang.org/x/mod/semver`: `v1.8.0-rc.1 < v1.8.0`, and `1.7.2` (a build) equals
`v1.7.2` (its tag).

## Where each choice lives

| Choice | Who | Where |
|---|---|---|
| Skip a version | one person, one machine | `updates.json` in den's state directory (`den paths`) |
| Pin a version | a team | `updates.pin` in `den.yaml` |
| Channel | a team or a tester | `updates.channel` in `den.yaml` |
| Yank a release | the maintainers, for everyone | `.github/yanked.json`, a PR |

## The Updates pane

`v` opens the picker on the check's index, or loads one. Rows: version, date, current /
↑ upgrade / ↓ downgrade, and `rc`, `pinned`, `skipped`, `⚠ yanked: reason`, `no <os>/<arch>
build`. `enter` asks before installing (`y` only) and says what it means: the direction, a
yanked reason, that a den older than 1.8.0 offers the latest release again and knows no
skip list or pin, a pin that will pull it back, and that keys newer than the release are
ignored or rejected. A downgrade offers to skip the version it leaves (on by default),
saved once the install worked. `s` shows a release's notes, `x` skips it (saved at once; the
check runs again when the picker closes), `p` shows release candidates. The install is the
pane's existing step chain with the chosen release.

## `den update`

`den update [--json]`, `den update list [--all] [--json]`,
`den update install [VERSION] [--yes] [--skip-current]`, `den update skip|unskip VERSION`.
It asks at a terminal and needs `--yes` elsewhere, and reads only the `updates:` block of a
`den.yaml` that has errors elsewhere: a release that rejects a config must still be able to
install the one that accepts it.

## The release testing process

| Layer | Who sees it | How |
|---|---|---|
| Unit and pane tests | CI | `internal/update` (Offer table, index readers, fallbacks), the pane, the CLI against an httptest releases API |
| Updater end to end | CI | `scripts/update_e2e_test.sh` in `make script-test` |
| Pull request snapshot | reviewers | CI job `snapshot`, artifact `den-pr-<n>` |
| Release candidate | testers with `updates.channel: prerelease` | `make release-tag VERSION=X.Y.Z-rc.N` on the release PR branch |
| Release | everyone | `make release-tag VERSION=X.Y.Z` on `main`, then the index job |
| Yank | everyone on 1.8.0 or later | a PR editing `.github/yanked.json`, then a fix release |

1. **Every pull request** runs the tests and the end-to-end updater test: three den
   binaries built from the branch, an S3 mirror laid out by `scripts/snapshot-mirror.sh`
   and read through a fake `aws`; the test upgrades, downgrades with `--skip-current`,
   pins, passes over a yanked release, refuses a tampered archive and installs from the
   mirror with `install.sh`, each time checked by the binary's own `--version`.
2. **Every pull request** also builds its release archives (`goreleaser release
   --snapshot`, versioned `<last release, patch + 1>-snapshot.<commit>`: above the last
   release, and a prerelease) and uploads them as a mirror, `den-pr-<n>`, for 7 days. The
   job summary has the commands; the build installs next to the reviewer's den.
3. **A release worth trying first** gets a release candidate: on the pushed release PR
   branch `chore/release-vX.Y.Z`, `make release-tag VERSION=X.Y.Z-rc.1`. The release
   workflow publishes a GitHub prerelease with the `## [X.Y.Z]` notes under a "Release
   candidate" line and no `latest.json` (a mirror copying a release's files must not make
   it everyone's latest); the index lists it. Testers on the prerelease channel are offered
   it; `releases/latest` ignores it. A fix goes on the branch, then `-rc.2`.
4. **The release**: the PR merges, `make release-tag VERSION=X.Y.Z` on `main`; the index
   job uploads `releases.json` to it.
5. **A bad release** is yanked: a PR adding it to `.github/yanked.json`. Dens from 1.8.0
   stop offering it and step a den running it down to the newest good release. Older dens
   read only `latest.json`, so a yank of the newest release is followed by a fix release.

## Caveat

Skip, pin and yank work only in dens that have them, from 1.8.0. Downgrading to 1.7.x
lands on a den with no picker, which offers the latest release again; the confirm box and
`den update install` say so.
