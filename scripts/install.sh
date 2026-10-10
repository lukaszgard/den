#!/bin/sh
# install.sh — install den (Developer Environment Navigator) with one command.
#
#   curl -fsSL https://github.com/lukaszgard/den/releases/latest/download/install.sh | sh
#   curl -fsSL https://github.com/lukaszgard/den/releases/latest/download/install.sh | sh -s -- --version v1.0.0 --dir /usr/local/bin
#
# It finds the latest release (or --version), downloads the archive for this
# OS and CPU, checks it against the release's checksums.txt and puts `den` in
# a directory of your choice (default ~/.local/bin). Releases are read from
# GitHub Releases, or from a mirror laid out like one when DEN_INSTALL_BASE is
# set (latest.json and <version>/<files>): an s3:// location through the `aws`
# CLI, anything else (https://, file://) through curl.
#
# Options (each has an environment twin):
#   --version vX.Y.Z   release to install          DEN_VERSION      (default: latest)
#   --dir DIR          where `den` goes            DEN_INSTALL_DIR  (default: ~/.local/bin)
#   --profile NAME     AWS profile for s3 reads    AWS_PROFILE
#   -h, --help         this text
# Other environment: DEN_INSTALL_BASE (default: GitHub Releases),
# DEN_AWS_REGION (default eu-central-1).
#
# Everything sits in functions and main runs on the last line: a download cut
# off halfway runs nothing.
set -eu

# usage does not read $0: piped into sh it is just "sh".
usage() {
	cat <<'EOT'
usage: install.sh [--version vX.Y.Z] [--dir DIR] [--profile NAME]

  --version vX.Y.Z   release to install       DEN_VERSION      (default: latest)
  --dir DIR          where den goes           DEN_INSTALL_DIR  (default: ~/.local/bin)
  --profile NAME     AWS profile for s3 reads AWS_PROFILE

Other environment: DEN_INSTALL_BASE (default: GitHub Releases; a mirror laid
out as latest.json and <version>/<files>, s3:// or https://),
DEN_AWS_REGION (default eu-central-1).
EOT
}

say() { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

# detect_os prints darwin or linux.
detect_os() {
	case $(uname -s) in
	Darwin) echo darwin ;;
	Linux) echo linux ;;
	MINGW* | MSYS* | CYGWIN*)
		die "this is Windows: use install.ps1 instead (irm https://github.com/lukaszgard/den/releases/latest/download/install.ps1 | iex)"
		;;
	*) die "unsupported OS $(uname -s): den releases cover macOS, Linux and Windows" ;;
	esac
}

# detect_arch prints amd64 or arm64. Under Rosetta uname reports x86_64 on an
# Apple silicon Mac: the native arm64 build is the right one there.
detect_arch() {
	case $(uname -m) in
	x86_64 | amd64)
		if [ "$1" = darwin ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
			echo arm64
		else
			echo amd64
		fi
		;;
	arm64 | aarch64) echo arm64 ;;
	*) die "unsupported CPU $(uname -m): den releases cover amd64 and arm64" ;;
	esac
}

# asset_url SRC prints where SRC, a path under the base, is. GitHub Releases
# keep the newest release's files under latest/download and the others under
# download/<tag>; a mirror keeps both as plain paths.
asset_url() {
	if [ -n "$github" ]; then
		case $1 in
		latest.json) echo "$base/latest/download/latest.json" ;;
		*) echo "$base/download/$1" ;;
		esac
	else
		echo "$base/$1"
	fi
}

# fetch SRC DEST: SRC is a path under the base. DEST may be - for stdout.
fetch() {
	case $base in
	s3://*)
		set -- "$base/$1" "$2"
		if [ -n "$profile" ]; then
			aws s3 cp "$1" "$2" --no-progress --only-show-errors --region "$region" --profile "$profile" </dev/null
		else
			aws s3 cp "$1" "$2" --no-progress --only-show-errors --region "$region" </dev/null
		fi
		;;
	*)
		if [ "$2" = - ]; then
			curl -fsSL "$(asset_url "$1")"
		else
			curl -fsSL -o "$2" "$(asset_url "$1")"
		fi
		;;
	esac
}

# fetch_or_explain SRC DEST: fetch, and on failure say what to try.
fetch_or_explain() {
	if ! fetch "$1" "$2"; then
		case $base in
		s3://*)
			say "" >&2
			say "Could not read $base/$1." >&2
			say "  - Is the SSO session alive?   aws sso login${profile:+ --profile $profile}" >&2
			say "  - Does the profile have s3:GetObject on ${base#s3://}/*?" >&2
			say "  - Pick a profile with --profile NAME or AWS_PROFILE." >&2
			;;
		*)
			say "" >&2
			say "Could not download $(asset_url "$1")." >&2
			;;
		esac
		exit 1
	fi
}

# latest_version prints the tag in latest.json. sed, not jq: jq is not a den
# prerequisite.
latest_version() {
	fetch_or_explain latest.json - |
		sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
}

# sha256_of FILE prints the file's SHA-256.
sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	else
		die "neither sha256sum nor shasum found: cannot verify the download"
	fi
}

# verify ARCHIVE CHECKSUMS NAME: ARCHIVE's SHA-256 must be the one CHECKSUMS lists for NAME.
verify() {
	want=$(awk -v n="$3" '$2 == n || $2 == "*" n { print $1; exit }' "$2")
	[ -n "$want" ] || die "checksums.txt does not list $3: nothing installed"
	got=$(sha256_of "$1")
	[ "$got" = "$want" ] || die "checksum mismatch for $3 (got $got, want $want): nothing installed"
}

# path_hint DIR prints the command that puts DIR on PATH for the user's shell.
path_hint() {
	case ${SHELL:-} in
	*/zsh) rc=$HOME/.zshrc ;;
	*/bash) if [ "$(uname -s)" = Darwin ]; then rc=$HOME/.bash_profile; else rc=$HOME/.bashrc; fi ;;
	*/fish)
		echo "  fish_add_path $1"
		return
		;;
	*) rc=$HOME/.profile ;;
	esac
	echo "  echo 'export PATH=\"$1:\$PATH\"' >> $rc"
}

on_path() {
	case ":$PATH:" in
	*":$1:"*) return 0 ;;
	esac
	return 1
}

main() {
	version=${DEN_VERSION:-}
	dir=${DEN_INSTALL_DIR:-}
	profile=${AWS_PROFILE:-}
	github=
	base=${DEN_INSTALL_BASE:-}
	if [ -z "$base" ]; then
		github=1
		base=https://github.com/lukaszgard/den/releases
	fi
	region=${DEN_AWS_REGION:-eu-central-1}
	base=${base%/}

	while [ $# -gt 0 ]; do
		case $1 in
		--version) [ $# -ge 2 ] || die "--version needs a value"; version=$2; shift 2 ;;
		--dir) [ $# -ge 2 ] || die "--dir needs a value"; dir=$2; shift 2 ;;
		--profile) [ $# -ge 2 ] || die "--profile needs a value"; profile=$2; shift 2 ;;
		--version=*) version=${1#*=}; shift ;;
		--dir=*) dir=${1#*=}; shift ;;
		--profile=*) profile=${1#*=}; shift ;;
		-h | --help) usage; exit 0 ;;
		*) die "unknown option $1 (see --help)" ;;
		esac
	done
	[ -n "$dir" ] || dir=$HOME/.local/bin

	os=$(detect_os)
	arch=$(detect_arch "$os")

	case $base in
	s3://*) command -v aws >/dev/null 2>&1 || die "the aws CLI (v2) is needed to read $base: https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html" ;;
	*) command -v curl >/dev/null 2>&1 || die "curl is needed to download from $base" ;;
	esac

	if [ -z "$version" ]; then
		version=$(latest_version)
		[ -n "$version" ] || die "latest.json has no version"
	fi
	case $version in v*) ;; *) version=v$version ;; esac

	# GoReleaser strips the tag's leading v from archive names (update.ArchiveName).
	name=den_${version#v}_${os}_${arch}.tar.gz

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT INT TERM

	say "Installing den $version ($os/$arch) from $base"
	fetch_or_explain "$version/$name" "$tmp/$name"
	fetch_or_explain "$version/checksums.txt" "$tmp/checksums.txt"
	verify "$tmp/$name" "$tmp/checksums.txt" "$name"
	say "Checksum ok"

	mkdir -p "$tmp/x"
	tar -xzf "$tmp/$name" -C "$tmp/x" den || die "$name has no den binary"

	mkdir -p "$dir"
	# Copy next to the target, then rename over it: a running den keeps its old
	# inode and the replacement is atomic.
	cp "$tmp/x/den" "$dir/.den.new"
	chmod 755 "$dir/.den.new"
	mv -f "$dir/.den.new" "$dir/den"

	say "Installed $dir/den"
	"$dir/den" --version || warn "could not run $dir/den --version"

	if ! on_path "$dir"; then
		say ""
		say "$dir is not on your PATH. Add it:"
		path_hint "$dir"
		say "and open a new terminal."
	else
		found=$(command -v den 2>/dev/null || true)
		if [ -n "$found" ] && [ "$found" != "$dir/den" ]; then
			warn "$found comes first on your PATH and shadows the den just installed ($dir/den)"
		fi
	fi

	say ""
	say "Next:"
	say "  den doctor                         check the tools den needs"
	say "  den init --profile ${profile:-<aws-profile>}    write a den.yaml from your AWS account"
}

main "$@"
