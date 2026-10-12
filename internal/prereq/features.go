package prereq

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/lukaszgard/den/internal/config"
)

// Feature is something a user can want den for, and what it needs.
type Feature struct {
	About string
	Tools []string
	// Tunnelled features reach their store through a transport; without a
	// transport feature alongside, the default one (ssm) is assumed.
	Tunnelled bool
}

// Features are the names `den doctor --for` accepts. Service and transport
// names are the ones den.yaml uses.
var Features = map[string]Feature{
	string(config.ServiceTypeRDS):        {About: "RDS/Aurora PostgreSQL", Tools: []string{"psql"}, Tunnelled: true},
	"mysql":                              {About: "RDS/Aurora MySQL", Tools: []string{"mysql"}, Tunnelled: true},
	string(config.ServiceTypeRedshift):   {About: "Redshift", Tools: []string{"psql"}, Tunnelled: true},
	string(config.ServiceTypeRedis):      {About: "ElastiCache and MemoryDB", Tools: []string{"redis-cli"}, Tunnelled: true},
	string(config.ServiceTypeDocumentDB): {About: "DocumentDB", Tools: []string{"mongosh"}, Tunnelled: true},
	string(config.ServiceTypeOpenSearch): {About: "OpenSearch", Tools: []string{"curl"}, Tunnelled: true},
	string(config.ServiceTypeNeptune):    {About: "Neptune", Tools: []string{"curl"}, Tunnelled: true},
	string(config.ServiceTypeDocker):     {About: "Docker Compose environments", Tools: []string{"docker"}},

	config.TransportSSM:     {About: "tunnels through an SSM bastion (the default)", Tools: []string{"aws", "session-manager-plugin"}},
	config.TransportSSH:     {About: "tunnels through an SSH jump host", Tools: []string{"ssh"}},
	config.TransportEICE:    {About: "tunnels through EC2 Instance Connect Endpoint", Tools: []string{"aws"}},
	config.TransportKubectl: {About: "tunnels through kubectl port-forward", Tools: []string{"kubectl"}},

	"tunnel": {About: "user-defined SSH/SOCKS tunnels", Tools: []string{"ssh"}},
	"vpn":    {About: "FortiGate SSL VPN", Tools: []string{"openfortivpn"}},
	"aws":    {About: "AWS SSO logins", Tools: []string{"aws"}},

	"sops":               {About: "sops-encrypted secret files", Tools: []string{"sops"}},
	"vault":              {About: "HashiCorp Vault secrets", Tools: []string{"vault"}},
	"keepassxc":          {About: "KeePassXC databases", Tools: []string{"keepassxc-cli"}},
	"aws_secretsmanager": {About: "AWS Secrets Manager", Tools: []string{"aws"}},

	"tui": {About: "the dashboard's icons", Tools: []string{NerdFont}},

	"runbook-sources": {About: "runbooks from git repositories (runbooks.sources)", Tools: []string{"git"}},

	"claude":   {About: "writing runbooks with Claude Code", Tools: []string{"claude"}},
	"codex":    {About: "writing runbooks with Codex", Tools: []string{"codex"}},
	"opencode": {About: "writing runbooks with OpenCode", Tools: []string{"opencode"}},
	"copilot":  {About: "writing runbooks with Copilot CLI", Tools: []string{"copilot"}},
}

var transports = []string{config.TransportSSM, config.TransportSSH, config.TransportEICE, config.TransportKubectl}

// FeatureNames lists the feature names in order, for help and completion.
func FeatureNames() []string {
	names := make([]string, 0, len(Features)+1)
	for n := range Features {
		names = append(names, n)
	}
	sort.Strings(names)
	return append(names, "all")
}

// Resolve turns feature names into the catalog IDs they need, in catalog
// order. "all" is every tool den knows.
func Resolve(names []string) ([]string, error) {
	want := map[string]bool{}
	tunnelled, transport := false, false
	for _, raw := range names {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		if name == "all" {
			for _, t := range Catalog {
				want[t.ID] = true
			}
			continue
		}
		f, ok := Features[name]
		if !ok {
			return nil, fmt.Errorf("unknown feature %q; choose from %s", raw, strings.Join(FeatureNames(), ", "))
		}
		for _, t := range f.Tools {
			want[t] = true
		}
		tunnelled = tunnelled || f.Tunnelled
		transport = transport || slices.Contains(transports, name)
	}
	if tunnelled && !transport {
		for _, t := range Features[config.TransportSSM].Tools {
			want[t] = true
		}
	}
	var out []string
	for _, t := range Catalog {
		if want[t.ID] {
			out = append(out, t.ID)
		}
	}
	return out, nil
}
