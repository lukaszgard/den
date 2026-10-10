package runbook

import (
	"fmt"
	"slices"
	"strings"

	den "github.com/lukaszgard/den"
)

// Guide is how to write a runbook for this den: the guide every agent gets,
// then what only this den knows — where runbooks go, the environments a
// runbook can name, and the names already taken. target is the folder to
// write in, when there is one.
//
// Nothing here is a credential: environment names, AWS profile names, regions
// and bastion ids, the same kind of facts list_services hands an agent.
func (r *Registry) Guide(target string) string {
	r.mu.RLock()
	cfg, envs, base := r.cfg, r.envs, configDir(r.cfgPath)
	r.mu.RUnlock()

	var b strings.Builder
	b.WriteString(strings.TrimSpace(den.RunbookGuide))
	b.WriteString("\n\n## In this den\n\n")
	if target != "" {
		fmt.Fprintf(&b, "Write the runbook in `%s`: den has created it for you.\n\n", target)
	}

	b.WriteString("### Runbooks directories\n\n")
	if len(cfg.Dirs) == 0 {
		b.WriteString("None: ask the user to add `runbooks.dirs` to den.yaml first.\n")
	}
	for i, dir := range cfg.Dirs {
		note := ""
		if i == 0 {
			note = " (new runbooks go here)"
		}
		fmt.Fprintf(&b, "- `%s`%s\n", resolvePath(dir, base), note)
	}

	b.WriteString("\n### Environments\n\nThe `env:` a runbook can name.\n\n")
	if len(envs) == 0 {
		b.WriteString("None are defined: a runbook here runs with whatever AWS profile the user's shell has.\n")
	} else {
		b.WriteString("| env | AWS profile | region | environment | bastion | production |\n|---|---|---|---|---|---|\n")
		names := make([]string, 0, len(envs))
		for name := range envs {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			e := envs[name]
			prod := "no"
			if e.IsProduction() {
				prod = "**yes**"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s |\n", name,
				orDash(e.AWSProfile), orDash(e.AWSRegionCode), orDash(e.Environment), orDash(e.EC2InstanceID), prod)
		}
	}

	b.WriteString("\n### Runbooks that exist\n\nA new runbook needs a name that is not one of these.\n\n")
	taken := r.names()
	if len(taken) == 0 {
		b.WriteString("None yet.\n")
	}
	for _, name := range taken {
		fmt.Fprintf(&b, "- %s\n", name)
	}
	return b.String()
}

// names are the runbooks' and sequences' names, sorted.
func (r *Registry) names() []string {
	var out []string
	for _, run := range r.Runbooks() {
		out = append(out, run.Runbook().Name)
	}
	for _, seq := range r.Sequences() {
		out = append(out, seq.Sequence().Name)
	}
	slices.Sort(out)
	return out
}

func orDash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}
