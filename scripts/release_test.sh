#!/bin/sh
# release_test.sh — tests of `release.sh next` and `check` against throwaway
# repositories: one commit type at a time, on top of a main branch with a tag
# and a CHANGELOG.md. Run with `make script-test`.
set -eu

script=$(cd "$(dirname "$0")" && pwd)/release.sh
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# The developer's git config (signing, hooks, templates) must not leak in.
GIT_CONFIG_GLOBAL=/dev/null
GIT_CONFIG_SYSTEM=/dev/null
export GIT_CONFIG_GLOBAL GIT_CONFIG_SYSTEM
unset GITHUB_ACTIONS GITHUB_STEP_SUMMARY

failed=0
fail() {
	printf 'FAIL %s\n' "$*" >&2
	failed=$((failed + 1))
}

# repo TAG CHANGELOG-VERSION: a fresh repository on main, tagged vTAG, with
# CHANGELOG-VERSION as the top entry; leaves the shell on a feature branch.
repo() {
	dir=$(mktemp -d "$work/r.XXXXXX")
	cd "$dir"
	git init -q -b main
	git config user.name test
	git config user.email test@example.com
	git config commit.gpgsign false
	printf '# Changelog\n\n## [%s] - 2026-01-01\n\n- first\n' "$2" >CHANGELOG.md
	git add CHANGELOG.md
	git commit -q -m 'docs: start'
	git tag "v$1"
	git switch -q -c feature
}

# commit SUBJECT [BODY]
commit() {
	if [ $# -ge 2 ]; then
		git commit -q --allow-empty -m "$1" -m "$2"
	else
		git commit -q --allow-empty -m "$1"
	fi
}

# entry VERSION: a new top CHANGELOG.md entry, committed.
entry() {
	printf '# Changelog\n\n## [%s] - 2026-02-02\n\n- new\n\n%s\n' "$1" "$(sed 1,2d CHANGELOG.md)" >CHANGELOG.md.new
	mv CHANGELOG.md.new CHANGELOG.md
	git commit -q -am "docs: release v$1"
}

# expect_next NAME WANT: `next -q main` prints WANT (empty for no release).
expect_next() {
	got=$(sh "$script" next -q main)
	[ "$got" = "$2" ] || fail "$1: next -q printed '$got', want '$2'"
}

# --- next ---------------------------------------------------------------

repo 0.0.61 0.0.61
commit 'feat: a feature'
commit 'fix: a fix'
expect_next 'feat and fix' 0.1.0

repo 0.0.61 0.0.61
commit 'fix: a fix'
commit 'docs: words'
expect_next 'fix only' 0.0.62

repo 0.0.61 0.0.61
commit 'perf: faster'
expect_next 'perf only' 0.0.62

repo 0.0.61 0.0.61
commit 'docs: words'
commit 'test: more'
commit 'refactor: tidy'
commit 'build: flags'
commit 'ci: job'
expect_next 'docs/test/refactor/build/ci only' ''

repo 0.0.61 0.0.61
expect_next 'no commits' ''

repo 0.0.61 0.0.61
commit 'feat!: drop a key'
expect_next 'feat! on 0.x' 0.1.0

repo 0.0.61 0.0.61
commit 'fix(config)!: reject a key'
expect_next 'fix(scope)! on 0.x' 0.1.0

repo 1.2.3 1.2.3
commit 'fix!: change an output'
expect_next 'fix! on 1.x' 2.0.0

repo 1.2.3 1.2.3
commit 'feat: a feature'
expect_next 'feat on 1.x' 1.3.0

repo 0.0.61 0.0.61
commit 'refactor: split a package' 'Some words.

BREAKING CHANGE: the old key is gone.'
expect_next 'BREAKING CHANGE in a body on 0.x' 0.1.0

repo 0.0.61 0.0.61
commit 'fix: mentions' 'This is not a BREAKING CHANGE: in the line start sense.'
expect_next 'BREAKING CHANGE mid-line' 0.0.62

# main's CHANGELOG.md is ahead of the tags: a merged release PR not yet tagged.
repo 0.0.61 0.0.62
commit 'fix: a fix'
expect_next 'CHANGELOG ahead of the tag' 0.0.63

# A tag ahead of CHANGELOG.md wins too.
repo 0.0.70 0.0.61
commit 'fix: a fix'
expect_next 'tag ahead of CHANGELOG' 0.0.71

# A merge commit is skipped (here its subject would count as a feat).
repo 0.0.61 0.0.61
git switch -q -c side
commit 'docs: on the side'
git switch -q feature
commit 'docs: on the branch'
git merge -q --no-ff -m 'feat: merge side' side
expect_next 'merge commit ignored' ''

# The default output.
repo 0.0.61 0.0.61
commit 'feat: a feature'
commit 'fix: a fix'
commit 'fix: another'
out=$(sh "$script" next main)
case $out in
*'last v0.0.61 (CHANGELOG.md on main)'*'bump minor — 1 feat, 2 fix, 0 breaking'*'next v0.1.0'*) ;;
*) fail "next output: $out" ;;
esac

# --- check --------------------------------------------------------------

repo 0.0.61 0.0.61
commit 'feat: a feature'
entry 0.0.62
summary="$work/summary.md"
rc=0
out=$(GITHUB_ACTIONS=true GITHUB_STEP_SUMMARY=$summary sh "$script" check main) || rc=$?
[ "$rc" = 0 ] || fail "check with a low entry exited $rc"
case $out in
*'::warning::'*0.0.62*0.1.0*) ;;
*) fail "check with a low entry printed no warning naming both: $out" ;;
esac
grep -q '| Expected | v0.1.0 |' "$summary" || fail "step summary lacks the expected version"

repo 0.0.61 0.0.61
commit 'feat: a feature'
entry 0.1.0
out=$(GITHUB_ACTIONS=true sh "$script" check main)
case $out in
*warning*) fail "check with a 0.1.0 entry warned: $out" ;;
esac

repo 0.0.61 0.0.61
commit 'fix: a fix'
entry 0.3.0
out=$(GITHUB_ACTIONS=true sh "$script" check main)
case $out in
*warning*) fail "check with a higher entry warned: $out" ;;
esac

repo 0.0.61 0.0.61
commit 'fix: a fix'
rc=0
out=$(GITHUB_ACTIONS=true sh "$script" check main) || rc=$?
[ "$rc" = 0 ] || fail "check with no entry exited $rc"
case $out in
*'::warning::'*'## [0.0.62]'*) ;;
*) fail "check with no entry did not ask for one: $out" ;;
esac

repo 0.0.61 0.0.61
commit 'docs: words'
out=$(GITHUB_ACTIONS=true sh "$script" check main)
case $out in
*warning*) fail "check with docs only warned: $out" ;;
esac

if [ "$failed" -gt 0 ]; then
	printf '%s script test(s) failed\n' "$failed" >&2
	exit 1
fi
echo "release.sh: ok"
