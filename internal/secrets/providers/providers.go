// Package providers builds the secret items a config lists, across every
// provider. It sits apart from package secrets because the providers import
// that package.
package providers

import (
	"cmp"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/secrets"
	"github.com/lukaszgard/den/internal/secrets/awssm"
	"github.com/lukaszgard/den/internal/secrets/keepassxc"
	"github.com/lukaszgard/den/internal/secrets/sops"
	"github.com/lukaszgard/den/internal/secrets/vault"
)

// Items lists the secrets: block of cfg, in provider order. cfg may be nil.
// An item without its own view gets secrets.view here, so the providers need
// not know about the group default. Every item read through AWS shares
// logins, so the AWS config is read once for all of them; nil skips the
// check of aws_profile.
func Items(cfg *config.Config, logins *secrets.AWSLogins) []secrets.Item {
	if cfg == nil || cfg.Secrets == nil {
		return nil
	}
	view := cfg.Secrets.View
	var items []secrets.Item
	for _, spec := range cfg.Secrets.SOPS {
		spec.View = cmp.Or(spec.View, view)
		items = append(items, sops.New(spec, logins))
	}
	for _, spec := range cfg.Secrets.AWSSecretsManager {
		spec.View = cmp.Or(spec.View, view)
		items = append(items, awssm.New(spec, logins))
	}
	for _, spec := range cfg.Secrets.KeePassXC {
		items = append(items, keepassxc.New(spec))
	}
	for _, spec := range cfg.Secrets.Vault {
		spec.View = cmp.Or(spec.View, view)
		items = append(items, vault.New(spec))
	}
	return items
}
