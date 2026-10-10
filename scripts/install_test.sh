#!/bin/sh
# install_test.sh — tests of install.sh against a directory laid out like the
# release bucket, with a fake `aws`, `uname` and `sysctl` on PATH. The archives
# hold a stub `den` that prints a version. Run with `make script-test`.
set -eu

script=$(cd "$(dirname "$0")" && pwd)/install.sh
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

bucket=$work/bucket
fakebin=$work/fakebin
awslog=$work/aws.log
mkdir -p "$bucket" "$fakebin"

# The developer's AWS and shell settings must not leak in.
unset AWS_PROFILE DEN_VERSION DEN_INSTALL_DIR DEN_INSTALL_BASE DEN_AWS_REGION
HOME=$work/home
mkdir -p "$HOME"
SHELL=/bin/zsh
export HOME SHELL

# aws s3 cp s3://acme-releases/den/<key> <dest|-> [flags]: copies from the bucket
# directory, logs its arguments, and fails when FAKE_AWS_FAIL is set.
cat >"$fakebin/aws" <<EOF
#!/bin/sh
echo "\$*" >>"$awslog"
[ -z "\${FAKE_AWS_FAIL:-}" ] || { echo "An error occurred (ExpiredToken)" >&2; exit 1; }
[ "\$1 \$2" = "s3 cp" ] || exit 2
key=\${3#s3://acme-releases/den/}
[ -f "$bucket/\$key" ] || { echo "download failed: Not Found" >&2; exit 1; }
if [ "\$4" = - ]; then cat "$bucket/\$key"; else cp "$bucket/\$key" "\$4"; fi
EOF
# uname and sysctl answer from FAKE_UNAME_S / FAKE_UNAME_M / FAKE_ROSETTA when
# set, and are the real ones otherwise.
cat >"$fakebin/uname" <<'EOF'
#!/bin/sh
case ${1:-} in
-s) [ -z "${FAKE_UNAME_S:-}" ] || { echo "$FAKE_UNAME_S"; exit 0; } ;;
-m) [ -z "${FAKE_UNAME_M:-}" ] || { echo "$FAKE_UNAME_M"; exit 0; } ;;
esac
exec /usr/bin/uname "$@"
EOF
cat >"$fakebin/sysctl" <<'EOF'
#!/bin/sh
echo "${FAKE_ROSETTA:-0}"
EOF
chmod +x "$fakebin/aws" "$fakebin/uname" "$fakebin/sysctl"
[ -x /usr/bin/uname ] || sed -i.bak 's|/usr/bin/uname|/bin/uname|' "$fakebin/uname"
# curl answers the GitHub Releases URLs from the bucket directory and logs them;
# any other URL goes to the real curl.
curllog=$work/curl.log
realcurl=$(command -v curl || true)
cat >"$fakebin/curl" <<EOF
#!/bin/sh
for url in "\$@"; do :; done
case \$url in
https://github.com/lukaszgard/den/releases/*)
	echo "\$url" >>"$curllog"
	case \$url in
	*/latest/download/*) key=\${url##*/latest/download/} ;;
	*) key=\${url##*/releases/download/} ;;
	esac
	[ -f "$bucket/\$key" ] || exit 22
	if [ "\$2" = -o ]; then cp "$bucket/\$key" "\$3"; else cat "$bucket/\$key"; fi
	;;
*) exec "${realcurl:-false}" "\$@" ;;
esac
EOF
chmod +x "$fakebin/curl"
PATH=$fakebin:$PATH
export PATH
# The bucket tests read a mirror; the GitHub ones clear this.
export DEN_INSTALL_BASE=s3://acme-releases/den

failed=0
fail() {
	printf 'FAIL %s\n' "$*" >&2
	failed=$((failed + 1))
}

sha() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d ' ' -f 1; else shasum -a 256 "$1" | cut -d ' ' -f 1; fi
}

# release VERSION: <bucket>/vVERSION with an archive per OS/CPU whose den prints
# "den version VERSION <os>_<arch>", plus checksums.txt.
release() {
	rel=$bucket/v$1
	mkdir -p "$rel"
	: >"$rel/checksums.txt"
	for target in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
		stage=$(mktemp -d "$work/s.XXXXXX")
		printf '#!/bin/sh\necho "den version %s %s"\n' "$1" "$target" >"$stage/den"
		chmod +x "$stage/den"
		echo readme >"$stage/README.md"
		archive=den_${1}_${target}.tar.gz
		tar -czf "$rel/$archive" -C "$stage" den README.md
		printf '%s  %s\n' "$(sha "$rel/$archive")" "$archive" >>"$rel/checksums.txt"
	done
}

# latest VERSION: latest.json names vVERSION.
latest() {
	printf '{"version":"v%s","notes":"a\\nb"}\n' "$1" >"$bucket/latest.json"
}

# run [VAR=value…] ARGS…: runs install.sh with those variables set for it alone
# (a prefix assignment before a function call would outlive the call); leaves
# its output in $out and its status in $rc.
run() {
	: >"$awslog"
	out=$(
		while [ $# -gt 0 ]; do
			case $1 in
			([A-Z]*=*) export "${1?}"; shift ;;
			(*) break ;;
			esac
		done
		sh "$script" "$@" 2>&1
	) && rc=0 || rc=$?
}

# expect_out NAME PATTERN: $out matches the case pattern.
expect_out() {
	# shellcheck disable=SC2254
	case $out in
	$2) ;;
	*) fail "$1: output lacks '$2':
$out" ;;
	esac
}

expect_no_out() {
	# shellcheck disable=SC2254
	case $out in
	$2) fail "$1: output has '$2':
$out" ;;
	esac
}

expect_ok() { [ "$rc" -eq 0 ] || fail "$1: exit $rc, want 0:
$out"; }
expect_fail() { [ "$rc" -ne 0 ] || fail "$1: exit 0, want a failure:
$out"; }

release 0.1.0
release 0.2.0
latest 0.2.0

export FAKE_UNAME_S=Darwin FAKE_UNAME_M=arm64

# --- latest -------------------------------------------------------------

d=$work/t1/bin
run --dir "$d"
expect_ok latest
expect_out latest '*Checksum ok*'
expect_out latest "*Installed $d/den*"
[ "$("$d/den")" = "den version 0.2.0 darwin_arm64" ] || fail "latest: installed '$("$d/den")'"
[ ! -e "$d/.den.new" ] || fail "latest: temporary file left behind"
[ ! -e "$d/README.md" ] || fail "latest: extracted more than den"
expect_out latest '*den doctor*den init --profile <aws-profile>*'

# --- --version, with and without the v ----------------------------------

d=$work/t2/bin
run --version v0.1.0 --dir "$d"
expect_ok '--version'
[ "$("$d/den")" = "den version 0.1.0 darwin_arm64" ] || fail "--version: installed '$("$d/den")'"
run --version=0.1.0 --dir "$d"
expect_ok '--version=0.1.0'
run DEN_VERSION=0.1.0 --dir "$work/t2b"
expect_ok 'DEN_VERSION'
[ "$("$work/t2b/den")" = "den version 0.1.0 darwin_arm64" ] || fail "DEN_VERSION: installed '$("$work/t2b/den")'"
if grep -q latest.json "$awslog"; then fail "--version: read latest.json"; fi

# --- --dir, DEN_INSTALL_DIR and the default -----------------------------

run DEN_INSTALL_DIR="$work/t3/env/bin"
expect_ok DEN_INSTALL_DIR
[ -x "$work/t3/env/bin/den" ] || fail "DEN_INSTALL_DIR: not installed"
run
expect_ok 'default dir'
[ -x "$HOME/.local/bin/den" ] || fail "default dir: not installed in ~/.local/bin"

# --- an existing den is replaced ----------------------------------------

d=$work/t4/bin
mkdir -p "$d"
echo old >"$d/den"
run --dir "$d"
expect_ok replace
[ "$("$d/den")" = "den version 0.2.0 darwin_arm64" ] || fail "replace: still the old den"

# --- the OS and the CPU pick the archive --------------------------------

d=$work/t5
run FAKE_UNAME_S=Linux FAKE_UNAME_M=x86_64 --dir "$d/a"
expect_ok linux/amd64
[ "$("$d/a/den")" = "den version 0.2.0 linux_amd64" ] || fail "linux/x86_64: got '$("$d/a/den")'"
run FAKE_UNAME_S=Linux FAKE_UNAME_M=aarch64 --dir "$d/b"
[ "$("$d/b/den")" = "den version 0.2.0 linux_arm64" ] || fail "linux/aarch64: got '$("$d/b/den")'"
run FAKE_UNAME_S=Darwin FAKE_UNAME_M=x86_64 --dir "$d/c"
[ "$("$d/c/den")" = "den version 0.2.0 darwin_amd64" ] || fail "darwin/x86_64: got '$("$d/c/den")'"
run FAKE_UNAME_S=Darwin FAKE_UNAME_M=x86_64 FAKE_ROSETTA=1 --dir "$d/d"
[ "$("$d/d/den")" = "den version 0.2.0 darwin_arm64" ] || fail "rosetta: got '$("$d/d/den")'"

# --- unsupported platforms ----------------------------------------------

d=$work/t6
run FAKE_UNAME_S=FreeBSD --dir "$d"
expect_fail 'unsupported OS'
expect_out 'unsupported OS' '*unsupported OS FreeBSD*'
run FAKE_UNAME_S=Linux FAKE_UNAME_M=riscv64 --dir "$d"
expect_fail 'unsupported CPU'
expect_out 'unsupported CPU' '*unsupported CPU riscv64*'
run FAKE_UNAME_S=MINGW64_NT-10.0 --dir "$d"
expect_fail 'Windows shell'
expect_out 'Windows shell' '*install.ps1*'
[ ! -e "$d" ] || fail "unsupported: created $d"

# --- a checksum mismatch installs nothing -------------------------------

release 0.3.0
latest 0.3.0
echo tampered >>"$bucket/v0.3.0/den_0.3.0_darwin_arm64.tar.gz"
d=$work/t7/bin
run --dir "$d"
expect_fail mismatch
expect_out mismatch '*checksum mismatch*'
if [ -e "$d/den" ] || [ -e "$d/.den.new" ]; then fail "mismatch: left a file in $d"; fi
mkdir -p "$d"
echo old >"$d/den"
run --dir "$d"
[ "$(cat "$d/den")" = old ] || fail "mismatch: replaced the installed den"

# --- an archive missing from checksums.txt ------------------------------

release 0.4.0
grep -v darwin_arm64 "$bucket/v0.4.0/checksums.txt" >"$work/sums"
mv "$work/sums" "$bucket/v0.4.0/checksums.txt"
d=$work/t8/bin
run --version v0.4.0 --dir "$d"
expect_fail 'not listed'
expect_out 'not listed' '*does not list den_0.4.0_darwin_arm64.tar.gz*'
[ ! -e "$d/den" ] || fail "not listed: installed anyway"

# --- the PATH hint ------------------------------------------------------

latest 0.2.0
d=$work/t9/bin
run --dir "$d"
expect_out 'off PATH' "*$d is not on your PATH*"
expect_out 'off PATH' "*export PATH=\"$d:\$PATH\"*$HOME/.zshrc*"
run SHELL=/usr/local/bin/fish --dir "$d"
expect_out 'fish' "*fish_add_path $d*"
run PATH="$d:$PATH" --dir "$d"
expect_ok 'on PATH'
expect_no_out 'on PATH' '*not on your PATH*'
expect_no_out 'on PATH' '*shadows*'

# another den earlier on PATH shadows the new one
other=$work/t9/other
mkdir -p "$other"
printf '#!/bin/sh\necho other\n' >"$other/den"
chmod +x "$other/den"
run PATH="$other:$d:$PATH" --dir "$d"
expect_out shadow "*$other/den comes first on your PATH and shadows*"

# --- file:// through curl -----------------------------------------------

if command -v curl >/dev/null 2>&1; then
	d=$work/t10/bin
	run DEN_INSTALL_BASE="file://$bucket" --dir "$d"
	expect_ok curl
	[ "$("$d/den")" = "den version 0.2.0 darwin_arm64" ] || fail "curl: installed '$("$d/den")'"
	[ ! -s "$awslog" ] || fail "curl: called aws: $(cat "$awslog")"
else
	printf 'skip file:// test (no curl)\n'
fi

# --- GitHub Releases are the default -------------------------------------

d=$work/t12/bin
: >"$curllog"
run DEN_INSTALL_BASE= --dir "$d"
expect_ok github
expect_out github '*from https://github.com/lukaszgard/den/releases*'
[ "$("$d/den")" = "den version 0.2.0 darwin_arm64" ] || fail "github: installed '$("$d/den")'"
[ ! -s "$awslog" ] || fail "github: called aws: $(cat "$awslog")"
grep -q 'releases/latest/download/latest.json' "$curllog" || fail "github: latest.json not read from latest/download: $(cat "$curllog")"
grep -q 'releases/download/v0.2.0/den_0.2.0_darwin_arm64.tar.gz' "$curllog" || fail "github: archive not read from download/<tag>: $(cat "$curllog")"
grep -q 'releases/download/v0.2.0/checksums.txt' "$curllog" || fail "github: checksums.txt not read from download/<tag>"
: >"$curllog"
run DEN_INSTALL_BASE= --version 0.1.0 --dir "$d"
expect_ok 'github --version'
if grep -q latest.json "$curllog"; then fail "github --version: read latest.json"; fi

# --- aws -----------------------------------------------------------------

d=$work/t11/bin
run FAKE_AWS_FAIL=1 --dir "$d" --profile acme-dev
expect_fail 'aws failure'
expect_out 'aws failure' '*aws sso login --profile acme-dev*'
expect_out 'aws failure' '*s3:GetObject*'
[ ! -e "$d/den" ] || fail "aws failure: installed anyway"

run --dir "$d" --profile acme-dev
expect_ok profile
grep -q -- '--profile acme-dev' "$awslog" || fail "profile: aws got no --profile: $(cat "$awslog")"
grep -q -- '--region eu-central-1' "$awslog" || fail "profile: aws got no default region"
run AWS_PROFILE=from-env DEN_AWS_REGION=eu-west-1 --dir "$d"
grep -q -- '--profile from-env' "$awslog" || fail "AWS_PROFILE: aws got no --profile: $(cat "$awslog")"
grep -q -- '--region eu-west-1' "$awslog" || fail "DEN_AWS_REGION: aws got another region"
expect_out 'profile in next steps' '*den init --profile from-env*'

# --- options ------------------------------------------------------------

run --bogus
expect_fail 'unknown option'
expect_out 'unknown option' '*unknown option --bogus*'
run --version
expect_fail 'missing value'
run -h
expect_ok help
expect_out help '*--version vX.Y.Z*'

if [ "$failed" -ne 0 ]; then
	printf '%s install.sh test(s) failed\n' "$failed" >&2
	exit 1
fi
echo "install.sh tests passed"
