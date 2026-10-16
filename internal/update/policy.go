package update

import (
	"fmt"
	"slices"
)

// The update channels: which releases a check offers.
const (
	ChannelStable     = "stable"     // releases only; the default
	ChannelPrerelease = "prerelease" // release candidates too
)

// PolicySince is the first den that follows a Policy, with the version picker:
// an older one offers the latest release whatever a pin, a skip or a yank say.
const PolicySince = "v1.8.0"

// FollowsPolicy reports whether den at version follows a Policy. Its release
// candidates do too: -0 sorts below every prerelease of a version.
func FollowsPolicy(version string) bool { return Compare(version, PolicySince+"-0") >= 0 }

// Policy says which releases an update check may offer: the team's channel
// and pin from den.yaml, and the versions this machine skipped.
type Policy struct {
	Channel string   // ChannelStable when empty
	Pin     string   // hold den on this version, older or newer; empty for none
	Skipped []string // never offered, as with a yanked release
}

// Admits reports whether the channel offers rel: a release candidate only on
// the prerelease channel.
func (p Policy) Admits(rel Release) bool {
	return p.Channel == ChannelPrerelease || (!rel.Prerelease && !IsPrerelease(rel.Version))
}

// Skips reports whether version is on the skip list.
func (p Policy) Skips(version string) bool {
	return slices.ContainsFunc(p.Skipped, func(s string) bool { return SameVersion(s, version) })
}

// eligible reports whether a check may offer rel.
func (p Policy) eligible(rel Release) bool {
	return rel.Yanked == "" && !p.Skips(rel.Version) && p.Admits(rel)
}

// Offer picks the release an update check offers from rels, in any order. It
// returns the current release (or one carrying only its version) when there is
// nothing to install, so a caller comparing versions sees "up to date":
//
//   - with a pin, the pinned release, older than current or newer; an error
//     when rels does not have it.
//   - otherwise the newest eligible release newer than current: not yanked,
//     not skipped, admitted by the channel.
//   - with nothing newer and the current release yanked, the newest eligible
//     one below it: the way off a bad release.
//
// A current version that is not one ("dev") is older than every release.
func Offer(rels []Release, current string, p Policy) (Release, error) {
	if p.Pin != "" {
		for _, r := range rels {
			if SameVersion(r.Version, p.Pin) {
				return r, nil
			}
		}
		return Release{}, fmt.Errorf("updates.pin %s is not a published release", Canonical(p.Pin))
	}
	var newer, older, cur Release
	for _, r := range rels {
		if SameVersion(r.Version, current) {
			cur = r
		}
		if !p.eligible(r) {
			continue
		}
		switch c := Compare(r.Version, current); {
		case c > 0 && (newer.Version == "" || Compare(r.Version, newer.Version) > 0):
			newer = r
		case c < 0 && (older.Version == "" || Compare(r.Version, older.Version) > 0):
			older = r
		}
	}
	switch {
	case newer.Version != "":
		return newer, nil
	case cur.Yanked != "" && older.Version != "":
		return older, nil
	case cur.Version != "":
		return cur, nil
	}
	return Release{Version: current}, nil
}

// Why says in one line why Offer chose offer, or "" when it is simply the
// newest release or there is nothing newer: a pin, a yanked current release,
// or a newer release left out because it is skipped or yanked.
func Why(rels []Release, current string, offer Release, p Policy) string {
	if p.Pin != "" {
		if SameVersion(current, p.Pin) {
			return "pinned to " + Canonical(p.Pin) + " by updates.pin"
		}
		return "updates.pin holds den on " + Canonical(p.Pin)
	}
	if Compare(offer.Version, current) < 0 {
		for _, r := range rels {
			if SameVersion(r.Version, current) && r.Yanked != "" {
				return Canonical(current) + " is yanked: " + r.Yanked
			}
		}
	}
	// rels may come in any order: the newest left out is the one to name.
	var left Release
	for _, r := range rels {
		if Compare(r.Version, offer.Version) <= 0 || Compare(r.Version, current) <= 0 || !p.Admits(r) {
			continue
		}
		if left.Version == "" || Compare(r.Version, left.Version) > 0 {
			left = r
		}
	}
	switch {
	case left.Yanked != "":
		return Canonical(left.Version) + " is yanked: " + left.Yanked
	case p.Skips(left.Version):
		return Canonical(left.Version) + " is skipped on this machine"
	}
	return ""
}
