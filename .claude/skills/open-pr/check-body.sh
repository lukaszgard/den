#!/bin/sh
# check-body.sh <body-file> [title] — checks a PR body against the repo's
# .github/pull_request_template.md before it is published: no HTML comment
# left, every heading and checkbox of the template present in the same order
# and indentation (a box may be ticked and add detail after its label, nothing
# may be dropped, added or reordered), and a conventional-commit title (<type>: <summary>).
set -eu

body=${1:?usage: check-body.sh <body-file> [title]}
title=${2-}
tmpl=$(git rev-parse --show-toplevel)/.github/pull_request_template.md
fail=0

if grep -n '<!--' "$body" >&2; then
	echo "✗ HTML comments are left in the body (lines above)" >&2
	fail=1
fi
if [ -n "$title" ] && ! printf '%s\n' "$title" | grep -Eq '^(feat|fix|refactor|perf|test|docs|build|ci)(\([^)]+\))?!?: '; then
	echo "✗ title must start with <type>: (feat, fix, refactor, perf, test, docs, build, ci) — got: $title" >&2
	fail=1
fi

# Headings and checkbox lines are the template's skeleton. Both sides are
# normalised the same way ([x] → [ ], comments and trailing blanks dropped);
# a body line must then start with the template line it stands for.
awk '
function norm(s) {
	gsub(/<!--.*-->/, "", s)
	sub(/[ \t]+$/, "", s)
	sub(/\[[xX]\]/, "[ ]", s)
	return s
}
function skeleton(s) { return s ~ /^#/ || s ~ /^[ \t]*- \[[ xX]\]/ }
FNR == NR { if (skeleton($0)) t[++nt] = norm($0); next }
skeleton($0) { b[++nb] = norm($0) }
END {
	# Stop at the first difference: after a dropped line every later one is
	# shifted, and listing them all hides the one that matters.
	for (i = 1; i <= nt || i <= nb; i++) {
		if (i > nt) { print "✗ not in the template: " b[i]; exit 1 }
		if (i > nb) { print "✗ missing from the body: " t[i]; exit 1 }
		if (index(b[i], t[i]) != 1) {
			printf "✗ the body differs from the template here (dropped, added or reordered?)\n    template: %s\n    body:     %s\n", t[i], b[i]
			exit 1
		}
	}
}' "$tmpl" "$body" >&2 || fail=1

[ "$fail" = 0 ] && echo "✓ body matches $(basename "$tmpl")"
exit "$fail"
