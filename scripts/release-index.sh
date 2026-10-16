#!/bin/sh
# release-index.sh OWNER/REPO [YANKED_JSON] — print releases.json, the list of
# every den release the version picker and the update check read, newest first.
#
# It is built from the published releases (drafts left out) with a tag vX.Y.Z
# or vX.Y.Z-rc.N from v1.0.0 on, the first with checksums.txt, and marks the
# ones YANKED_JSON ({"v1.7.0": "why"}) names. The release workflow uploads it to
# the latest stable release, so it reads at releases/latest/download/releases.json.
# Needs gh (authenticated: GH_TOKEN in CI) and jq.
set -eu

die() {
	printf 'release-index: %s\n' "$*" >&2
	exit 1
}

[ $# -ge 1 ] || die "usage: release-index.sh OWNER/REPO [YANKED_JSON]"
repo=$1
yanked=${2:-}
command -v gh >/dev/null || die "gh not found"
command -v jq >/dev/null || die "jq not found"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

if [ -n "$yanked" ]; then
	[ -f "$yanked" ] || die "$yanked: no such file"
	# Every value a reason: an empty one would mark nothing and look like it did.
	jq -e 'type == "object" and all(.[]; type == "string" and length > 0)' "$yanked" >/dev/null ||
		die "$yanked must map each version to the reason it is yanked, e.g. {\"v1.7.0\": \"use v1.7.1\"}"
	cp "$yanked" "$work/yanked.json"
else
	echo '{}' >"$work/yanked.json"
fi

# --paginate prints one JSON array per page; jq -s joins them.
gh api --paginate "repos/$repo/releases?per_page=100" >"$work/pages.json" ||
	die "could not list the releases of $repo"

jq -s --slurpfile yanked "$work/yanked.json" '
	def key: capture("^v(?<ma>[0-9]+)\\.(?<mi>[0-9]+)\\.(?<pa>[0-9]+)(-rc\\.(?<rc>[0-9]+))?$")
		| [(.ma | tonumber), (.mi | tonumber), (.pa | tonumber), (if .rc then 0 else 1 end), ((.rc // "0") | tonumber)];
	$yanked[0] as $y
	| {schema: 1, releases: (
		add // []
		| map(select((.draft | not) and (.tag_name | test("^v[0-9]+\\.[0-9]+\\.[0-9]+(-rc\\.[0-9]+)?$"))))
		| map(select((.tag_name | key)[0] >= 1))
		| sort_by(.tag_name | key) | reverse
		| map({version: .tag_name, date: .published_at, prerelease: (.prerelease or (.tag_name | test("-rc\\."))), notes: (.body // "")}
			+ (if $y[.tag_name] then {yanked: $y[.tag_name]} else {} end))
	)}' "$work/pages.json"
