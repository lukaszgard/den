---
name: open-pr
description: This skill should be used when the user asks to "open a PR", "create a pull request", "raise a PR", "publish the branch", or when work on a branch is finished — committed and `make check` passing — and the branch has no open PR yet.
---

# Open a PR

A finished branch is published at once: push it, open a **ready-for-review** PR into
`main` with `.github/pull_request_template.md` filled in, and report the link. The user
decided this; do not ask again. A branch with `feat`/`fix` commits ships as its own
release: one bundled PR that also carries the `CHANGELOG.md` entry, tagged after merge.

Never merge, push `main`, force-push or push a tag. Tagging waits for the user's
go-ahead (skill `release`).

## 1. Preconditions

Stop at the first one that fails and report it.

| Check | How |
|---|---|
| on a branch, not `main`; clean tree | `git branch --show-current`, `git status --porcelain` |
| contains current `main` | `git fetch origin && git merge-base --is-ancestor origin/main HEAD`; rebase an unpushed branch, report a pushed one |
| commit subjects | every `git log --format=%s origin/main..HEAD` line matches `^(feat\|fix\|refactor\|perf\|test\|docs\|build\|ci)(\(.+\))?!?: ` |
| CI passes **on this HEAD** | `make check`, `make cover` (the coverage floors) and `make generate` leaving no diff, since CI runs all three. Prefix `GOTOOLCHAIN=<go.mod toolchain>` if golangci-lint panics. A release branch runs `make release-prep` in place of `make check`. |

## 2. Release entry: only for branches that call for a release (`feat`, `fix`, `perf`, breaking)

1. Version: `make release-next` (minor for `feat`, patch for `fix`/`perf`, minor for a
   breaking change on 0.x). If an open release PR (`gh pr list --search "release in:title"`)
   already claims that version, take the next one of the same kind after it, unless the
   user names one.
2. If `CHANGELOG.md` has no `## [X.Y.Z]` yet, follow skill `release`, steps 3–5:
   `make release-draft VERSION=X.Y.Z`, rewrite the entry for users,
   `make release-prep VERSION=X.Y.Z`, commit `docs: release vX.Y.Z`.
3. Title: `feat: release vX.Y.Z — <what users get>` (`fix:` for a patch release). End the Description with
   "After merging: tag the merge commit with `make release-tag VERSION=X.Y.Z`."

For any other branch (`docs`, `test`, `refactor`, `build`, `ci` only), the title is the one commit's subject, or one
`<type>: <summary>` that covers all the commits.

## 3. Body

The body **is** the template, read fresh from `.github/pull_request_template.md`:
- Every heading and checkbox stays, in order, with the same indentation.
- HTML comments are removed.
- A ticked box may add detail after its label.

Fill it from `git log origin/main..HEAD`, `git diff --stat origin/main...HEAD` and what
actually ran in this session:

| Section | Fill with |
|---|---|
| Description | What changed and why, for a reviewer. Behaviour first, one bullet per area. Follow-ups. Last line: `**Release:** vX.Y.Z — <kind> (<counts>), after vLAST` from `make release-next` (e.g. `**Release:** v0.1.0 — minor (2 feat, 1 fix), after v0.0.60`), or `**Release:** none — only docs/ci/test/refactor commits`. |
| Contents | feat → Feature, fix → Bugfix, refactor → Refactor, `docs/` or `*.md` → Documentation, `den.yaml` or `internal/config/` → Configuration, `.github/`, `Makefile`, `.goreleaser.yaml` or `scripts/` → Pipeline |
| How to Test | Numbered steps a reviewer can run: the commands, the keys to press in den, and what they should see |
| Locally | What ran, with results, then what did **not** run (real AWS, a real terminal, an unrendered tape). Tick "Against a real AWS account" only if one was used. |
| Tests created | The test files and what they pin, or "No, because" and the reason |
| Configuration changed? | A `den.yaml` schema change ticks the parent box, with the keys named. The Java sub-items stay unticked. Otherwise keep "Not relevant". |
| Documentation updated? | "Yes, in this repository" with the files named, or "Not relevant" |
| Breaking Changes | "None", unless config keys, commands, flags or relied-on behaviour changed. Then untick it and give the migration. |

End with the harness's PR attribution line, if it has one. For a filled example, run
`gh pr view 12 --json body -q .body`.

Write the body to a scratch file and check it until it passes (✓):
`sh .claude/skills/open-pr/check-body.sh <body-file> "<title>"`

## 4. Publish

```sh
git push -u origin <branch>                      # explicit: never rely on the upstream
gh pr view --json url -q .url                    # a PR exists → gh pr edit --title … --body-file <file>
gh pr create --base main --title "<title>" --body-file <file>
```

Report the URL, the same `**Release:**` line and the `gh pr checks` state; the user reads the
version per PR from that message. Do not wait for CI.

## Common mistakes

- Ticking "Locally" for checks that ran on an earlier commit. Re-run them on the HEAD you
  publish, including after a rebase or a release commit.
- A branch made with `git switch -c x origin/main` tracks `main`, so a bare `git push`
  targets `main`. Always push `origin <branch>` explicitly.
- Two open release PRs claiming the same version.
- Bumping the patch on a branch with `feat` commits; `release-version` in CI warns about it.
