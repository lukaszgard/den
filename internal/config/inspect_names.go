package config

import (
	"fmt"
	"maps"
	"regexp"
	"slices"

	"gopkg.in/yaml.v3"
)

// regionRe is the shape of an AWS region code: eu-central-1, us-gov-west-1.
var regionRe = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-\d+$`)

// instanceRe is the shape of an EC2 instance id: i- and 8 or 17 hex digits.
var instanceRe = regexp.MustCompile(`^i-([0-9a-f]{8}|[0-9a-f]{17})$`)

// nameProblems checks that the entries of one kind have names of their own.
// The service manager keeps them by type and name, so two with both the same
// would be one: the later never appears. (An RDS and a Redis service may share
// a name, as the example den.yaml's do, per environment.)
func nameProblems(cfg *Config, root *yaml.Node) Problems {
	seen := map[string]string{} // type and name → the key of the entry that has it
	var ps Problems
	check := func(kind, key, name string) {
		if name == "" {
			return
		}
		id := kind + "\x00" + name
		if first, dup := seen[id]; dup {
			ps = append(ps, Problem{
				Severity: SeverityWarning, Line: lineAt(root, key+".name"), Key: key + ".name", Code: "name-duplicate",
				Msg:  fmt.Sprintf("%s %q: the name is already used by %s, and only one of them is kept", key, name, first),
				Hint: "rename one: den connect, auto_connect and den mcp look entries up by name",
			})
			return
		}
		seen[id] = key
	}
	for i, s := range cfg.Services {
		check(string(s.Type), fmt.Sprintf("services[%d]", i), s.Name)
	}
	for i, t := range cfg.Tunnels {
		check("tunnel", fmt.Sprintf("tunnels[%d]", i), t.Name)
	}
	for i, v := range cfg.VPNs {
		check("vpn", fmt.Sprintf("vpn[%d]", i), v.Name)
	}
	return ps
}

// formatProblems checks the values of a service's bastion that are easy to
// mistype and fail late, deep inside an AWS call. Only values the file writes
// are checked: the built-in environments are den's own.
func formatProblems(cfg *Config, root *yaml.Node) Problems {
	var ps Problems
	flagged := map[string]bool{} // an environment shared by ten services is one mistake
	hints := map[string]string{
		"region-format":   "write the region code, such as eu-central-1",
		"instance-format": "copy the instance id from the EC2 console (i- and 8 or 17 hex digits)",
	}
	emit := func(key, value, code, what string) {
		if flagged[key] {
			return
		}
		flagged[key] = true
		ps = append(ps, Problem{
			Severity: SeverityWarning, Line: lineAt(root, key), Key: key, Code: code,
			Msg:  fmt.Sprintf("%s: %q does not look like %s", key, value, what),
			Hint: hints[code],
		})
	}
	for i, svc := range cfg.Services {
		t := svc.Target()
		if t == nil {
			continue
		}
		check := func(field, value, code, what string, re *regexp.Regexp) {
			if value == "" || re.MatchString(value) {
				return
			}
			keys := []string{fmt.Sprintf("services[%d].%s.%s", i, svc.Type, field)}
			if svc.Env != "" {
				keys = append(keys, fmt.Sprintf("environments.%s.%s", svc.Env, field))
			}
			for _, k := range keys {
				if hasKey(root, k) {
					emit(k, value, code, what)
					return
				}
			}
		}
		check("aws_region_code", t.AWSRegionCode, "region-format", "an AWS region code", regionRe)
		check("ec2_instance_id", t.EC2InstanceID, "instance-format", "an EC2 instance id", instanceRe)
	}
	// Environments no service uses yet are checked as well.
	for _, name := range slices.Sorted(maps.Keys(cfg.Environments)) {
		env, at := cfg.Environments[name], "environments."+name
		if env.AWSRegionCode != "" && !regionRe.MatchString(env.AWSRegionCode) {
			emit(at+".aws_region_code", env.AWSRegionCode, "region-format", "an AWS region code")
		}
		if env.EC2InstanceID != "" && !instanceRe.MatchString(env.EC2InstanceID) {
			emit(at+".ec2_instance_id", env.EC2InstanceID, "instance-format", "an EC2 instance id")
		}
	}
	return ps
}
