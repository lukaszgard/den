# Updates

Install new den releases from inside den

## What it is

The Updates panel keeps den current. den checks for a newer release when it starts; installing it downloads the archive, verifies it against the release's checksums and replaces the running binary. On macOS and Linux den then offers to restart into the new version. Releases come from this project's GitHub Releases, or from an S3 bucket when `updates:` names one. The version picker installs any published release, an older one too, which is the way off a bad release.

## What you can do

- See whether a newer release exists, and check again whenever you like.
- Read its release notes, above the changelog of the den you run, before installing.
- Install it: downloaded, verified against `checksums.txt`, and swapped in place.
- Restart into it now, or later: the prompt lists every connection a restart closes.
- Pick any published version with `v`, older ones included: each row says whether it is an upgrade or a downgrade, a release candidate, pinned, skipped or yanked, and the question before the install says what a downgrade means.
- Skip a version on this machine, so update checks stop offering it; a downgrade offers to skip the version it leaves.
- Hold the whole team on a tested version with `updates.pin`, or try release candidates with `updates.channel: prerelease`.
- Do the same from a shell: `den update` says what is offered and why, `den update list` lists the releases, `den update install VERSION` installs one, and `den update skip VERSION` skips one.
- Copy the commands to install by hand, when den cannot write to its own folder.
- Install den on another machine, or pin a version, with the install script: `curl -fsSL https://github.com/lukaszgard/den/releases/latest/download/install.sh | sh -s -- --version v1.0.0` (`install.ps1` on Windows).
- Keys: press `h` in the Updates panel.

## Before you start

- Public repository (the default): nothing to configure. No `updates:` block, no token, no AWS. den needs HTTPS access to `github.com` and to the host it redirects downloads to, `release-assets.githubusercontent.com`; behind a corporate proxy it honours `HTTPS_PROXY` and `NO_PROXY`.
- Private repository: export `GITHUB_TOKEN` before starting den. The token needs Contents: read, or the `repo` scope.
- Fork or GitHub Enterprise: set `updates.github_api`, plus `GITHUB_TOKEN` when the repository is private.
- S3 mirror: set `updates.s3_bucket`, with the AWS CLI v2 and a profile allowed `s3:GetObject` on `latest.json`, `releases.json`, the archives and `checksums.txt`. No token and no GitHub access are needed.
- Write access to the folder den is installed in.

## Configure

An `updates:` section; leave it out to use this project's GitHub Releases.

- `github_api`: the `releases/latest` API URL of a fork or a GitHub Enterprise server, such as `https://api.github.com/repos/you/den/releases/latest`. A fork on github.com reads the release feed of its own repository; `GITHUB_TOKEN` makes den use the API, which a private one needs.
- `s3_bucket`: a bucket you mirror the releases to; it wins over `github_api`.
- `s3_prefix`: the key prefix of `latest.json`, `releases.json` and the archives, without a leading slash.
- `aws_profile`: the profile `aws s3 cp` runs as; the default profile without it.
- `aws_region`: the bucket's region.
- `channel`: which releases the check offers and the version picker lists: `stable` (the default), or `prerelease` for release candidates too.
- `pin`: hold den on one version, such as `v1.7.1`: the check offers it, older or newer, and nothing else; for a team that stays on a release it tested.

Releases from a bucket, read as a dedicated profile:

```yaml
updates:
  s3_bucket: my-den-releases
  s3_prefix: den/
  aws_profile: tools
  aws_region: eu-central-1
```

A fork on GitHub, or an S3 mirror with the default profile and its region:

```yaml
updates:
  github_api: https://api.github.com/repos/you/den/releases/latest
```

```yaml
updates:
  s3_bucket: my-den-releases
```

Try release candidates before everyone else, or hold the team on a tested release:

```yaml
updates:
  channel: prerelease
```

```yaml
updates:
  pin: v1.7.1
```

## Limitations

- A restart closes every connection den has open, the VPN included, and nothing reconnects by itself.
- On Windows den cannot replace itself while it runs: quit and start it again after installing.
- A den in a root-owned folder cannot update itself; the panel shows the commands to install by hand.
- The changelog shown is the running den's, with only the newer release's notes added on top.
- Skip, pin and yank work in dens from 1.8.0, the first with the picker: a downgrade to an older den lands on one that offers the latest release again, whatever is skipped, pinned or yanked.
- A skip belongs to one machine; for the whole team, pin the version in `den.yaml`.
- Without a `releases.json` (no release before 1.8.0 published one) the picker asks the GitHub API once, which allows 60 requests an hour per IP without a token.

## When it goes wrong

- The check fails right after start: the S3 profile's SSO login has expired; log in from the AWS SSO panel and check again.
- "could not reach github.com": den has no HTTPS route to github.com; check the network, and behind a proxy set `HTTPS_PROXY` before starting den.
- "HTTP 404 … set GITHUB_TOKEN": the repository is private, or the release does not exist; export `GITHUB_TOKEN` with Contents: read in the shell den starts from.
- "HTTP 404 … GITHUB_TOKEN is valid but GitHub shows it no such release": the token cannot read this repository, which GitHub reports as a missing one; a fine-grained token needs the repository selected under its owner and Contents: read, a classic one the `repo` scope, authorized for SSO when the organization enforces it.
- "GitHub's rate limit was reached": the check went through the API, which allows 60 requests an hour per IP without a token; set `GITHUB_TOKEN` to raise it, or unset it so den reads the release feed.
- `GITHUB_TOKEN` errors: the variable is missing in the shell den was started from, or lacks access to the releases.
- The install fails with a permission error: den cannot write next to its binary; use the manual commands.
- "updates.pin vX is not a published release": the pin names a version that was never released, or a release candidate the check cannot see; fix the pin in `den.yaml`.
- "no releases.json published … copy releases.json": the S3 mirror has not copied the release list; copy it from the latest GitHub release next to `latest.json`.
- A release keeps being offered after a downgrade: it was not skipped; press `x` on it in the picker, or run `den update skip VERSION`.

## See also

- Menu and logs
- AWS SSO sessions
- `docs/updates/README.md`
