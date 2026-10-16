#!/bin/sh
# release.sh — the scriptable steps of a den release; .claude/skills/release has
# the whole procedure. Writing the CHANGELOG.md entry and merging the release PR
# stay with a person, so a release is three calls with those two in between:
#
#   release.sh next    [-q] [BASE]  version this branch's commits call for (BASE=origin/main)
#   release.sh check   [BASE]       warn when CHANGELOG.md is below that version (CI; exits 0)
#   release.sh draft   X.Y.Z [REF]  git-cliff draft of the entry, last tag..REF
#   release.sh prepare X.Y.Z        check this branch can ship as X.Y.Z
#   release.sh tag     X.Y.Z        tag origin/main as vX.Y.Z and push the tag
#   release.sh tag     X.Y.Z-rc.N   tag the release PR's branch as a release candidate
#
# Only `tag` changes anything outside this checkout, and it asks first.
set -eu

die() {
	printf 'release: %s\n' "$*" >&2
	exit 1
}

step() { printf '\n==> %s\n' "$*"; }

usage() {
	sed -n '2,13s/^# \{0,1\}//p' "$0" >&2
	exit 2
}

# The header the workflow cuts the notes at: no "v", an ISO date.
check_version() {
	expr "$1" : '[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*$' >/dev/null ||
		die "VERSION must look like 1.2.3 (no v), got '$1'"
}

# A tag may also be a release candidate, X.Y.Z-rc.N: a GitHub prerelease, offered
# only on updates.channel: prerelease, its notes X.Y.Z's entry.
check_tag_version() {
	printf '%s\n' "$1" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$' ||
		die "VERSION must look like 1.2.3 or 1.2.3-rc.1 (no v), got '$1'"
}

# The newest release, never a release candidate: an rc of 1.8.0 must not make
# the next version 1.9.0, nor 1.8.0 look released.
last_tag() {
	git tag --list 'v[0-9]*' | grep -v -- - | sort -t. -k1.2,1n -k2,2n -k3,3n | tail -n1
}

# newer A B: A is a higher X.Y.Z than B.
newer() {
	[ "$1" != "$2" ] &&
		[ "$(printf '%s\n%s\n' "$1" "$2" | sort -t. -k1,1n -k2,2n -k3,3n | tail -n1)" = "$1" ]
}

check_new_tag() {
	last=$(last_tag)
	# A release candidate comes before its release: X.Y.Z must not be out yet.
	newer "${1%%-*}" "${last#v}" || die "$1 is not newer than the last tag, $last"
	git rev-parse -q --verify "refs/tags/v$1" >/dev/null && die "tag v$1 already exists here"
	if git ls-remote --exit-code --tags origin "refs/tags/v$1" >/dev/null 2>&1; then
		die "tag v$1 already exists on origin"
	fi
	echo "$last -> v$1"
}

# changelog_version REF prints the version of the first "## [X.Y.Z]" header of
# CHANGELOG.md at REF, empty when there is none.
changelog_version() {
	git show "$1:CHANGELOG.md" 2>/dev/null | grep -m1 '^## \[v\{0,1\}[0-9]' |
		sed -E 's/^## \[v?([0-9]+\.[0-9]+\.[0-9]+)\].*/\1/' | grep -E '^[0-9]+\.[0-9]+\.[0-9]+$' || true
}

# base_version BASE prints the version already released or about to be: the
# higher of the newest v* tag and the top CHANGELOG.md entry on BASE (a merged
# release PR is not tagged yet).
base_version() {
	tag=$(last_tag)
	tag=${tag#v}
	cl=$(changelog_version "$1")
	if [ -z "$tag" ] && [ -z "$cl" ]; then
		echo 0.0.0
	elif [ -z "$tag" ]; then
		echo "$cl"
	elif [ -z "$cl" ] || newer "$tag" "$cl"; then
		echo "$tag"
	else
		echo "$cl"
	fi
}

# bump_kind BASE reads the commits BASE..HEAD (merges skipped) and prints "KIND FEAT FIX BREAKING": KIND is major, minor,
# patch or none. fix counts perf too.
bump_kind() {
	git log --no-merges --format='%s%n%b%n--' "$1..HEAD" | awk '
		function finish() {
			kind = "none"
			if (breaking > 0) kind = "major"
			else if (feat > 0) kind = "minor"
			else if (fix > 0) kind = "patch"
			print kind, feat + 0, fix + 0, breaking + 0
		}
		BEGIN { subject = 1 }
		$0 == "--" { subject = 1; next }
		subject {
			subject = 0
			line = $0
			if (match(line, /^[a-z]+(\([^)]*\))?!?:/)) {
				head = substr(line, 1, RLENGTH - 1)
				bang = 0
				if (substr(head, length(head)) == "!") { bang = 1; head = substr(head, 1, length(head) - 1) }
				sub(/\(.*$/, "", head)
				if (bang) breaking++
				if (head == "feat") feat++
				else if (head == "fix" || head == "perf") fix++
			}
			next
		}
		/^BREAKING[ -]CHANGE:/ { breaking++ }
		END { finish() }
	'
}

# bump VERSION KIND prints the next version. On 0.x a breaking change stays in
# the minor number: 1.0.0 is its own decision.
bump() {
	major=${1%%.*}
	rest=${1#*.}
	minor=${rest%%.*}
	patch=${rest#*.}
	case $2 in
	major) if [ "$major" -ge 1 ]; then echo "$((major + 1)).0.0"; else echo "0.$((minor + 1)).0"; fi ;;
	minor) echo "$major.$((minor + 1)).0" ;;
	patch) echo "$major.$minor.$((patch + 1))" ;;
	*) echo "" ;;
	esac
}

# plan BASE sets what the commits since BASE call for: base, base_src, kind,
# n_feat, n_fix, n_break and next (empty when nothing needs a release).
plan() {
	git rev-parse -q --verify "$1^{commit}" >/dev/null || die "base ref '$1' not found (git fetch origin?)"
	base=$(base_version "$1")
	if [ "$(changelog_version "$1")" = "$base" ]; then
		base_src="CHANGELOG.md on $1"
	else
		base_src="tag v$base"
	fi
	read -r kind n_feat n_fix n_break <<EOF
$(bump_kind "$1")
EOF
	next=$(bump "$base" "$kind")
	summary="bump $kind"
	[ "$kind" != none ] && summary="$summary — $n_feat feat, $n_fix fix, $n_break breaking"
	return 0
}

# warn MESSAGE: a plain line, and a workflow annotation under GitHub Actions.
warn() {
	printf 'warning: %s\n' "$*"
	[ -z "${GITHUB_ACTIONS:-}" ] || printf '::warning::%s\n' "$*"
}

# below_expected VERSION: warn and return 0 when VERSION is lower than plan's next.
below_expected() {
	[ -n "$next" ] && newer "$next" "$1" || return 1
	why="the commits since v$base call for $summary"
	[ "$kind" != major ] || [ "${base%%.*}" -ge 1 ] || why="$why (breaking, held to a minor on 0.x)"
	warn "CHANGELOG.md says $1 but $next is expected: $why. Rename the entry to ## [$next] (a higher version is fine, a lower one hides what changed)."
}

cmd_next() {
	quiet=
	if [ "${1:-}" = -q ]; then
		quiet=1
		shift
	fi
	plan "${1:-origin/main}"
	if [ -n "$quiet" ]; then
		echo "$next"
		return 0
	fi
	echo "last v$base ($base_src)"
	echo "$summary"
	if [ -n "$next" ]; then echo "next v$next"; else echo "next none (nothing to release)"; fi
}

cmd_check() {
	ref=${1:-origin/main}
	plan "$ref"
	# A new entry is a first header that differs from the base's.
	fresh=$(changelog_version HEAD)
	[ "$fresh" != "$(changelog_version "$ref")" ] || fresh=
	result=ok
	out=$(
		if [ -z "$fresh" ]; then
			if [ -n "$next" ]; then
				warn "this branch has feat/fix commits ($summary) but CHANGELOG.md has no new entry: add a ## [$next] entry."
			else
				echo "ok: $summary, no release needed"
			fi
		elif below_expected "$fresh"; then
			:
		else
			echo "ok: CHANGELOG.md has $fresh, expected ${next:-no release}"
		fi
	)
	printf '%s\n' "$out"
	case $out in *warning:*) result="warning" ;; esac
	if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
		{
			echo "### Release version"
			echo
			echo "| | |"
			echo "|---|---|"
			echo "| Last release | v$base ($base_src) |"
			echo "| Commits | $summary |"
			echo "| Expected | ${next:+v}${next:-none} |"
			echo "| CHANGELOG.md | ${fresh:-no new entry} |"
			echo "| Result | $result |"
		} >>"$GITHUB_STEP_SUMMARY"
	fi
	return 0
}

# notes VERSION < CHANGELOG.md prints the text the release will publish. Keep
# the awk in step with the "Release notes from CHANGELOG.md" step of
# .github/workflows/release.yaml.
notes() {
	awk -v v="$1" '
		index($0, "## [" v "]") == 1 { on = 1; next }
		on && /^## \[/ { exit }
		on { print }
	'
}

# show_notes NOTES WHY-EMPTY
show_notes() {
	printf '%s' "$1" | grep -q '[^[:space:]]' || die "$2"
	printf '%s\n' "$1" | sed 's/^/  | /'
}

# The repo's web URL, from origin (git@host:owner/repo.git or https://...).
repo_url() {
	git remote get-url origin | sed -E 's#^[^@/]+@([^:]+):#https://\1/#; s#^ssh://[^@]+@#https://#; s#\.git$##'
}

cmd_draft() {
	version=$1
	ref=${2:-HEAD}
	check_version "$version"
	command -v git-cliff >/dev/null || die "git-cliff not found: brew install git-cliff"
	last=$(last_tag)
	# PRs are squash-merged: on main, git-cliff only sees PR titles. Point REF at
	# the feature branch before it is merged to draft from its commits.
	git-cliff "$last..$ref" --tag "v$version" --strip header
	step "Every feat/fix needs a line in the entry ($last..$ref)"
	git log --format='  - %s' "$last..$ref" | grep -E ': (feat|fix)' || echo "  (none)"
	cat <<EOF

Rewrite the draft for users, grouped by area, behaviour changes first, at the
top of CHANGELOG.md under exactly:

  ## [$version] - $(date +%Y-%m-%d)

then: make release-prep VERSION=$version
EOF
}

cmd_prepare() {
	version=$1
	check_version "$version"

	step "Version"
	check_new_tag "$version"
	if git rev-parse -q --verify origin/main >/dev/null; then
		plan origin/main
		below_expected "$version" || true
	fi

	step "CHANGELOG.md"
	header=$(grep -m1 '^## \[' CHANGELOG.md || true)
	if ! printf '%s\n' "$header" | grep -Eq "^## \[$version\] - [0-9]{4}-[0-9]{2}-[0-9]{2}\$"; then
		die "the first section of CHANGELOG.md must be '## [$version] - $(date +%Y-%m-%d)', found '$header'.
Draft it with: make release-draft VERSION=$version [REF=<feature branch>]"
	fi
	show_notes "$(notes "$version" <CHANGELOG.md)" "the $version section of CHANGELOG.md is empty"
	[ "${header##* }" = "$(date +%Y-%m-%d)" ] ||
		echo "warning: the entry is dated ${header##* }, not today"

	step "make check"
	make check

	step "GoReleaser dry run"
	goreleaser check
	make snapshot
	for os in darwin linux windows; do
		ext=tar.gz
		[ "$os" = windows ] && ext=zip
		for arch in amd64 arm64; do
			set -- dist/den_*_"${os}_${arch}.$ext"
			[ -f "$1" ] || die "no $os/$arch archive in dist/"
			name=${1#dist/}
			grep -q " $name\$" dist/checksums.txt || die "$name is missing from dist/checksums.txt"
			if [ "$ext" = zip ]; then
				unzip -Z1 "$1" | grep -qx 'skills/den/SKILL.md' || die "$name has no skills/den/SKILL.md"
			else
				tar -tzf "$1" | grep -qx 'skills/den/SKILL.md' || die "$name has no skills/den/SKILL.md"
			fi
			echo "  ok  $name"
		done
	done

	step "Ready to ship v$version"
	git status --short
	echo
	echo "Next:"
	[ -z "$(git status --porcelain CHANGELOG.md)" ] ||
		echo "  git add CHANGELOG.md && git commit -m \"docs: release v$version\""
	cat <<EOF
  git push -u origin $(git branch --show-current)    # then open a PR into main
After the PR is merged:
  git switch main && git pull --ff-only
  make release-tag VERSION=$version
EOF
}

cmd_tag() {
	version=$1
	check_tag_version "$version"
	release=${version%%-*}

	if [ "$release" = "$version" ]; then
		step "main"
		git fetch --quiet --tags origin main
		[ "$(git branch --show-current)" = main ] ||
			die "run this on main: git switch main && git pull --ff-only"
		[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] ||
			die "main is not origin/main: git pull --ff-only"
	else
		# A release candidate is the release PR before it merges: its branch,
		# as pushed, with the entry the release will have.
		branch=chore/release-v$release
		step "$branch"
		[ "$(git branch --show-current)" = "$branch" ] ||
			die "a release candidate is tagged on the release PR's branch: git switch $branch"
		git fetch --quiet --tags origin "$branch" || die "no $branch on origin: push it first"
		[ "$(git rev-parse HEAD)" = "$(git rev-parse "origin/$branch")" ] ||
			die "$branch is not origin/$branch: push it, or git pull --ff-only"
	fi
	git log -1 --format='  %h %s'
	[ -z "$(git status --porcelain)" ] ||
		echo "warning: uncommitted changes; they are not part of the tagged commit"

	step "Version"
	check_new_tag "$version"

	# The workflow reads CHANGELOG.md from the tagged commit, not the worktree.
	step "Release notes (CHANGELOG.md at HEAD)"
	show_notes "$(git show HEAD:CHANGELOG.md | notes "$release")" \
		"CHANGELOG.md at HEAD has no $release entry; is the release PR merged (or, for a release candidate, committed)?"

	printf '\nTag %s as v%s and push it to origin? The release workflow starts at once. [y/N] ' \
		"$(git rev-parse --short HEAD)" "$version"
	read -r answer || answer=
	[ "$answer" = y ] || [ "$answer" = Y ] || die "nothing tagged"

	git tag "v$version" HEAD
	git push origin "v$version"
	echo
	echo "Workflow: $(repo_url)/actions"
	if [ "$release" = "$version" ]; then
		echo "Then check the release page, latest.json among its assets, and that the previous den offers v$version (Updates pane, i)."
	else
		echo "Then check the release page says Pre-release, and that a den with updates.channel: prerelease offers v$version (Updates pane, v)."
	fi
}

[ $# -ge 1 ] || usage
cmd=$1
shift
case $cmd in
next) cmd_next "$@" ;;
check) cmd_check "$@" ;;
draft | prepare | tag)
	[ $# -ge 1 ] || usage
	"cmd_$cmd" "$@"
	;;
*) usage ;;
esac
