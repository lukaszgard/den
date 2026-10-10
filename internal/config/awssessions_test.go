package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// sample mirrors the shape of a real ~/.aws/config: two sso-sessions, profiles
// referencing them in file order, and one session nothing references.
func sample() []AWSProfile {
	return []AWSProfile{
		{Kind: "sso-session", Name: "alpha", SSOStartURL: "https://alpha.awsapps.com/start", SSORegion: "eu-central-1"},
		{Kind: "sso-session", Name: "beta", SSOStartURL: "https://beta.awsapps.com/start", SSORegion: "us-east-1"},
		{Kind: "sso-session", Name: "orphan", SSOStartURL: "https://orphan.awsapps.com/start"},
		{Kind: "profile", Name: "alpha-dev", SSOSession: "alpha"},
		{Kind: "profile", Name: "alpha-prod", SSOSession: "alpha"},
		{Kind: "profile", Name: "beta-dev", SSOSession: "beta"},
		{Kind: "profile", Name: "static-creds"},
	}
}

func names(sessions []SSOSession) []string {
	out := make([]string, len(sessions))
	for i, s := range sessions {
		out[i] = s.Name
	}
	return out
}

func TestResolveSSOSessionsNoCuration(t *testing.T) {
	got, warns := ResolveSSOSessions(sample(), nil)
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	want := []string{"alpha", "beta", "orphan"}
	if strings.Join(names(got), ",") != strings.Join(want, ",") {
		t.Fatalf("discovery order not preserved: got %v want %v", names(got), want)
	}
	for _, s := range got {
		if s.DisplayName != s.Name {
			t.Errorf("%s: DisplayName should default to Name, got %q", s.Name, s.DisplayName)
		}
	}
}

func TestResolveSSOSessionsFirstProfileWins(t *testing.T) {
	got, _ := ResolveSSOSessions(sample(), nil)
	want := map[string]string{"alpha": "alpha-dev", "beta": "beta-dev", "orphan": ""}
	for _, s := range got {
		if s.Profile != want[s.Name] {
			t.Errorf("%s: Profile = %q, want %q", s.Name, s.Profile, want[s.Name])
		}
	}
}

func TestResolveSSOSessionsCurationSelectsAndOrders(t *testing.T) {
	// beta before alpha, and orphan left out entirely.
	got, warns := ResolveSSOSessions(sample(), []AWSSessionSpec{
		{Session: "beta", Name: "Beta Org", Description: "second"},
		{Session: "alpha"},
	})
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if want := "beta,alpha"; strings.Join(names(got), ",") != want {
		t.Fatalf("curation order not honoured: got %v want %v", names(got), want)
	}
	if got[0].DisplayName != "Beta Org" || got[0].Description != "second" {
		t.Errorf("display fields not applied: %+v", got[0])
	}
	// Name stays the section name even when a display name is set — it is the
	// argument `aws sso login --sso-session` takes.
	if got[0].Name != "beta" {
		t.Errorf("Name must remain the section name, got %q", got[0].Name)
	}
	if got[1].DisplayName != "alpha" {
		t.Errorf("uncustomised entry should fall back to Name, got %q", got[1].DisplayName)
	}
	// Curated entries still carry what was discovered.
	if got[1].StartURL != "https://alpha.awsapps.com/start" || got[1].Profile != "alpha-dev" {
		t.Errorf("discovered fields lost under curation: %+v", got[1])
	}
}

func TestResolveSSOSessionsUnknownSessionWarnsAndSkips(t *testing.T) {
	got, warns := ResolveSSOSessions(sample(), []AWSSessionSpec{
		{Session: "alpha"},
		{Session: "ghost"},
	})
	if want := "alpha"; strings.Join(names(got), ",") != want {
		t.Fatalf("unknown session should be skipped: got %v", names(got))
	}
	if len(warns) != 1 {
		t.Fatalf("want exactly one warning, got %v", warns)
	}
	// The warning has to name both the config key and the session, or it is
	// useless for working out which file to edit.
	if !strings.Contains(warns[0], "ghost") || !strings.Contains(warns[0], "aws.sessions[1]") {
		t.Errorf("warning should name the entry and the session: %q", warns[0])
	}
}

// Omitting the key and writing `sessions: []` have to mean different things, or
// there is no way to ask for no SSO sessions at all.
func TestResolveSSOSessionsEmptyCurationMeansNone(t *testing.T) {
	got, warns := ResolveSSOSessions(sample(), []AWSSessionSpec{})
	if len(got) != 0 {
		t.Fatalf("explicit empty curation should show no sessions, got %v", names(got))
	}
	if len(warns) != 0 {
		t.Errorf("empty curation is deliberate, not a problem to warn about: %v", warns)
	}

	// ...while nil still means "not configured".
	all, _ := ResolveSSOSessions(sample(), nil)
	if len(all) == 0 {
		t.Fatal("nil curation should still show every discovered session")
	}
}

// The nil/empty distinction only works if the YAML decoder preserves it, so pin
// that down rather than trusting it.
func TestSessionsYAMLPreservesNilVersusEmpty(t *testing.T) {
	var omitted Config
	if err := yaml.Unmarshal([]byte("aws:\n  config_path: x\n"), &omitted); err != nil {
		t.Fatal(err)
	}
	if omitted.AWS.Sessions != nil {
		t.Errorf("omitted sessions should decode to nil, got %#v", omitted.AWS.Sessions)
	}

	var empty Config
	if err := yaml.Unmarshal([]byte("aws:\n  sessions: []\n"), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.AWS.Sessions == nil {
		t.Error("`sessions: []` should decode to an empty non-nil slice, got nil")
	}
	if len(empty.AWS.Sessions) != 0 {
		t.Errorf("`sessions: []` should be empty, got %d entries", len(empty.AWS.Sessions))
	}
}

func TestResolveSSOSessionsNoSessionsAtAll(t *testing.T) {
	profiles := []AWSProfile{{Kind: "profile", Name: "static-creds"}}
	got, warns := ResolveSSOSessions(profiles, nil)
	if len(got) != 0 || len(warns) != 0 {
		t.Fatalf("want empty result, got %v / %v", names(got), warns)
	}
}

func TestValidateRejectsBadSessions(t *testing.T) {
	missing := &Config{AWS: &AWSSpec{Sessions: []AWSSessionSpec{{Name: "no session key"}}}}
	if err := Validate(missing); err == nil {
		t.Error("empty session should be rejected")
	} else if !strings.Contains(err.Error(), "aws.sessions[0]") {
		t.Errorf("error should name the entry: %v", err)
	}

	dup := &Config{AWS: &AWSSpec{Sessions: []AWSSessionSpec{{Session: "a"}, {Session: "a"}}}}
	if err := Validate(dup); err == nil {
		t.Error("duplicate session should be rejected")
	}

	ok := &Config{AWS: &AWSSpec{Sessions: []AWSSessionSpec{{Session: "a"}, {Session: "b"}}}}
	if err := Validate(ok); err != nil {
		t.Errorf("valid sessions rejected: %v", err)
	}
}
