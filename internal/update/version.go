package update

import (
	"strings"

	"golang.org/x/mod/semver"
)

// canonical returns v with the leading "v" semver wants, or "" when v is not a
// semantic version. A release build reports its version without the v
// (GoReleaser's {{.Version}}), a tag has it, and both must order alike.
func canonical(v string) string {
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return ""
	}
	return v
}

// IsVersion reports whether v is a semantic version, with or without the
// leading v. A build from a checkout is "dev", which is not.
func IsVersion(v string) bool { return canonical(v) != "" }

// Compare orders two versions as semver does: -1 when a is older than b, 0
// when they are the same release, +1 when a is newer. A release candidate is
// older than its release (v1.8.0-rc.1 < v1.8.0), and anything that is not a
// version is older than every version.
func Compare(a, b string) int { return semver.Compare(canonical(a), canonical(b)) }

// IsPrerelease reports whether v is a prerelease: a release candidate
// (v1.8.0-rc.1) or a snapshot build (1.7.3-snapshot.abc1234).
func IsPrerelease(v string) bool { return semver.Prerelease(canonical(v)) != "" }

// Canonical returns v with a leading v when it is a version, and v unchanged
// otherwise: the spelling den stores and prints versions in.
func Canonical(v string) string {
	if c := canonical(v); c != "" {
		return c
	}
	return v
}
