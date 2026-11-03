package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
)

// secretLoginConfig is a config in dir with an rds service logging in with the
// secret orders-login, an IAM one, and an unrelated secret. secretID is the
// secret's secret_id and other the unrelated one's, so a test can change either.
func secretLoginConfig(t *testing.T, dir, secretID, other string) *config.Config {
	t.Helper()
	// applyConfig exports the AWS paths; this registers putting them back.
	t.Setenv("AWS_CONFIG_FILE", os.Getenv("AWS_CONFIG_FILE"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", os.Getenv("AWS_SHARED_CREDENTIALS_FILE"))
	aws := filepath.Join(dir, "aws-config")
	if err := os.WriteFile(aws, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "den.yaml")
	body := `version: "1"
aws:
  config_path: ` + aws + `
  credentials_path: ` + aws + `
environments:
  dev: {region: lab, environment: dev, ec2_instance_id: i-1, aws_profile: dev, aws_region_code: eu-west-1}
services:
  - name: with-password
    type: rds
    env: dev
    rds:
      rds_host: db.example
      local_port: 50200
      auth:
        secret: orders-login
  - name: with-token
    type: rds
    env: dev
    rds:
      rds_host: db.example
      db_user: app
      local_port: 50201
secrets:
  aws_secretsmanager:
    - name: orders-login
      secret_id: ` + secretID + `
    - name: unrelated
      secret_id: ` + other + `
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func serviceNamed(t *testing.T, mgr *service.Manager, name string) service.Service {
	t.Helper()
	svc, err := findService(mgr, name)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// The service must read the entry auth: names, not merely any secret.
func TestServiceWithAuthReadsTheNamedSecret(t *testing.T) {
	mgr := service.NewManager()
	t.Cleanup(mgr.Close)
	res := applyConfig(t.Context(), mgr, secretLoginConfig(t, t.TempDir(), "/acme/orders", "/acme/other"))
	if len(res.Errors) > 0 {
		t.Fatalf("errors: %v", res.Errors)
	}

	login, ok := service.PasswordLoginOf(serviceNamed(t, mgr, "with-password"))
	if !ok || login.Secret != "orders-login" || !strings.Contains(login.Provider, "secrets manager") {
		t.Errorf("PasswordLoginOf = %+v, %v", login, ok)
	}
	if _, ok := service.PasswordLoginOf(serviceNamed(t, mgr, "with-token")); ok {
		t.Error("a service without auth must log in with a token")
	}
}

// Reloading a config keeps a running connection unless what it is built from
// changed, and the secret a service reads is part of that.
func TestReloadRebuildsOnlyWhatTheSecretChangeTouches(t *testing.T) {
	cases := []struct {
		name         string
		secretID     string
		other        string
		wantPassword bool // the password service keeps its instance
	}{
		{"nothing changed", "/acme/orders", "/acme/other", true},
		{"the entry it reads changed", "/acme/orders-v2", "/acme/other", false},
		{"another entry changed", "/acme/orders", "/acme/other-v2", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mgr := service.NewManager()
			t.Cleanup(mgr.Close)
			dir := t.TempDir()
			applyConfig(t.Context(), mgr, secretLoginConfig(t, dir, "/acme/orders", "/acme/other"))
			password := serviceNamed(t, mgr, "with-password")
			token := serviceNamed(t, mgr, "with-token")

			applyConfig(t.Context(), mgr, secretLoginConfig(t, dir, c.secretID, c.other))

			if kept := serviceNamed(t, mgr, "with-password") == password; kept != c.wantPassword {
				t.Errorf("password service kept = %v, want %v", kept, c.wantPassword)
			}
			if serviceNamed(t, mgr, "with-token") != token {
				t.Error("a service without auth must keep its instance whatever a secret does")
			}
		})
	}
}
