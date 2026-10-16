package update

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Offer decides which version a person is told to install, and with a pin or a
// yanked release that may be an older one: a wrong pick here installs a bad
// release on everyone, or keeps someone on one.

func TestCompareOrdersVersions(t *testing.T) {
	ordered := []string{"dev", "v0.0.1", "1.7.2", "v1.7.10", "v1.8.0-rc.1", "v1.8.0-rc.2", "1.8.0"}
	for i := range ordered {
		for j := range ordered {
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got := Compare(ordered[i], ordered[j]); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
	if Compare("1.7.2", "v1.7.2") != 0 {
		t.Error("a leading v must not matter")
	}
	for v, want := range map[string]bool{"v1.8.0-rc.1": true, "1.7.3-snapshot.abc1234": true, "v1.8.0": false, "dev": false} {
		if got := IsPrerelease(v); got != want {
			t.Errorf("IsPrerelease(%s) = %v, want %v", v, got, want)
		}
	}
	if IsVersion("dev") || !IsVersion("1.2.3") || Canonical("1.2.3") != "v1.2.3" || Canonical("dev") != "dev" {
		t.Error("IsVersion/Canonical disagree with semver")
	}
}

func rels(specs ...string) []Release {
	var out []Release
	for _, s := range specs {
		v, yanked, _ := strings.Cut(s, " yanked:")
		out = append(out, Release{Version: v, Prerelease: IsPrerelease(v), Yanked: yanked})
	}
	return out
}

func TestOffer(t *testing.T) {
	list := rels("v1.8.0-rc.1", "v1.7.2", "v1.7.1", "v1.7.0 yanked:reconnect loop")
	cases := []struct {
		name    string
		rels    []Release
		current string
		policy  Policy
		want    string
	}{
		{"newer stable", list, "1.7.1", Policy{}, "v1.7.2"},
		{"up to date", list, "1.7.2", Policy{}, "v1.7.2"},
		{"stable channel by name", list, "1.7.2", Policy{Channel: ChannelStable}, "v1.7.2"},
		{"prerelease channel offers the rc", list, "1.7.2", Policy{Channel: ChannelPrerelease}, "v1.8.0-rc.1"},
		{"skipped newest", list, "1.7.1", Policy{Skipped: []string{"1.7.2"}}, "v1.7.1"},
		{"skip below the newest changes nothing", list, "1.7.0", Policy{Skipped: []string{"v1.7.1"}}, "v1.7.2"},
		{"pin older is a downgrade", list, "1.7.2", Policy{Pin: "v1.7.1"}, "v1.7.1"},
		{"pin held", list, "1.7.1", Policy{Pin: "1.7.1"}, "v1.7.1"},
		{"pin wins over skip and channel", list, "1.7.1", Policy{Pin: "v1.8.0-rc.1", Skipped: []string{"v1.8.0-rc.1"}}, "v1.8.0-rc.1"},
		{"yanked current with a newer one", list, "1.7.0", Policy{}, "v1.7.2"},
		{"yanked current, nothing newer: the newest below",
			rels("v1.7.0 yanked:bad", "v1.6.1 yanked:worse", "v1.6.0", "v1.5.0"), "1.7.0", Policy{}, "v1.6.0"},
		{"yanked current, nothing below either", rels("v1.7.0 yanked:bad"), "1.7.0", Policy{}, "v1.7.0"},
		{"yanked newest is not offered", rels("v1.7.3 yanked:bad", "v1.7.2"), "1.7.2", Policy{}, "v1.7.2"},
		{"dev build gets the newest", list, "dev", Policy{}, "v1.7.2"},
		{"rc on stable moves to its release", rels("v1.8.0", "v1.8.0-rc.1"), "1.8.0-rc.1", Policy{}, "v1.8.0"},
		{"rc on stable stays without a release", list, "1.8.0-rc.1", Policy{}, "v1.8.0-rc.1"},
		{"order of the list does not matter", rels("v1.7.0", "v1.7.2", "v1.7.1"), "1.7.0", Policy{}, "v1.7.2"},
		{"current not listed and nothing newer", list, "1.9.0", Policy{}, "1.9.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Offer(c.rels, c.current, c.policy)
			if err != nil {
				t.Fatal(err)
			}
			if got.Version != c.want {
				t.Errorf("Offer = %s, want %s", got.Version, c.want)
			}
		})
	}

	if _, err := Offer(list, "1.7.2", Policy{Pin: "v9.9.9"}); err == nil || !strings.Contains(err.Error(), "updates.pin v9.9.9") {
		t.Errorf("err = %v, want the unpublished pin named", err)
	}
}

func TestWhy(t *testing.T) {
	list := rels("v1.7.3 yanked:SSM loop", "v1.7.2", "v1.7.1", "v1.7.0 yanked:bad")
	cases := []struct {
		name    string
		rels    []Release
		current string
		policy  Policy
		want    string
	}{
		{"newest", rels("v1.7.2", "v1.7.1"), "1.7.1", Policy{}, ""},
		{"pinned and there", list, "1.7.1", Policy{Pin: "1.7.1"}, "pinned to v1.7.1 by updates.pin"},
		{"pin to move to", list, "1.7.2", Policy{Pin: "1.7.1"}, "updates.pin holds den on v1.7.1"},
		{"newer yanked", list, "1.7.2", Policy{}, "v1.7.3 is yanked: SSM loop"},
		{"newer skipped", rels("v1.7.3", "v1.7.2"), "1.7.2", Policy{Skipped: []string{"v1.7.3"}}, "v1.7.3 is skipped on this machine"},
		{"yanked current goes down", rels("v1.7.0 yanked:bad", "v1.6.0"), "1.7.0", Policy{}, "v1.7.0 is yanked: bad"},
		{"rc on stable is not mentioned", rels("v1.8.0-rc.1", "v1.7.2"), "1.7.2", Policy{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			offer, err := Offer(c.rels, c.current, c.policy)
			if err != nil {
				t.Fatal(err)
			}
			if got := Why(c.rels, c.current, offer, c.policy); got != c.want {
				t.Errorf("Why = %q, want %q", got, c.want)
			}
		})
	}
}

func TestPrefsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "updates.json")
	p, err := LoadPrefs(path)
	if err != nil || len(p.Skipped) != 0 {
		t.Fatalf("a missing file is no prefs, got %+v, %v", p, err)
	}
	if !p.Skip("1.7.1") || !p.Skip("v1.7.3") || p.Skip("v1.7.1") {
		t.Fatal("Skip must add a version once, whatever its spelling")
	}
	if want := []string{"v1.7.3", "v1.7.1"}; !slices.Equal(p.Skipped, want) {
		t.Fatalf("Skipped = %v, want %v", p.Skipped, want)
	}
	if err := SavePrefs(path, p); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPrefs(path)
	if err != nil || !got.Skips("1.7.3") || !got.Skips("v1.7.1") || got.Skips("v1.7.2") {
		t.Fatalf("loaded %+v, %v", got, err)
	}
	if !got.Unskip("v1.7.3") || got.Unskip("v1.7.3") || got.Skips("1.7.3") {
		t.Fatal("Unskip must take a version off once")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".updates-*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}

	// A skip list that does not parse is reported, not silently emptied.
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPrefs(path); err == nil {
		t.Fatal("a malformed updates.json must be an error")
	}
	if p, err := LoadPrefs(""); err != nil || len(p.Skipped) != 0 {
		t.Fatal("no path is no prefs")
	}
	if err := SavePrefs("", p); err == nil {
		t.Fatal("saving without a path must fail")
	}
}

func TestPrefsFileFollowsStateDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	if got, want := PrefsFile(), filepath.Join(dir, "den", "updates.json"); got != want {
		t.Fatalf("PrefsFile() = %s, want %s", got, want)
	}
}
