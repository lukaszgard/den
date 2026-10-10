package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, yaml string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "den.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	return cfg, Validate(cfg)
}

const envBlock = `
environments:
  eu-dev:
    region: eu
    environment: int
    ec2_instance_id: i-0123
    aws_profile: devops
    credential_profile: acme-data
    aws_region_code: eu-central-1
services:
`

func TestInlineTargetKeysAndEnvMerge(t *testing.T) {
	cfg, err := load(t, envBlock+`
  - name: mysql
    type: rds
    env: eu-dev
    rds:
      engine: mysql
      local_port: 50131
      reconnect: true
      credential_profile: override
      rds_host: db.example
      db_user: app_iam
`)
	if err != nil {
		t.Fatal(err)
	}
	rds := cfg.Services[0].RDS
	if rds.LocalPort != 50131 || !rds.Reconnect {
		t.Errorf("inline keys not parsed: local_port=%d reconnect=%v", rds.LocalPort, rds.Reconnect)
	}
	if rds.EC2InstanceID != "i-0123" || rds.AWSProfile != "devops" || rds.AWSRegionCode != "eu-central-1" {
		t.Errorf("env not merged: %+v", rds.SSMTarget)
	}
	if rds.CredentialProfile != "override" {
		t.Errorf("credential_profile = %q, the service's own value must win over the env", rds.CredentialProfile)
	}
}

func TestRDSEngineDefaults(t *testing.T) {
	cfg, err := load(t, envBlock+`
  - name: pg
    type: rds
    env: eu-dev
    rds:
      rds_host: db.example
      db_user: app
`)
	if err != nil {
		t.Fatal(err)
	}
	rds := cfg.Services[0].RDS
	if rds.Engine != RDSEnginePostgres || rds.DBUser != "app" || rds.LocalPort != DefaultLocalPort(ServiceTypeRDS) {
		t.Errorf("postgres defaults = engine %q user %q port %d", rds.Engine, rds.DBUser, rds.LocalPort)
	}
}

func TestEverySSMTypeGetsItsDefaultLocalPort(t *testing.T) {
	tests := []struct {
		typ  ServiceType
		body string
		want int
	}{
		{ServiceTypeRDS, "rds_host: db.example\n      db_user: app", 50432},
		{ServiceTypeRedshift, "redshift_host: rs.example\n      cluster_identifier: rs", 55439},
		{ServiceTypeRedis, "redis_host: cache.example", 16379},
		{ServiceTypeDocumentDB, "documentdb_host: docdb.example", 27017},
		{ServiceTypeOpenSearch, "opensearch_host: os.example", 59200},
		{ServiceTypeNeptune, "neptune_host: n.example", 58182},
		{ServiceTypeKafka, "kafka_host: k.example", 19092},
	}
	for _, tt := range tests {
		t.Run(string(tt.typ), func(t *testing.T) {
			cfg, ps := problemsOf(t, envBlock+"\n  - name: x\n    type: "+string(tt.typ)+"\n    env: eu-dev\n    "+string(tt.typ)+":\n      "+tt.body+"\n")
			if cfg == nil {
				t.Fatalf("problems: %v", ps)
			}
			if got := cfg.Services[0].Target().LocalPort; got != tt.want || got != DefaultLocalPort(tt.typ) {
				t.Errorf("local port = %d, want %d", got, tt.want)
			}
		})
	}
	if DefaultLocalPort(ServiceTypeDocker) != 0 {
		t.Error("docker is not forwarded, it has no local port")
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name    string
		service string
		wantErr string
	}{
		{
			name: "mysql without db_user",
			service: `
  - name: mysql
    type: rds
    env: eu-dev
    rds:
      engine: mysql
      rds_host: db.example`,
			wantErr: "rds.db_user is required",
		},
		{
			name: "unknown rds engine",
			service: `
  - name: oracle
    type: rds
    env: eu-dev
    rds:
      engine: oracle
      rds_host: db.example
      db_user: app`,
			wantErr: `rds.engine must be "postgres" or "mysql"`,
		},
		{
			name: "memorydb and serverless",
			service: `
  - name: cache
    type: redis
    env: eu-dev
    redis:
      memorydb: true
      serverless: true
      redis_host: cache.example`,
			wantErr: "mutually exclusive",
		},
		{
			name: "redshift without cluster or workgroup",
			service: `
  - name: rs
    type: redshift
    env: eu-dev
    redshift:
      redshift_host: rs.example`,
			wantErr: "exactly one of cluster_identifier",
		},
		{
			name: "redshift with both cluster and workgroup",
			service: `
  - name: rs
    type: redshift
    env: eu-dev
    redshift:
      redshift_host: rs.example
      cluster_identifier: c1
      workgroup_name: wg`,
			wantErr: "exactly one of cluster_identifier",
		},
		{
			name: "redshift serverless with db_user",
			service: `
  - name: rs
    type: redshift
    env: eu-dev
    redshift:
      redshift_host: rs.example
      workgroup_name: wg
      db_user: analyst`,
			wantErr: "provisioned-only",
		},
		{
			name: "redshift ttl out of range",
			service: `
  - name: rs
    type: redshift
    env: eu-dev
    redshift:
      redshift_host: rs.example
      cluster_identifier: c1
      credential_ttl: 2h`,
			wantErr: "between 15m and 1h",
		},
		{
			name: "opensearch without host",
			service: `
  - name: os
    type: opensearch
    env: eu-dev
    opensearch:
      local_port: 50161`,
			wantErr: "opensearch.opensearch_host is required",
		},
		{
			name: "neptune without host",
			service: `
  - name: np
    type: neptune
    env: eu-dev
    neptune:
      local_port: 50171`,
			wantErr: "neptune.neptune_host is required",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := load(t, envBlock+c.service+"\n")
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, c.wantErr)
			}
		})
	}
}

func TestRedshiftDefaults(t *testing.T) {
	cfg, err := load(t, envBlock+`
  - name: rs
    type: redshift
    env: eu-dev
    redshift:
      redshift_host: rs.example
      workgroup_name: wg
`)
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.Services[0].Redshift
	if r.LocalPort != DefaultLocalPort(ServiceTypeRedshift) || r.DBName != "dev" || r.CredentialTTL != "15m0s" {
		t.Errorf("defaults = port %d db %q ttl %q", r.LocalPort, r.DBName, r.CredentialTTL)
	}
	if r.EC2InstanceID != "i-0123" {
		t.Errorf("env not merged into redshift: %+v", r.SSMTarget)
	}
}

func TestSecretsValidation(t *testing.T) {
	cases := []struct {
		name, yaml, wantErr string
	}{
		{"ok", `
secrets:
  sops:
    - name: eu staging
      file: ~/secrets/e2e.enc.yaml
      aws_profile: non-prod
      auto_reveal: true
`, ""},
		{"missing file", `
secrets:
  sops:
    - name: eu staging
`, "file is required"},
		{"missing name", `
secrets:
  sops:
    - file: a.enc.yaml
`, "name is required"},
		{"duplicate name", `
secrets:
  sops:
    - {name: a, file: a.enc.yaml}
    - {name: a, file: b.enc.yaml}
`, "duplicate name"},
		{"secretsmanager ok", `
secrets:
  aws_secretsmanager:
    - {name: m2m, secret_id: /acme/api/m2m, aws_profile: p, version_stage: AWSPREVIOUS}
`, ""},
		{"secretsmanager missing secret_id", `
secrets:
  aws_secretsmanager:
    - {name: m2m}
`, "secret_id is required"},
		{"duplicate name across providers", `
secrets:
  sops:
    - {name: a, file: a.enc.yaml}
  aws_secretsmanager:
    - {name: a, secret_id: x}
`, "already used by secrets.sops[0]"},
		{"keepassxc ok", `
secrets:
  keepassxc:
    - {name: jenkins, database: ~/team.kdbx, entry: CI/Jenkins, remember_password: 10m, totp: true}
    - {name: bot, database: ~/team.kdbx, entry: CI/Bot, key_file: ~/k.key, no_password: true}
`, ""},
		{"keepassxc browse whole database", `
secrets:
  keepassxc:
    - {name: j, database: a.kdbx}
    - {name: g, database: a.kdbx, group: AppointmentManagement360}
`, ""},
		{"keepassxc entry and group", `
secrets:
  keepassxc:
    - {name: j, database: a.kdbx, entry: A/b, group: A}
`, "not both"},
		{"keepassxc missing database", `
secrets:
  keepassxc:
    - {name: j, entry: e}
`, "database is required"},
		{"keepassxc no_password without key_file", `
secrets:
  keepassxc:
    - {name: j, database: a.kdbx, entry: e, no_password: true}
`, "no_password needs key_file"},
		{"keepassxc bad remember_password", `
secrets:
  keepassxc:
    - {name: j, database: a.kdbx, entry: e, remember_password: ten}
`, "remember_password must be a positive duration"},
		{"vault ok", `
secrets:
  vault:
    - {name: one, path: grafana/admin, field: password, version: 2, login: "vault login -method=oidc"}
    - {name: folder, mount: kv, path: apps/}
`, ""},
		{"vault missing path", `
secrets:
  vault:
    - {name: v}
`, "path is required"},
		{"vault field on folder", `
secrets:
  vault:
    - {name: v, path: apps/, field: password}
`, "not a folder path"},
		{"views", `
secrets:
  view: flat
  sops:
    - {name: s, file: s.enc.yaml, view: tree}
  aws_secretsmanager:
    - {name: a, secret_id: x, view: flat}
  vault:
    - {name: v, path: p, view: tree}
`, ""},
		{"bad global view", `
secrets:
  view: yaml
`, "secrets.view: must be tree or flat"},
		{"bad sops view", `
secrets:
  sops:
    - {name: s, file: s.enc.yaml, view: nested}
`, `secrets.sops[0] "s": view must be tree or flat`},
		{"bad aws_secretsmanager view", `
secrets:
  aws_secretsmanager:
    - {name: a, secret_id: x, view: Tree}
`, `secrets.aws_secretsmanager[0] "a": view must be tree or flat`},
		{"bad vault view", `
secrets:
  vault:
    - {name: v, path: p, view: list}
`, `secrets.vault[0] "v": view must be tree or flat`},
		// A provider den does not know yet must be reported, not silently dropped.
		{"unsupported provider", `
secrets:
  aws_ssm:
    - name: grafana
`, "secrets.aws_ssm: unsupported provider"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load(t, "services: []\n"+tc.yaml)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestSecretsAutoRevealParsed(t *testing.T) {
	cfg, err := load(t, `
services: []
secrets:
  sops:
    - {name: a, file: a.enc.yaml, auto_reveal: true}
    - {name: b, file: b.enc.yaml}
`)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Secrets.SOPS[0].AutoReveal || cfg.Secrets.SOPS[1].AutoReveal {
		t.Errorf("auto_reveal = %v, %v; want true, false", cfg.Secrets.SOPS[0].AutoReveal, cfg.Secrets.SOPS[1].AutoReveal)
	}
}

// view sits next to the provider groups, which an inline map catches; it
// must land in its own field, not be reported as an unknown provider.
func TestSecretsViewParsed(t *testing.T) {
	cfg, err := load(t, `
services: []
secrets:
  view: flat
  sops:
    - {name: a, file: a.enc.yaml, view: tree}
`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Secrets.View != SecretViewFlat || cfg.Secrets.SOPS[0].View != SecretViewTree {
		t.Errorf("views = %q, %q; want flat, tree", cfg.Secrets.View, cfg.Secrets.SOPS[0].View)
	}
	if len(cfg.Secrets.Other) != 0 {
		t.Errorf("Other = %v, want view kept out of it", cfg.Secrets.Other)
	}
}

// An explicit -c path that does not exist used to fall back silently to
// ./den.yaml or ~/.config/den/den.yaml — a different file than the one asked for.
func TestDiscoverExplicitMissingIsAnError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "den.yaml"), []byte("services: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Fatal("Discover of a missing explicit path must fail, not fall back to ./den.yaml")
	}
	got, err := Discover("")
	if err != nil || filepath.Base(got) != "den.yaml" {
		t.Fatalf("Discover(\"\") = %q, %v; want ./den.yaml", got, err)
	}
}

// XDG_CONFIG_HOME moves the config directory; ~/.config/den still loads when
// nothing is there, so a setup made before den honoured it keeps working.
func TestDiscoverConfigDirAndLegacy(t *testing.T) {
	work, home, xdg := t.TempDir(), t.TempDir(), t.TempDir()
	t.Chdir(work)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	write := func(dir string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "den.yaml")
		if err := os.WriteFile(p, []byte("services: []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	_, err := Discover("")
	if err == nil || !strings.Contains(err.Error(), filepath.Join(xdg, "den", "den.yaml")) ||
		!strings.Contains(err.Error(), filepath.Join(home, ".config", "den", "den.yaml")) {
		t.Fatalf("Discover with nothing = %v; want an error naming every place tried", err)
	}

	legacy := write(filepath.Join(home, ".config", "den"))
	got, source, err := DiscoverSource("")
	if err != nil || got != legacy || source != SourceLegacy {
		t.Fatalf("legacy only: %q %q %v; want %q from the legacy location", got, source, err, legacy)
	}

	user := write(filepath.Join(xdg, "den"))
	got, source, err = DiscoverSource("")
	if err != nil || got != user || source != SourceUser {
		t.Fatalf("XDG and legacy: %q %q %v; want %q from the config directory", got, source, err, user)
	}

	local := write(work)
	got, source, err = DiscoverSource("")
	if err != nil || filepath.Base(got) != "den.yaml" || source != SourceCwd {
		t.Fatalf("with ./den.yaml: %q %q %v; want %q from the current directory", got, source, err, local)
	}
	if _, source, _ = DiscoverSource(local); source != SourceFlag {
		t.Errorf("explicit path: source = %q, want the -c flag", source)
	}
}

func TestSettingsHelpersOnNilConfig(t *testing.T) {
	var c *Config
	home, _ := os.UserHomeDir()
	if got := c.AWSConfigPath(); got != filepath.Join(home, ".aws", "config") {
		t.Errorf("AWSConfigPath = %q", got)
	}
	if got := c.AWSCredentialsPath(); got != filepath.Join(home, ".aws", "credentials") {
		t.Errorf("AWSCredentialsPath = %q", got)
	}
	if c.AWSSessions() != nil || c.LogsSettings() != nil || c.UpdatesSettings() != nil {
		t.Error("a nil config has no sessions, logs or updates settings")
	}
	c = &Config{AWS: &AWSSpec{ConfigPath: "~/custom/config"}, Logs: &LogsConfig{FileLocation: "~/den.log"}}
	if got := c.AWSConfigPath(); got != filepath.Join(home, "custom", "config") {
		t.Errorf("AWSConfigPath = %q", got)
	}
	if got := c.LogsSettings().FileLocation; got != filepath.Join(home, "den.log") {
		t.Errorf("LogsSettings().FileLocation = %q", got)
	}
	if c.Logs.FileLocation != "~/den.log" {
		t.Error("LogsSettings must not modify the config it reads")
	}
}

// The den.yaml in the repo doubles as the annotated example, and is shipped in
// every release archive: it must keep loading and validating as the schema
// changes.
func TestRepoConfigLoadsAndValidates(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "den.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Services) == 0 {
		t.Fatal("the example config should define services")
	}
}

// Whatever a config file contains, loading it may fail but must not panic.
func FuzzParse(f *testing.F) {
	if data, err := os.ReadFile(filepath.Join("..", "..", "den.yaml")); err == nil {
		f.Add(data)
	}
	f.Add([]byte(envBlock))
	f.Add([]byte("services:\n  - name: x\n    type: rds\n    env: missing\n"))
	f.Add([]byte("secrets:\n  aws_ssm: [1]\nrunbooks:\n  items: [{name: a}]\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		cfg, err := parse(data)
		if err != nil {
			return
		}
		_ = Validate(cfg)
	})
}

func TestTransportValidation(t *testing.T) {
	cases := []struct {
		spec    TransportSpec
		wantErr string
	}{
		{TransportSpec{Type: "ssm"}, ""},
		{TransportSpec{Type: "ssh", Host: "ops@bastion"}, ""},
		{TransportSpec{Type: "ssh", Host: "b", Args: []string{"-p", "2222"}}, ""},
		{TransportSpec{Type: "eice", EndpointID: "eice-1"}, ""},
		{TransportSpec{Type: "kubectl", Resource: "svc/proxy", Namespace: "data"}, ""},
		{TransportSpec{}, "type is required"},
		{TransportSpec{Type: "vpn"}, "unknown type"},
		{TransportSpec{Type: "ssh"}, "ssh needs host"},
		{TransportSpec{Type: "eice", EndpointID: "e", Host: "x"}, "eice takes no host"},
		{TransportSpec{Type: "kubectl", Namespace: "data"}, "kubectl needs resource"},
		{TransportSpec{Type: "ssm", Host: "x"}, "ssm takes no host"},
	}
	for _, c := range cases {
		err := validateTransport(&c.spec)
		if c.wantErr == "" && err != nil || c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%+v: err = %v, want %q", c.spec, err, c.wantErr)
		}
	}
}

// A transport set on an environment applies to every service using it, and a
// non-SSM transport needs no bastion instance — not even the built-in map.
func TestTransportFromEnvironmentNeedsNoBastion(t *testing.T) {
	cfg, err := load(t, `
environments:
  lab:
    region: lab
    environment: lab
    aws_region_code: eu-west-1
    transport:
      type: ssh
      host: ops@bastion.example.com
services:
  - name: db
    type: rds
    env: lab
    rds:
      rds_host: db.internal
      db_user: app
`)
	if err != nil {
		t.Fatal(err)
	}
	target := cfg.Services[0].Target()
	if target.Transport == nil || target.Transport.Host != "ops@bastion.example.com" {
		t.Fatalf("transport not inherited: %+v", target.Transport)
	}
	env, err := target.ResolveEnv()
	if err != nil || env.Transport.TypeOrDefault() != TransportSSH {
		t.Fatalf("ResolveEnv = %+v, %v", env, err)
	}

	_, err = load(t, `
environments:
  lab:
    region: lab
    environment: lab
    transport: {type: ssh}
services: []
`)
	if err == nil || !strings.Contains(err.Error(), "environments.lab: transport ssh needs host") {
		t.Fatalf("an invalid environment transport must be reported: %v", err)
	}
}

func TestTunnelAutoConnect(t *testing.T) {
	const vpns = `
vpn:
  - name: Office VPN
    gateway: vpn.example.com
  - name: Lab VPN
    gateway: lab.example.com
`
	tunnel := func(autoConnect string) string {
		return vpns + `
tunnels:
  - name: jump
    command: ssh -N -D 5555 jump.example.com
` + autoConnect
	}

	cfg, err := load(t, tunnel(`    auto_connect:
      vpn: Office VPN
`))
	if err != nil {
		t.Fatal(err)
	}
	if ac := cfg.Tunnels[0].AutoConnect; ac == nil || ac.VPN != "Office VPN" || !ac.On() {
		t.Errorf("enabled defaults to true when the block is present: %+v", ac)
	}

	cfg, err = load(t, tunnel(`    auto_connect:
      vpn: Office VPN
      enabled: false
`))
	if err != nil {
		t.Fatal(err)
	}
	if ac := cfg.Tunnels[0].AutoConnect; ac.On() || ac.VPN != "Office VPN" {
		t.Errorf("enabled: false keeps the link but switches it off: %+v", ac)
	}
	var none *TunnelAutoConnect
	if none.On() {
		t.Error("no block means no auto-connect")
	}

	cases := map[string]string{
		"    auto_connect:\n      vpn: Offce VPN\n": `auto_connect.vpn "Offce VPN" is not a configured VPN (have "Office VPN", "Lab VPN")`,
		"    auto_connect:\n      enabled: true\n":  "auto_connect.vpn is required",
	}
	for block, want := range cases {
		if _, err := load(t, tunnel(block)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", block, err, want)
		}
	}
	noVPNs := "tunnels:\n  - name: jump\n    command: ssh -N jump\n    auto_connect:\n      vpn: Office VPN\n"
	if _, err := load(t, noVPNs); err == nil || !strings.Contains(err.Error(), "no vpn: entries are configured") {
		t.Errorf("no VPNs at all: %v", err)
	}
}

func TestRunbookValidation(t *testing.T) {
	cases := []struct {
		name, runbooks, wantErr string
	}{
		{"item without command", "items: [{name: a}]", `runbook "a": command is required`},
		{"item with unknown env", "items: [{name: a, command: x, env: nowhere}]", `unknown env "nowhere"`},
		{"negative history on an item", "items: [{name: a, command: x, history: -1}]", `runbook "a": history cannot be negative`},
		{"negative history in defaults", "defaults: {history: -2}", "runbooks.defaults: history cannot be negative"},
		{"unbounded retry", "items: [{name: a, command: x, retry: {until_success: true, interval: 1s}}]", "needs max_attempts or timeout"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := load(t, envBlock+"\nrunbooks:\n  "+c.runbooks+"\n")
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, c.wantErr)
			}
		})
	}
}

func TestRunbookAgents(t *testing.T) {
	cases := []struct {
		name, runbooks, wantErr string
	}{
		{"valid", `
  agent: Local
  agents:
    - {name: Opus, command: claude --model opus}
    - {name: Local, command: ollama launch codex --model x}
    - {name: Wrapper, command: ./run-it.sh, kind: codex}`, ""},
		{"an entry name as the default", `
  agent: Opus
  agents: [{name: Opus, command: claude}]`, ""},
		{"a built-in as the default beside entries", `
  agent: codex
  agents: [{name: Opus, command: claude}]`, ""},
		{"name required", `
  agents: [{command: claude}]`, `runbooks.agents[0]: name is required`},
		{"whitespace-only name", `
  agents: [{name: "  ", command: claude}]`, `runbooks.agents[0]: name is required`},
		{"padded name", `
  agents: [{name: " Opus", command: claude}]`, `name cannot start or end with whitespace`},
		{"auto is reserved", `
  agents: [{name: auto, command: claude}]`, `runbooks.agents[0]: "auto" is reserved for the first installed agent`},
		{"claude and codex may be shadowed", `
  agents:
    - {name: claude, command: claude --model opus}
    - {name: codex, command: codex --model x}`, ""},
		{"duplicate name", `
  agents:
    - {name: Opus, command: claude}
    - {name: Opus, command: codex}`, `runbooks.agents[1] "Opus": duplicate name`},
		{"empty command", `
  agents: [{name: Opus, command: "  "}]`, `runbooks.agents[0] "Opus": command is required`},
		{"kind not inferable", `
  agents: [{name: Opus, command: ./run-it.sh}]`, `cannot tell whether "./run-it.sh" starts claude or codex, set kind`},
		{"ollama launch of something else", `
  agents: [{name: Opus, command: ollama launch opencode}]`, `set kind`},
		{"unknown kind", `
  agents: [{name: Opus, command: claude, kind: gemini}]`, `kind "gemini" is not claude or codex`},
		{"unknown agent", `
  agent: Nope
  agents: [{name: Opus, command: claude}]`, `runbooks.agent: "Nope" is not an agent den can start`},
		{"unknown agent without entries", `
  agent: Opus`, `runbooks.agent: "Opus" is not an agent den can start`},
		{"a runbook's agent: a command line", `
  defaults: {agent: ollama launch claude --model glm-5:cloud}
  items: [{name: a, command: x, agent: claude --model opus}]`, ""},
		{"a runbook's agent: a name", `
  agents: [{name: Opus, command: claude --model opus}]
  defaults: {agent: codex}
  items: [{name: a, command: x, agent: Opus}]`, ""},
		{"a runbook's agent den cannot start", `
  items: [{name: a, command: x, agent: ./my-agent.sh}]`, `runbook "a": agent "./my-agent.sh" is no runbooks.agents name and does not start claude or codex`},
		{"the defaults' agent den cannot start", `
  defaults: {agent: Opus}`, `runbooks.defaults: agent "Opus" is no runbooks.agents name`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := load(t, envBlock+"\nrunbooks:"+c.runbooks+"\n")
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v, want none", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, c.wantErr)
			}
		})
	}
}

func TestAgentKind(t *testing.T) {
	for command, want := range map[string]string{
		"claude":                                   "claude",
		"claude --model opus":                      "claude",
		"/usr/local/bin/claude -p":                 "claude",
		`C:\Tools\Claude.exe --model x`:            "claude",
		"codex --model gpt-5.4":                    "codex",
		"ollama launch claude --model glm-5:cloud": "claude",
		"ollama launch codex":                      "codex",
		"/opt/bin/ollama launch codex":             "codex",
		"ollama launch opencode":                   "",
		"ollama launch":                            "",
		"ollama run claude":                        "",
		"./claude-wrapper.sh":                      "",
		"   ":                                      "",
		"":                                         "",
	} {
		if got := AgentKind(command); got != want {
			t.Errorf("AgentKind(%q) = %q, want %q", command, got, want)
		}
	}
}

func TestRunbookSettingsParse(t *testing.T) {
	cfg, err := load(t, envBlock+`
runbooks:
  rescan: false
  template: ~/tpl
  defaults: {history: 5, about: false, shell: bash, editor: nvim}
  items:
    - {name: a, command: x, history: 0, editor: code --wait}
`)
	if err != nil {
		t.Fatal(err)
	}
	rb := cfg.Runbooks
	if rb.RescanOn() || rb.Template != "~/tpl" {
		t.Errorf("rescan/template = %v/%q", rb.RescanOn(), rb.Template)
	}
	d := rb.Defaults
	if *d.History != 5 || *d.About || d.Shell != "bash" || d.Editor != "nvim" {
		t.Errorf("defaults = %+v", d)
	}
	it := rb.Items[0].RunbookSettings
	if it.History == nil || *it.History != 0 || it.Editor != "code --wait" || it.About != nil {
		t.Errorf("item settings = %+v; history 0 must be kept apart from unset", it)
	}
	if !(*RunbooksConfig)(nil).RescanOn() {
		t.Error("rescan is off without a runbooks block, want on")
	}
}

func TestValidateRunbookSources(t *testing.T) {
	const base = `
environments:
  eu-prod:
    region: eu
    environment: prod
    ec2_instance_id: i-0123
    aws_profile: prod
    aws_region_code: eu-central-1
runbooks:
`
	cases := []struct {
		name, runbooks, wantErr string
	}{
		{"valid", `
  allow_sources: ["ghe.acme.com/ops/*"]
  sources:
    - name: platform-ops
      url: git@ghe.acme.com:ops/den-runbooks.git
      ref: v1.4.0
      path: runbooks
      include: ["rds-*"]
      exclude: [old]
      env_map: {prod: eu-prod}`, ""},
		{"name required", `
  sources:
    - url: acme/runbooks`, `runbooks.sources[0]: name is required`},
		{"name shape", `
  sources:
    - name: Platform Ops
      url: acme/runbooks`, `lower case letters, digits and dashes`},
		{"duplicate name", `
  sources:
    - name: ops
      url: acme/runbooks
    - name: ops
      url: acme/other`, `runbooks.sources[1] "ops": duplicate name`},
		{"url required", `
  sources:
    - name: ops`, `runbooks.sources[0] "ops": url is required`},
		{"url with a token", `
  sources:
    - name: ops
      url: https://ghp_x@github.com/acme/runbooks`, `credential helper`},
		{"not allowed", `
  allow_sources: ["ghe.acme.com/ops/*"]
  sources:
    - name: ops
      url: github.com/acme/runbooks`, `github.com/acme/runbooks is not in runbooks.allow_sources`},
		{"ref like an option", `
  sources:
    - name: ops
      url: acme/runbooks
      ref: --upload-pack=x`, `ref "--upload-pack=x" is not a tag, branch or commit name`},
		{"ref climbing", `
  sources:
    - name: ops
      url: acme/runbooks
      ref: a..b`, `is not a tag, branch or commit name`},
		{"path escaping", `
  sources:
    - name: ops
      url: acme/runbooks
      path: ../x`, `path must be a folder inside the repository`},
		{"absolute path", `
  sources:
    - name: ops
      url: acme/runbooks
      path: /etc`, `path must be a folder inside the repository`},
		{"bad pattern", `
  sources:
    - name: ops
      url: acme/runbooks
      include: ["[x"]`, `include: bad pattern "[x"`},
		{"env_map to nothing", `
  sources:
    - name: ops
      url: acme/runbooks
      env_map: {prod: nowhere}`, `env_map.prod: unknown env "nowhere"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := load(t, base+c.runbooks+"\n")
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Fatalf("error = %v, want one containing %q", err, c.wantErr)
			}
		})
	}
}

func TestHeaderTitle(t *testing.T) {
	var nilCfg *Config
	for name, tc := range map[string]struct {
		cfg  *Config
		want string
	}{
		"nil config":   {nilCfg, DefaultTitle},
		"empty title":  {&Config{}, DefaultTitle},
		"blank title":  {&Config{Title: "  \t"}, DefaultTitle},
		"padded title": {&Config{Title: "  Acme  "}, "Acme"},
		"set title":    {&Config{Title: "Acme platform — data access"}, "Acme platform — data access"},
	} {
		if got := tc.cfg.HeaderTitle(); got != tc.want {
			t.Errorf("%s: HeaderTitle() = %q, want %q", name, got, tc.want)
		}
	}
}

func TestValidateTitle(t *testing.T) {
	if err := Validate(&Config{Title: "Acme platform — data access"}); err != nil {
		t.Fatalf("a normal title: %v", err)
	}
	for _, title := range []string{"a\nb", "a\rb", "a\x1b[31mb"} {
		err := Validate(&Config{Title: title})
		if err == nil || !strings.Contains(err.Error(), "title:") {
			t.Errorf("title %q: err = %v, want a title error", title, err)
		}
	}
}

func TestResolveEnvNeedsAnInstanceOnlyForSSM(t *testing.T) {
	for _, tc := range []struct {
		name string
		t    SSMTarget
		ok   bool
	}{
		{"ssm without an instance", SSMTarget{}, false},
		{"ssm with an instance", SSMTarget{EC2InstanceID: "i-0123456789abcdef0"}, true},
		{"ssh", SSMTarget{Transport: &TransportSpec{Type: TransportSSH, Host: "bastion.example.com"}}, true},
		{"eice", SSMTarget{Transport: &TransportSpec{Type: TransportEICE, EndpointID: "eice-1"}}, true},
		{"kubectl", SSMTarget{Transport: &TransportSpec{Type: TransportKubectl, Resource: "svc/db"}}, true},
	} {
		if _, err := tc.t.ResolveEnv(); (err == nil) != tc.ok {
			t.Errorf("%s: ResolveEnv error = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestProduction(t *testing.T) {
	for _, tc := range []struct {
		environment string
		production  bool
		want        bool
	}{
		{"prod", false, true}, {"PRODUCTION", false, true}, {"Prod", false, true},
		{"staging", false, false}, {"eu-live", false, false}, {"", false, false},
		{"eu-live", true, true},
	} {
		target := SSMTarget{Environment: tc.environment, Production: tc.production}
		if got := target.IsProduction(); got != tc.want {
			t.Errorf("environment %q production %v: IsProduction = %v, want %v", tc.environment, tc.production, got, tc.want)
		}
		if got := (EnvSpec{Environment: tc.environment, Production: tc.production}).IsProduction(); got != tc.want {
			t.Errorf("EnvSpec %q production %v: IsProduction = %v, want %v", tc.environment, tc.production, got, tc.want)
		}
	}
}

func TestProductionIsInheritedFromTheEnvironment(t *testing.T) {
	cfg, err := load(t, `
environments:
  live:
    environment: eu-live
    production: true
    ec2_instance_id: i-0123456789abcdef0
    aws_region_code: eu-central-1
services:
  - name: orders
    type: rds
    env: live
    rds:
      rds_host: db1.abcdefghijkl.eu-central-1.rds.amazonaws.com
      db_user: app
`)
	if err != nil {
		t.Fatal(err)
	}
	target := cfg.Services[0].Target()
	env, err := target.ResolveEnv()
	if err != nil || !env.Production {
		t.Fatalf("ResolveEnv = %+v, %v; production must be inherited", env, err)
	}
}

func TestServiceValidationAsksForWhatItNeeds(t *testing.T) {
	const base = `
services:
  - name: orders
    type: rds
    rds:
      ec2_instance_id: i-0123456789abcdef0
      %s
`
	for _, tc := range []struct {
		name, keys, want string
	}{
		{"no rds_host", "aws_region_code: eu-central-1\n      db_user: app", "rds.rds_host is required"},
		{"no region code", "rds_host: db1.example.com\n      db_user: app", "aws_region_code is required"},
	} {
		_, err := load(t, fmt.Sprintf(base, tc.keys))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestCredentialProfileDefaultsToAWSProfile(t *testing.T) {
	for _, tc := range []struct {
		name, keys, want string
	}{
		{"one role", "aws_profile: acme-dev", "acme-dev"},
		{"explicit wins", "aws_profile: acme-dev\n      credential_profile: acme-data", "acme-data"},
		{"credential only", "credential_profile: acme-data", "acme-data"},
		{"neither", "local_port: 50111", ""},
	} {
		cfg, err := load(t, `
services:
  - name: orders
    type: rds
    rds:
      ec2_instance_id: i-0123456789abcdef0
      aws_region_code: eu-central-1
      rds_host: db1.abcdefghijkl.eu-central-1.rds.amazonaws.com
      db_user: app
      `+tc.keys+"\n")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		env, err := cfg.Services[0].Target().ResolveEnv()
		if err != nil || env.CredentialProfile != tc.want {
			t.Errorf("%s: CredentialProfile = %q, %v; want %q", tc.name, env.CredentialProfile, err, tc.want)
		}
	}
}

func TestRDSNeedsADBUser(t *testing.T) {
	for _, engine := range []string{"postgres", "mysql"} {
		_, err := load(t, `
services:
  - name: orders
    type: rds
    rds:
      engine: `+engine+`
      ec2_instance_id: i-0123456789abcdef0
      aws_region_code: eu-central-1
      rds_host: db1.example.com
`)
		if err == nil || !strings.Contains(err.Error(), "rds.db_user is required") {
			t.Errorf("%s: err = %v, want db_user required", engine, err)
		}
	}
}
