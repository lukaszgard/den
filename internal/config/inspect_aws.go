package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// profileUse is one thing in den.yaml that runs as an AWS profile.
type profileUse struct {
	profile string
	user    string // what uses it: a service, a secret, "update checks"
	key     string
}

// profileUses lists every AWS profile den.yaml makes den use, as den resolves
// them: a service's through its environment, and for the transports that sign
// with the profile only.
func profileUses(cfg *Config, root *yaml.Node) []profileUse {
	var uses []profileUse
	add := func(profile, user, key string) {
		if profile != "" {
			uses = append(uses, profileUse{profile, user, key})
		}
	}
	for i, svc := range cfg.Services {
		t := svc.Target()
		if t == nil {
			continue
		}
		env, err := t.ResolveEnv()
		if err != nil {
			continue // Validate reports it
		}
		// Blamed where the file writes it: the service, the environment it
		// names, or the service as a whole when the built-in map supplies it.
		keyOf := func(field string) string {
			keys := []string{fmt.Sprintf("services[%d].%s.%s", i, svc.Type, field)}
			if svc.Env != "" {
				keys = append(keys, fmt.Sprintf("environments.%s.%s", svc.Env, field))
			}
			return firstKey(root, append(keys, fmt.Sprintf("services[%d]", i))...)
		}
		// The credential is minted through AWS whatever carries the traffic.
		add(env.CredentialProfile, svc.Name, keyOf("credential_profile"))
		if tr := env.Transport.TypeOrDefault(); tr == TransportSSM || tr == TransportEICE {
			add(env.AWSProfile, svc.Name, keyOf("aws_profile"))
		}
	}
	// An environment no service uses yet is still a place to mistype a profile;
	// one that is used is covered by its services.
	for _, name := range slices.Sorted(maps.Keys(cfg.Environments)) {
		if slices.ContainsFunc(cfg.Services, func(s ServiceSpec) bool { return s.Env == name }) {
			continue
		}
		env := cfg.Environments[name]
		user, key := "environment "+name, "environments."+name
		add(env.CredentialProfile, user, key+".credential_profile")
		if tr := env.Transport.TypeOrDefault(); tr == TransportSSM || tr == TransportEICE {
			add(env.AWSProfile, user, key+".aws_profile")
		}
	}
	if s := cfg.Secrets; s != nil {
		for i, it := range s.SOPS {
			add(it.AWSProfile, it.Name, fmt.Sprintf("secrets.sops[%d].aws_profile", i))
		}
		for i, it := range s.AWSSecretsManager {
			add(it.AWSProfile, it.Name, fmt.Sprintf("secrets.aws_secretsmanager[%d].aws_profile", i))
		}
	}
	if u := cfg.Updates; u != nil && u.S3Bucket != "" {
		add(u.AWSProfile, "update checks", "updates.aws_profile")
	}
	return uses
}

// awsProblems checks den.yaml against the AWS files: that the profiles it
// runs as and the SSO sessions it lists are there. A profile that is not
// breaks only what uses it, which is why this is a warning.
func awsProblems(cfg *Config, root *yaml.Node) Problems {
	uses := profileUses(cfg, root)
	sessions := cfg.AWSSessions()
	if len(uses) == 0 && len(sessions) == 0 {
		return nil
	}

	profiles, profErr := LoadAWSProfiles(cfg.AWSConfigPath())
	credentials, _ := CredentialProfileNames(cfg.AWSCredentialsPath())
	if profErr != nil {
		// A config set in den.yaml that is not there is a path problem already.
		if cfg.AWS != nil && cfg.AWS.ConfigPath != "" && errors.Is(profErr, fs.ErrNotExist) {
			return nil
		}
		var unmet []string
		for _, u := range uses {
			if !slices.Contains(credentials, u.profile) && !slices.Contains(unmet, u.profile) {
				unmet = append(unmet, u.profile)
			}
		}
		if len(unmet) == 0 && len(sessions) == 0 {
			return nil
		}
		msg := fmt.Sprintf("the AWS config %s cannot be read: %v", cfg.AWSConfigPath(), profErr)
		if errors.Is(profErr, fs.ErrNotExist) {
			msg = fmt.Sprintf("the AWS config %s does not exist, and den.yaml uses AWS profiles", cfg.AWSConfigPath())
		}
		key := "aws"
		if cfg.AWS == nil && len(uses) > 0 {
			key = uses[0].key
		}
		return Problems{{
			Severity: SeverityWarning, Line: lineAt(root, key), Key: key, Code: "aws-config-missing", Msg: msg,
			Hint: "create it with `aws configure sso`, or set aws.config_path in den.yaml",
		}}
	}

	byName := map[string]AWSProfile{}
	bare := map[string]bool{}
	var known []string
	for _, p := range profiles {
		switch {
		case p.Kind == "profile" && p.Bare:
			bare[p.Name] = true
		case p.Kind == "profile":
			byName[p.Name] = p
			known = append(known, p.Name)
		}
	}
	// Profiles may also live in the credentials file, where [name] is right.
	inCredentials := map[string]bool{}
	for _, name := range credentials {
		inCredentials[name] = true
		known = append(known, name)
	}

	var ps Problems
	reported := map[string]bool{}
	for _, u := range uses {
		if reported[u.profile] {
			continue
		}
		reported[u.profile] = true
		users := userList(uses, u.profile)
		// Follow source_profile: a profile that assumes a role is only as
		// good as the profile it takes its credentials from.
		for cur, seen := u.profile, map[string]bool{}; cur != "" && !seen[cur]; {
			seen[cur] = true
			if p, ok := byName[cur]; ok {
				cur = rawKey(p, "source_profile")
				continue
			}
			if inCredentials[cur] {
				break
			}
			p := Problem{Severity: SeverityWarning, Line: lineAt(root, u.key), Key: u.key}
			switch {
			case bare[cur]:
				p.Code = "profile-bare"
				p.Msg = fmt.Sprintf("AWS profile %q (%s) is written [%s] in %s", cur, users, cur, cfg.AWSConfigPath())
				p.Hint = fmt.Sprintf("write it [profile %s]: the AWS config only knows [default] and [profile NAME]", cur)
			case cur == u.profile:
				p.Code = "profile-missing"
				p.Msg = fmt.Sprintf("AWS profile %q (%s) is not in %s", cur, users, cfg.AWSConfigPath())
				p.Hint = missingProfileHint(cur, known)
			default:
				p.Code = "profile-source-missing"
				p.Msg = fmt.Sprintf("AWS profile %q (%s) takes its credentials from %q (source_profile), which is not in %s", u.profile, users, cur, cfg.AWSConfigPath())
				p.Hint = missingProfileHint(cur, known)
			}
			ps = append(ps, p)
			break
		}
	}

	if len(sessions) > 0 {
		discovered := map[string]bool{}
		var names []string
		for _, p := range profiles {
			if p.Kind == "sso-session" {
				discovered[p.Name] = true
				names = append(names, p.Name)
			}
		}
		for i, s := range sessions {
			if discovered[s.Session] {
				continue
			}
			key := fmt.Sprintf("aws.sessions[%d].session", i)
			hint := fmt.Sprintf("add [sso-session %s] to %s, or correct the name", s.Session, cfg.AWSConfigPath())
			if sug := suggest(s.Session, names); sug != "" {
				hint = fmt.Sprintf("did you mean %q?", sug)
			}
			ps = append(ps, Problem{
				Severity: SeverityWarning, Line: lineAt(root, key), Key: key, Code: "sso-session-unknown",
				Msg:  fmt.Sprintf("aws.sessions[%d]: no [sso-session %s] in %s, skipped", i, s.Session, cfg.AWSConfigPath()),
				Hint: hint,
			})
		}
	}
	return ps
}

// userList names who uses a profile: up to three, then a count.
func userList(uses []profileUse, profile string) string {
	var users []string
	for _, u := range uses {
		if u.profile == profile && !slices.Contains(users, u.user) {
			users = append(users, u.user)
		}
	}
	if len(users) > 3 {
		return fmt.Sprintf("used by %s and %d more", strings.Join(users[:3], ", "), len(users)-3)
	}
	return "used by " + strings.Join(users, ", ")
}

func missingProfileHint(name string, known []string) string {
	if s := suggest(name, known); s != "" {
		return fmt.Sprintf("did you mean %q?", s)
	}
	return fmt.Sprintf("add it with `aws configure sso --profile %s`, or correct the name", name)
}

// rawKey is the value of a key in a profile's section, "" when it has none.
func rawKey(p AWSProfile, key string) string {
	for _, kv := range p.RawKeys {
		if kv.Key == key {
			return kv.Value
		}
	}
	return ""
}
