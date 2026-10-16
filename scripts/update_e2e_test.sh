#!/bin/sh
# update_e2e_test.sh — the updater end to end, on real den binaries: three
# versions built from this checkout, laid out by snapshot-mirror.sh in a fake
# S3 bucket read through a fake `aws`, and a den that upgrades, downgrades,
# skips, pins and refuses a tampered archive, checked by its own --version.
# Run with `make script-test`; needs go and jq.
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

failed=0
fail() {
	printf 'FAIL %s\n' "$*" >&2
	failed=$((failed + 1))
}

# The developer's AWS, den state and GitHub token must not leak in.
unset AWS_PROFILE AWS_REGION GITHUB_TOKEN DEN_INSTALL_BASE DEN_VERSION
HOME=$work/home
XDG_STATE_HOME=$work/state
XDG_CONFIG_HOME=$work/config
mkdir -p "$HOME"
export HOME XDG_STATE_HOME XDG_CONFIG_HOME

os=$(cd "$root" && go env GOOS)
arch=$(cd "$root" && go env GOARCH)
bucket=$work/s3/acme-releases
mirror=$bucket/den

# --- three releases --------------------------------------------------------

for v in 0.0.1 0.0.2 0.0.3; do
	dist=$work/dist-$v
	mkdir -p "$dist/pkg"
	(cd "$root" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$v" -o "$dist/pkg/den" ./cmd/den)
	name=den_${v}_${os}_${arch}.tar.gz
	tar -czf "$dist/$name" -C "$dist/pkg" den
	if command -v sha256sum >/dev/null 2>&1; then
		(cd "$dist" && sha256sum "$name" >checksums.txt)
	else
		(cd "$dist" && shasum -a 256 "$name" >checksums.txt)
	fi
	printf '{"version":"%s","commit":"e2e"}' "$v" >"$dist/metadata.json"
	sh "$root/scripts/snapshot-mirror.sh" "$dist" "$mirror" >/dev/null
done
[ "$(jq -r .version "$mirror/latest.json")" = v0.0.3 ] || fail "latest.json: $(cat "$mirror/latest.json")"
for f in "v0.0.2/den_0.0.2_${os}_${arch}.tar.gz" v0.0.2/checksums.txt install.sh releases.json; do
	[ -f "$mirror/$f" ] || fail "snapshot-mirror.sh did not write $f"
done

# index YANKED: releases.json for the three, v0.0.3 newest, YANKED a jq object
# of version → reason.
index() {
	jq -n --argjson y "$1" '{schema: 1, releases: [
		{version: "v0.0.3", date: "2026-10-03T10:00:00Z", prerelease: false, notes: "three"},
		{version: "v0.0.2", date: "2026-10-02T10:00:00Z", prerelease: false, notes: "two"},
		{version: "v0.0.1", date: "2026-10-01T10:00:00Z", prerelease: false, notes: "one"}]
		| map(. + (if $y[.version] then {yanked: $y[.version]} else {} end))}' >"$mirror/releases.json"
}
index '{}'

# --- a fake aws over the bucket directory --------------------------------------

mkdir -p "$work/bin"
cat >"$work/bin/aws" <<FAKE
#!/bin/sh
root="$work/s3"
case "\$1 \$2" in
"s3 cp")
	src="\$root/\${3#s3://}"
	[ -f "\$src" ] || { echo "fatal error: An error occurred (404) when calling the HeadObject operation: Key does not exist" >&2; exit 1; }
	if [ "\$4" = - ]; then cat "\$src"; else cp "\$src" "\$4"; fi
	;;
"s3api head-object")
	shift 2
	while [ \$# -gt 0 ]; do
		case "\$1" in
		--bucket) b=\$2; shift ;;
		--key) k=\$2; shift ;;
		esac
		shift
	done
	[ -f "\$root/\$b/\$k" ] || { echo "An error occurred (404) when calling the HeadObject operation: Not Found" >&2; exit 254; }
	wc -c <"\$root/\$b/\$k" | tr -d ' '
	;;
*) echo "unexpected: \$*" >&2; exit 2 ;;
esac
FAKE
chmod +x "$work/bin/aws"
PATH=$work/bin:$PATH
export PATH

cfg=$work/den.yaml
config() { # config [PIN]
	printf 'updates:\n  s3_bucket: acme-releases\n  s3_prefix: den/\n' >"$cfg"
	[ -z "${1:-}" ] || printf '  pin: %s\n' "$1" >>"$cfg"
}
config

# The den under test, installed where a person would have it.
den=$work/install/den
mkdir -p "$work/install"
cp "$work/dist-0.0.1/pkg/den" "$den"

# runs NAME WANT ARGS...: den ARGS succeeds and prints WANT.
runs() {
	name=$1
	want=$2
	shift 2
	if ! out=$("$den" -c "$cfg" "$@" 2>&1); then
		fail "$name: den $* failed: $out"
		return 0
	fi
	case $out in
	*"$want"*) ;;
	*) fail "$name: den $* did not print '$want': $out" ;;
	esac
}

# is VERSION: the installed den reports VERSION.
is() {
	got=$("$den" --version)
	case $got in
	"den $1 "*) ;;
	*) fail "installed den is '$got', want $1" ;;
	esac
}

# --- upgrade -------------------------------------------------------------------

runs 'the check offers the newest' 'den v0.0.3 is available (you run 0.0.1)' update
runs 'the list' 'v0.0.1   2026-10-01  current' update list
runs 'install the offer' 'Installed v0.0.3' update install --yes
is 0.0.3
runs 'up to date' 'den 0.0.3 is up to date' update

# --- downgrade, skipping the version left --------------------------------------

runs 'downgrade' 'v0.0.3 is skipped in update checks' update install v0.0.2 --yes --skip-current
is 0.0.2
jq -e '.skipped == ["v0.0.3"]' "$XDG_STATE_HOME/den/updates.json" >/dev/null || fail "skip list: $(cat "$XDG_STATE_HOME/den/updates.json")"
runs 'a skipped release is not offered' 'v0.0.3 is skipped on this machine' update
runs 'and nothing is installed' 'nothing to install' update install --yes
is 0.0.2

# --- pin -------------------------------------------------------------------------

config v0.0.1
runs 'a pin offers an older release' 'updates.pin holds den on v0.0.1' update
runs 'and installs it' 'Installed v0.0.1' update install --yes
is 0.0.1
runs 'held by the pin' 'pinned to v0.0.1 by updates.pin' update
config

# --- yanked ----------------------------------------------------------------------

runs 'unskip' 'v0.0.3 is no longer skipped' update unskip v0.0.3
index '{"v0.0.3": "eats tunnels"}'
runs 'a yanked release is not offered' 'v0.0.3 is yanked: eats tunnels' update
runs 'the next one down is' 'den v0.0.2 is available' update
runs 'the list says why' 'yanked: eats tunnels' update list
index '{}'

# --- a tampered archive ----------------------------------------------------------

tar -czf "$mirror/v0.0.3/den_0.0.3_${os}_${arch}.tar.gz" -C "$work/dist-0.0.1/pkg" den
if out=$("$den" -c "$cfg" update install v0.0.3 --yes 2>&1); then
	fail "a tampered archive installed: $out"
fi
case $out in
*'checksum mismatch'*) ;;
*) fail "a tampered archive did not fail on its checksum: $out" ;;
esac
is 0.0.1
left=$(find "$work/install" -name 'den-*' | head -n1)
[ -z "$left" ] || fail "temp files left next to den: $left"

# --- install.sh from the mirror, as from a pull request's snapshot -----------------

if out=$(DEN_INSTALL_BASE="file://$mirror" sh "$mirror/install.sh" --version v0.0.2 --dir "$work/other" 2>&1); then
	case $("$work/other/den" --version) in
	"den 0.0.2 "*) ;;
	*) fail "install.sh from the mirror installed $("$work/other/den" --version)" ;;
	esac
else
	fail "install.sh from the mirror failed: $out"
fi

if [ "$failed" -gt 0 ]; then
	printf '%s script test(s) failed\n' "$failed" >&2
	exit 1
fi
echo "update e2e: ok"
