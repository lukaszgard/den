#!/bin/sh
# release_index_test.sh — tests of release-index.sh with a fake `gh` that
# answers the releases API from fixture pages. Run with `make script-test`.
set -eu

script=$(cd "$(dirname "$0")" && pwd)/release-index.sh
repo_yanked=$(cd "$(dirname "$0")/.." && pwd)/.github/yanked.json
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

failed=0
fail() {
	printf 'FAIL %s\n' "$*" >&2
	failed=$((failed + 1))
}

# gh api --paginate repos/<repo>/releases?per_page=100: prints the two pages
# as --paginate does, one JSON array after the other.
mkdir -p "$work/bin"
cat >"$work/bin/gh" <<FAKE
#!/bin/sh
[ "\$1 \$2 \$3" = "api --paginate repos/acme/den/releases?per_page=100" ] || { echo "unexpected: \$*" >&2; exit 1; }
cat "$work/page1.json" "$work/page2.json"
FAKE
chmod +x "$work/bin/gh"
PATH=$work/bin:$PATH
export PATH

rel() { # rel TAG DATE [PRERELEASE] [DRAFT]
	printf '{"tag_name":"%s","published_at":"%s","prerelease":%s,"draft":%s,"body":"notes of %s"}' \
		"$1" "$2" "${3:-false}" "${4:-false}" "$1"
}
printf '[%s,%s,%s]' "$(rel v1.8.0-rc.1 2026-10-20T10:00:00Z true)" "$(rel v1.10.0 2026-10-21T10:00:00Z)" \
	"$(rel v1.9.0 2026-10-19T10:00:00Z false true)" >"$work/page1.json"
printf '[%s,%s,%s,%s,%s]' "$(rel v1.7.2 2026-10-16T11:28:55Z)" "$(rel v1.7.0 2026-10-10T10:00:00Z)" \
	"$(rel v0.0.41 2026-01-01T10:00:00Z)" "$(rel nightly 2026-01-01T10:00:00Z)" "$(rel v1.8.0 2026-10-20T12:00:00Z)" >"$work/page2.json"
echo '{"v1.7.0": "SSM reconnect loop — use v1.7.1"}' >"$work/yanked.json"

# field JQ: the value of JQ in $out.
field() { printf '%s' "$out" | jq -r "$1"; }

out=$(sh "$script" acme/den "$work/yanked.json") || fail "release-index.sh exited $?"
# Newest first by semver (1.10 above 1.9, a release above its rc); no draft, no
# tag before v1.0.0, nothing that is not a version.
got=$(field '.releases | map(.version) | join(" ")')
[ "$got" = "v1.10.0 v1.8.0 v1.8.0-rc.1 v1.7.2 v1.7.0" ] || fail "versions: $got"
[ "$(field .schema)" = 1 ] || fail "schema: $out"
[ "$(field '.releases[] | select(.version == "v1.7.0") | .yanked')" = "SSM reconnect loop — use v1.7.1" ] ||
	fail "v1.7.0 is not yanked: $out"
[ "$(field '[.releases[] | select(.yanked)] | length')" = 1 ] || fail "more than v1.7.0 yanked: $out"
[ "$(field '.releases[] | select(.version == "v1.8.0-rc.1") | .prerelease')" = true ] || fail "the rc is not a prerelease: $out"
[ "$(field '.releases[] | select(.version == "v1.7.2") | .date + " " + .notes')" = "2026-10-16T11:28:55Z notes of v1.7.2" ] ||
	fail "date and notes lost: $out"

# Without a yanked file nothing is yanked.
out=$(sh "$script" acme/den) || fail "without a yanked file exited $?"
[ "$(field '[.releases[] | select(.yanked)] | length')" = 0 ] || fail "yanked without a file: $out"

# A yanked file that does not map versions to reasons is refused.
for bad in '[]' '{"v1.7.0": ""}' '{"v1.7.0": 1}' 'not json'; do
	printf '%s' "$bad" >"$work/bad.json"
	if sh "$script" acme/den "$work/bad.json" >/dev/null 2>&1; then
		fail "yanked file $bad was accepted"
	fi
done
if sh "$script" acme/den "$work/missing.json" >/dev/null 2>&1; then
	fail "a missing yanked file was accepted"
fi

# A broken API answer fails the script: a half index must never be uploaded.
printf 'not json' >"$work/page2.json"
if sh "$script" acme/den >/dev/null 2>&1; then
	fail "a broken API answer was accepted"
fi

# The repository's own list must stay valid: the workflow reads it.
jq -e 'type == "object" and all(.[]; type == "string" and length > 0)' "$repo_yanked" >/dev/null ||
	fail ".github/yanked.json does not map versions to reasons"

if [ "$failed" -gt 0 ]; then
	printf '%s script test(s) failed\n' "$failed" >&2
	exit 1
fi
echo "release-index.sh: ok"
