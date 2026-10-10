package config

import (
	"strings"
	"testing"
)

func TestParseRepo(t *testing.T) {
	cases := []struct {
		raw, id, clone string
	}{
		{"https://github.com/acme/den-runbooks", "github.com/acme/den-runbooks", "https://github.com/acme/den-runbooks"},
		{"https://GHE.acme.com/Ops/Runbooks.git/", "ghe.acme.com/ops/runbooks", "https://GHE.acme.com/Ops/Runbooks.git/"},
		{"ssh://git@ghe.acme.com:2222/ops/runbooks.git", "ghe.acme.com/ops/runbooks", "ssh://git@ghe.acme.com:2222/ops/runbooks.git"},
		{"git@ghe.acme.com:ops/runbooks.git", "ghe.acme.com/ops/runbooks", "git@ghe.acme.com:ops/runbooks.git"},
		{"ghe.acme.com/ops/runbooks", "ghe.acme.com/ops/runbooks", "https://ghe.acme.com/ops/runbooks"},
		{"acme/den-runbooks", "github.com/acme/den-runbooks", "https://github.com/acme/den-runbooks"},
		{"file:///srv/git/runbooks", "file/srv/git/runbooks", "file:///srv/git/runbooks"},
		{"/srv/git/runbooks.git", "file/srv/git/runbooks", "/srv/git/runbooks.git"},
	}
	for _, c := range cases {
		repo, err := ParseRepo(c.raw)
		if err != nil {
			t.Errorf("ParseRepo(%q): %v", c.raw, err)
			continue
		}
		if repo.ID != c.id || repo.Clone != c.clone {
			t.Errorf("ParseRepo(%q) = %+v, want ID %q, Clone %q", c.raw, repo, c.id, c.clone)
		}
	}
}

func TestParseRepoRejects(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"", "empty"},
		{"https://ghp_token@github.com/acme/runbooks", "credential helper"},
		{"https://me:secret@github.com/acme/runbooks", "credential helper"},
		{"ssh://git:secret@ghe.acme.com/ops/runbooks", "credential helper"},
		{"-uupload-pack=touch /tmp/x", "not a git repository"},
		{"github.com", "not a git repository"},
		{"https://github.com/", "no repository"},
		{"git@ghe.acme.com:", "no repository"},
		{"ftp://example.com/x", "not a git repository"},
		{"acme/den runbooks", "not a git repository"},
	}
	for _, c := range cases {
		_, err := ParseRepo(c.raw)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("ParseRepo(%q) error = %v, want one mentioning %q", c.raw, err, c.want)
		}
	}
}

func TestRepoAllowed(t *testing.T) {
	repo := Repo{ID: "ghe.acme.com/ops/runbooks"}
	if !repo.Allowed(nil) {
		t.Error("no allow_sources should allow every repository")
	}
	if !repo.Allowed([]string{"github.com/*/*", "GHE.acme.com/ops/*"}) {
		t.Error("ghe.acme.com/ops/* should allow ghe.acme.com/ops/runbooks")
	}
	if repo.Allowed([]string{"ghe.acme.com/dev/*", "github.com/ops/*"}) {
		t.Error("patterns for other owners and hosts should not allow it")
	}
}

func TestSourceNameFromURL(t *testing.T) {
	cases := map[string]string{
		"git@ghe.acme.com:ops/Den_Runbooks.git": "den-runbooks",
		"https://github.com/acme/runbooks/":     "runbooks",
		"acme/ops.tools":                        "ops-tools",
		"/srv/git/x.git":                        "x",
	}
	for raw, want := range cases {
		if got := SourceNameFromURL(raw); got != want {
			t.Errorf("SourceNameFromURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
