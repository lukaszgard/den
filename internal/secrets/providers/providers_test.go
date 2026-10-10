package providers

import (
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/secrets"
)

func TestItemsNilConfig(t *testing.T) {
	if items := Items(nil, nil); items != nil {
		t.Errorf("Items(nil) = %v, want none", items)
	}
	if items := Items(&config.Config{}, nil); items != nil {
		t.Errorf("Items without a secrets block = %v, want none", items)
	}
}

// An item's own view wins over secrets.view, which wins over the default
// (tree). KeePassXC entries are flat and take no view at all.
func TestItemsResolveView(t *testing.T) {
	for _, tc := range []struct {
		name         string
		global, item string
		wantTree     bool
	}{
		{"default", "", "", true},
		{"global flat", config.SecretViewFlat, "", false},
		{"item overrides global", config.SecretViewFlat, config.SecretViewTree, true},
		{"item flat", "", config.SecretViewFlat, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Secrets: &config.SecretsConfig{
				View:              tc.global,
				SOPS:              []config.SOPSSecretSpec{{Name: "s", File: "s.enc.yaml", View: tc.item}},
				AWSSecretsManager: []config.AWSSecretsManagerSecretSpec{{Name: "a", SecretID: "x", View: tc.item}},
				KeePassXC:         []config.KeePassXCSecretSpec{{Name: "k", Database: "k.kdbx"}},
				Vault:             []config.VaultSecretSpec{{Name: "v", Path: "p", View: tc.item}},
			}}
			items := Items(cfg, nil)
			if len(items) != 4 {
				t.Fatalf("got %d items, want 4", len(items))
			}
			for _, it := range items {
				v, ok := it.(secrets.Viewer)
				if it.Provider() == "keepassxc" {
					if ok {
						t.Errorf("%s: KeePassXC must open flat, but implements Viewer", it.Name())
					}
					continue
				}
				if !ok {
					t.Fatalf("%s (%s) does not implement secrets.Viewer", it.Name(), it.Provider())
				}
				if got := v.TreeView(); got != tc.wantTree {
					t.Errorf("%s: TreeView = %v, want %v", it.Name(), got, tc.wantTree)
				}
			}
		})
	}
}
