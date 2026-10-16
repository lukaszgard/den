#!/bin/sh
# snapshot-mirror.sh DIST MIRROR — lay out the GoReleaser build in DIST as a
# release mirror in MIRROR, the layout install.sh and den's S3 source read:
#
#   MIRROR/latest.json  MIRROR/releases.json  MIRROR/install.sh
#   MIRROR/v<version>/den_<version>_<os>_<arch>.tar.gz|.zip  checksums.txt
#
# CI runs it on the snapshot of every pull request and uploads MIRROR as the
# artifact den-pr-<n>, which installs with
#
#   DEN_INSTALL_BASE="file://$PWD/MIRROR" sh MIRROR/install.sh --dir DIR
#
# MIRROR is created when missing; another version's files in it stay, and
# latest.json and releases.json then name this one. Needs jq.
set -eu

die() {
	printf 'snapshot-mirror: %s\n' "$*" >&2
	exit 1
}

[ $# -eq 2 ] || die "usage: snapshot-mirror.sh DIST MIRROR"
dist=$1
mirror=$2
here=$(cd "$(dirname "$0")" && pwd)
[ -f "$dist/checksums.txt" ] || die "$dist has no checksums.txt: build it with goreleaser release --snapshot --clean --skip=publish"

# GoReleaser writes the version (without v) and the commit to metadata.json;
# without it, the version is read off an archive name.
version=
commit=
if [ -f "$dist/metadata.json" ]; then
	version=$(jq -r '.version // empty' "$dist/metadata.json")
	commit=$(jq -r '.commit // empty' "$dist/metadata.json")
fi
if [ -z "$version" ]; then
	for f in "$dist"/den_*_linux_amd64.tar.gz; do
		[ -f "$f" ] || die "$dist has neither metadata.json nor a linux/amd64 archive to read the version from"
		version=${f##*/den_}
		version=${version%_linux_amd64.tar.gz}
	done
fi
version=v${version#v}

mkdir -p "$mirror/$version"
n=0
for f in "$dist"/den_*.tar.gz "$dist"/den_*.zip; do
	[ -f "$f" ] || continue
	cp "$f" "$mirror/$version/"
	n=$((n + 1))
done
[ "$n" -gt 0 ] || die "$dist has no den archives"
cp "$dist/checksums.txt" "$mirror/$version/checksums.txt"
cp "$here/install.sh" "$mirror/install.sh"

notes="Snapshot build${commit:+ of $commit}: not a release, for trying a change before it is merged."
date=$(date -u +%Y-%m-%dT%H:%M:%SZ)
jq -n --arg v "$version" --arg n "$notes" '{version: $v, notes: $n}' >"$mirror/latest.json"
jq -n --arg v "$version" --arg n "$notes" --arg d "$date" \
	'{schema: 1, releases: [{version: $v, date: $d, prerelease: true, notes: $n}]}' >"$mirror/releases.json"
echo "$mirror: den $version, $n archives"
