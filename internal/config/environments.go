package config

import "strings"

// EnvMapping is what a service's tunnel and credential calls need to know
// about the AWS side: the bastion, the profiles and the region. ResolveEnv
// builds it from the target's own fields, which den.yaml's environments:
// block has already filled in.
type EnvMapping struct {
	EC2InstanceID     string
	AWSProfile        string
	CredentialProfile string
	AWSRegionCode     string
	Transport         *TransportSpec // nil means SSM through EC2InstanceID
	Production        bool           // an agent needs the user's confirmation to connect it
}

// isProductionLabel reports whether an environment label names production by
// itself, so a config that already says environment: prod needs no extra key.
func isProductionLabel(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "prod", "production":
		return true
	}
	return false
}
