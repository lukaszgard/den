package runbook

import (
	"maps"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/lukaszgard/den/internal/config"
)

// PortSource reports the local ports of currently-connected services, keyed by
// "<TYPE>_<NAME>" already normalised for use in a variable name.
//
// It is a func rather than an interface so this package never imports
// service.Manager: the coupling stays one-way and the environment builder is
// testable without spinning up any services.
type PortSource func() map[string]int

// BuildEnv assembles the environment a runbook runs with. Precedence, lowest
// first: the inherited process environment, the colour request, den's
// connection context, live tunnel ports, parameters, and finally the
// runbook's own env_vars.
//
// The connection context is the point of the whole feature — it is what a
// plain terminal plus a Makefile cannot supply.
func BuildEnv(rb Runbook, env config.EnvSpec, cfgPath string, params map[string]string, ports map[string]int) []string {
	vars := make(map[string]string, 64)
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			vars[kv[:i]] = kv[i+1:]
		}
	}
	// A run's output goes through pipes, and a tool that finds no terminal
	// prints plain text: Logs and History would never show a colour. Most
	// tools (and boto/rich/chalk-style libraries) honour one of these two.
	// NO_COLOR, inherited or in env_vars, is the way out.
	if vars["NO_COLOR"] == "" && rb.EnvVars["NO_COLOR"] == "" {
		vars["FORCE_COLOR"], vars["CLICOLOR_FORCE"] = "1", "1"
	}
	maps.Copy(vars, ContextVars(rb, env, cfgPath, params, ports))
	return envList(vars)
}

// ContextVars are the variables den adds to the environment it inherited:
// the connection context, live ports, parameters and env_vars, with the same
// precedence as BuildEnv.
func ContextVars(rb Runbook, env config.EnvSpec, cfgPath string, params map[string]string, ports map[string]int) map[string]string {
	vars := make(map[string]string, 16)
	setIf := func(k, v string) {
		if v != "" {
			vars[k] = v
		}
	}

	// AWS_DEFAULT_REGION as well as AWS_REGION: v1-era tooling reads the
	// former, the v2 CLI reads either.
	setIf("AWS_PROFILE", env.AWSProfile)
	setIf("AWS_REGION", env.AWSRegionCode)
	setIf("AWS_DEFAULT_REGION", env.AWSRegionCode)
	// The same fallback as the services: one role for everything is one key.
	credential := env.CredentialProfile
	if credential == "" {
		credential = env.AWSProfile
	}
	setIf("DEN_CREDENTIAL_PROFILE", credential)
	setIf("DEN_ENV", env.Environment)
	setIf("DEN_REGION", env.Region)
	setIf("DEN_EC2_INSTANCE_ID", env.EC2InstanceID)
	setIf("DEN_ENV_NAME", rb.Env)
	setIf("DEN_CONFIG", cfgPath)
	setIf("DEN_RUNBOOK", rb.Name)
	setIf("DEN_RUNBOOK_STATE", rb.StateDir)

	for key, port := range ports {
		vars["DEN_PORT_"+key] = strconv.Itoa(port)
	}
	for name, val := range params {
		vars["DEN_PARAM_"+EnvKey(name)] = val
	}
	// env_vars is the author's explicit override and wins over everything den
	// derived on their behalf.
	maps.Copy(vars, rb.EnvVars)
	return vars
}

// envList renders vars as sorted KEY=value entries.
func envList(vars map[string]string) []string {
	out := make([]string, 0, len(vars))
	for k, v := range vars {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// EnvKey normalises an arbitrary name into the uppercase, underscore-separated
// form used inside environment variable names: "EU STAGING" becomes "EU_STAGING".
func EnvKey(s string) string {
	var b strings.Builder
	underscore := false
	for _, r := range strings.ToUpper(s) {
		switch {
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			underscore = false
		case !underscore:
			b.WriteByte('_')
			underscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

// redactMask is what a secret value is replaced with.
const redactMask = "••••••"

// Redactor removes secret parameter values from output before it is displayed
// or written to den's log file. The values still exist in process memory; what
// this prevents is them reaching disk or a shared screen.
type Redactor struct {
	secrets []string
}

// NewRedactor builds a Redactor for the secret-marked values in params.
// Very short values are ignored: masking a one-character secret would redact
// most of the output and reveal more than it hides.
func NewRedactor(rb Runbook, params map[string]string) *Redactor {
	var secrets []string
	for _, p := range rb.Params {
		if !p.Secret {
			continue
		}
		if v := params[p.Name]; len(v) >= 4 {
			secrets = append(secrets, v)
		}
	}
	// Longest first, so an overlapping shorter secret cannot leave a fragment.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return &Redactor{secrets: secrets}
}

// Line returns s with every known secret replaced.
func (r *Redactor) Line(s string) string {
	if r == nil || len(r.secrets) == 0 {
		return s
	}
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, redactMask)
	}
	// A colour change inside a secret hides it from the match above. Such a
	// line is shown without its colours rather than with the secret.
	if strings.Contains(s, "\x1b") {
		if plain := ansi.Strip(s); r.hasSecret(plain) {
			for _, secret := range r.secrets {
				plain = strings.ReplaceAll(plain, secret, redactMask)
			}
			return plain
		}
	}
	return s
}

// hasSecret reports whether s holds any of the secrets.
func (r *Redactor) hasSecret(s string) bool {
	for _, secret := range r.secrets {
		if strings.Contains(s, secret) {
			return true
		}
	}
	return false
}

// Lines applies Line to a copy of the slice.
func (r *Redactor) Lines(in []string) []string {
	if r == nil || len(r.secrets) == 0 {
		return in
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = r.Line(s)
	}
	return out
}

// ServiceState is the minimal view of a connected service that runbooks need.
// It exists so this package never imports service.Manager.
type ServiceState struct {
	Type      string
	Name      string
	LocalPort int
	Connected bool
}

// PortsFrom adapts a service lister into a PortSource, applying the
// DEN_PORT_<TYPE>_<NAME> naming in one place.
//
// Only live connections are reported. A standalone `den run` has no tunnels of
// its own, so it passes a nil PortSource and exports no DEN_PORT_* at all —
// exporting a configured-but-unconnected port would point scripts at a dead
// local port.
func PortsFrom(list func() []ServiceState) PortSource {
	return func() map[string]int {
		out := make(map[string]int)
		for _, s := range list() {
			if !s.Connected || s.LocalPort == 0 {
				continue
			}
			out[EnvKey(s.Type)+"_"+EnvKey(s.Name)] = s.LocalPort
		}
		return out
	}
}
