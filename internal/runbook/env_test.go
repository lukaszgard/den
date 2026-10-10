package runbook

import (
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
)

func envMap(kvs []string) map[string]string {
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

func TestEnvKey(t *testing.T) {
	cases := map[string]string{
		"EU TEST":    "EU_TEST",
		"eu-test":    "EU_TEST",
		"rds":        "RDS",
		"a.b/c":      "A_B_C",
		"  spaced  ": "SPACED",
		"US PROD":    "US_PROD",
		"documentdb": "DOCUMENTDB",
	}
	for in, want := range cases {
		if got := EnvKey(in); got != want {
			t.Errorf("EnvKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildEnvInjectsConnectionContext(t *testing.T) {
	rb := Runbook{Name: "Rotate", Env: "eu-prod"}
	env := config.EnvSpec{
		Region:            "eu",
		Environment:       "prod",
		EC2InstanceID:     "i-123",
		AWSProfile:        "acme-prod",
		CredentialProfile: "acme-data-prod",
		AWSRegionCode:     "eu-central-1",
	}
	got := envMap(BuildEnv(rb, env, "/etc/den.yaml", nil, map[string]int{"RDS_EU_TEST": 50110}))

	want := map[string]string{
		"AWS_PROFILE":            "acme-prod",
		"AWS_REGION":             "eu-central-1",
		"AWS_DEFAULT_REGION":     "eu-central-1",
		"DEN_CREDENTIAL_PROFILE": "acme-data-prod",
		"DEN_ENV":                "prod",
		"DEN_REGION":             "eu",
		"DEN_EC2_INSTANCE_ID":    "i-123",
		"DEN_ENV_NAME":           "eu-prod",
		"DEN_CONFIG":             "/etc/den.yaml",
		"DEN_RUNBOOK":            "Rotate",
		"DEN_PORT_RDS_EU_TEST":   "50110",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestBuildEnvPrecedence(t *testing.T) {
	// env_vars is the author's explicit override and must beat everything den
	// derived on their behalf.
	rb := Runbook{
		Name:    "X",
		EnvVars: map[string]string{"AWS_PROFILE": "explicit-wins"},
		Params:  []config.ParamSpec{{Name: "cluster"}},
	}
	env := config.EnvSpec{AWSProfile: "from-env-spec"}
	got := envMap(BuildEnv(rb, env, "", map[string]string{"cluster": "app-prod"}, nil))

	if got["AWS_PROFILE"] != "explicit-wins" {
		t.Errorf("AWS_PROFILE = %q, want env_vars to win", got["AWS_PROFILE"])
	}
	if got["DEN_PARAM_CLUSTER"] != "app-prod" {
		t.Errorf("DEN_PARAM_CLUSTER = %q, want %q", got["DEN_PARAM_CLUSTER"], "app-prod")
	}
	// The inherited environment must still be present.
	if got["PATH"] == "" {
		t.Error("expected the inherited PATH to survive")
	}
}

func TestBuildEnvSkipsEmptyValues(t *testing.T) {
	got := envMap(BuildEnv(Runbook{Name: "X"}, config.EnvSpec{}, "", nil, nil))
	for _, k := range []string{"AWS_PROFILE", "DEN_ENV", "DEN_EC2_INSTANCE_ID"} {
		if _, present := got[k]; present {
			t.Errorf("%s should be absent when the env spec is empty", k)
		}
	}
}

// A run's output goes through pipes, where tools print no colour unless asked.
func TestBuildEnvAsksForColour(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	got := envMap(BuildEnv(Runbook{Name: "X"}, config.EnvSpec{}, "", nil, nil))
	if got["FORCE_COLOR"] != "1" || got["CLICOLOR_FORCE"] != "1" {
		t.Errorf("FORCE_COLOR=%q CLICOLOR_FORCE=%q, want both 1", got["FORCE_COLOR"], got["CLICOLOR_FORCE"])
	}

	// The author's env_vars decide, whether they refuse colour or force it off.
	rb := Runbook{Name: "X", EnvVars: map[string]string{"NO_COLOR": "1"}}
	got = envMap(BuildEnv(rb, config.EnvSpec{}, "", nil, nil))
	if _, set := got["FORCE_COLOR"]; set {
		t.Error("NO_COLOR in env_vars must keep den from forcing colour")
	}
	rb = Runbook{Name: "X", EnvVars: map[string]string{"FORCE_COLOR": "0"}}
	if got = envMap(BuildEnv(rb, config.EnvSpec{}, "", nil, nil)); got["FORCE_COLOR"] != "0" {
		t.Errorf("FORCE_COLOR = %q, want env_vars to win", got["FORCE_COLOR"])
	}
}

func TestBuildEnvHonoursInheritedNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	got := envMap(BuildEnv(Runbook{Name: "X"}, config.EnvSpec{}, "", nil, nil))
	if _, set := got["FORCE_COLOR"]; set {
		t.Error("an inherited NO_COLOR must keep den from forcing colour")
	}
}

// A shell tab is a real terminal: nothing is forced there.
func TestContextVarsDoNotForceColour(t *testing.T) {
	got := ContextVars(Runbook{Name: "X"}, config.EnvSpec{}, "", nil, nil)
	if _, set := got["FORCE_COLOR"]; set {
		t.Error("ContextVars feeds shell tabs, which are terminals already")
	}
}

func TestRedactorCatchesASecretSplitByColour(t *testing.T) {
	rb := Runbook{Params: []config.ParamSpec{{Name: "token", Secret: true}}}
	r := NewRedactor(rb, map[string]string{"token": "SUPERSECRET123"})

	got := r.Line("token \x1b[31mSUPER\x1b[0mSECRET123 used")
	if strings.Contains(got, "SUPERSECRET") || strings.Contains(got, "SUPER") {
		t.Errorf("secret leaked through a colour change: %q", got)
	}
	// A coloured line without a secret keeps its colours.
	if want := "\x1b[32mok\x1b[0m"; r.Line(want) != want {
		t.Errorf("a line with no secret lost its colour: %q", r.Line(want))
	}
	// Colour around a secret that is whole is still masked in place.
	if got := r.Line("\x1b[31mSUPERSECRET123\x1b[0m"); strings.Contains(got, "SUPERSECRET") {
		t.Errorf("secret leaked: %q", got)
	}
}

func TestRedactor(t *testing.T) {
	rb := Runbook{Params: []config.ParamSpec{
		{Name: "token", Secret: true},
		{Name: "cluster"},
		{Name: "pin", Secret: true},
	}}
	params := map[string]string{
		"token":   "SUPERSECRET123",
		"cluster": "app-prod",
		"pin":     "99", // too short to mask safely
	}
	r := NewRedactor(rb, params)

	got := r.Line("using SUPERSECRET123 against app-prod")
	if strings.Contains(got, "SUPERSECRET123") {
		t.Errorf("secret leaked: %q", got)
	}
	if !strings.Contains(got, "app-prod") {
		t.Errorf("non-secret value should survive: %q", got)
	}
	// A two-character secret would redact half the output; leave it alone.
	if r.Line("exit code 99") != "exit code 99" {
		t.Error("very short secrets must not be masked")
	}

	var nilRedactor *Redactor
	if nilRedactor.Line("untouched") != "untouched" {
		t.Error("nil Redactor must pass text through")
	}
}

func TestPortsFromOnlyReportsLiveConnections(t *testing.T) {
	src := PortsFrom(func() []ServiceState {
		return []ServiceState{
			{Type: "rds", Name: "EU TEST", LocalPort: 50110, Connected: true},
			{Type: "rds", Name: "EU PROD", LocalPort: 50113, Connected: false},
			{Type: "redis", Name: "EU DEV", LocalPort: 0, Connected: true},
		}
	})
	got := src()
	if got["RDS_EU_TEST"] != 50110 {
		t.Errorf("RDS_EU_TEST = %d, want 50110", got["RDS_EU_TEST"])
	}
	if _, ok := got["RDS_EU_PROD"]; ok {
		t.Error("a disconnected service must not be exported")
	}
	if _, ok := got["REDIS_EU_DEV"]; ok {
		t.Error("a service with no local port must not be exported")
	}
}

func TestApplyParamDefaults(t *testing.T) {
	specs := []config.ParamSpec{
		{Name: "tier", Default: "test"},
		{Name: "cluster"},
	}
	got := ApplyParamDefaults(specs, map[string]string{"cluster": "app-prod"})
	if got["tier"] != "test" {
		t.Errorf("tier = %q, want the default %q", got["tier"], "test")
	}
	got = ApplyParamDefaults(specs, map[string]string{"tier": "prod"})
	if got["tier"] != "prod" {
		t.Errorf("tier = %q, want the supplied value to win", got["tier"])
	}
}

func TestCredentialProfileFallsBackToAWSProfile(t *testing.T) {
	for _, tc := range []struct {
		spec config.EnvSpec
		want string
	}{
		{config.EnvSpec{AWSProfile: "acme-dev"}, "acme-dev"},
		{config.EnvSpec{AWSProfile: "acme-dev", CredentialProfile: "acme-data"}, "acme-data"},
	} {
		got := envMap(BuildEnv(Runbook{Name: "r", Env: "eu-dev"}, tc.spec, "", nil, nil))
		if got["DEN_CREDENTIAL_PROFILE"] != tc.want {
			t.Errorf("%+v: DEN_CREDENTIAL_PROFILE = %q, want %q", tc.spec, got["DEN_CREDENTIAL_PROFILE"], tc.want)
		}
	}
}
