package documentdb

import (
	"path/filepath"
	"testing"
)

// The CA bundle is cached beside den.yaml, so XDG_CONFIG_HOME moves both.
func TestDefaultCABundlePathFollowsConfigDir(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, want := defaultCABundlePath(), filepath.Join(xdg, "den", "global-bundle.pem"); got != want {
		t.Errorf("defaultCABundlePath = %q, want %q", got, want)
	}
}
