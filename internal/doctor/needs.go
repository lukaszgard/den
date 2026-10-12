package doctor

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/secrets"
)

// needs is what a config depends on outside den, with the services that
// depend on each thing so results can say who is affected.
type needs struct {
	aws      bool                 // anything goes through the AWS CLI or SDK
	bastions map[bastion][]string // SSM target → services using it
	ports    map[int][]string     // local port → services listening on it
	tools    map[string][]string  // client tool → services handing commands to it
}

type bastion struct{ profile, region, instance string }

func (b bastion) String() string { return b.instance + "@" + b.region + "/" + b.profile }

type tool struct{ name, why string }

func (n needs) toolList() []tool {
	names := make([]string, 0, len(n.tools))
	for t := range n.tools {
		names = append(names, t)
	}
	sort.Strings(names)
	out := make([]tool, 0, len(names))
	for _, t := range names {
		users := n.tools[t]
		why := strings.Join(users, ", ")
		if len(users) > 3 {
			why = fmt.Sprintf("%s and %d more", strings.Join(users[:3], ", "), len(users)-3)
		}
		out = append(out, tool{name: t, why: why})
	}
	return out
}

// clientTool is the command a service type's connect command runs.
func clientTool(spec config.ServiceSpec) string {
	switch spec.Type {
	case config.ServiceTypeRDS:
		if spec.RDS != nil && spec.RDS.Engine == config.RDSEngineMySQL {
			return "mysql"
		}
		return "psql"
	case config.ServiceTypeRedshift:
		return "psql"
	case config.ServiceTypeRedis:
		return "redis-cli"
	case config.ServiceTypeDocumentDB:
		return "mongosh"
	case config.ServiceTypeOpenSearch, config.ServiceTypeNeptune:
		return "curl"
	case config.ServiceTypeDocker:
		return "docker"
	}
	return ""
}

func collectNeeds(in Input) needs {
	cfg := in.Config
	n := needs{
		bastions: map[bastion][]string{},
		ports:    map[int][]string{},
		tools:    map[string][]string{},
	}

	// An agent named in runbooks.agent is one den starts; auto picks whichever
	// is installed, so it needs none in particular.
	if cfg.Runbooks != nil {
		if a := cfg.Runbooks.Agent; slices.Contains(config.AgentKinds, a) {
			n.tools[a] = append(n.tools[a], "runbooks.agent")
		}
		if len(cfg.Runbooks.Sources) > 0 {
			n.tools["git"] = append(n.tools["git"], "runbooks.sources")
		}
	}

	for _, spec := range cfg.Services {
		if t := clientTool(spec); t != "" {
			n.tools[t] = append(n.tools[t], spec.Name)
		}
		target := spec.Target()
		if target == nil {
			continue
		}
		env, err := target.ResolveEnv()
		if err != nil {
			continue // Validate reports it
		}
		// The credential is minted through AWS whatever carries the traffic.
		if env.CredentialProfile != "" {
			n.aws = true
		}
		switch env.Transport.TypeOrDefault() {
		case config.TransportSSM:
			n.aws = true
			b := bastion{profile: env.AWSProfile, region: env.AWSRegionCode, instance: env.EC2InstanceID}
			if b.instance != "" {
				n.bastions[b] = append(n.bastions[b], spec.Name)
			}
		case config.TransportEICE:
			n.aws = true
		case config.TransportSSH:
			n.tools["ssh"] = appendOnce(n.tools["ssh"], spec.Name)
		case config.TransportKubectl:
			n.tools["kubectl"] = appendOnce(n.tools["kubectl"], spec.Name)
		}
	}
	for _, t := range cfg.Tunnels {
		if fields := strings.Fields(t.Command); len(fields) > 0 {
			n.tools[fields[0]] = append(n.tools[fields[0]], t.Name)
		}
	}
	for _, v := range cfg.VPNs {
		n.tools["openfortivpn"] = append(n.tools["openfortivpn"], v.Name)
	}
	if cfg.AWS != nil && len(cfg.AWS.Sessions) > 0 {
		n.aws = true
	}
	if s := cfg.Secrets; s != nil {
		if len(s.AWSSecretsManager) > 0 {
			n.aws = true
		}
	}
	for _, it := range in.Secrets {
		if tu, ok := it.(secrets.ToolUser); ok {
			for _, t := range tu.Tools() {
				n.tools[t] = appendOnce(n.tools[t], it.Name())
			}
		}
	}
	if u := cfg.Updates; u != nil && u.S3Bucket != "" {
		n.aws = true
	}
	for _, s := range in.Services {
		if s.LocalPort > 0 {
			n.ports[s.LocalPort] = append(n.ports[s.LocalPort], s.Name)
		}
	}
	return n
}

func appendOnce(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}
