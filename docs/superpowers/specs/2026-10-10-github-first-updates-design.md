# GitHub-first updates — design

Status: approved · Date: 2026-10-10 · Scope: the in-app updater (phase 1);
signing and package managers are follow-ups (see the end).

## Why

den is going public. Releases already live on this repository's GitHub Releases and the
updater already reads them by default; the S3 mirror is something a team opts into with
`updates.s3_bucket` and pays for itself. `release.yaml` never uploads to S3, so a public
audience cannot cost the maintainer anything. That part needs no change.

What does need a change is *how* the default path talks to GitHub:

- **The update check calls `api.github.com/repos/.../releases/latest`**
  (`DefaultGitHubAPI`, `internal/update/update.go`). Unauthenticated, GitHub allows 60 API
  requests an hour per IP. den checks on every start (`internal/tui/app.go:407`). An
  office, a VPN exit or a CI fleet behind one NAT starts seeing HTTP 403 on the Updates
  pane, and a public project cannot ask people to export a token.
- **The install scripts already avoid the API.** `install.sh` and `install.ps1` read
  `…/releases/latest/download/latest.json` (a redirect to the release's asset, which the API
  limit does not cover) and the archive by its predictable
  `…/releases/download/<tag>/<file>` URL. The updater should use the same route.

## Goals

1. The default update check and download make no GitHub API call.
2. Nothing changes for S3 mirror users or GitHub Enterprise.
3. Old releases keep working: a den 1.0.0 using the API path is unaffected.
4. A person who needs something (a token, a proxy) is told what, in the pane, in the Help
   and in the docs, in the same words.

## Non-goals

- Removing or trimming the S3 mirror (the updater and the install scripts keep it).
- Signing, attestations, Homebrew/Scoop and managed-install detection: separate specs.
- A setting to turn the startup check off. The check stays on every start.

## Design

### Source resolution

`Source.Latest` picks one route:

| Condition | Route |
|-----------|-------|
| `S3Bucket` set | S3 through `aws` (unchanged) |
| `Token` set (`GITHUB_TOKEN`) | releases API with the token (5000 requests/h; also what a private repo needs) |
| `GitHubAPI` empty | **feed** for `lukaszgard/den` |
| `GitHubAPI` = `https://api.github.com/repos/<o>/<r>/releases/latest` | **feed** for `<o>/<r>` |
| any other `GitHubAPI` (GitHub Enterprise, tests' httptest URLs) | releases API (unchanged) |
| feed answers **404** | releases API without a token (old tags, private repo, the seconds before `latest.json` is uploaded) |

**Release feed.** `GET <web>/<owner>/<repo>/releases/latest/download/latest.json`,
following the redirect. `<web>` is `Source.GitHubWeb`, `https://github.com` by default
(tests point it at an httptest server). It returns `{"version": "v1.2.3", "notes": "…"}`,
which the release workflow already publishes. From the version den builds the rest without
asking GitHub:

- release page: `<web>/<owner>/<repo>/releases/tag/<version>`
- archive: `<web>/<owner>/<repo>/releases/download/<version>/<ArchiveName(...)>`
- checksums: `<web>/<owner>/<repo>/releases/download/<version>/checksums.txt`

A feed release whose `checksums.txt` answers 404 maps to `ErrNoChecksums`, as an empty
`ChecksumsURL` does on the API route.

**Fallback on 404 only.** A 404 on the feed means the release has no `latest.json`: a tag
cut before it was published, a fork's workflow without that step, a private repository
answered anonymously, or the seconds between GoReleaser publishing a release and the
workflow uploading `latest.json`. The API route then answers without a token (the feed
route only runs when none is set), as it does today. Other failures (network, 5xx,
unparsable JSON, an empty version) are errors: the API would fail the same way, and a
second request would only hide the first message.

**Tokens.** The feed requests carry no `Authorization` header: they are public, and the
feed route only runs without a token. A token selects the API route outright. `Download`
and `Checksums` need no change: they stream `ArchiveURL`/`ChecksumsURL` through
`fetchGitHub`, which sends the token only when there is one.

### Where the source is named

`Source.Location()` returns the `s3://bucket/prefix` URL, the feed URL, or the API URL
(with " (GITHUB_TOKEN)" when a token is set). `CheckForUpdates` logs it and the Updates
pane's help overlay prints it, so the two cannot drift.

### What an end user needs

**Public repository (the target): nothing to configure.**
- No `updates:` block, no token, no AWS.
- Outbound HTTPS to `github.com` and to the host GitHub redirects release downloads to.
- Behind a corporate proxy, den honours `HTTPS_PROXY`/`NO_PROXY`.
- Write access to the folder den is installed in.

**Private repository (now):** export `GITHUB_TOKEN` before starting den, with Contents:
read or the `repo` scope.

**Fork or GitHub Enterprise:** `updates.github_api`, plus `GITHUB_TOKEN` when private.

**S3 mirror:** unchanged.

This list appears in the same words in `docs/updates/README.md` ("What you need"), in the
in-app Help (`docs/help/updates.md`, "Before you start") and in the Updates pane's `h`
overlay. A failed check says which one applies:

- the feed cannot be reached: "could not reach github.com — den needs HTTPS access to
  github.com; behind a proxy set HTTPS_PROXY";
- 404 on the feed and on the API: the existing hint to set `GITHUB_TOKEN` for a private
  repository;
- 403 or 429 from the API: GitHub's rate limit was reached; `GITHUB_TOKEN` raises it, or
  unsetting it lets den use the release feed.

### Components

| Unit | Change |
|------|--------|
| `internal/update/update.go` | `DefaultRepo`, `Source.GitHubWeb`, `feedRepo`, `latestFeed`, the route choice, direct URLs in `Release`, `Location`, the error messages. `DefaultGitHubAPI` stays for the fallback. |
| `internal/tui/screens/updates/updates.go` | `CheckForUpdates` logs `Location()`; the help overlay's "What you need" section. |

No new dependency, no config key, no schema change.

### Docs and config text

- `docs/updates/README.md`: "What you need", "Where releases come from" (the feed, the
  token, the fallback, forks), "Prerequisites" (network access to `github.com`, not
  `api.github.com`, by default).
- `docs/help/updates.md`: "Before you start" as the end-user list, "When it goes wrong".
- `den.yaml` and the `UpdatesConfig` comment (`internal/config/schema.go`): the default is
  the project's release feed; `github_api` is for a fork or GitHub Enterprise. Then
  `make generate`.
- `docs/install/README.md`: the sentence that the scripts and the Updates pane read the same
  releases stays true; no change.

### Testing

All with the existing `roundTripFunc` fake transport and httptest servers; no real network.

- Default source asks `…/releases/latest/download/latest.json`, sends no `Authorization`
  (replaces `TestDefaultSourceIsPublicGitHub`).
- A feed twin of `TestGitHubDownloadVerifiedEndToEnd`: latest, download, checksums,
  verify, extract, replace.
- 404 on the feed falls back to the API; a 500 does not.
- A token goes straight to the API and never asks the feed.
- `github_api` on `api.github.com` derives the feed for that repository; on another host it
  stays on the API.
- Missing `checksums.txt` is `ErrNoChecksums`; an empty `version` is an error.
- `Location()` for each route.
- An unreachable feed names github.com and `HTTPS_PROXY`; a 403 from the API gives the
  rate-limit message.
- S3 tests (`s3_test.go`) pass unchanged.

`make check` and `make cover` (the update package's floor must not drop). UI: a manual run
of `den` with `logs.level: trace`, with and without `GITHUB_TOKEN`, then `h` in Updates.

## Rollout and compatibility

- Releases already carry `latest.json`; nothing to republish. Tags without it fall back to
  the API.
- Released dens on the API route keep working; they only keep the limit.
- The release workflow is untouched in this phase.
- Version bump from the commit types (`make release-next`): the feed is a `fix:` (a user
  behind a shared IP could not check).

## Decisions

1. **The repository stays private for now.** A private repo answers the feed with 404. A
   token keeps the API route; without one the feed's 404 falls back to the API and its
   existing "set GITHUB_TOKEN" message. Nothing more is needed until the repo opens.
2. **The startup check stays on every start**, as today; the feed has no rate limit to
   protect.
3. **Forks use the feed.** A `github_api` on `api.github.com` derives the feed from its
   owner/repo, so a fork stops depending on the API limit too.
4. **The S3 mirror stays unchanged**, in the updater and in the install scripts.

## Follow-ups (own specs, in this order)

1. **Signed releases.** Cosign keyless signature of `checksums.txt` (GoReleaser `signs`) and
   build-provenance attestations (`actions/attest-build-provenance`; workflow permissions
   `id-token: write`, `attestations: write`). Documented verification with
   `gh attestation verify`; the install script verifies when `gh` or `cosign` is present;
   verification inside the Go updater is a later step because of the dependency it brings.
   Closes the gap that today `checksums.txt` sits beside the archive and guards against
   corruption, not against a tampered release.
2. **Package managers.** A Homebrew tap and a Scoop bucket from GoReleaser (check the
   current docs: `brews` has been superseded by `homebrew_casks`), each its own repository
   and a push token in the workflow. The updater detects a binary a package manager owns
   and shows `brew upgrade den` / `scoop update den` instead of replacing it.
3. **Immutable releases.** The workflow uploads `latest.json` and the scripts after the
   release is published (`--clobber`). GitHub's immutable-releases setting would refuse
   that: the order must become draft → upload everything → publish first. Verify the
   setting's current behaviour before changing anything.
