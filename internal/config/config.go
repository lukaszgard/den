package config

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/lukaszgard/den/internal/platform"
)

// DefaultTitle is the dashboard's header line when den.yaml sets no title.
const DefaultTitle = "den — Developer Environment Navigator"

// DefaultLocalPort is the local end of the tunnel a service of this type gets
// when it sets no local_port; 0 for a type that is not forwarded. Each type has
// its own, so two services of different types never collide by default.
func DefaultLocalPort(t ServiceType) int {
	switch t {
	case ServiceTypeRDS:
		return 50432
	case ServiceTypeRedshift:
		return 55439
	case ServiceTypeRedis:
		return 16379
	case ServiceTypeDocumentDB:
		return 27017
	case ServiceTypeOpenSearch:
		return 59200
	case ServiceTypeNeptune:
		return 58182
	case ServiceTypeKafka:
		return 19092
	}
	return 0
}

// Where Discover found a config file, for the messages that say so.
const (
	SourceFlag    = "the -c flag"
	SourceCwd     = "the current directory"
	SourceUser    = "the config directory"
	SourceLegacy  = "~/.config/den (the pre-XDG location)"
	sourceMissing = ""
)

// Candidate is one place Discover looks for den.yaml.
type Candidate struct {
	Path   string
	Source string
}

// SearchOrder lists the places Discover tries when no -c is given, in order:
// ./den.yaml, then platform.ConfigDir()/den.yaml, then ~/.config/den/den.yaml
// where that differs (a setup made before den honoured XDG_CONFIG_HOME and
// APPDATA keeps loading).
func SearchOrder() []Candidate {
	var out []Candidate
	if cwd, err := os.Getwd(); err == nil {
		out = append(out, Candidate{filepath.Join(cwd, "den.yaml"), SourceCwd})
	}
	dir := platform.ConfigDir()
	if dir != "" {
		out = append(out, Candidate{filepath.Join(dir, "den.yaml"), SourceUser})
	}
	if legacy := platform.LegacyConfigDir(os.UserHomeDir); legacy != "" && legacy != dir {
		out = append(out, Candidate{filepath.Join(legacy, "den.yaml"), SourceLegacy})
	}
	return out
}

// Discover finds the config file using the discovery order:
// 1. explicit path (if non-empty) — an error when it does not exist, rather
// than quietly falling back to another file the user did not ask for
// 2. den.yaml in current working directory
// 3. den.yaml in platform.ConfigDir (~/.config/den by default)
// 4. ~/.config/den/den.yaml, when ConfigDir is somewhere else
func Discover(explicit string) (string, error) {
	path, _, err := DiscoverSource(explicit)
	return path, err
}

// DiscoverSource is Discover that also says where the file came from, one of
// the Source constants.
func DiscoverSource(explicit string) (path, source string, err error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", sourceMissing, fmt.Errorf("config file %s: %w", explicit, err)
		}
		return explicit, SourceFlag, nil
	}
	order := SearchOrder()
	for _, c := range order {
		if _, err := os.Stat(c.Path); err == nil {
			return c.Path, c.Source, nil
		}
	}
	tried := make([]string, 0, len(order))
	for _, c := range order {
		tried = append(tried, c.Path)
	}
	return "", sourceMissing, fmt.Errorf("no config file found (tried %s)", strings.Join(tried, ", "))
}

// Load reads and parses a config file from the given path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.setBaseDir(configDir(path))
	return cfg, nil
}

// configDir is the absolute folder of the config file at path.
func configDir(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Dir(path)
}

// setBaseDir tells the entries that read relative paths against den.yaml where
// it is.
func (c *Config) setBaseDir(dir string) {
	for i := range c.VPNs {
		c.VPNs[i].Dir = dir
	}
}

// parse decodes a config file's contents and resolves environments and
// defaults.
func parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Resolve(); err != nil {
		return nil, fmt.Errorf("resolve config: %w", err)
	}
	applyDefaults(&cfg)
	return &cfg, nil
}

// Validate checks the config for logical errors. It reports every entry that
// has one, joined, so a mistake does not hide the next behind it; within an
// entry it stops at the first, since a later check may lean on an earlier one
// (a section that is not there).
func Validate(cfg *Config) error {
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	// A header is one terminal row: a second line would push the frame past
	// the terminal height and cut off its top.
	if strings.ContainsFunc(cfg.Title, unicode.IsControl) {
		add(errors.New("title: must be one line without control characters"))
	}
	for i, svc := range cfg.Services {
		add(validateService(i, svc))
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Environments)) {
		if err := validateTransport(cfg.Environments[name].Transport); err != nil {
			add(fmt.Errorf("environments.%s: %w", name, err))
		}
	}
	// Curated SSO sessions. Whether the named session actually exists is NOT
	// checked here: that lives in ~/.aws/config, outside den's control, and a
	// missing or unreadable AWS config must not stop den from starting. An
	// unknown name is reported by ResolveSSOSessions instead.
	if cfg.AWS != nil {
		seenSession := make(map[string]int, len(cfg.AWS.Sessions))
		for i, s := range cfg.AWS.Sessions {
			if s.Session == "" {
				add(fmt.Errorf("aws.sessions[%d]: session is required", i))
				continue
			}
			if first, dup := seenSession[s.Session]; dup {
				add(fmt.Errorf("aws.sessions[%d]: session %q already listed at aws.sessions[%d]", i, s.Session, first))
				continue
			}
			seenSession[s.Session] = i
		}
	}
	for i, v := range cfg.VPNs {
		if v.Name == "" {
			add(fmt.Errorf("vpn[%d]: name is required", i))
		} else if v.Gateway == "" && v.ConfigFile == "" {
			add(fmt.Errorf("vpn[%d] %q: gateway or config_file is required", i, v.Name))
		}
	}
	for i, t := range cfg.Tunnels {
		add(validateTunnel(i, t, cfg.VPNs))
	}
	errs = append(errs, validateRunbooks(cfg)...)
	errs = append(errs, validateSecrets(cfg.Secrets)...)
	return errors.Join(errs...)
}

// validateService checks one services: entry.
func validateService(i int, svc ServiceSpec) error {
	// Services are addressed by name (den connect, auto_connect, den mcp), and
	// the schema marks it required.
	if strings.TrimSpace(svc.Name) == "" {
		return fmt.Errorf("service[%d]: name is required", i)
	}
	switch svc.Type {
	case ServiceTypeRDS:
		if svc.RDS == nil {
			return fmt.Errorf("service[%d] %q: type=rds but no rds section", i, svc.Name)
		}
		if svc.RDS.RDSHost == "" {
			return fmt.Errorf("service[%d] %q: rds.rds_host is required", i, svc.Name)
		}
		if svc.RDS.DBUser == "" {
			return fmt.Errorf("service[%d] %q: rds.db_user is required", i, svc.Name)
		}
		switch svc.RDS.Engine {
		case RDSEnginePostgres, RDSEngineMySQL:
		default:
			return fmt.Errorf("service[%d] %q: rds.engine must be %q or %q, got %q",
				i, svc.Name, RDSEnginePostgres, RDSEngineMySQL, svc.RDS.Engine)
		}
	case ServiceTypeKafka:
		if svc.Kafka == nil {
			return fmt.Errorf("service[%d] %q: type=kafka but no kafka section", i, svc.Name)
		}
	case ServiceTypeRedis:
		if svc.Redis == nil {
			return fmt.Errorf("service[%d] %q: type=redis but no redis section", i, svc.Name)
		}
		if svc.Redis.MemoryDB && svc.Redis.Serverless {
			return fmt.Errorf("service[%d] %q: redis.memorydb and redis.serverless are mutually exclusive", i, svc.Name)
		}
	case ServiceTypeDocumentDB:
		// Catches the easy mistake of nesting DocumentDB settings under a
		// redis: key — without this the service registers with a nil spec.
		if svc.DocumentDB == nil {
			return fmt.Errorf("service[%d] %q: type=documentdb but no documentdb section", i, svc.Name)
		}
		if svc.DocumentDB.DocumentDBHost == "" {
			return fmt.Errorf("service[%d] %q: documentdb.documentdb_host is required", i, svc.Name)
		}
	case ServiceTypeRedshift:
		if err := validateRedshift(svc.Redshift); err != nil {
			return fmt.Errorf("service[%d] %q: %w", i, svc.Name, err)
		}
	case ServiceTypeOpenSearch:
		if svc.OpenSearch == nil {
			return fmt.Errorf("service[%d] %q: type=opensearch but no opensearch section", i, svc.Name)
		}
		if svc.OpenSearch.OpenSearchHost == "" {
			return fmt.Errorf("service[%d] %q: opensearch.opensearch_host is required", i, svc.Name)
		}
	case ServiceTypeNeptune:
		if svc.Neptune == nil {
			return fmt.Errorf("service[%d] %q: type=neptune but no neptune section", i, svc.Name)
		}
		if svc.Neptune.NeptuneHost == "" {
			return fmt.Errorf("service[%d] %q: neptune.neptune_host is required", i, svc.Name)
		}
	case ServiceTypeDocker:
		if svc.Docker == nil {
			return fmt.Errorf("service[%d] %q: type=docker but no docker section", i, svc.Name)
		}
		if svc.Docker.ComposeFile == "" {
			return fmt.Errorf("service[%d] %q: docker.compose_file is required", i, svc.Name)
		}
	default:
		return fmt.Errorf("service[%d] %q: unknown type %q", i, svc.Name, svc.Type)
	}
	if t := svc.Target(); t != nil {
		if err := validateTransport(t.Transport); err != nil {
			return fmt.Errorf("service[%d] %q: %w", i, svc.Name, err)
		}
		if _, err := t.ResolveEnv(); err != nil {
			return fmt.Errorf("service[%d] %q: %w", i, svc.Name, err)
		}
		// Kafka only reports that it is planned; every other type calls AWS
		// for its credential or its tunnel.
		if t.AWSRegionCode == "" && svc.Type != ServiceTypeKafka {
			return fmt.Errorf("service[%d] %q: aws_region_code is required", i, svc.Name)
		}
	}
	return nil
}

// validateTunnel checks one tunnels: entry. Tunnels were historically never
// validated, so a typo'd entry silently did nothing at all.
func validateTunnel(i int, t TunnelSpec, vpns []VPNSpec) error {
	if t.Name == "" {
		return fmt.Errorf("tunnel[%d]: name is required", i)
	}
	if t.Command == "" {
		return fmt.Errorf("tunnel[%d] %q: command is required", i, t.Name)
	}
	if t.HealthCheck != "" {
		if host, port, err := net.SplitHostPort(t.HealthCheck); err != nil || host == "" || port == "" {
			return fmt.Errorf("tunnel[%d] %q: health_check must be host:port, got %q", i, t.Name, t.HealthCheck)
		}
	}
	if err := validateAutoConnect(t.AutoConnect, vpns); err != nil {
		return fmt.Errorf("tunnel[%d] %q: %w", i, t.Name, err)
	}
	return nil
}

// validateAutoConnect checks that a tunnel's auto_connect names a configured
// VPN: a typo would otherwise leave the tunnel waiting for a VPN that can
// never come up.
func validateAutoConnect(a *TunnelAutoConnect, vpns []VPNSpec) error {
	if a == nil {
		return nil
	}
	names := make([]string, len(vpns))
	for i, v := range vpns {
		if v.Name == a.VPN {
			return nil
		}
		names[i] = fmt.Sprintf("%q", v.Name)
	}
	switch {
	case a.VPN == "":
		return errors.New("auto_connect.vpn is required")
	case len(names) == 0:
		return fmt.Errorf("auto_connect.vpn %q: no vpn: entries are configured", a.VPN)
	}
	return fmt.Errorf("auto_connect.vpn %q is not a configured VPN (have %s)", a.VPN, strings.Join(names, ", "))
}

// validSecretView reports whether v is a view: value; empty means inherited.
func validSecretView(v string) bool {
	return v == "" || v == SecretViewTree || v == SecretViewFlat
}

// validateSecrets checks the secrets: block. Whether the file exists or the
// sops binary is installed is not checked here: that is shown per item in the
// Secrets pane, and a missing file must not stop den from starting.
func validateSecrets(s *SecretsConfig) []error {
	if s == nil {
		return nil
	}
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	for _, group := range slices.Sorted(maps.Keys(s.Other)) {
		add(fmt.Errorf("secrets.%s: unsupported provider (supported: sops, aws_secretsmanager, keepassxc, vault)", group))
	}
	if !validSecretView(s.View) {
		add(fmt.Errorf("secrets.view: must be %s or %s, got %q", SecretViewTree, SecretViewFlat, s.View))
	}
	checkView := func(where, name, view string) error {
		if !validSecretView(view) {
			return fmt.Errorf("%s %q: view must be %s or %s, got %q", where, name, SecretViewTree, SecretViewFlat, view)
		}
		return nil
	}
	// Names are unique across groups: the pane and its search treat them as one list.
	seen := map[string]string{}
	checkName := func(where, name string) error {
		if name == "" {
			return fmt.Errorf("%s: name is required", where)
		}
		if first, dup := seen[name]; dup {
			return fmt.Errorf("%s %q: duplicate name, already used by %s", where, name, first)
		}
		seen[name] = where
		return nil
	}
	for i, it := range s.SOPS {
		where := fmt.Sprintf("secrets.sops[%d]", i)
		add(func() error {
			if err := checkName(where, it.Name); err != nil {
				return err
			}
			if it.File == "" {
				return fmt.Errorf("%s %q: file is required", where, it.Name)
			}
			return checkView(where, it.Name, it.View)
		}())
	}
	for i, it := range s.AWSSecretsManager {
		where := fmt.Sprintf("secrets.aws_secretsmanager[%d]", i)
		add(func() error {
			if err := checkName(where, it.Name); err != nil {
				return err
			}
			if it.SecretID == "" {
				return fmt.Errorf("%s %q: secret_id is required", where, it.Name)
			}
			return checkView(where, it.Name, it.View)
		}())
	}
	for i, it := range s.Vault {
		where := fmt.Sprintf("secrets.vault[%d]", i)
		add(func() error {
			if err := checkName(where, it.Name); err != nil {
				return err
			}
			if it.Path == "" {
				return fmt.Errorf("%s %q: path is required", where, it.Name)
			}
			folder := strings.HasSuffix(it.Path, "/")
			if folder && (it.Field != "" || it.Version != 0) {
				return fmt.Errorf("%s %q: field and version apply to one secret, not a folder path ending in /", where, it.Name)
			}
			if it.Version < 0 {
				return fmt.Errorf("%s %q: version must be 1 or more", where, it.Name)
			}
			return checkView(where, it.Name, it.View)
		}())
	}
	for i, it := range s.KeePassXC {
		where := fmt.Sprintf("secrets.keepassxc[%d]", i)
		add(func() error {
			if err := checkName(where, it.Name); err != nil {
				return err
			}
			if it.Database == "" {
				return fmt.Errorf("%s %q: database is required", where, it.Name)
			}
			if it.Entry != "" && it.Group != "" {
				return fmt.Errorf("%s %q: set entry (one entry) or group (entries to pick from), not both", where, it.Name)
			}
			if it.NoPassword && it.KeyFile == "" {
				return fmt.Errorf("%s %q: no_password needs key_file, or the database cannot be opened", where, it.Name)
			}
			if it.RememberPassword != "" {
				d, err := time.ParseDuration(it.RememberPassword)
				if err != nil || d <= 0 {
					return fmt.Errorf("%s %q: remember_password must be a positive duration like 10m, got %q", where, it.Name, it.RememberPassword)
				}
			}
			return nil
		}())
	}
	return errs
}

// validateRunbooks checks what is knowable from the config file alone.
// Cross-references that involve discovered runbooks — sequence steps, and name
// collisions between inline and discovered entries — are checked by the runbook
// registry, which is the only place that sees both sources.
func validateRunbooks(cfg *Config) []error {
	if cfg.Runbooks == nil {
		return nil
	}
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	add(validateRunbookSettings("runbooks.defaults", cfg.Runbooks.Defaults))
	add(CheckRunbookAgent("runbooks.defaults", cfg.Runbooks.Defaults.Agent, cfg.Runbooks.Agents))
	errs = append(errs, validateRunbookAgents(cfg.Runbooks)...)
	add(validateRunbookDrafts(cfg.Runbooks.Drafts))
	seen := make(map[string]string) // name -> what declared it
	for i, rb := range cfg.Runbooks.Items {
		add(func() error {
			if rb.Name == "" {
				return fmt.Errorf("runbook[%d]: name is required", i)
			}
			if prev, dup := seen[rb.Name]; dup {
				return fmt.Errorf("runbook[%d] %q: duplicate name, already declared by %s", i, rb.Name, prev)
			}
			seen[rb.Name] = "another runbook"
			if err := ValidateRunbook(rb, cfg.Environments); err != nil {
				return err
			}
			return CheckRunbookAgent(fmt.Sprintf("runbook %q", rb.Name), rb.Agent, cfg.Runbooks.Agents)
		}())
	}
	for i, sq := range cfg.Runbooks.Sequences {
		add(func() error {
			if sq.Name == "" {
				return fmt.Errorf("sequence[%d]: name is required", i)
			}
			if len(sq.Steps) == 0 {
				return fmt.Errorf("sequence[%d] %q: at least one step is required", i, sq.Name)
			}
			// Sequences and runbooks share the `den run <name>` namespace.
			if prev, dup := seen[sq.Name]; dup {
				return fmt.Errorf("sequence[%d] %q: duplicate name, already declared by %s", i, sq.Name, prev)
			}
			seen[sq.Name] = "another sequence"
			return nil
		}())
	}
	return append(errs, validateRunbookSources(cfg.Runbooks, cfg.Environments)...)
}

// keepRequestValues are what runbooks.drafts.keep_request takes.
var keepRequestValues = []string{KeepUntilDeleted, KeepUntilExit, KeepNever}

func validateRunbookDrafts(d *RunbookDrafts) error {
	if d == nil {
		return nil
	}
	if d.KeepRequest != "" && !slices.Contains(keepRequestValues, d.KeepRequest) {
		return fmt.Errorf("runbooks.drafts.keep_request: %q is not one of %s", d.KeepRequest, strings.Join(keepRequestValues, ", "))
	}
	if d.Days != nil && *d.Days < 0 {
		return fmt.Errorf("runbooks.drafts.days cannot be negative, got %d", *d.Days)
	}
	return nil
}

// AgentKinds are the agent CLIs den can start to write a runbook, in the order
// runbooks.agent: auto tries them. A kind is also its program's name and its
// `ollama launch` integration's. The names live here, not in internal/agent,
// because validation needs them and agent imports prereq, which imports config.
var AgentKinds = []string{"claude", "codex", "opencode", "copilot", "qwen", "droid", "pi", "kimi"}

// ollamaAgentAliases are the other names `ollama launch` takes for a kind.
var ollamaAgentAliases = map[string]string{"copilot-cli": "copilot"}

// validateRunbookAgents checks runbooks.agents and which of them (or of the
// built-in names) runbooks.agent picks.
func validateRunbookAgents(rc *RunbooksConfig) []error {
	var errs []error
	names := make(map[string]bool)
	for i, a := range rc.Agents {
		switch {
		case strings.TrimSpace(a.Name) == "":
			errs = append(errs, fmt.Errorf("runbooks.agents[%d]: name is required", i))
			continue
		case a.Name != strings.TrimSpace(a.Name):
			// Padding would make `--agent "Opus"` miss the entry it names.
			errs = append(errs, fmt.Errorf("runbooks.agents[%d] %q: name cannot start or end with whitespace", i, a.Name))
			continue
		case a.Name == "auto":
			errs = append(errs, fmt.Errorf("runbooks.agents[%d]: \"auto\" is reserved for the first installed agent", i))
			continue
		case names[a.Name]:
			errs = append(errs, fmt.Errorf("runbooks.agents[%d] %q: duplicate name", i, a.Name))
			continue
		}
		names[a.Name] = true
		switch {
		case strings.TrimSpace(a.Command) == "":
			errs = append(errs, fmt.Errorf("runbooks.agents[%d] %q: command is required", i, a.Name))
		case a.Kind != "" && !slices.Contains(AgentKinds, a.Kind):
			errs = append(errs, fmt.Errorf("runbooks.agents[%d] %q: kind %q is not one of %s", i, a.Name, a.Kind, strings.Join(AgentKinds, ", ")))
		case a.Kind == "" && AgentKind(a.Command) == "":
			errs = append(errs, fmt.Errorf("runbooks.agents[%d] %q: cannot tell which agent CLI %q starts, set kind (one of %s)", i, a.Name, a.Command, strings.Join(AgentKinds, ", ")))
		}
	}
	if rc.Agent != "" && rc.Agent != "auto" && !slices.Contains(AgentKinds, rc.Agent) && !names[rc.Agent] {
		errs = append(errs, fmt.Errorf("runbooks.agent: %q is not an agent den can start (auto, %s or the name of a runbooks.agents entry)", rc.Agent, strings.Join(AgentKinds, ", ")))
	}
	return errs
}

// CheckRunbookAgent checks a runbook's agent setting: a name den knows (auto,
// an agent kind or a runbooks.agents entry), or a command line it can tell
// starts one of the kinds, since that decides the arguments den adds. It is
// apart from ValidateRunbook, which knows no agents: a runbook.yaml with a
// bad agent still runs, only E cannot start it.
func CheckRunbookAgent(where, value string, agents []AgentSpec) error {
	if value == "" || value == "auto" || slices.Contains(AgentKinds, value) {
		return nil
	}
	for _, a := range agents {
		if a.Name == value {
			return nil
		}
	}
	if AgentKind(value) != "" {
		return nil
	}
	return fmt.Errorf("%s: agent %q is no runbooks.agents name and starts none of %s: "+
		"add it to runbooks.agents with kind, and name it", where, value, strings.Join(AgentKinds, ", "))
}

// AgentKind infers which of AgentKinds a command line starts, or "" when it
// cannot tell. It reads the base name of the first word, so
// /usr/local/bin/claude and claude.exe count, and `ollama launch <kind>`.
// It lives here, not in internal/agent, because validation needs it and agent
// imports prereq, which imports config.
func AgentKind(command string) string {
	words := strings.Fields(command)
	if len(words) == 0 {
		return ""
	}
	prog := program(words[0])
	switch {
	case slices.Contains(AgentKinds, prog):
		return prog
	case prog == "ollama" && len(words) >= 3 && words[1] == "launch":
		kind := words[2]
		if alias, ok := ollamaAgentAliases[kind]; ok {
			kind = alias
		}
		if slices.Contains(AgentKinds, kind) {
			return kind
		}
	}
	return ""
}

// program is a command's file name without its folder or .exe, lower case;
// both slash styles count so a Windows path reads the same everywhere.
func program(word string) string {
	word = strings.ReplaceAll(word, `\`, "/")
	return strings.TrimSuffix(strings.ToLower(path.Base(word)), ".exe")
}

var (
	sourceNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	// sourceRefRe is what a tag, branch or commit name may hold here: no
	// leading dash, which git would read as an option.
	sourceRefRe = regexp.MustCompile(`^[A-Za-z0-9._/][A-Za-z0-9._/-]*$`)
)

// validateRunbookSources checks runbooks.sources and allow_sources. Whether a
// repository exists and holds the ref is git's to say, at update time.
func validateRunbookSources(rc *RunbooksConfig, envs map[string]EnvSpec) []error {
	var errs []error
	for _, p := range rc.AllowSources {
		if _, err := path.Match(p, ""); err != nil {
			errs = append(errs, fmt.Errorf("runbooks.allow_sources: bad pattern %q", p))
		}
	}
	// A bad pattern would make every source below look disallowed.
	if len(errs) > 0 {
		return errs
	}
	seen := make(map[string]bool, len(rc.Sources))
	for i, s := range rc.Sources {
		if err := validateRunbookSource(i, s, rc.AllowSources, envs, seen); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// validateRunbookSource checks one runbooks.sources entry; seen holds the
// names of the entries before it.
func validateRunbookSource(i int, s RunbookSource, allow []string, envs map[string]EnvSpec, seen map[string]bool) error {
	if s.Name == "" {
		return fmt.Errorf("runbooks.sources[%d]: name is required", i)
	}
	if !sourceNameRe.MatchString(s.Name) {
		return fmt.Errorf("runbooks.sources[%d]: name %q must be lower case letters, digits and dashes", i, s.Name)
	}
	where := fmt.Sprintf("runbooks.sources[%d] %q", i, s.Name)
	if seen[s.Name] {
		return fmt.Errorf("%s: duplicate name", where)
	}
	seen[s.Name] = true
	if s.URL == "" {
		return fmt.Errorf("%s: url is required", where)
	}
	repo, err := ParseRepo(s.URL)
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	if !repo.Allowed(allow) {
		return fmt.Errorf("%s: %s is not in runbooks.allow_sources", where, repo.ID)
	}
	if s.Ref != "" && (!sourceRefRe.MatchString(s.Ref) || strings.Contains(s.Ref, "..")) {
		return fmt.Errorf("%s: ref %q is not a tag, branch or commit name", where, s.Ref)
	}
	if s.Path != "" && !filepath.IsLocal(filepath.FromSlash(s.Path)) {
		return fmt.Errorf("%s: path must be a folder inside the repository, got %q", where, s.Path)
	}
	for key, patterns := range map[string][]string{"include": s.Include, "exclude": s.Exclude} {
		for _, p := range patterns {
			if _, err := path.Match(p, ""); err != nil {
				return fmt.Errorf("%s: %s: bad pattern %q", where, key, p)
			}
		}
	}
	for _, from := range slices.Sorted(maps.Keys(s.EnvMap)) {
		if _, ok := envs[s.EnvMap[from]]; !ok {
			return fmt.Errorf("%s: env_map.%s: unknown env %q", where, from, s.EnvMap[from])
		}
	}
	return nil
}

// ValidateRunbook checks one named runbook against the environments it may
// refer to. den.yaml's items and every runbook.yaml go through it, so a key
// means the same in both places.
func ValidateRunbook(rb RunbookSpec, envs map[string]EnvSpec) error {
	if rb.Command == "" {
		return fmt.Errorf("runbook %q: command is required", rb.Name)
	}
	if rb.Env != "" {
		if _, ok := envs[rb.Env]; !ok {
			return fmt.Errorf("runbook %q: unknown env %q", rb.Name, rb.Env)
		}
	}
	if rb.Timeout != "" {
		if _, err := time.ParseDuration(rb.Timeout); err != nil {
			return fmt.Errorf("runbook %q: invalid timeout %q: %w", rb.Name, rb.Timeout, err)
		}
	}
	if err := validateRetry(rb.Name, rb.Retry); err != nil {
		return err
	}
	params := make(map[string]bool, len(rb.Params))
	for j, p := range rb.Params {
		if p.Name == "" {
			return fmt.Errorf("runbook %q: param[%d] name is required", rb.Name, j)
		}
		if params[p.Name] {
			return fmt.Errorf("runbook %q: duplicate param %q", rb.Name, p.Name)
		}
		params[p.Name] = true
	}
	return validateRunbookSettings(fmt.Sprintf("runbook %q", rb.Name), rb.RunbookSettings)
}

func validateRunbookSettings(where string, s RunbookSettings) error {
	if s.History != nil && *s.History < 0 {
		return fmt.Errorf("%s: history cannot be negative, got %d", where, *s.History)
	}
	return nil
}

// validateTransport checks that a transport names a known type and sets the
// keys that type needs — and none of another type's, which would be ignored.
func validateTransport(t *TransportSpec) error {
	if t == nil {
		return nil
	}
	set := map[string]bool{
		"host": t.Host != "" || len(t.Args) > 0, "endpoint_id": t.EndpointID != "",
		"resource": t.Resource != "" || t.Context != "" || t.Namespace != "",
	}
	uses := map[string]string{TransportSSH: "host", TransportEICE: "endpoint_id", TransportKubectl: "resource"}
	switch t.Type {
	case TransportSSM:
		for key, on := range set {
			if on {
				return fmt.Errorf("transport ssm takes no %s: it forwards through ec2_instance_id", key)
			}
		}
		return nil
	case TransportSSH, TransportEICE, TransportKubectl:
		own := uses[t.Type]
		if !set[own] {
			return fmt.Errorf("transport %s needs %s", t.Type, own)
		}
		for key, on := range set {
			if on && key != own {
				return fmt.Errorf("transport %s takes no %s", t.Type, key)
			}
		}
		if t.Type == TransportKubectl && t.Resource == "" {
			return fmt.Errorf("transport kubectl needs resource")
		}
		return nil
	case "":
		return fmt.Errorf("transport: type is required (ssm, ssh, eice or kubectl)")
	}
	return fmt.Errorf("transport: unknown type %q (ssm, ssh, eice or kubectl)", t.Type)
}

// Redshift issues temporary credentials for 15 minutes to an hour.
const (
	minRedshiftCredentialTTL = 15 * time.Minute
	maxRedshiftCredentialTTL = time.Hour
)

func validateRedshift(r *RedshiftSpec) error {
	if r == nil {
		return fmt.Errorf("type=redshift but no redshift section")
	}
	if r.RedshiftHost == "" {
		return fmt.Errorf("redshift.redshift_host is required")
	}
	if (r.ClusterIdentifier == "") == (r.WorkgroupName == "") {
		return fmt.Errorf("redshift: set exactly one of cluster_identifier (provisioned) or workgroup_name (Serverless)")
	}
	if r.WorkgroupName != "" && r.DBUser != "" {
		return fmt.Errorf("redshift.db_user is provisioned-only; Serverless always maps the caller's IAM identity")
	}
	if r.CredentialTTL != "" {
		ttl, err := time.ParseDuration(r.CredentialTTL)
		if err != nil {
			return fmt.Errorf("invalid redshift.credential_ttl %q: %w", r.CredentialTTL, err)
		}
		if ttl < minRedshiftCredentialTTL || ttl > maxRedshiftCredentialTTL {
			return fmt.Errorf("redshift.credential_ttl must be between 15m and 1h, got %s", ttl)
		}
	}
	return nil
}

func validateRetry(name string, r *RetrySpec) error {
	if r == nil {
		return nil
	}
	if r.Interval == "" {
		return fmt.Errorf("runbook %q: retry.interval is required", name)
	}
	if _, err := time.ParseDuration(r.Interval); err != nil {
		return fmt.Errorf("runbook %q: invalid retry.interval %q: %w", name, r.Interval, err)
	}
	if r.Timeout != "" {
		if _, err := time.ParseDuration(r.Timeout); err != nil {
			return fmt.Errorf("runbook %q: invalid retry.timeout %q: %w", name, r.Timeout, err)
		}
	}
	if r.MaxAttempts < 0 {
		return fmt.Errorf("runbook %q: retry.max_attempts cannot be negative", name)
	}
	// An unbounded retry with no ceiling would spin forever with no way to
	// notice from the config alone.
	if r.UntilSuccess && r.MaxAttempts == 0 && r.Timeout == "" {
		return fmt.Errorf("runbook %q: retry.until_success needs max_attempts or timeout to bound it", name)
	}
	return nil
}

func applyDefaults(cfg *Config) {
	for i := range cfg.Services {
		svc := &cfg.Services[i]
		if svc.Type == ServiceTypeRDS && svc.RDS != nil {
			if svc.RDS.Engine == "" {
				svc.RDS.Engine = RDSEnginePostgres
			}
		}
		if svc.Type == ServiceTypeRedshift && svc.Redshift != nil {
			if svc.Redshift.DBName == "" {
				svc.Redshift.DBName = "dev" // the database every Redshift cluster is created with
			}
			if svc.Redshift.CredentialTTL == "" {
				svc.Redshift.CredentialTTL = minRedshiftCredentialTTL.String()
			}
		}
		// Every forwarded type gets its default here, so the checks that compare
		// ports see two services that would share one.
		if t := svc.Target(); t != nil && t.LocalPort == 0 {
			t.LocalPort = DefaultLocalPort(svc.Type)
		}
	}
}

// HeaderTitle is the line above the dashboard: title trimmed, or DefaultTitle
// when it is empty. c may be nil.
func (c *Config) HeaderTitle() string {
	if c != nil {
		if t := strings.TrimSpace(c.Title); t != "" {
			return t
		}
	}
	return DefaultTitle
}

// AWSConfigPath is the AWS CLI config file den reads: aws.config_path with ~
// expanded, or ~/.aws/config. c may be nil.
func (c *Config) AWSConfigPath() string {
	if c != nil && c.AWS != nil && c.AWS.ConfigPath != "" {
		return platform.ExpandHome(c.AWS.ConfigPath)
	}
	return platform.ExpandHome("~/.aws/config")
}

// AWSCredentialsPath is the AWS credentials file: aws.credentials_path with ~
// expanded, or ~/.aws/credentials. c may be nil.
func (c *Config) AWSCredentialsPath() string {
	if c != nil && c.AWS != nil && c.AWS.CredentialsPath != "" {
		return platform.ExpandHome(c.AWS.CredentialsPath)
	}
	return platform.ExpandHome("~/.aws/credentials")
}

// AWSEnv is the environment the aws CLI and the AWS SDK need to find the files
// den.yaml names: they know a non-default config or credentials file only
// through AWS_CONFIG_FILE and AWS_SHARED_CREDENTIALS_FILE. A key den.yaml does
// not set is left out, so the user's own variable keeps applying. c may be nil.
func (c *Config) AWSEnv() map[string]string {
	env := map[string]string{}
	if c == nil || c.AWS == nil {
		return env
	}
	if c.AWS.ConfigPath != "" {
		env["AWS_CONFIG_FILE"] = c.AWSConfigPath()
	}
	if c.AWS.CredentialsPath != "" {
		env["AWS_SHARED_CREDENTIALS_FILE"] = c.AWSCredentialsPath()
	}
	return env
}

// AWSSessions is the aws.sessions curation list; nil means show every
// discovered session. c may be nil.
func (c *Config) AWSSessions() []AWSSessionSpec {
	if c == nil || c.AWS == nil {
		return nil
	}
	return c.AWS.Sessions
}

// LogsSettings returns the logs block with file_location's ~ expanded, or nil.
// c may be nil.
func (c *Config) LogsSettings() *LogsConfig {
	if c == nil || c.Logs == nil {
		return nil
	}
	l := *c.Logs
	l.FileLocation = platform.ExpandHome(l.FileLocation)
	return &l
}

// UpdatesSettings returns the updates block, or nil. c may be nil.
func (c *Config) UpdatesSettings() *UpdatesConfig {
	if c == nil {
		return nil
	}
	return c.Updates
}
