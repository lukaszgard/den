---
name: release
description: This skill should be used when the user asks to "cut a release", "tag a new version", "release den", "prepare the changelog", or "publish vX.Y.Z".
---

# Release den

Releases are built by `.github/workflows/release.yaml` when a `v*.*.*` tag is pushed:
GoReleaser builds macOS/Linux/Windows × amd64/arm64 from the **tagged commit**, publishes
`checksums.txt` (the in-app updater verifies downloads against it), publishes the
release page, and attaches `latest.json` (for update checks) and the install scripts.

The release notes — on the release page and in `latest.json`, which the Updates pane
shows as "what's new" — are the tag's section of `CHANGELOG.md`, cut out by the
workflow. **A tag without a `## [X.Y.Z]` section fails the release** before anything is
published. `CHANGELOG.md` is also embedded in the binary (Updates pane, changelog view).

## Steps

`scripts/release.sh`, through make, runs the mechanical steps; the entry and the PR stay
with a person:

| Step | Command |
|------|---------|
| 2 pick the version | `make release-next [BASE=origin/main]` |
| 3 draft the entry | `make release-draft VERSION=X.Y.Z [REF=feature/<branch>]` |
| 4–5 check notes, `make check`, dry run | `make release-prep VERSION=X.Y.Z` |
| 7 tag the merged commit | `make release-tag VERSION=X.Y.Z` (on `main`; asks before pushing) |

If golangci-lint panics because the local Go is newer than it supports, run `release-prep`
with the toolchain `go.mod` names, e.g. `GOTOOLCHAIN=go1.27.1 make release-prep …`.

1. Branch from an up-to-date `main` (`chore/release-vX.Y.Z`); `make check` passes on it.
2. Pick the version: run `make release-next`. It reads the commit types since the base
   and prints `last vX`, `bump minor — N feat, N fix, N breaking` and `next vX.Y.Z`
   (breaking → minor on 0.x, `feat` → minor, `fix`/`perf` → patch, nothing else → no
   release). Use that version unless the user names a higher one. A breaking change on
   0.x gets a `**Breaking:**` line at the top of the entry with the migration.
3. Draft the entry with git-cliff (`cliff.toml` groups by `feat:`/`fix:`/…):
   - PRs are **squash-merged**, so `main` holds one commit per PR and
     `git cliff --unreleased` only sees PR titles. Draft from the individual commits
     instead, while their branch still exists:
     `git cliff vLAST..feature/<branch> --tag vX.Y.Z` (one run per merged branch), or
     from the PR descriptions on GitHub.
   - Rewrite the draft into sentences a user understands, grouped by area, leading with
     **behaviour changes**. Every `feat`/`fix` commit must map to a line; list them with
     `git log --format=%s vLAST..<branch> | grep -E ': (feat|fix)'` and tick them off.
   - Header format is exactly `## [X.Y.Z] - YYYY-MM-DD` (no `v`): the workflow matches it.
4. Check the notes the workflow will publish:
   `GITHUB_REF_NAME=vX.Y.Z` + the awk from the "Release notes from CHANGELOG.md" step,
   run against `CHANGELOG.md`; it must print the section and nothing of the next one.
5. Dry run: `goreleaser check && make snapshot`; confirm `dist/` holds every archive,
   `checksums.txt`, and `skills/den/SKILL.md` inside the archives. (Snapshots skip the
   release notes; a non-snapshot `goreleaser release --skip=publish` on a local,
   deleted-afterwards tag writes them to `dist/CHANGELOG.md`.)
6. Commit, and — only when the user says so — push the branch and open a PR into `main`
   with the `open-pr` skill.
7. After the PR is merged: `git switch main && git pull`, then tag **that** commit and
   push the tag, again only on the user's go-ahead:
   `git tag vX.Y.Z && git push origin vX.Y.Z` — or `make release-tag VERSION=X.Y.Z`,
   which checks `main` is `origin/main` and HEAD has the entry, then asks.
8. After the workflow: the release page shows the curated notes, the archives and
   `checksums.txt`; `latest.json` among the assets has the version and the same notes; `den` from
   the previous version finds, shows and installs the new one (Updates pane, `i`).

Never push tags or branches without the user's explicit go-ahead.
