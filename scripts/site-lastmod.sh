#!/bin/sh
# site-lastmod.sh — writes site/data/lastmod.json, the date of the last commit
# of every markdown file the documentation site shows, for its "Last updated"
# line. The site's pages come from a content adapter, which has no git history
# to read; this is the history, handed over as data. `make site` and
# `make site-build` run it first.
#
#   scripts/site-lastmod.sh [ROOT]     ROOT is the repository (default: this one)
#
# A shallow clone has one commit that touched everything, so it would date every
# page the same: the file is then empty and the site shows no dates. The Docs
# site workflow checks out the full history for that reason.
set -eu

root=${1:-$(cd "$(dirname "$0")/.." && pwd)}
cd "$root"
out=site/data/lastmod.json
mkdir -p site/data

if [ "$(git rev-parse --is-shallow-repository)" = true ]; then
	echo '{}' >"$out"
	echo "site-lastmod: shallow clone, no dates written (fetch the full history)" >&2
	exit 0
fi

{
	printf '{'
	sep=
	# The Help topics (docs/help) are den's in-app manual, not pages of the site.
	for f in README.md CHANGELOG.md $(git ls-files 'docs/*.md' | grep -v '^docs/help/'); do
		date=$(git log -1 --format=%cI -- "$f")
		# A file with no commit yet (a new page) has no date to show.
		[ -n "$date" ] || continue
		printf '%s\n  "%s": "%s"' "$sep" "$f" "$date"
		sep=,
	done
	printf '\n}\n'
} >"$out.tmp"
mv "$out.tmp" "$out"
