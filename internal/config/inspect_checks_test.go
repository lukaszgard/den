package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// envs is an environment whose values look right, so only what a test breaks
// is reported.
const envs = `
environments:
  eu-dev:
    region: eu
    environment: int
    ec2_instance_id: i-0123456789abcdef0
    aws_profile: devops
    credential_profile: acme-data
    aws_region_code: eu-central-1
`

// svc is one Redis service on the environment above.
func svc(name, extra string) string {
	return fmt.Sprintf("  - name: %s\n    type: redis\n    env: eu-dev\n    redis:\n      redis_host: cache.example\n%s", name, extra)
}

// withAWS is the aws: block that defines the profiles envs uses, then envs.
func withAWS(t *testing.T, dir string) string {
	t.Helper()
	return awsFile(t, dir, goodAWS, "") + envs
}

// lineWith is the line of den.yaml in dir holding the nth (from 1) occurrence
// of needle, so the tests do not count lines by hand.
func lineWith(t *testing.T, dir, needle string, nth int) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "den.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, needle) {
			if nth--; nth == 0 {
				return i + 1
			}
		}
	}
	t.Fatalf("no %d. %q in den.yaml", nth, needle)
	return 0
}

func codes(ps Problems) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

func TestTunnelPortsReadsEveryForwardForm(t *testing.T) {
	tests := map[string][]int{
		"ssh -L 16379:cache:6379 bastion":                 {16379},
		"ssh -L127.0.0.1:8080:h:80 bastion":               {8080},
		"ssh -D 5555 -L 6000:h:60 -L 6001:h:61 bastion":   {5555, 6000, 6001},
		"ssh -D 127.0.0.1:1080 bastion":                   {1080},
		"ssh -L [::1]:9000:h:90 bastion":                  {9000},
		"ssh -L 8080:/var/run/x.sock bastion":             {8080},
		"ssh -L /tmp/a.sock:h:80 bastion":                 nil,
		"ssh -L /tmp/a.sock:/tmp/b.sock bastion":          nil,
		"ssh -R 9000:localhost:90 bastion":                nil, // remote forward: nothing listens here
		"ssh -N bastion":                                  nil,
		"ssh -L":                                          nil,
		"ssh -L [::1:9000:h:90 bastion":                   nil,
		"sshuttle -r bastion 10.0.0.0/8":                  nil,
		"ssh -o ExitOnForwardFailure=yes -D 5555 bastion": {5555},
	}
	for cmd, want := range tests {
		if got := tunnelPorts(cmd); !slices.Equal(got, want) {
			t.Errorf("tunnelPorts(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestInspectWarnsAboutAPortTwoServicesShare(t *testing.T) {
	// Neither sets local_port: both get Redis's default, which is easy to miss.
	dir := scaffold(t)
	_, ps := inspectIn(t, dir, withAWS(t, dir)+"services:\n"+svc("A Redis", "")+svc("B Redis", "")+svc("C Redis", ""))
	p := onlyProblem(t, ps, "port-shared")
	want := `local port 16379 is used by "A Redis", "B Redis" and "C Redis": only one can be connected at a time`
	if p.Msg != want || p.Hint != "give each its own local_port" || p.Severity != SeverityWarning {
		t.Errorf("problem = %+v", p)
	}
	if want := lineWith(t, dir, "- name: A Redis", 1); p.Line != want || p.Key != "services[0]" {
		t.Errorf("line %d key %q, want the first service on line %d", p.Line, p.Key, want)
	}
}

func TestInspectWarnsAboutAServiceAndATunnelOnOnePort(t *testing.T) {
	dir := scaffold(t)
	_, ps := inspectIn(t, dir, withAWS(t, dir)+"services:\n"+svc("cache", "      local_port: 16000\n")+
		"tunnels:\n  - name: forward\n    command: ssh -N -L 16000:db:5432 bastion\n")
	p := onlyProblem(t, ps, "port-shared")
	if !strings.Contains(p.Msg, `"cache" and "forward"`) || p.Key != "services[0].redis.local_port" {
		t.Errorf("problem = %+v", p)
	}
}

func TestInspectLeavesDistinctPortsAlone(t *testing.T) {
	dir := scaffold(t)
	_, ps := inspectIn(t, dir, withAWS(t, dir)+"services:\n"+svc("a", "      local_port: 17000\n")+svc("b", "      local_port: 17001\n")+
		"tunnels:\n  - name: forward\n    command: ssh -N -L 17002:db:5432 -L 17002:db:5433 bastion\n")
	if len(ps) != 0 {
		t.Errorf("problems = %v", ps)
	}
}

func TestInspectWarnsAboutPortsOutOfRangeAndPrivileged(t *testing.T) {
	dir := scaffold(t)
	_, ps := inspectIn(t, dir, withAWS(t, dir)+"services:\n"+svc("big", "      local_port: 70000\n")+svc("low", "      local_port: 80\n")+
		"tunnels:\n  - name: socks\n    command: ssh -D 70001 bastion\n")
	if got := codes(ps); !slices.Equal(got, []string{"port-range", "port-privileged", "port-range"}) {
		t.Fatalf("codes = %v in %v", got, ps)
	}
	if ps[0].Key != "services[0].redis.local_port" || ps[0].Line != lineWith(t, dir, "local_port: 70000", 1) || ps[1].Line != lineWith(t, dir, "local_port: 80", 1) {
		t.Errorf("keys/lines: %+v", ps)
	}
	if !strings.Contains(ps[1].Hint, "above 1024") {
		t.Errorf("hint = %q", ps[1].Hint)
	}
}

func TestInspectWarnsAboutNamesUsedTwice(t *testing.T) {
	dir := scaffold(t)
	_, ps := inspectIn(t, dir, withAWS(t, dir)+"services:\n"+svc("shared", "      local_port: 17000\n")+svc("shared", "      local_port: 17001\n")+
		"tunnels:\n  - name: t\n    command: ssh -N -D 5000 bastion\n  - name: t\n    command: ssh -N -D 5001 bastion\n"+
		"vpn:\n  - name: v\n    gateway: vpn.example\n  - name: v\n    gateway: vpn.example\n")
	var keys []string
	for _, p := range ps {
		if p.Code == "name-duplicate" {
			keys = append(keys, p.Key)
		}
	}
	if want := []string{"services[1].name", "tunnels[1].name", "vpn[1].name"}; !slices.Equal(keys, want) {
		t.Fatalf("name-duplicate keys = %v, want %v in %v", keys, want, ps)
	}
	for _, p := range ps {
		if p.Key == "services[1].name" && !strings.Contains(p.Msg, `"shared": the name is already used by services[0]`) {
			t.Errorf("message = %q", p.Msg)
		}
	}
}

func TestInspectAllowsOneNameForDifferentTypesAndKinds(t *testing.T) {
	// The example den.yaml names an RDS and a Redis service alike per
	// environment; a tunnel may share a name with a service as well.
	dir := scaffold(t)
	rds := "  - name: same\n    type: rds\n    env: eu-dev\n    rds:\n      rds_host: db.example\n      db_user: app\n      local_port: 17005\n"
	_, ps := inspectIn(t, dir, withAWS(t, dir)+"services:\n"+svc("same", "      local_port: 17000\n")+rds+
		"tunnels:\n  - name: same\n    command: ssh -N -D 5000 bastion\n")
	if len(ps) != 0 {
		t.Errorf("problems = %v", ps)
	}
}

func TestInspectChecksTheFormOfRegionsAndInstances(t *testing.T) {
	bad := strings.NewReplacer("eu-central-1", "Frankfurt", "i-0123456789abcdef0", "i-123")
	// Both come from the environment, which three services share: one mistake.
	dir := scaffold(t)
	_, ps := inspectIn(t, dir, awsFile(t, dir, goodAWS, "")+bad.Replace(envs)+"services:\n"+svc("a", "      local_port: 17000\n")+svc("b", "      local_port: 17001\n")+svc("c", "      local_port: 17002\n"))
	if got := codes(ps); !slices.Equal(got, []string{"instance-format", "region-format"}) {
		t.Fatalf("codes = %v in %v", got, ps)
	}
	if ps[0].Key != "environments.eu-dev.ec2_instance_id" || ps[0].Line != lineWith(t, dir, "ec2_instance_id", 1) {
		t.Errorf("instance problem = %+v", ps[0])
	}
	if ps[1].Key != "environments.eu-dev.aws_region_code" || ps[1].Line != lineWith(t, dir, "aws_region_code", 1) {
		t.Errorf("region problem = %+v", ps[1])
	}
}

func TestInspectAcceptsTheFormsAWSUses(t *testing.T) {
	for _, region := range []string{"eu-central-1", "us-gov-west-1", "cn-north-1", "ap-southeast-2"} {
		if !regionRe.MatchString(region) {
			t.Errorf("region %s rejected", region)
		}
	}
	for _, id := range []string{"i-0123abcd", "i-0123456789abcdef0"} {
		if !instanceRe.MatchString(id) {
			t.Errorf("instance %s rejected", id)
		}
	}
	for _, id := range []string{"i-0123", "0123456789abcdef0", "i-0123456789ABCDEF0", "i-"} {
		if instanceRe.MatchString(id) {
			t.Errorf("instance %s accepted", id)
		}
	}
}

// awsFile writes an AWS config and credentials file and returns the aws:
// block that points at them.
func awsFile(t *testing.T, dir, config, credentials string) string {
	t.Helper()
	cfgPath, credPath := filepath.Join(dir, "aws-config"), filepath.Join(dir, "aws-credentials")
	if err := os.WriteFile(cfgPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credPath, []byte(credentials), 0o600); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("aws:\n  config_path: %s\n  credentials_path: %s\n", cfgPath, credPath)
}

const goodAWS = `
[profile devops]
region = eu-central-1
[profile acme-data]
region = eu-central-1
`

func TestInspectAcceptsProfilesThatAreDefined(t *testing.T) {
	dir := scaffold(t)
	_, ps := inspectIn(t, dir, awsFile(t, dir, goodAWS, "")+envs+"services:\n"+svc("a", ""))
	if len(ps) != 0 {
		t.Errorf("problems = %v", ps)
	}
	// The credentials file names profiles as [name], and may spell [profile name].
	dir = scaffold(t)
	_, ps = inspectIn(t, dir, awsFile(t, dir, "[profile acme-data]\n", "[devops]\nkey = x\n")+envs+"services:\n"+svc("a", ""))
	if len(ps) != 0 {
		t.Errorf("a profile in the credentials file: %v", ps)
	}
	dir = scaffold(t)
	_, ps = inspectIn(t, dir, awsFile(t, dir, "[profile acme-data]\n", "[profile devops]\nkey = x\n")+envs+"services:\n"+svc("a", ""))
	if len(ps) != 0 {
		t.Errorf("[profile x] in the credentials file: %v", ps)
	}
}

func TestInspectWarnsOnceAboutAMissingProfileAndSuggestsOne(t *testing.T) {
	dir := scaffold(t)
	// devops is misspelt in the AWS config; three services use it.
	aws := awsFile(t, dir, "[profile devpos]\n[profile acme-data]\n", "")
	_, ps := inspectIn(t, dir, aws+envs+"services:\n"+svc("a", "      local_port: 17000\n")+svc("b", "      local_port: 17001\n")+svc("c", "      local_port: 17002\n")+svc("d", "      local_port: 17003\n"))
	p := onlyProblem(t, ps, "profile-missing")
	if !strings.Contains(p.Msg, `"devops" (used by a, b, c and 1 more)`) {
		t.Errorf("message = %q", p.Msg)
	}
	if p.Hint != `did you mean "devpos"?` || p.Key != "environments.eu-dev.aws_profile" || p.Line != lineWith(t, dir, "aws_profile:", 1) {
		t.Errorf("problem = %+v", p)
	}
	if len(ps) != 1 {
		t.Errorf("problems = %v", ps)
	}
}

func TestInspectSuggestsAddingAProfileThatIsNowhereNear(t *testing.T) {
	dir := scaffold(t)
	_, ps := inspectIn(t, dir, awsFile(t, dir, "[profile acme-data]\n[profile unrelated-name]\n", "")+envs+"services:\n"+svc("a", ""))
	p := onlyProblem(t, ps, "profile-missing")
	if !strings.Contains(p.Hint, "aws configure sso --profile devops") {
		t.Errorf("hint = %q", p.Hint)
	}
}

func TestInspectWarnsAboutAProfileWrittenWithoutItsPrefix(t *testing.T) {
	dir := scaffold(t)
	_, ps := inspectIn(t, dir, awsFile(t, dir, "[devops]\n[profile acme-data]\n", "")+envs+"services:\n"+svc("a", ""))
	p := onlyProblem(t, ps, "profile-bare")
	if !strings.Contains(p.Hint, "[profile devops]") {
		t.Errorf("hint = %q", p.Hint)
	}
}

func TestInspectFollowsSourceProfile(t *testing.T) {
	dir := scaffold(t)
	_, ps := inspectIn(t, dir, awsFile(t, dir, "[profile devops]\nsource_profile = base\nrole_arn = x\n[profile acme-data]\n", "")+envs+"services:\n"+svc("a", ""))
	p := onlyProblem(t, ps, "profile-source-missing")
	if !strings.Contains(p.Msg, `takes its credentials from "base"`) {
		t.Errorf("message = %q", p.Msg)
	}
	// A source_profile defined in the credentials file is fine.
	dir = scaffold(t)
	_, ps = inspectIn(t, dir, awsFile(t, dir, "[profile devops]\nrole_arn = x\nsource_profile = keys\n[profile acme-data]\n", "[keys]\naws_access_key_id = never-read\n")+envs+"services:\n"+svc("a", ""))
	if len(ps) != 0 {
		t.Errorf("problems = %v", ps)
	}
	// A loop of source_profile ends instead of hanging.
	dir = scaffold(t)
	_, ps = inspectIn(t, dir, awsFile(t, dir, "[profile devops]\nsource_profile = acme-data\n[profile acme-data]\nsource_profile = devops\n", "")+envs+"services:\n"+svc("a", ""))
	if len(ps) != 0 {
		t.Errorf("problems = %v", ps)
	}
}

func TestInspectChecksTheProfilesSecretsAndUpdatesUse(t *testing.T) {
	dir := scaffold(t)
	_, ps := inspectIn(t, dir, awsFile(t, dir, goodAWS, "")+`
secrets:
  sops:
    - name: s
      file: `+dir+`/den.yaml
      aws_profile: kms
  aws_secretsmanager:
    - name: m
      secret_id: x
      aws_profile: sm
updates:
  s3_bucket: b
  aws_profile: upd
`)
	var keys []string
	for _, p := range ps {
		if p.Code == "profile-missing" {
			keys = append(keys, p.Key)
		}
	}
	want := []string{"secrets.sops[0].aws_profile", "secrets.aws_secretsmanager[0].aws_profile", "updates.aws_profile"}
	if !slices.Equal(keys, want) {
		t.Errorf("keys = %v, want %v in %v", keys, want, ps)
	}
}

func TestInspectWarnsWhenTheAWSConfigIsMissing(t *testing.T) {
	dir := scaffold(t) // HOME has no .aws
	_, ps := inspectIn(t, dir, envs+"services:\n"+svc("a", ""))
	p := onlyProblem(t, ps, "aws-config-missing")
	if !strings.Contains(p.Msg, filepath.Join(dir, "home", ".aws", "config")) || !strings.Contains(p.Hint, "aws configure sso") {
		t.Errorf("problem = %+v", p)
	}
	if len(ps) != 1 {
		t.Errorf("one root cause, one problem: %v", ps)
	}
	// Every profile in the credentials file: nothing is wrong.
	dir = scaffold(t)
	if err := os.MkdirAll(filepath.Join(dir, "home", ".aws"), 0o750); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(dir, "home", ".aws", "credentials"))
	if err := os.WriteFile(filepath.Join(dir, "home", ".aws", "credentials"), []byte("[devops]\n[acme-data]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ps = inspectIn(t, dir, envs+"services:\n"+svc("a", "")); len(ps) != 0 {
		t.Errorf("problems = %v", ps)
	}
	// A config_path that is not there is already a path problem; not twice.
	dir = scaffold(t)
	_, ps = inspectIn(t, dir, "aws:\n  config_path: "+dir+"/nope\n"+envs+"services:\n"+svc("a", ""))
	if got := codes(ps); !slices.Equal(got, []string{"path-missing"}) {
		t.Errorf("codes = %v", got)
	}
}

func TestInspectWarnsAboutAnSSOSessionTheAWSConfigLacks(t *testing.T) {
	dir := scaffold(t)
	aws := awsFile(t, dir, "[sso-session prod-sso]\nsso_start_url = https://x\n", "")
	_, ps := inspectIn(t, dir, aws+"  sessions:\n    - session: prod-ssso\n    - session: prod-sso\n")
	p := onlyProblem(t, ps, "sso-session-unknown")
	if p.Key != "aws.sessions[0].session" || p.Hint != `did you mean "prod-sso"?` || p.Line != lineWith(t, dir, "session: prod-ssso", 1) {
		t.Errorf("problem = %+v", p)
	}
}

func TestInspectChecksAnEnvironmentNoServiceUsesYet(t *testing.T) {
	dir := scaffold(t)
	unused := `
environments:
  spare:
    region: eu
    environment: test
    ec2_instance_id: i-123
    aws_profile: devpos
    credential_profile: devops
    aws_region_code: Frankfurt
`
	_, ps := inspectIn(t, dir, awsFile(t, dir, goodAWS, "")+unused)
	if got := codes(ps); !slices.Equal(got, []string{"instance-format", "profile-missing", "region-format"}) {
		t.Fatalf("codes = %v in %v", got, ps)
	}
	for _, p := range ps {
		if p.Code == "profile-missing" && (p.Key != "environments.spare.aws_profile" || !strings.Contains(p.Msg, "used by environment spare") || p.Line != lineWith(t, dir, "aws_profile:", 1)) {
			t.Errorf("profile problem = %+v", p)
		}
	}
	// With a transport that does not sign with aws_profile, it is not looked up.
	ssh := strings.Replace(unused, "    aws_profile: devpos\n", "    transport: {type: ssh, host: h}\n    aws_profile: devpos\n", 1)
	_, ps = inspectIn(t, dir, awsFile(t, dir, goodAWS, "")+ssh)
	for _, p := range ps {
		if p.Code == "profile-missing" {
			t.Errorf("ssh needs no aws_profile: %+v", p)
		}
	}
}
