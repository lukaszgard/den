package config

import (
	"strings"
	"testing"
)

const secretAuthSecrets = `
secrets:
  aws_secretsmanager:
    - name: orders-aws
      secret_id: /acme/orders
  sops:
    - name: orders-sops
      file: secrets/orders.yaml
  vault:
    - name: orders-vault
      path: orders/db
    - name: all-vault
      path: orders/
  keepassxc:
    - name: orders-keepass
      database: team.kdbx
      entry: DB/orders
`

// secretAuthConfig is a config with one rds service made of rdsBody.
func secretAuthConfig(rdsBody string) string {
	return `
environments:
  eu-dev: {region: eu, environment: dev, ec2_instance_id: i-1, aws_profile: dev, aws_region_code: eu-central-1}
services:
  - name: orders
    type: rds
    env: eu-dev
    rds:
      rds_host: db.example
` + rdsBody + secretAuthSecrets
}

func TestSecretAuthAcceptsASecretOfOneValue(t *testing.T) {
	for _, secret := range []string{"orders-aws", "orders-sops", "orders-vault"} {
		t.Run(secret, func(t *testing.T) {
			_, err := load(t, secretAuthConfig(`      auth:
        secret: `+secret+"\n"))
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// The secret may hold the username, so db_user stops being required, but only
// with auth: an IAM token is always minted for a named user.
func TestDBUserRequiredOnlyWithoutAuth(t *testing.T) {
	if _, err := load(t, secretAuthConfig("")); err == nil || !strings.Contains(err.Error(), "rds.db_user is required") {
		t.Errorf("without auth: err = %v, want db_user required", err)
	}
	cfg, err := load(t, secretAuthConfig(`      db_user: app
      auth:
        secret: orders-aws
        username_key: user
        password_key: pass
`))
	if err != nil {
		t.Fatal(err)
	}
	auth := cfg.Services[0].RDS.Auth
	if auth == nil || auth.Secret != "orders-aws" || auth.UsernameKey != "user" || auth.PasswordKey != "pass" {
		t.Errorf("auth = %+v", auth)
	}
}

func TestSecretAuthRejects(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"no secret named", "      auth: {}\n", "rds.auth.secret is required"},
		{"a secret that does not exist", "      auth:\n        secret: nope\n", `rds.auth.secret "nope": no such entry in secrets`},
		{"a KeePassXC entry, whose master password den cannot ask for here", "      auth:\n        secret: orders-keepass\n", "KeePassXC"},
		{"a Vault folder, which is many secrets", "      auth:\n        secret: all-vault\n", "folder"},
		{"writing the password to pgpass", "      update_pgpass: true\n      auth:\n        secret: orders-aws\n", "update_pgpass"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := load(t, secretAuthConfig(c.body))
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v, want it to mention %q", err, c.wantErr)
			}
		})
	}
}

// A service must name what it needs even when the config has no secrets: block.
func TestSecretAuthWithoutASecretsBlock(t *testing.T) {
	_, err := load(t, strings.TrimSuffix(secretAuthConfig("      auth:\n        secret: orders-aws\n"), secretAuthSecrets))
	if err == nil || !strings.Contains(err.Error(), "no such entry in secrets") {
		t.Errorf("err = %v", err)
	}
}

// What a reload compares: the entry a service reads its password from.
func TestSecretSpecFindsAnEntryOfAnyProvider(t *testing.T) {
	cfg, err := load(t, secretAuthConfig("      db_user: app\n"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		want any
	}{
		{"orders-aws", cfg.Secrets.AWSSecretsManager[0]},
		{"orders-sops", cfg.Secrets.SOPS[0]},
		{"orders-vault", cfg.Secrets.Vault[0]},
		{"orders-keepass", cfg.Secrets.KeePassXC[0]},
		{"nope", nil},
	}
	for _, c := range cases {
		got := cfg.Secrets.Spec(c.name)
		switch want := c.want.(type) {
		case AWSSecretsManagerSecretSpec:
			if s, ok := got.(AWSSecretsManagerSecretSpec); !ok || s.SecretID != want.SecretID {
				t.Errorf("Spec(%q) = %#v", c.name, got)
			}
		case SOPSSecretSpec:
			if s, ok := got.(SOPSSecretSpec); !ok || s.File != want.File {
				t.Errorf("Spec(%q) = %#v", c.name, got)
			}
		case VaultSecretSpec:
			if s, ok := got.(VaultSecretSpec); !ok || s.Path != want.Path {
				t.Errorf("Spec(%q) = %#v", c.name, got)
			}
		case KeePassXCSecretSpec:
			if s, ok := got.(KeePassXCSecretSpec); !ok || s.Database != want.Database {
				t.Errorf("Spec(%q) = %#v", c.name, got)
			}
		case nil:
			if got != nil {
				t.Errorf("Spec(%q) = %#v, want nil", c.name, got)
			}
		}
	}
	var none *SecretsConfig
	if got := none.Spec("orders-aws"); got != nil {
		t.Errorf("nil config: Spec = %#v, want nil", got)
	}
}
