package config

import "fmt"

// SSOSession is a display-ready AWS SSO session: one [sso-session ...] section
// from the AWS config, with any den.yaml curation applied.
//
// This type exists so the Connect list and the AWS pane agree on what a session
// is. Both used to walk LoadAWSProfiles output themselves, with their own notion
// of which profile belonged to which session, which is how they drifted apart.
type SSOSession struct {
	// Name is the [sso-session X] section name — the literal argument
	// `aws sso login --sso-session` takes. Never a display name.
	Name        string
	DisplayName string // what the UI shows; defaults to Name
	Description string
	StartURL    string
	Region      string

	// Profile is the first profile referencing this session. It is what
	// export-credentials runs as, and is empty when no profile references the
	// session — which is legal, just means a precise expiry can't be read.
	Profile string
}

// ResolveSSOSessions applies den.yaml curation to the sessions discovered in the
// AWS config.
//
// Sessions are discovered from the AWS config, never declared here, so curation
// is a filter rather than a registration list. A nil curation — the key absent
// from den.yaml — returns every discovered session in discovery order, which is
// what den did before curation existed, so an existing config behaves exactly as
// it always has. An empty but non-nil curation (`sessions: []`) is an explicit
// "none", and is the only way to show no sessions while keeping the pane.
//
// A curated entry naming a session that is not in the AWS config is skipped and
// reported rather than dropped silently: `aws sso login` would otherwise fail much
// later with a message that does not mention den.yaml at all.
func ResolveSSOSessions(profiles []AWSProfile, curation []AWSSessionSpec) (sessions []SSOSession, warnings []string) {
	discovered := make(map[string]AWSProfile)
	var order []string
	for _, p := range profiles {
		if p.Kind != "sso-session" {
			continue
		}
		if _, seen := discovered[p.Name]; seen {
			continue
		}
		discovered[p.Name] = p
		order = append(order, p.Name)
	}

	// First profile referencing each session, in file order.
	sessionProfile := make(map[string]string)
	for _, p := range profiles {
		if p.Kind != "profile" || p.SSOSession == "" {
			continue
		}
		if _, exists := sessionProfile[p.SSOSession]; !exists {
			sessionProfile[p.SSOSession] = p.Name
		}
	}

	build := func(name string, spec *AWSSessionSpec) SSOSession {
		src := discovered[name]
		s := SSOSession{
			Name:        name,
			DisplayName: name,
			StartURL:    src.SSOStartURL,
			Region:      src.SSORegion,
			Profile:     sessionProfile[name],
		}
		if spec != nil {
			if spec.Name != "" {
				s.DisplayName = spec.Name
			}
			s.Description = spec.Description
		}
		return s
	}

	// nil means "not configured"; an empty non-nil slice means "show none", so
	// this deliberately tests for nil rather than length.
	if curation == nil {
		for _, name := range order {
			sessions = append(sessions, build(name, nil))
		}
		return sessions, nil
	}

	for i := range curation {
		spec := curation[i]
		if _, ok := discovered[spec.Session]; !ok {
			warnings = append(warnings, fmt.Sprintf(
				"aws.sessions[%d]: no [sso-session %s] in the AWS config — skipped", i, spec.Session))
			continue
		}
		sessions = append(sessions, build(spec.Session, &spec))
	}
	return sessions, warnings
}
