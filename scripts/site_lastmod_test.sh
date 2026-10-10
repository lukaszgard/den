#!/bin/sh
# site_lastmod_test.sh — tests of site-lastmod.sh against throwaway repositories.
# Run with `make script-test`.
set -eu

script=$(cd "$(dirname "$0")" && pwd)/site-lastmod.sh
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# The developer's git config (signing, hooks, templates) must not leak in.
GIT_CONFIG_GLOBAL=/dev/null
GIT_CONFIG_SYSTEM=/dev/null
export GIT_CONFIG_GLOBAL GIT_CONFIG_SYSTEM

failed=0
fail() {
	printf 'FAIL %s\n' "$*" >&2
	failed=$((failed + 1))
}

# commit DATE FILE...: commits the files at DATE.
commit() {
	date=$1
	shift
	git add "$@"
	GIT_AUTHOR_DATE=$date GIT_COMMITTER_DATE=$date git commit -q -m "change $*"
}

repo=$work/repo
git init -q -b main "$repo"
cd "$repo"
git config user.name test
git config user.email test@example.com
git config commit.gpgsign false
mkdir -p docs/rds docs/help
echo a >README.md
echo a >docs/rds/README.md
echo a >docs/help/rds.md
commit 2026-01-01T10:00:00+00:00 README.md docs/rds/README.md docs/help/rds.md
echo b >docs/rds/README.md
commit 2026-02-02T10:00:00+00:00 docs/rds/README.md
echo new >docs/new.md # not committed yet

sh "$script" "$repo"
json=$repo/site/data/lastmod.json
grep -q '"README.md": "2026-01-01T10:00:00Z"' "$json" || fail "README.md is dated by its own commit"
grep -q '"docs/rds/README.md": "2026-02-02T10:00:00Z"' "$json" || fail "a page is dated by its last commit, not its first"
grep -q 'docs/help' "$json" && fail "a Help topic is not a page of the site"
grep -q 'docs/new.md' "$json" && fail "a file without a commit has no date"
# What the site reads must be JSON.
if command -v python3 >/dev/null 2>&1; then
	python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$json" || fail "lastmod.json is not valid JSON"
fi

# A shallow clone dates every file by the one commit it has: write no dates.
git clone -q --depth 1 "file://$repo" "$work/shallow"
sh "$script" "$work/shallow" 2>/dev/null
[ "$(tr -d ' \n' <"$work/shallow/site/data/lastmod.json")" = '{}' ] || fail "a shallow clone gets no dates"

[ "$failed" -eq 0 ] || exit 1
echo "site_lastmod_test: ok"
