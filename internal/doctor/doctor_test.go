package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/prereq"
	"github.com/lukaszgard/den/internal/secrets"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/vpn"
)

var now = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

var macWithBrew = platform.Info{OS: platform.MacOS, Arch: "arm64", Managers: []string{"brew"}}

// fakeDeps: a Mac with Homebrew where every tool in onPath is installed and
// `aws --version` says awsVersion; ports in busy are taken.
func fakeDeps(onPath []string, awsVersion string, busy ...int) Deps {
	versions := map[string]string{"/usr/bin/aws": awsVersion}
	d := fakeMachine(macWithBrew, onPath, nil, versions)
	taken := map[int]bool{}
	for _, p := range busy {
		taken[p] = true
	}
	d.PortFree = func(p int) bool { return !taken[p] }
	return d
}

// fakeMachine is platform p with the tools in onPath installed in /usr/bin,
// the files present and `--version` answering from versions (by path).
func fakeMachine(p platform.Info, onPath, files []string, versions map[string]string) Deps {
	have := map[string]bool{}
	for _, t := range onPath {
		have[t] = true
	}
	return Deps{
		Tools: prereq.Env{
			Platform: p,
			LookPath: func(f string) (string, error) {
				if have[f] {
					return "/usr/bin/" + f, nil
				}
				return "", errors.New("not found")
			},
			Glob: func(pattern string) []string {
				var out []string
				for _, f := range files {
					if ok, _ := filepath.Match(pattern, f); ok {
						out = append(out, f)
					}
				}
				return out
			},
			Output: func(_ context.Context, name string, _ ...string) (string, error) {
				return versions[name], nil
			},
			Getenv: func(string) string { return "" },
		},
		PortFree: func(int) bool { return true },
		Now:      func() time.Time { return now },
		ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
	}
}

// awsHome writes an AWS config (and optionally credentials) and returns the
// den config pointing at them.
func awsHome(t *testing.T, awsConfig, credentials string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config")
	if err := os.WriteFile(cfgPath, []byte(awsConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	credPath := filepath.Join(dir, "credentials")
	if credentials != "" {
		if err := os.WriteFile(credPath, []byte(credentials), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &config.Config{AWS: &config.AWSSpec{ConfigPath: cfgPath, CredentialsPath: credPath}}
}

func rdsService(name, profile, credential string, port int) config.ServiceSpec {
	return config.ServiceSpec{Name: name, Type: config.ServiceTypeRDS, RDS: &config.RDSSpec{
		Region: "x", Environment: "y", EC2InstanceID: "i-0abc", AWSProfile: profile, CredentialProfile: credential,
		AWSRegionCode: "eu-central-1", LocalPort: port,
	}}
}

func find(t *testing.T, results []Result, check string) Result {
	t.Helper()
	for _, r := range results {
		if r.Check == check {
			return r
		}
	}
	t.Fatalf("no %q result in %+v", check, results)
	return Result{}
}

func TestConfigProblems(t *testing.T) {
	d := fakeDeps(nil, "")
	if r := Run(context.Background(), Input{}, d); r[0].Status != Warn || len(r) != 1 {
		t.Fatalf("no config file: %+v", r)
	}
	r := Run(context.Background(), Input{ConfigPath: "den.yaml", LoadErr: errors.New("yaml: line 3")}, d)
	if r[0].Status != Fail || !strings.Contains(r[0].Detail, "line 3") || !Failed(r) {
		t.Fatalf("broken config: %+v", r)
	}
}

func TestAWSCLIAndPlugin(t *testing.T) {
	cfg := awsHome(t, "[profile ssm]\n[profile acme-data]\n", "")
	cfg.Services = []config.ServiceSpec{rdsService("db", "ssm", "acme-data", 50001)}
	in := Input{ConfigPath: "den.yaml", Config: cfg}

	r := Run(context.Background(), in, fakeDeps([]string{"psql"}, ""))
	if find(t, r, "aws").Status != Fail || find(t, r, "session-manager-plugin").Status != Fail {
		t.Fatalf("missing aws and plugin must fail: %+v", r)
	}
	if got := find(t, r, "aws"); !reflect.DeepEqual(got.Install, []string{"brew install awscli"}) {
		t.Errorf("a missing aws CLI needs its install command: %+v", got)
	}
	r = Run(context.Background(), in, fakeDeps([]string{"aws", "session-manager-plugin", "psql"}, "aws-cli/1.33.0 Python/3"))
	if got := find(t, r, "aws"); got.Status != Fail || !strings.Contains(got.Detail, "v2") ||
		!reflect.DeepEqual(got.Install, []string{"brew upgrade awscli"}) {
		t.Fatalf("aws v1 must fail and say how to upgrade: %+v", got)
	}
	r = Run(context.Background(), in, fakeDeps([]string{"aws", "session-manager-plugin", "psql"}, "aws-cli/2.27.28 Python/3.13"))
	if Failed(r) {
		t.Fatalf("a complete setup must pass: %+v", r)
	}
}

func TestConfigProblemsAreReportedOncePerProblemWithTheirHint(t *testing.T) {
	cfg := awsHome(t, "[profile ssm]\n", "")
	in := Input{ConfigPath: "den.yaml", Config: cfg, Problems: config.Problems{
		{Severity: config.SeverityWarning, Line: 7, Code: "profile-missing", Msg: `AWS profile "typo" (used by gone) is not in the AWS config`, Hint: `did you mean "ssm"?`},
		{Severity: config.SeverityWarning, Code: "port-shared", Msg: "local port 50001 is used by a and b", Hint: "give each its own local_port"},
	}}
	r := Run(context.Background(), in, fakeDeps(nil, "aws-cli/2.1"))
	var got []Result
	for _, res := range r {
		if res.Check == "config" && res.Status != OK {
			got = append(got, res)
		}
	}
	if len(got) != 2 {
		t.Fatalf("want a config result per problem, got %+v", got)
	}
	if got[0].Status != Warn || got[0].Detail != `den.yaml:7: AWS profile "typo" (used by gone) is not in the AWS config` || got[0].Fix != `did you mean "ssm"?` {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Detail != "den.yaml: local port 50001 is used by a and b" || got[1].Fix != "give each its own local_port" {
		t.Errorf("second = %+v", got[1])
	}
	// The doctor no longer looks for them itself: a missing profile it is not
	// told about is not reported, and nothing is reported twice.
	cfg.Services = []config.ServiceSpec{rdsService("gone", "typo", "", 50003)}
	for _, res := range Run(context.Background(), Input{ConfigPath: "den.yaml", Config: cfg}, fakeDeps(nil, "aws-cli/2.1")) {
		if strings.Contains(res.Check, "profile") {
			t.Errorf("unexpected profile result: %+v", res)
		}
	}
}

func TestSSOSessions(t *testing.T) {
	cfg := awsHome(t,
		"[sso-session live]\nsso_start_url = https://live\nsso_region = eu-central-1\n"+
			"[sso-session stale]\nsso_start_url = https://stale\n"+
			"[sso-session never]\nsso_start_url = https://never\n"+
			"[sso-session renews]\nsso_start_url = https://renews\n", "")
	cache := config.SSOCacheDir(cfg.AWS.ConfigPath)
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(file, session, expires string) {
		body := `{"sessionName":"` + session + `","accessToken":"x","expiresAt":"` + expires + `"}`
		if err := os.WriteFile(filepath.Join(cache, file), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.json", "live", now.Add(time.Hour).Format(time.RFC3339))
	write("b.json", "stale", now.Add(-time.Hour).Format(time.RFC3339))
	// The access token ran out, but the refresh token renews it on the next
	// call: the login is alive.
	refreshable := `{"startUrl":"https://renews","accessToken":"x","expiresAt":"` + now.Add(-10*time.Minute).Format(time.RFC3339) +
		`","refreshToken":"r","registrationExpiresAt":"` + now.AddDate(0, 3, 0).Format(time.RFC3339) + `"}`
	if err := os.WriteFile(config.SSOTokenPath(cache, "renews"), []byte(refreshable), 0o600); err != nil {
		t.Fatal(err)
	}

	r := Run(context.Background(), Input{ConfigPath: "den.yaml", Config: cfg}, fakeDeps(nil, ""))
	if got := find(t, r, "SSO live"); got.Status != OK {
		t.Errorf("live: %+v", got)
	}
	if got := find(t, r, "SSO stale"); got.Status != Warn || !strings.Contains(got.Detail, "expired") {
		t.Errorf("stale: %+v", got)
	}
	if got := find(t, r, "SSO never"); got.Status != Warn || !strings.Contains(got.Fix, "aws sso login --sso-session never") {
		t.Errorf("never: %+v", got)
	}
	if got := find(t, r, "SSO renews"); got.Status != OK || got.Detail != "logged in (den renews its token)" {
		t.Errorf("renews: %+v", got)
	}
}

func TestPortsAndTools(t *testing.T) {
	cfg := awsHome(t, "[profile ssm]\n", "")
	cfg.Services = []config.ServiceSpec{rdsService("a", "ssm", "", 50001), rdsService("b", "ssm", "", 50001)}
	cfg.Tunnels = []config.TunnelSpec{{Name: "bastion", Command: "ssh -N -D 5555 host"}}
	in := Input{ConfigPath: "den.yaml", Config: cfg, Services: []service.Summary{
		{Name: "a", LocalPort: 50001}, {Name: "b", LocalPort: 50001}, {Name: "bastion", LocalPort: 5555},
	}}
	r := Run(context.Background(), in, fakeDeps([]string{"aws", "session-manager-plugin"}, "aws-cli/2.1", 5555))
	for _, res := range r {
		// Two services on one port is the config check's to report.
		if res.Check == "local ports" && res.Status != OK {
			t.Errorf("shared port reported by the doctor: %+v", res)
		}
	}
	if got := find(t, r, "ports in use"); !strings.Contains(got.Detail, ":5555 (bastion)") {
		t.Errorf("busy port: %+v", got)
	}
	if got := find(t, r, "psql"); got.Status != Warn || !strings.Contains(got.Detail, "a, b") {
		t.Errorf("missing psql should warn and name its users: %+v", got)
	}
	if got := find(t, r, "ssh"); got.Status != Warn {
		t.Errorf("the tunnel's ssh should be checked: %+v", got)
	}
}

type fakeItem struct {
	secrets.Item
	name, reason string
}

func (f fakeItem) Name() string     { return f.name }
func (f fakeItem) Provider() string { return "sops" }
func (f fakeItem) Probe() secrets.Status {
	return secrets.Status{Ready: f.reason == "", Reason: f.reason}
}

func TestSecretsSharingACauseShareALine(t *testing.T) {
	cfg := awsHome(t, "", "")
	items := []secrets.Item{
		fakeItem{name: "a", reason: "SSO login expired"}, fakeItem{name: "ok"},
		fakeItem{name: "b", reason: "SSO login expired"}, fakeItem{name: "c", reason: "file not found"},
	}
	r := Run(context.Background(), Input{ConfigPath: "den.yaml", Config: cfg, Secrets: items}, fakeDeps(nil, ""))
	if got := find(t, r, "2 secrets"); !strings.Contains(got.Detail, "SSO login expired (a, b)") {
		t.Errorf("grouped: %+v", got)
	}
	if got := find(t, r, "secret c"); got.Detail != "file not found (c)" {
		t.Errorf("single: %+v", got)
	}
}

func TestDeepBastionChecks(t *testing.T) {
	cfg := awsHome(t, "[profile ssm]\n", "")
	svc := func(name, instance string) config.ServiceSpec {
		s := rdsService(name, "ssm", "", 0)
		s.RDS.EC2InstanceID = instance
		return s
	}
	cfg.Services = []config.ServiceSpec{svc("up", "i-up"), svc("down", "i-down"), svc("err", "i-err")}
	d := fakeDeps([]string{"aws", "session-manager-plugin", "psql"}, "aws-cli/2.1")
	d.SSMOnline = func(_ context.Context, _, _, instance string) (bool, error) {
		switch instance {
		case "i-up":
			return true, nil
		case "i-err":
			return false, errors.New("operation error SSO OIDC: CreateToken, InvalidGrantException")
		}
		return false, nil
	}
	in := Input{ConfigPath: "den.yaml", Config: cfg}
	if r := Run(context.Background(), in, d); len(r) > 0 && strings.HasPrefix(r[len(r)-1].Check, "bastion") {
		t.Fatal("bastions are only checked with Deep")
	}
	in.Deep = true
	r := Run(context.Background(), in, d)
	if find(t, r, "bastion i-up").Status != OK || find(t, r, "bastion i-down").Status != Fail {
		t.Fatalf("online/offline: %+v", r)
	}
	if got := find(t, r, "bastion i-err"); got.Status != Warn || !strings.Contains(got.Detail, "SSO login expired") {
		t.Fatalf("an AWS error should warn in plain words: %+v", got)
	}
}

// toolItem is a secret source that runs a CLI which is not installed.
type toolItem struct {
	fakeItem
	tools []string
}

func (f toolItem) Tools() []string { return f.tools }
func (f toolItem) Probe() secrets.Status {
	return secrets.Status{Reason: f.tools[0] + " not installed", Missing: f.tools[0]}
}

func TestToolInstallSteps(t *testing.T) {
	cfg := awsHome(t, "[profile ssm]\n", "")
	shop := rdsService("shop", "ssm", "", 50001)
	shop.RDS.Engine = config.RDSEngineMySQL
	cfg.Services = []config.ServiceSpec{shop}
	cfg.VPNs = []config.VPNSpec{{Name: "office", Gateway: "vpn.example.com"}}
	in := Input{ConfigPath: "den.yaml", Config: cfg,
		Secrets: []secrets.Item{toolItem{name: "team", tools: []string{"sops"}}}}
	have := []string{"aws", "session-manager-plugin"}

	// A keg-only Homebrew install is there, just not linked onto PATH.
	mac := fakeMachine(macWithBrew, have, []string{"/opt/homebrew/opt/mysql-client/bin/mysql"}, nil)
	r := Run(context.Background(), in, mac)
	if got := find(t, r, "mysql"); got.Status != Warn || !strings.Contains(got.Detail, "not on PATH") ||
		!reflect.DeepEqual(got.Install, []string{"brew link --force mysql-client"}) {
		t.Errorf("keg-only mysql-client: %+v", got)
	}
	if got := find(t, r, "sops"); got.Status != Warn || !strings.Contains(got.Detail, "needed by team") ||
		!reflect.DeepEqual(got.Install, []string{"brew install sops"}) {
		t.Errorf("missing sops: %+v", got)
	}
	if got := find(t, r, "openfortivpn"); !reflect.DeepEqual(got.Install, []string{"brew install openfortivpn"}) {
		t.Errorf("missing openfortivpn: %+v", got)
	}
	for _, res := range r {
		if strings.HasPrefix(res.Check, "secret") {
			t.Errorf("a missing CLI is reported once, by its tool check: %+v", res)
		}
	}

	win := fakeMachine(platform.Info{OS: platform.Windows, Arch: "amd64", Managers: []string{"winget"}}, have, nil, nil)
	r = Run(context.Background(), in, win)
	if got := find(t, r, "openfortivpn"); got.Status != Warn || !strings.Contains(got.Detail, "not available on Windows") || len(got.Install) != 0 {
		t.Errorf("openfortivpn on Windows: %+v", got)
	}
	if got := find(t, r, "sops"); !reflect.DeepEqual(got.Install, []string{"winget install -e --id SecretsOPerationS.SOPS"}) {
		t.Errorf("sops on Windows: %+v", got)
	}

	bare := fakeMachine(platform.Info{OS: platform.MacOS, Arch: "arm64"}, have, nil, nil)
	if got := find(t, Run(context.Background(), in, bare), "sops"); got.Fix != "install Homebrew first: https://brew.sh" {
		t.Errorf("a Mac without Homebrew: %+v", got)
	}
}

// Ubuntu 22.04 ships openfortivpn 1.17.1, which has no SAML: it reads the
// helper's saml-login line as a bad key and then fails as a wrong password.
// Doctor has to name the version, and offer a build since apt has nothing newer.
func TestOpenfortivpnWithoutSAML(t *testing.T) {
	ubuntu := platform.Info{OS: platform.Linux, Distro: platform.Debian, Arch: "amd64", Managers: []string{"apt-get"}}
	cfg := awsHome(t, "", "")
	cfg.VPNs = []config.VPNSpec{{Name: "office", Gateway: "vpn.example.com"}}
	in := Input{ConfigPath: "den.yaml", Config: cfg}

	old := fakeMachine(ubuntu, []string{"aws", "openfortivpn"}, nil, map[string]string{"/usr/bin/openfortivpn": "1.17.1\n"})
	got := find(t, Run(context.Background(), in, old), "openfortivpn")
	if got.Status != Warn || !strings.Contains(got.Detail, "1.17.1") || !strings.Contains(got.Detail, "SAML") ||
		!slices.Contains(got.Install, "sudo apt-get remove -y openfortivpn") {
		t.Errorf("an openfortivpn without SAML: %+v", got)
	}
	if !Failed(Run(context.Background(), Input{For: []string{"openfortivpn"}}, old)) {
		t.Error("den doctor --for vpn must fail on an openfortivpn without SAML")
	}

	fresh := fakeMachine(ubuntu, []string{"aws"}, nil, nil)
	got = find(t, Run(context.Background(), in, fresh), "openfortivpn")
	if len(got.Install) != 2 || got.Install[0] != "sudo apt-get install -y openfortivpn" || !strings.Contains(got.Install[1], "den doctor again") {
		t.Errorf("a fresh apt install must be checked again: %q", got.Install)
	}
}

// The client each service type hands its connect command to must be the
// tool `den doctor --for <type>` checks, and the catalog must know it.
func TestClientToolsMatchFeatures(t *testing.T) {
	for name, feature := range prereq.Features {
		spec := config.ServiceSpec{Type: config.ServiceType(name)}
		if name == "mysql" {
			spec = config.ServiceSpec{Type: config.ServiceTypeRDS, RDS: &config.RDSSpec{Engine: config.RDSEngineMySQL}}
		}
		tool := clientTool(spec)
		if tool == "" {
			continue // not a service type
		}
		if !slices.Contains(feature.Tools, tool) {
			t.Errorf("feature %s checks %v, but %s services run %s", name, feature.Tools, name, tool)
		}
		if _, ok := prereq.Lookup(tool); !ok {
			t.Errorf("%s is not in the prereq catalog", tool)
		}
	}
}

func TestRunbookSourcesNeedGit(t *testing.T) {
	cfg := awsHome(t, "", "")
	cfg.Runbooks = &config.RunbooksConfig{Sources: []config.RunbookSource{{Name: "ops", URL: "acme/ops"}}}
	r := Run(context.Background(), Input{ConfigPath: "den.yaml", Config: cfg}, fakeDeps(nil, ""))
	if got := find(t, r, "git"); got.Status != Warn || !strings.Contains(got.Detail, "runbooks.sources") {
		t.Errorf("missing git should warn and name runbooks.sources: %+v", got)
	}
}

func TestRunbookSourceChecks(t *testing.T) {
	cfg := awsHome(t, "", "")
	in := Input{ConfigPath: "den.yaml", Config: cfg, Sources: []SourceCheck{
		{Name: "ops", Ref: "v1.4.0", Commit: "abc1234def5678"},
		{Name: "team", Problem: "not checked out yet: den runbook source update team"},
	}}
	r := Run(context.Background(), in, fakeDeps([]string{"git"}, ""))
	if got := find(t, r, "runbook source ops"); got.Status != OK || got.Detail != "checked out at abc1234 (v1.4.0)" {
		t.Errorf("ops: %+v", got)
	}
	got := find(t, r, "runbook source team")
	if got.Status != Warn || got.Detail != "not checked out yet" || got.Fix != "den runbook source update team" {
		t.Errorf("team: %+v", got)
	}
}

func TestConfigResultNamesTheFileAndWhereItCameFrom(t *testing.T) {
	d := fakeDeps(nil, "")
	r := Run(context.Background(), Input{ConfigPath: "/x/den/den.yaml", ConfigSource: "the config directory"}, d)
	if r[0].Check != "config" || r[0].Status != OK || r[0].Detail != "/x/den/den.yaml (from the config directory)" {
		t.Errorf("config in use: %+v", r[0])
	}
}

// With no den.yaml a new user needs to be told where to put one, not only that
// it is missing.
func TestFirstRunGuidanceNamesTheConfigDirectory(t *testing.T) {
	d := fakeDeps(nil, "")
	dir := filepath.Join("/home", "u", ".config", "den")
	r := Run(context.Background(), Input{ConfigDir: dir}, d)
	if len(r) != 1 || r[0].Status != Warn {
		t.Fatalf("no config file: %+v", r)
	}
	for _, want := range []string{"den init -o " + filepath.Join(dir, "den.yaml"), "release archive", "den paths"} {
		if !strings.Contains(r[0].Fix, want) {
			t.Errorf("fix lacks %q: %s", want, r[0].Fix)
		}
	}
}

// --for vpn without a vpn: entry says the smallest one and where it goes.
func TestForVPNSaysTheSmallestEntry(t *testing.T) {
	d := fakeDeps([]string{"openfortivpn"}, "")
	dir := filepath.Join("/home", "u", ".config", "den")
	target := filepath.Join(dir, "den.yaml")

	r := Run(context.Background(), Input{For: []string{"openfortivpn"}, ConfigDir: dir}, d)
	got := find(t, r, "vpn")
	if got.Status != Warn || !strings.Contains(got.Fix, target) || !strings.Contains(got.Fix, "gateway: vpn.example.com") ||
		!strings.Contains(got.Fix, "a gateway is enough") {
		t.Errorf("no den.yaml: %+v", got)
	}

	cfg := &config.Config{}
	r = Run(context.Background(), Input{For: []string{"openfortivpn"}, ConfigPath: "/w/den.yaml", Config: cfg, ConfigDir: dir}, d)
	if got := find(t, r, "vpn"); !strings.Contains(got.Fix, "/w/den.yaml") {
		t.Errorf("a den.yaml without a vpn entry names that file: %+v", got)
	}

	cfg.VPNs = []config.VPNSpec{{Name: "office", Gateway: "vpn.example.com"}}
	r = Run(context.Background(), Input{For: []string{"openfortivpn"}, ConfigPath: "/w/den.yaml", Config: cfg, ConfigDir: dir}, d)
	for _, res := range r {
		if res.Check == "vpn" {
			t.Errorf("a vpn entry needs no guidance: %+v", res)
		}
	}
	// Other features never get VPN guidance.
	for _, res := range Run(context.Background(), Input{For: []string{"sops"}, ConfigDir: dir}, d) {
		if res.Check == "vpn" {
			t.Errorf("--for sops: %+v", res)
		}
	}
}

// The permission and git warnings reach `--for vpn` from the config check.
func TestForVPNShowsTheSettingsFileWarnings(t *testing.T) {
	d := fakeDeps([]string{"openfortivpn"}, "")
	cfg := &config.Config{VPNs: []config.VPNSpec{{Name: "office", Gateway: "g"}}}
	in := Input{For: []string{"openfortivpn"}, ConfigPath: "den.yaml", Config: cfg, Problems: config.Problems{
		{Severity: config.SeverityWarning, Line: 4, Key: "vpn[0].config_file", Code: "path-permissive", Msg: "vpn[0].config_file: /f.conf can be read by other users (mode 0644)", Hint: "run chmod 600 /f.conf"},
		{Severity: config.SeverityWarning, Key: "services[0].documentdb.ca_file", Code: "path-missing", Msg: "elsewhere"},
	}}
	var got []Result
	for _, res := range Run(context.Background(), in, d) {
		if res.Check == "vpn" {
			got = append(got, res)
		}
	}
	if len(got) != 1 || got[0].Status != Warn || got[0].Fix != "run chmod 600 /f.conf" || !strings.Contains(got[0].Detail, "den.yaml:4:") {
		t.Errorf("vpn results = %+v", got)
	}
}

// An installed helper older than this den's still answers its protocol, so den
// keeps using it, and its stop leaves a hung openfortivpn running. Only doctor
// and `den vpn status` say so.
func TestCheckVPNHelper(t *testing.T) {
	cfg := &config.Config{VPNs: []config.VPNSpec{{Name: "office", Gateway: "vpn.example.com"}}}
	current := vpn.Script("/opt/homebrew/bin/openfortivpn")
	machine := func(installed string) Deps {
		d := fakeMachine(macWithBrew, nil, nil, nil)
		d.ReadFile = func(name string) ([]byte, error) {
			if name == vpn.HelperPath && installed != "" {
				return []byte(installed), nil
			}
			return nil, os.ErrNotExist
		}
		return d
	}

	for _, c := range []struct {
		name, installed string
		want            Status
	}{
		{"current", current, OK},
		{"outdated", strings.Replace(current, "sleep 1\n", "sleep 2\n", 1), Warn},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := find(t, Run(context.Background(), Input{Config: cfg, For: []string{"openfortivpn"}}, machine(c.installed)), "vpn helper")
			if r.Status != c.want {
				t.Fatalf("status = %s, want %s: %+v", r.Status, c.want, r)
			}
			if c.want == Warn && !strings.Contains(r.Fix, "den vpn install-helper") {
				t.Errorf("fix = %q, want it to say to reinstall", r.Fix)
			}
		})
	}

	// No helper: den uses the admin prompt, and there is nothing to update.
	for _, r := range Run(context.Background(), Input{Config: cfg, For: []string{"openfortivpn"}}, machine("")) {
		if r.Check == "vpn helper" {
			t.Errorf("no helper installed, yet: %+v", r)
		}
	}
}
