package config

import "fmt"

// EnvSpec holds the shared AWS/bastion fields for a named environment group.
type EnvSpec struct {
	Region            string         `yaml:"region,omitempty"`             // a label you choose, e.g. eu or us; shown to people and runbooks, den does not interpret it
	Environment       string         `yaml:"environment,omitempty"`        // a label you choose, e.g. dev, staging or prod; prod and production count as production
	Production        bool           `yaml:"production,omitempty"`         // an agent connects a production service only if mcp.allow_production is set, and then asks the user; implied by environment: prod
	EC2InstanceID     string         `yaml:"ec2_instance_id,omitempty"`    // the bastion instance the SSM transport forwards through
	AWSProfile        string         `yaml:"aws_profile,omitempty"`        // profile the SSM session runs as
	CredentialProfile string         `yaml:"credential_profile,omitempty"` // profile the credential is minted as, and the identity the data store sees; aws_profile when unset
	AWSRegionCode     string         `yaml:"aws_region_code,omitempty"`    // AWS region of the bastion and of the credential calls, e.g. eu-central-1
	Transport         *TransportSpec `yaml:"transport,omitempty"`          // how the network is reached; SSM through ec2_instance_id when unset
}

// Transport types.
const (
	TransportSSM     = "ssm"     // AWS Systems Manager port forwarding through ec2_instance_id (the default)
	TransportSSH     = "ssh"     // ssh -L through a bastion host
	TransportEICE    = "eice"    // an EC2 Instance Connect Endpoint
	TransportKubectl = "kubectl" // kubectl port-forward to an in-cluster resource
)

// TransportSpec says how den reaches the network a service lives in. Each type
// uses its own keys; the others must be left out.
type TransportSpec struct {
	Type string `yaml:"type" jsonschema:"required,enum=ssm,enum=ssh,enum=eice,enum=kubectl"` // ssm (default) | ssh | eice | kubectl

	// ssh: the destination (user@host) and extra ssh arguments such as
	// [-i, ~/.ssh/key, -J, jump.example.com]. Key or agent auth only: den
	// cannot answer a password prompt.
	Host string   `yaml:"host,omitempty"` // ssh: user@host
	Args []string `yaml:"args,omitempty"` // ssh: extra arguments, e.g. [-i, ~/.ssh/key]

	// eice: the EC2 Instance Connect Endpoint ID. The service host is
	// resolved to an IP first, since the endpoint only tunnels to IPs.
	EndpointID string `yaml:"endpoint_id,omitempty"` // eice: the EC2 Instance Connect Endpoint ID

	// kubectl: the resource to forward to (svc/x, deploy/x, pod/x), which
	// must itself serve the service's port — typically a proxy in front of
	// the database — plus an optional context and namespace.
	Resource  string `yaml:"resource,omitempty"`  // kubectl: svc/x, deploy/x or pod/x
	Context   string `yaml:"context,omitempty"`   // kubectl: the kubeconfig context; the current one when empty
	Namespace string `yaml:"namespace,omitempty"` // kubectl: the namespace; the context's when empty
}

// TypeOrDefault is the transport type, SSM when t is nil or unset.
func (t *TransportSpec) TypeOrDefault() string {
	if t == nil || t.Type == "" {
		return TransportSSM
	}
	return t.Type
}

// AWSSpec holds AWS-related configuration paths.
type AWSSpec struct {
	ConfigPath      string `yaml:"config_path,omitempty"`      // default: ~/.aws/config
	CredentialsPath string `yaml:"credentials_path,omitempty"` // default: ~/.aws/credentials

	// Sessions curates which discovered SSO sessions den shows, and in what
	// order. Empty means show every session found in the AWS config.
	Sessions []AWSSessionSpec `yaml:"sessions,omitempty"`
}

// AWSSessionSpec curates one [sso-session ...] discovered in the AWS config. It
// references a session, it cannot define one: `aws sso login --sso-session X`
// resolves X from the AWS config and has no flag for a start URL, so den never
// invents those sections and never writes to that file.
type AWSSessionSpec struct {
	Session     string `yaml:"session" jsonschema:"required"` // must match an [sso-session X] section
	Name        string `yaml:"name,omitempty"`                // display name (default: Session)
	Description string `yaml:"description,omitempty"`         // shown as subtitle
}

// Config is the root structure for the den.yaml config file.
type Config struct {
	// Config format version, "1"
	Version string `yaml:"version"`
	// The dashboard's header line; "den — Developer Environment Navigator" when unset
	Title string `yaml:"title,omitempty"`
	// Where the AWS CLI files are and which SSO sessions to show
	AWS           *AWSSpec `yaml:"aws,omitempty"`
	MenuOrder     []string `yaml:"menu_order,omitempty" jsonschema:"enum=connect,enum=tunnel,enum=runbooks,enum=vpn,enum=aws,enum=secrets,enum=create-env,enum=config,enum=logs,enum=help,enum=updates"`     // panels to show, in this order; every panel when empty
	DisabledPanes []string `yaml:"disabled_panes,omitempty" jsonschema:"enum=connect,enum=tunnel,enum=runbooks,enum=vpn,enum=aws,enum=secrets,enum=create-env,enum=config,enum=logs,enum=help,enum=updates"` // panels to hide
	// Named groups of shared bastion and profile settings; a service picks one with env:
	Environments map[string]EnvSpec `yaml:"environments,omitempty"`
	// The data stores and stacks the Connect panel lists
	Services []ServiceSpec `yaml:"services"`
	// SSH and SOCKS tunnels
	Tunnels []TunnelSpec `yaml:"tunnels,omitempty"`
	// Openfortivpn connections
	VPNs []VPNSpec `yaml:"vpn,omitempty"`
	// Your scripts, run with den's connection context
	Runbooks *RunbooksConfig `yaml:"runbooks,omitempty"`
	// The Secrets panel's sources
	Secrets *SecretsConfig `yaml:"secrets,omitempty"`
	// The log file and level of den itself
	Logs *LogsConfig `yaml:"logs,omitempty"`
	// Where den looks for new releases
	Updates *UpdatesConfig `yaml:"updates,omitempty"`
	// What AI agents may do through den mcp
	MCP *MCPConfig `yaml:"mcp,omitempty"`
	// The model behind den doctor --explain
	AI *AIConfig `yaml:"ai,omitempty"`
}

// AIConfig configures `den doctor --explain`, the only place den calls a
// language model — and only when asked, after showing what it would send.
type AIConfig struct {
	// Model is the Claude model that explains the report (default claude-opus-5).
	Model string `yaml:"model,omitempty"`
}

// MCPConfig limits what an AI agent may do through `den mcp`. Absent, agents
// see and connect every non-production service and run no runbooks.
type MCPConfig struct {
	// Allow lists the services agents may see, by name, with * and ?
	// wildcards; empty means all of them.
	Allow []string `yaml:"allow,omitempty"`
	// AllowProduction lets agents connect production services — and even then
	// only after the user confirms each connect in their MCP client.
	AllowProduction bool `yaml:"allow_production,omitempty"`
	// AllowRunbooks lets agents list and run runbooks; a runbook marked
	// confirm: true still asks the user first.
	AllowRunbooks bool `yaml:"allow_runbooks,omitempty"`
	// AllowSourceRunbooks also lets agents list and run the runbooks of
	// runbooks.sources, and even then each run asks the user first.
	AllowSourceRunbooks bool `yaml:"allow_source_runbooks,omitempty"`
}

// SecretsConfig lists the secret sources shown in the Secrets pane, grouped by
// provider. It holds references only (file paths, profiles), never values, so
// den.yaml stays safe to commit.
type SecretsConfig struct {
	SOPS              []SOPSSecretSpec              `yaml:"sops,omitempty"`               // SOPS-encrypted files
	AWSSecretsManager []AWSSecretsManagerSecretSpec `yaml:"aws_secretsmanager,omitempty"` // AWS Secrets Manager secrets
	KeePassXC         []KeePassXCSecretSpec         `yaml:"keepassxc,omitempty"`          // entries of KeePass databases
	Vault             []VaultSecretSpec             `yaml:"vault,omitempty"`              // HashiCorp Vault KV secrets

	// View is how secrets with nested values open, unless one sets its own.
	View string `yaml:"view,omitempty" jsonschema:"enum=tree,enum=flat"` // tree (default) | flat

	// Other catches provider groups den does not support yet (aws_ssm:, ...), so validation can name them instead of ignoring them silently.
	Other map[string]any `yaml:",inline"`
}

// How a secret with nested values is shown in the Secrets pane.
const (
	SecretViewTree = "tree" // indented like the YAML it came from (the default)
	SecretViewFlat = "flat" // one row per value, keys joined with dots (db.password)
)

// SOPSSecretSpec is one SOPS-encrypted file. den shells out to the sops CLI,
// which already knows how to reach KMS, age or PGP from the file's metadata.
type SOPSSecretSpec struct {
	Name        string `yaml:"name" jsonschema:"required"`                      // shown in the Secrets list
	Description string `yaml:"description,omitempty"`                           // shown as subtitle
	File        string `yaml:"file" jsonschema:"required"`                      // ~ is expanded
	AWSProfile  string `yaml:"aws_profile,omitempty"`                           // exported as AWS_PROFILE for KMS keys
	AWSRegion   string `yaml:"aws_region,omitempty"`                            // exported as AWS_REGION
	AutoReveal  bool   `yaml:"auto_reveal,omitempty"`                           // show every value in clear when the file opens
	View        string `yaml:"view,omitempty" jsonschema:"enum=tree,enum=flat"` // tree | flat; secrets.view when empty
}

// AWSSecretsManagerSecretSpec is one AWS Secrets Manager secret, read with the
// SDK as aws_profile. Read-only: den never writes a secret back.
type AWSSecretsManagerSecretSpec struct {
	Name         string `yaml:"name" jsonschema:"required"`                      // shown in the Secrets list
	Description  string `yaml:"description,omitempty"`                           // shown as subtitle
	SecretID     string `yaml:"secret_id" jsonschema:"required"`                 // name or full ARN
	AWSProfile   string `yaml:"aws_profile,omitempty"`                           // default credential chain when empty
	AWSRegion    string `yaml:"aws_region,omitempty"`                            // the profile's region when empty
	VersionStage string `yaml:"version_stage,omitempty"`                         // AWSCURRENT (default), AWSPREVIOUS, ...
	AutoReveal   bool   `yaml:"auto_reveal,omitempty"`                           // show every value in clear when opened
	View         string `yaml:"view,omitempty" jsonschema:"enum=tree,enum=flat"` // tree | flat; secrets.view when empty
}

// KeePassXCSecretSpec is one entry of a KeePass database, read with
// keepassxc-cli. den asks for the master password and pipes it on stdin; it is
// kept in memory only for RememberPassword, if set. Read-only.
type KeePassXCSecretSpec struct {
	Name             string   `yaml:"name" jsonschema:"required"`     // shown in the Secrets list
	Description      string   `yaml:"description,omitempty"`          // shown as subtitle
	Database         string   `yaml:"database" jsonschema:"required"` // .kdbx, ~ is expanded
	Entry            string   `yaml:"entry,omitempty"`                // group/entry path, e.g. CI/Jenkins; unset = pick from a list
	Group            string   `yaml:"group,omitempty"`                // with no entry: list only entries under this group
	KeyFile          string   `yaml:"key_file,omitempty"`             // ~ is expanded
	NoPassword       bool     `yaml:"no_password,omitempty"`          // key file only; never prompts
	Attributes       []string `yaml:"attributes,omitempty"`           // default UserName, Password, URL, Notes
	TOTP             bool     `yaml:"totp,omitempty"`                 // add the current TOTP code
	RememberPassword string   `yaml:"remember_password,omitempty"`    // Go duration; unset = ask every time
	CLIPath          string   `yaml:"cli_path,omitempty"`             // default: PATH, then the macOS app
	AutoReveal       bool     `yaml:"auto_reveal,omitempty"`          // show every value in clear when opened
}

// VaultSecretSpec is one HashiCorp Vault KV secret, or a folder of them when
// Path ends in "/", read with the vault CLI and the user's own token.
// Read-only.
type VaultSecretSpec struct {
	Name        string `yaml:"name" jsonschema:"required"`                      // shown in the Secrets list
	Description string `yaml:"description,omitempty"`                           // shown as subtitle
	Address     string `yaml:"address,omitempty"`                               // VAULT_ADDR; your environment's when empty
	Namespace   string `yaml:"namespace,omitempty"`                             // VAULT_NAMESPACE (Enterprise / HCP)
	CACert      string `yaml:"ca_cert,omitempty"`                               // VAULT_CACERT, ~ is expanded
	Mount       string `yaml:"mount,omitempty"`                                 // KV mount, default "secret"
	Path        string `yaml:"path" jsonschema:"required"`                      // secret path; a trailing "/" lists a folder
	Field       string `yaml:"field,omitempty"`                                 // one key only
	Version     int    `yaml:"version,omitempty"`                               // KV v2 version; latest when 0
	Login       string `yaml:"login,omitempty"`                                 // shell command run by L, e.g. vault login -method=oidc
	CLIPath     string `yaml:"cli_path,omitempty"`                              // default: vault on PATH
	AutoReveal  bool   `yaml:"auto_reveal,omitempty"`                           // show every value in clear when opened
	View        string `yaml:"view,omitempty" jsonschema:"enum=tree,enum=flat"` // tree | flat; secrets.view when empty
}

// UpdatesConfig controls where den checks for and downloads new releases.
// By default that is the project's release feed on github.com, which needs no
// token and no API call. Set github_api for a fork or a GitHub Enterprise server
// (with GITHUB_TOKEN in the environment when its repository is private), or
// s3_bucket for a mirror you host: S3 is then used for both version checking and
// download.
type UpdatesConfig struct {
	GitHubAPI  string `yaml:"github_api,omitempty"`  // releases/latest API URL of a fork or GitHub Enterprise server, e.g. https://api.github.com/repos/you/den/releases/latest; a fork on api.github.com reads its own release feed, and GITHUB_TOKEN, when set, makes den use the API
	S3Bucket   string `yaml:"s3_bucket,omitempty"`   // an S3 mirror of the releases; wins over github_api
	S3Prefix   string `yaml:"s3_prefix,omitempty"`   // e.g. "den/releases/" — no leading slash
	AWSProfile string `yaml:"aws_profile,omitempty"` // uses default AWS profile if omitted
	AWSRegion  string `yaml:"aws_region,omitempty"`  // e.g. "eu-west-1"
}

// LogsConfig controls file-based logging and log level filtering.
type LogsConfig struct {
	Level        string `yaml:"level,omitempty" jsonschema:"enum=trace,enum=debug,enum=info,enum=warn,enum=error"` // trace, debug, info (default), warn, error
	FileLocation string `yaml:"file_location,omitempty"`                                                           // e.g. ./dist/logs/den.log
}

// TunnelSpec describes one SSH tunnel entry in the config file.
type TunnelSpec struct {
	Name        string `yaml:"name" jsonschema:"required"`    // shown in the Tunnel list
	Description string `yaml:"description,omitempty"`         // shown as subtitle
	Command     string `yaml:"command" jsonschema:"required"` // full shell command, e.g. "ssh -D 5555 -L ... user@host"
	Reconnect   bool   `yaml:"reconnect,omitempty"`           // restart the tunnel after it drops, with backoff
	HealthCheck string `yaml:"health_check,omitempty"`        // host:port dialled through the command's -D SOCKS proxy to confirm the far side is reachable
	Monitor     bool   `yaml:"monitor,omitempty"`             // re-run the connection checks every 15 s while connected; two misses count as a drop
	// AutoConnect ties the tunnel to the VPN it runs over: den connects it once
	// that VPN is connected and stops it when the VPN goes down.
	AutoConnect *TunnelAutoConnect `yaml:"auto_connect,omitempty"`
}

// TunnelAutoConnect names the VPN a tunnel runs over. While enabled, the TUI
// connects the tunnel when that VPN comes up (including a VPN adopted at
// startup) and stops it when the VPN goes down, instead of letting it retry
// against a network it cannot reach. Connecting it by hand always works; den
// only warns when the VPN is down.
type TunnelAutoConnect struct {
	VPN string `yaml:"vpn" jsonschema:"required"` // name of an entry in the vpn: section
	// Enabled switches the behaviour on (the default) or off, keeping the link
	// so den can still warn about a manual connect without the VPN.
	Enabled *bool `yaml:"enabled,omitempty"`
}

// On reports whether auto-connect is enabled: true unless enabled: false.
func (a *TunnelAutoConnect) On() bool {
	return a != nil && (a.Enabled == nil || *a.Enabled)
}

// RunbooksConfig holds user-defined operational scripts. Runbooks are
// deliberately not services: a service is a long-lived connection, whereas a
// runbook runs to completion and its exit code is the result.
type RunbooksConfig struct {
	// Dirs are scanned for runbooks: every folder in them holding a
	// runbook.yaml is one, and so is an executable carrying a "# den:name"
	// header. Relative paths are read against den.yaml's directory.
	Dirs []string `yaml:"dirs,omitempty"`
	// Rescan picks up runbook folders added, changed or removed while den
	// runs. On unless set to false.
	Rescan *bool `yaml:"rescan,omitempty"`
	// Template is a folder copied for a new runbook by `den runbook new` and
	// the Runbooks pane's n; empty uses the built-in one.
	Template string `yaml:"template,omitempty"`
	// Agent is the agent that writes a runbook from a description (`den
	// runbook new --agent`, the pane's n): auto (the first agent CLI
	// installed, in the order agents' kind lists them), a kind such as claude
	// or opencode, or the name of an agents entry. Default auto, or the first
	// entry of agents when there are any.
	Agent string `yaml:"agent,omitempty"`
	// Agents are command lines that write a runbook, for a model other than
	// the CLI's default or a wrapper such as `ollama launch opencode`. The
	// Runbooks pane's n offers them when there are two or more.
	Agents []AgentSpec `yaml:"agents,omitempty"`
	// Defaults apply to every runbook; a runbook.yaml or an items entry
	// overrides them one key at a time.
	Defaults RunbookSettings `yaml:"defaults,omitempty"`
	// Items are runbooks declared inline, for the curated team-shared set.
	Items     []RunbookSpec  `yaml:"items,omitempty"`
	Sequences []SequenceSpec `yaml:"sequences,omitempty"` // runbooks run in order, stopping at the first failure
	// Sources are git repositories of runbook folders that den checks out
	// and lists beside your own, each runbook under its source's name:
	// platform-ops/rds-status. Nothing updates on its own: `den runbook
	// source update` moves a source to the newest commit of its ref.
	Sources []RunbookSource `yaml:"sources,omitempty"`
	// AllowSources limits the repositories a source may come from, as
	// host/owner/repo patterns with * wildcards: "ghe.acme.com/ops/*".
	// Empty allows any.
	AllowSources []string `yaml:"allow_sources,omitempty"`
}

// AgentSpec is one named way to start an agent that writes a runbook.
type AgentSpec struct {
	// Name is what the pop-up shows and what runbooks.agent and `den runbook
	// new --agent` refer to.
	Name string `yaml:"name" jsonschema:"required"`
	// Command is the command line, split on whitespace (no shell quoting):
	// `claude --model opus`, `ollama launch claude --model glm-5:cloud`.
	// den adds its own arguments after it.
	Command string `yaml:"command" jsonschema:"required"`
	// Kind says which agent CLI the command starts, so den passes the
	// arguments that CLI takes: claude (Claude Code), codex (Codex), opencode
	// (OpenCode), copilot (Copilot CLI), qwen (Qwen Code), droid (Droid), pi
	// (Pi) or kimi (Kimi Code). Empty infers it from a command starting with
	// one of them, or `ollama launch <kind>` (copilot-cli for copilot); any
	// other wrapper needs it.
	Kind string `yaml:"kind,omitempty" jsonschema:"enum=claude,enum=codex,enum=opencode,enum=copilot,enum=qwen,enum=droid,enum=pi,enum=kimi"`
}

// RunbookSource is a git repository of runbook folders. Its runbooks run
// code someone else wrote with your AWS credentials, so den asks before the
// first run of each one and again after an update changed it.
type RunbookSource struct {
	// Name prefixes the names of its runbooks: lower case letters, digits
	// and dashes.
	Name string `yaml:"name" jsonschema:"required"`
	// URL is the repository: https://host/owner/repo,
	// git@host:owner/repo.git, host/owner/repo, or owner/repo on GitHub.
	// Credentials come from git (a credential helper or an SSH key), never
	// from the URL.
	URL string `yaml:"url" jsonschema:"required"`
	// Ref is the tag, branch or commit to check out; empty is the
	// repository's default branch. A tag or a commit stays put; a branch
	// moves on `den runbook source update`.
	Ref string `yaml:"ref,omitempty"`
	// Path is the folder of the repository holding the runbook folders;
	// empty is its root.
	Path string `yaml:"path,omitempty"`
	// Include lists the runbook folders to show, with * wildcards; empty
	// shows every one. `den runbook install` and `uninstall` edit it.
	Include []string `yaml:"include,omitempty"`
	// Exclude hides runbook folders Include would show.
	Exclude []string `yaml:"exclude,omitempty"`
	// EnvMap maps the env names the source's runbooks use to entries of
	// environments. A runbook whose env is not mapped is not shown: its
	// author's "prod" is not necessarily yours.
	EnvMap map[string]string `yaml:"env_map,omitempty"`
}

// RescanOn reports whether runbook folders are rescanned while den runs.
func (r *RunbooksConfig) RescanOn() bool {
	return r == nil || r.Rescan == nil || *r.Rescan
}

// RunbookSettings are how den presents a runbook rather than what it runs.
// They can be set for every runbook in runbooks.defaults and for one in its
// runbook.yaml or items entry.
type RunbookSettings struct {
	// History is how many finished runs are kept on disk; 0 keeps only the
	// last run, in memory. Default 20.
	History *int `yaml:"history,omitempty"`
	// About shows the About tab: the folder's README.md, the environment den
	// sets and the parameters. Default true.
	About *bool `yaml:"about,omitempty"`
	// Shell is the command t opens in the runbook's folder, e.g.
	// "bash --rcfile .venv/bin/activate". Default $SHELL.
	Shell string `yaml:"shell,omitempty"`
	// Editor is the command e opens the runbook's script with, and o a run's
	// log. Default $VISUAL, then $EDITOR, then vi.
	Editor string `yaml:"editor,omitempty"`
	// Agent is the agent E changes the runbook with: a command line such as
	// `claude --model opus`, the name of a runbooks.agents entry, a kind such
	// as claude or opencode, or auto. Default runbooks.agent.
	Agent string `yaml:"agent,omitempty"`
}

// RunbookSpec describes one runbook. Command is executed verbatim via `sh -c`,
// so any interpreter works without den knowing about it: "python3 x.py",
// "go run ./cmd/x", or a shebang script invoked by path.
type RunbookSpec struct {
	Name        string            `yaml:"name" jsonschema:"required"`    // what the Runbooks list shows and sequences refer to
	Description string            `yaml:"description,omitempty"`         // shown as subtitle
	Command     string            `yaml:"command" jsonschema:"required"` // run with sh -c
	Cwd         string            `yaml:"cwd,omitempty"`                 // working directory; relative to den.yaml's directory
	Env         string            `yaml:"env,omitempty"`                 // key into Config.Environments
	EnvVars     map[string]string `yaml:"env_vars,omitempty"`            // extra environment variables for the run
	Params      []ParamSpec       `yaml:"params,omitempty"`              // values asked for before the run
	Confirm     bool              `yaml:"confirm,omitempty"`             // ask before every run, also when an agent starts it
	Timeout     string            `yaml:"timeout,omitempty"`             // Go duration, e.g. "5m"
	Retry       *RetrySpec        `yaml:"retry,omitempty"`               // run again until it exits 0

	RunbookSettings `yaml:",inline"`

	// Source is the file a discovered runbook came from: its runbook.yaml or
	// its script; empty for inline items. Set during discovery, not parsed
	// from YAML.
	Source string `yaml:"-"`
	// Dir is the folder of a runbook defined by a runbook.yaml.
	Dir string `yaml:"-"`
}

// ParamSpec is one value prompted for before a runbook runs. It reaches the
// script as DEN_PARAM_<UPPERCASED_NAME>.
type ParamSpec struct {
	Name        string   `yaml:"name" json:"name"`                                   // the parameter's name; DEN_PARAM_ plus this, in capitals
	Description string   `yaml:"description,omitempty" json:"description,omitempty"` // shown beside the prompt
	Default     string   `yaml:"default,omitempty" json:"default,omitempty"`         // pre-filled value
	Options     []string `yaml:"options,omitempty" json:"options,omitempty"`         // non-empty renders a select
	Required    bool     `yaml:"required,omitempty" json:"required,omitempty"`       // the prompt does not accept an empty value
	Secret      bool     `yaml:"secret,omitempty" json:"secret,omitempty"`           // masked input, redacted from logs
}

// RetrySpec re-runs a runbook until it exits 0. This is deliberately the only
// control-flow den offers: everything richer belongs inside the script, where a
// real language is available.
type RetrySpec struct {
	UntilSuccess bool   `yaml:"until_success"`                  // run again until the exit code is 0; needs max_attempts or timeout
	Interval     string `yaml:"interval" jsonschema:"required"` // Go duration between attempts
	MaxAttempts  int    `yaml:"max_attempts,omitempty"`         // 0 = unlimited
	Timeout      string `yaml:"timeout,omitempty"`              // overall ceiling across attempts
}

// SequenceSpec runs named runbooks in order, stopping at the first failure.
type SequenceSpec struct {
	Name        string   `yaml:"name" jsonschema:"required"`  // shown in the Runbooks list
	Description string   `yaml:"description,omitempty"`       // shown as subtitle
	Steps       []string `yaml:"steps" jsonschema:"required"` // runbook names
	Confirm     bool     `yaml:"confirm,omitempty"`           // ask before running the sequence
}

// ServiceSpec describes one service entry in the config file.
type ServiceSpec struct {
	Name       string          `yaml:"name" jsonschema:"required"`                                                                                                       // shown in the Connect list
	Type       ServiceType     `yaml:"type" jsonschema:"required,enum=rds,enum=redis,enum=documentdb,enum=redshift,enum=opensearch,enum=neptune,enum=kafka,enum=docker"` // which section below holds its settings
	Env        string          `yaml:"env,omitempty"`                                                                                                                    // an entry of environments: its keys fill what the section leaves out
	RDS        *RDSSpec        `yaml:"rds,omitempty"`                                                                                                                    // type: rds — RDS or Aurora
	Kafka      *KafkaSpec      `yaml:"kafka,omitempty"`                                                                                                                  // type: kafka — Kafka (planned, not implemented)
	Redis      *RedisSpec      `yaml:"redis,omitempty"`                                                                                                                  // type: redis — ElastiCache or MemoryDB
	DocumentDB *DocumentDBSpec `yaml:"documentdb,omitempty"`                                                                                                             // type: documentdb — DocumentDB
	Redshift   *RedshiftSpec   `yaml:"redshift,omitempty"`                                                                                                               // type: redshift — Redshift
	OpenSearch *OpenSearchSpec `yaml:"opensearch,omitempty"`                                                                                                             // type: opensearch — OpenSearch
	Neptune    *NeptuneSpec    `yaml:"neptune,omitempty"`                                                                                                                // type: neptune — Neptune
	Docker     *DockerSpec     `yaml:"docker,omitempty"`                                                                                                                 // type: docker — Docker Compose
}

// ServiceType enumerates supported service types.
type ServiceType string

const (
	ServiceTypeRDS        ServiceType = "rds"
	ServiceTypeKafka      ServiceType = "kafka"
	ServiceTypeRedis      ServiceType = "redis"
	ServiceTypeDocumentDB ServiceType = "documentdb"
	ServiceTypeRedshift   ServiceType = "redshift"
	ServiceTypeOpenSearch ServiceType = "opensearch"
	ServiceTypeNeptune    ServiceType = "neptune"
	ServiceTypeDocker     ServiceType = "docker"
)

// SSMTarget is the connection context every SSM-forwarded service shares: which
// bastion to forward through, as whom, and where the tunnel lands locally. It is
// embedded inline, so its keys sit directly under the service's type section.
type SSMTarget struct {
	Region      string `yaml:"region,omitempty"`      // a label you choose, e.g. eu or us; shown to people and runbooks, den does not interpret it
	Environment string `yaml:"environment,omitempty"` // a label you choose, e.g. dev, staging or prod; prod and production count as production
	Production  bool   `yaml:"production,omitempty"`  // an agent connects a production service only if mcp.allow_production is set, and then asks the user; implied by environment: prod
	LocalPort   int    `yaml:"local_port,omitempty"`  // local end of the tunnel; each type has its own default (RDS 50432, Redshift 55439, Redis 16379, DocumentDB 27017, OpenSearch 59200, Neptune 58182)
	Reconnect   bool   `yaml:"reconnect,omitempty"`   // auto-reopen the SSM port-forward if it drops (e.g. SSM idle timeout); the IAM token is renewed while connected either way

	// Where and as whom (usually inherited from the named environment)

	EC2InstanceID     string         `yaml:"ec2_instance_id,omitempty"`    // the bastion instance the SSM transport forwards through
	AWSProfile        string         `yaml:"aws_profile,omitempty"`        // profile the SSM session runs as
	CredentialProfile string         `yaml:"credential_profile,omitempty"` // profile credentials are minted as, and the identity the data store sees; aws_profile when unset
	AWSRegionCode     string         `yaml:"aws_region_code,omitempty"`    // AWS region of the bastion and of the credential calls
	Transport         *TransportSpec `yaml:"transport,omitempty"`          // how the network is reached; SSM when unset
}

// ResolveEnv returns the AWS side of the target: the bastion, the profiles and
// the region its own fields name. Only an SSM forward needs a bastion instance;
// the other transports reach the network their own way.
func (t *SSMTarget) ResolveEnv() (EnvMapping, error) {
	if t.EC2InstanceID == "" && t.Transport.TypeOrDefault() == TransportSSM {
		return EnvMapping{}, fmt.Errorf("ec2_instance_id is required for the ssm transport")
	}
	// One role for the tunnel and the credential is the common case, so it
	// takes one key.
	credential := t.CredentialProfile
	if credential == "" {
		credential = t.AWSProfile
	}
	return EnvMapping{
		EC2InstanceID:     t.EC2InstanceID,
		AWSProfile:        t.AWSProfile,
		CredentialProfile: credential,
		AWSRegionCode:     t.AWSRegionCode,
		Transport:         t.Transport,
		Production:        t.IsProduction(),
	}, nil
}

// IsProduction reports whether an agent must ask before connecting the target:
// production: true, or an environment label of prod or production.
func (t *SSMTarget) IsProduction() bool {
	return t.Production || isProductionLabel(t.Environment)
}

// IsProduction is the same for a named environment, as the runbook guide lists it.
func (e EnvSpec) IsProduction() bool {
	return e.Production || isProductionLabel(e.Environment)
}

// RDS database engines. Aurora and RDS Proxy use the same values as the
// engine they are compatible with.
const (
	RDSEnginePostgres = "postgres"
	RDSEngineMySQL    = "mysql" // also MariaDB and Aurora MySQL
)

// RDSSpec holds RDS-specific config.
type RDSSpec struct {
	SSMTarget    `yaml:",inline"`
	Engine       string `yaml:"engine,omitempty" jsonschema:"enum=postgres,enum=mysql"` // postgres (default) | mysql
	UpdatePgpass bool   `yaml:"update_pgpass,omitempty"`                                // postgres only

	RDSHost string `yaml:"rds_host,omitempty"` // endpoint to forward to: instance, cluster or RDS Proxy
	DBName  string `yaml:"db_name,omitempty"`  // optional; the client's own default (PostgreSQL: the user name) when empty
	DBUser  string `yaml:"db_user,omitempty"`  // the database user the IAM token is minted for
}

// KafkaSpec holds Kafka-specific config.
type KafkaSpec struct {
	SSMTarget `yaml:",inline"`

	KafkaHost string `yaml:"kafka_host,omitempty"` // the broker to forward to (connecting reports that Kafka is planned)
}

// RedisSpec holds Redis-specific config.
type RedisSpec struct {
	SSMTarget `yaml:",inline"`

	// ElastiCache / MemoryDB IAM auth
	CacheName  string `yaml:"cache_name,omitempty"` // replication group ID, serverless cache name or MemoryDB cluster name
	UserID     string `yaml:"user_id,omitempty"`    // ElastiCache / MemoryDB user ID
	Serverless bool   `yaml:"serverless,omitempty"` // true for ElastiCache Serverless
	MemoryDB   bool   `yaml:"memorydb,omitempty"`   // true for MemoryDB (tokens are signed for memorydb, not elasticache)

	RedisHost string `yaml:"redis_host,omitempty"` // endpoint to forward to
}

// DocumentDBSpec holds Amazon DocumentDB-specific config.
//
// DocumentDB IAM auth has no token-generation step: the driver signs the auth
// handshake with whatever the AWS credential provider chain resolves, and the
// cluster calls sts:GetCallerIdentity to identify the caller. So there is no
// token TTL and no refresh loop here — unlike RDS and ElastiCache. CredentialProfile
// is the identity mongosh authenticates as; AWSProfile only opens the SSM tunnel.
type DocumentDBSpec struct {
	SSMTarget `yaml:",inline"`

	DocumentDBHost string `yaml:"documentdb_host,omitempty"` // cluster endpoint
	DBName         string `yaml:"db_name,omitempty"`         // optional, placed in the URI path
	CAFile         string `yaml:"ca_file,omitempty"`         // optional, defaults to ~/.config/den/global-bundle.pem
}

// RedshiftSpec holds Amazon Redshift config. Exactly one of ClusterIdentifier
// (provisioned) and WorkgroupName (Serverless) selects which API mints the
// temporary database credentials.
type RedshiftSpec struct {
	SSMTarget    `yaml:",inline"`
	UpdatePgpass bool `yaml:"update_pgpass,omitempty"` // keep ~/.pgpass in sync with the current credential

	RedshiftHost      string `yaml:"redshift_host,omitempty"`      // cluster or workgroup endpoint
	ClusterIdentifier string `yaml:"cluster_identifier,omitempty"` // provisioned
	WorkgroupName     string `yaml:"workgroup_name,omitempty"`     // Serverless
	DBName            string `yaml:"db_name,omitempty"`            // default "dev"
	// DBUser is provisioned-only: set, den calls GetClusterCredentials for that
	// user; unset, GetClusterCredentialsWithIAM maps the caller's IAM identity.
	DBUser        string `yaml:"db_user,omitempty"`
	CredentialTTL string `yaml:"credential_ttl,omitempty"` // Go duration, 15m–1h (default 15m)
}

// OpenSearchSpec holds Amazon OpenSearch Service config: a VPC domain, or an
// OpenSearch Serverless collection reached through its VPC endpoint.
type OpenSearchSpec struct {
	SSMTarget `yaml:",inline"`

	OpenSearchHost string `yaml:"opensearch_host,omitempty"` // domain or collection endpoint
	Serverless     bool   `yaml:"serverless,omitempty"`      // collection: requests are signed for aoss, not es
}

// NeptuneSpec holds Amazon Neptune config. The cluster must have IAM database
// authentication enabled.
type NeptuneSpec struct {
	SSMTarget `yaml:",inline"`

	NeptuneHost string `yaml:"neptune_host,omitempty"` // cluster, reader or instance endpoint
	Port        int    `yaml:"port,omitempty"`         // cluster port (default 8182)
}

// DockerSpec holds docker-compose-specific config.
type DockerSpec struct {
	ComposeFile string   `yaml:"compose_file" jsonschema:"required"` // the Compose file, relative to where den runs or absolute
	Services    []string `yaml:"services,omitempty"`                 // the Compose services to start; every one when empty
	EnvFile     string   `yaml:"env_file,omitempty"`                 // passed to docker compose with --env-file
}

// VPNSpec describes one VPN entry in the config file. Top-level alongside tunnels.
// Connection mode: set either gateway (direct) or config_file (INI config), or both
// (gateway as positional arg supplemented by the config file). At least one is required.
type VPNSpec struct {
	Name        string   `yaml:"name" jsonschema:"required"` // shown in the VPN list
	Gateway     string   `yaml:"gateway,omitempty"`          // gateway host[:port]; required unless config_file is set
	Description string   `yaml:"description,omitempty"`      // shown as subtitle
	ConfigFile  string   `yaml:"config_file,omitempty"`      // path to openfortivpn INI config file (-c flag)
	ExtraArgs   []string `yaml:"extra_args,omitempty"`       // more openfortivpn arguments
	HealthCheck string   `yaml:"health_check,omitempty"`     // host:port reachable ONLY via the VPN; probed to confirm the data path is alive
	// Reconnect re-establishes a tunnel that dropped after being fully connected.
	// Deliberately unlike tunnels: openfortivpn logs in through SAML, which opens a
	// browser tab, so retries are capped (see reconnectDelays) and a login that
	// times out is never retried — repeated half-finished logins burn the account's
	// FortiGate session slots until the gateway 403s every new login.
	Reconnect bool `yaml:"reconnect,omitempty"`

	// Dir is the folder of the den.yaml this entry came from, the base a
	// relative config_file is read against. Set on load, not parsed from YAML.
	Dir string `yaml:"-"`
}

// ConfigFilePath is the config_file den hands to openfortivpn: ~ expanded, and
// a relative path read against den.yaml's folder when the file is there, else
// as written, from the folder den runs in. The folder den happens to be
// started in must not decide which file a VPN uses.
func (v VPNSpec) ConfigFilePath() string {
	if v.ConfigFile == "" {
		return ""
	}
	return pathUse{raw: v.ConfigFile, home: true, base: v.Dir, beside: true}.resolve()
}

// Resolve merges named environment defaults into each service spec.
// Service-level explicit values take precedence over env defaults.
func (c *Config) Resolve() error {
	for i, svc := range c.Services {
		if svc.Env == "" {
			continue
		}
		env, ok := c.Environments[svc.Env]
		if !ok {
			return fmt.Errorf("service[%d] %q: unknown env %q", i, svc.Name, svc.Env)
		}
		if t := svc.Target(); t != nil {
			applyEnv(env, t)
		}
		c.Services[i] = svc
	}
	// Runbooks intentionally have nothing to merge: their env: key is resolved
	// at run time into environment variables, not folded into the spec. It is
	// checked in Validate instead.
	return nil
}

// RunbookItems returns the inline runbook items, or nil when none are declared.
// Discovered runbooks are merged in later, by the runbook package.
func (c *Config) RunbookItems() []RunbookSpec {
	if c.Runbooks == nil {
		return nil
	}
	return c.Runbooks.Items
}

// RunbookSequences returns the declared sequences, or nil when none.
func (c *Config) RunbookSequences() []SequenceSpec {
	if c.Runbooks == nil {
		return nil
	}
	return c.Runbooks.Sequences
}

// RunbookSources returns the runbook sources, or nil when none are declared.
func (c *Config) RunbookSources() []RunbookSource {
	if c.Runbooks == nil {
		return nil
	}
	return c.Runbooks.Sources
}

// RunbookDirs returns the directories to scan for discovered runbooks.
func (c *Config) RunbookDirs() []string {
	if c.Runbooks == nil {
		return nil
	}
	return c.Runbooks.Dirs
}

// Target returns the SSM connection context of whichever type section is set,
// or nil for services that are not reached through an SSM forward.
func (s *ServiceSpec) Target() *SSMTarget {
	switch {
	case s.RDS != nil:
		return &s.RDS.SSMTarget
	case s.Redis != nil:
		return &s.Redis.SSMTarget
	case s.DocumentDB != nil:
		return &s.DocumentDB.SSMTarget
	case s.Kafka != nil:
		return &s.Kafka.SSMTarget
	case s.Redshift != nil:
		return &s.Redshift.SSMTarget
	case s.OpenSearch != nil:
		return &s.OpenSearch.SSMTarget
	case s.Neptune != nil:
		return &s.Neptune.SSMTarget
	}
	return nil
}

// applyEnv fills every field the target leaves empty from its named environment.
func applyEnv(env EnvSpec, t *SSMTarget) {
	if t.Region == "" {
		t.Region = env.Region
	}
	if t.Environment == "" {
		t.Environment = env.Environment
	}
	if !t.Production {
		t.Production = env.Production
	}
	if t.EC2InstanceID == "" {
		t.EC2InstanceID = env.EC2InstanceID
	}
	if t.AWSProfile == "" {
		t.AWSProfile = env.AWSProfile
	}
	if t.CredentialProfile == "" {
		t.CredentialProfile = env.CredentialProfile
	}
	if t.AWSRegionCode == "" {
		t.AWSRegionCode = env.AWSRegionCode
	}
	if t.Transport == nil {
		t.Transport = env.Transport
	}
}
