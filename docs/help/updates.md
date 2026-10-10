# Updates

Install new den releases from inside den

## What it is

The Updates panel keeps den current. den checks for a newer release when it starts; installing it downloads the archive, verifies it against the release's checksums and replaces the running binary. On macOS and Linux den then offers to restart into the new version. Releases come from this project's GitHub Releases, or from an S3 bucket when `updates:` names one.

## What you can do

- See whether a newer release exists, and check again whenever you like.
- Read its release notes, above the changelog of the den you run, before installing.
- Install it: downloaded, verified against `checksums.txt`, and swapped in place.
- Restart into it now, or later: the prompt lists every connection a restart closes.
- Copy the commands to install by hand, when den cannot write to its own folder.
- Install den on another machine, or pin a version, with the install script: `curl -fsSL https://github.com/lukaszgard/den/releases/latest/download/install.sh | sh -s -- --version v1.0.0` (`install.ps1` on Windows).
- Keys: press `h` in the Updates panel.

## Before you start

- From GitHub: nothing for a public repository. A private fork or a GitHub Enterprise server needs `GITHUB_TOKEN` exported before starting den, with read access to the repository's releases.
- From an S3 mirror: the AWS CLI v2, and a profile allowed `s3:GetObject` on `latest.json`, the archives and `checksums.txt`.
- Write access to the folder den is installed in.

## Configure

An `updates:` section; leave it out to use this project's GitHub Releases.

- `github_api`: the `releases/latest` API URL of a fork or a GitHub Enterprise server, such as `https://api.github.com/repos/you/den/releases/latest`.
- `s3_bucket`: a bucket you mirror the releases to; it wins over `github_api`.
- `s3_prefix`: the key prefix of `latest.json` and the archives, without a leading slash.
- `aws_profile`: the profile `aws s3 cp` runs as; the default profile without it.
- `aws_region`: the bucket's region.

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

## Limitations

- A restart closes every connection den has open, the VPN included, and nothing reconnects by itself.
- On Windows den cannot replace itself while it runs: quit and start it again after installing.
- A den in a root-owned folder cannot update itself; the panel shows the commands to install by hand.
- The changelog shown is the running den's, with only the newer release's notes added on top.

## When it goes wrong

- The check fails right after start: the S3 profile's SSO login has expired; log in from the AWS SSO panel and check again.
- `GITHUB_TOKEN` errors: the variable is missing in the shell den was started from, or lacks access to the releases.
- The install fails with a permission error: den cannot write next to its binary; use the manual commands.

## See also

- Menu and logs
- AWS SSO sessions
- `docs/updates/README.md`
