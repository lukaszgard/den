// Package doctor checks what den needs outside itself — the AWS CLI and
// session plugin, the AWS config and SSO logins, free local ports, the client
// tools the configured services hand commands to, the kernel's PPP support
// for the VPN, and, on request, whether the bastions are reachable through
// SSM — and says how to fix what is not.
//
// Checks only report; they never change anything.
package doctor

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/prereq"
	"github.com/lukaszgard/den/internal/secrets"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/vpn"
)

// Status is a check's verdict.
type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn" // works, but something will likely bite
	Fail Status = "fail" // a configured feature cannot work
)

// Result is one check's outcome.
type Result struct {
	Check  string `json:"check"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
	// Install are the commands that fix a tool check on this platform:
	// install, upgrade or link. den only prints them.
	Install []string `json:"install,omitempty"`
	Docs    string   `json:"docs,omitempty"`
	// Tool is what a tool check found, for merging the install steps of
	// several checks into one block.
	Tool *prereq.Status `json:"-"`
}

// Deps is everything the checks reach outside the process, so tests can
// replace it.
type Deps struct {
	// Tools is the machine the tool checks look at.
	Tools    prereq.Env
	PortFree func(port int) bool
	Now      func() time.Time
	// SSMOnline reports whether an instance's SSM agent is online, as the
	// given profile in the given region. Only --deep calls it.
	SSMOnline func(ctx context.Context, profile, region, instanceID string) (bool, error)
	// ReadFile reads what the kernel says about itself (/proc, /lib/modules)
	// and the installed VPN helper.
	ReadFile func(name string) ([]byte, error)
}

// SystemDeps are the real implementations; SSMOnline is supplied by the caller
// so this package does not need the AWS SDK.
func SystemDeps() Deps {
	return Deps{
		Tools: prereq.SystemEnv(),
		PortFree: func(port int) bool {
			ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port)) //nolint:noctx
			if err != nil {
				return false
			}
			_ = ln.Close()
			return true
		},
		Now:      time.Now,
		ReadFile: os.ReadFile,
	}
}

// Input is what the checks look at.
type Input struct {
	ConfigPath string // empty when no config file was found
	// ConfigSource says where ConfigPath was found (config.SourceFlag, …).
	ConfigSource string
	// ConfigDir is where a person's own den files belong
	// (platform.ConfigDir), named when there is no den.yaml yet.
	ConfigDir string
	Config    *config.Config // nil when there is none or it failed to load
	LoadErr   error          // why Config is nil, if it failed
	// Problems are what the config check found in the file (the warnings, as
	// a config with errors is not loaded). Doctor shows them with the rest so
	// one command says everything; it does not look for them again.
	Problems config.Problems
	// Services are the services built from Config, for their local ports.
	Services []service.Summary
	// Secrets are the configured secret sources; each reports its own
	// readiness (a missing CLI, a missing file).
	Secrets []secrets.Item
	// Sources are where the runbook sources stand on this machine.
	Sources []SourceCheck
	Deep    bool // also ask AWS whether each bastion is online
	// For, when set, replaces everything above: only these tools (catalog
	// IDs, from prereq.Resolve) are checked, config or not. The one thing it
	// still reads from the config is the VPN entry, for the openfortivpn tool.
	For []string
}

// Run performs every check that applies to in and returns the results in a
// stable order.
func Run(ctx context.Context, in Input, d Deps) []Result {
	if len(in.For) > 0 {
		// Asked for by name, so a missing tool is a failure: scripts can
		// gate on `den doctor --for sops`.
		needs := make([]toolNeed, len(in.For))
		for i, id := range in.For {
			needs[i] = toolNeed{id: id, missing: Fail}
		}
		out := checkTools(ctx, needs, d.Tools)
		if needsPPPCheck(d, slices.Contains(in.For, "openfortivpn")) {
			out = append(out, checkPPP(d, Fail))
		}
		if slices.Contains(in.For, "openfortivpn") {
			out = append(out, checkVPNSettings(in)...)
			out = append(out, checkVPNHelper(in, d)...)
		}
		return out
	}
	var out []Result
	add := func(r Result) { out = append(out, r) }

	add(checkConfig(in))
	out = append(out, configProblems(in)...)
	cfg := in.Config
	if cfg == nil {
		return out
	}
	needs := collectNeeds(in)

	// Without these nothing that goes through AWS works, so their absence
	// fails; a missing client only affects the services that use it.
	out = append(out, checkTools(ctx, needs.toolNeeds(), d.Tools)...)
	if needsPPPCheck(d, len(needs.tools["openfortivpn"]) > 0) {
		add(checkPPP(d, Warn))
	}
	out = append(out, checkVPNHelper(in, d)...)

	// Whether the profiles den.yaml names exist is the config check's, already
	// reported above; what remains here is whether their logins are alive.
	profiles, profErr := config.LoadAWSProfiles(cfg.AWSConfigPath())
	if profErr == nil {
		out = append(out, checkSSO(cfg, profiles, d)...)
	}
	out = append(out, checkPorts(needs, d)...)
	out = append(out, checkSecrets(in.Secrets)...)
	out = append(out, checkSources(in.Sources)...)
	if in.Deep {
		out = append(out, checkBastions(ctx, needs, d)...)
	}
	return out
}

// SourceCheck is one runbook source as the checks see it.
type SourceCheck struct {
	Name, Ref, Commit string
	// Problem is what is wrong or off with it, ending in ": den …" with the
	// command that fixes it.
	Problem string
}

// checkSources reports each runbook source: where it is checked out, or
// what keeps its runbooks from being listed.
func checkSources(sources []SourceCheck) []Result {
	var out []Result
	for _, s := range sources {
		r := Result{Check: "runbook source " + s.Name, Status: OK}
		if s.Problem != "" {
			r.Status, r.Detail = Warn, s.Problem
			if i := strings.LastIndex(s.Problem, ": den "); i >= 0 {
				r.Detail, r.Fix = s.Problem[:i], s.Problem[i+2:]
			}
			out = append(out, r)
			continue
		}
		ref := s.Ref
		if ref == "" {
			ref = "default branch"
		}
		short := s.Commit
		if len(short) > 7 {
			short = short[:7]
		}
		r.Detail = fmt.Sprintf("checked out at %s (%s)", short, ref)
		out = append(out, r)
	}
	return out
}

// Failed reports whether any result failed.
func Failed(results []Result) bool {
	for _, r := range results {
		if r.Status == Fail {
			return true
		}
	}
	return false
}

// configProblems is one result per problem the config check found, with its
// hint as the fix.
func configProblems(in Input) []Result {
	var out []Result
	for _, p := range in.Problems {
		status := Warn
		if p.Severity == config.SeverityError {
			status = Fail
		}
		out = append(out, Result{Check: "config", Status: status, Detail: p.Located(in.ConfigPath), Fix: p.Hint})
	}
	return out
}

func checkConfig(in Input) Result {
	switch {
	case in.LoadErr != nil:
		return Result{Check: "config", Status: Fail, Detail: in.LoadErr.Error(), Fix: "fix den.yaml (`den schema` gives your editor completion and validation)"}
	case in.ConfigPath == "":
		fix := "generate one with `den init`, or run `den doctor --for sops,rds` to check what a feature needs"
		if in.ConfigDir != "" {
			target := filepath.Join(in.ConfigDir, "den.yaml")
			fix = "write one with `den init -o " + target + "`, or copy den.yaml from the release archive there;\n" +
				"your own files (den.yaml, vpn/*.conf, runbooks/) belong in " + in.ConfigDir + "; `den paths` lists every place den looks"
		}
		return Result{Check: "config", Status: Warn, Detail: "no den.yaml found; den shows an empty dashboard", Fix: fix}
	}
	detail := in.ConfigPath
	if in.ConfigSource != "" {
		detail += " (from " + in.ConfigSource + ")"
	}
	return Result{Check: "config", Status: OK, Detail: detail}
}

// smallestVPNEntry is the least den.yaml needs for a VPN: with a gateway den
// passes --saml-login and fills in the port and the SAML listener itself.
const smallestVPNEntry = "vpn:\n  - name: office\n    gateway: vpn.example.com"

// checkVPNSettings is the VPN's own part of `--for vpn`: where the VPN entry
// goes when there is none, and what is wrong with the settings files the
// entries name (a file others can read, one inside a git work tree).
func checkVPNSettings(in Input) []Result {
	if in.LoadErr != nil {
		return nil // the config is broken; checkConfig's job, and not run here
	}
	if in.Config == nil || len(in.Config.VPNs) == 0 {
		where := in.ConfigPath
		if where == "" && in.ConfigDir != "" {
			where = filepath.Join(in.ConfigDir, "den.yaml")
		}
		fix := "add the smallest entry; a gateway is enough, config_file only for openfortivpn settings den has no key for:\n" + smallestVPNEntry
		if where != "" {
			fix = "add this to " + where + " (a gateway is enough; config_file only for openfortivpn settings den has no key for):\n" + smallestVPNEntry
		}
		return []Result{{Check: "vpn", Status: Warn, Detail: "no vpn: entry in den.yaml", Fix: fix}}
	}
	var out []Result
	for _, p := range in.Problems {
		if strings.HasPrefix(p.Key, "vpn[") {
			out = append(out, Result{Check: "vpn", Status: Warn, Detail: p.Located(in.ConfigPath), Fix: p.Hint})
		}
	}
	return out
}

// checkVPNHelper reports whether the installed VPN helper is the one this den
// would install. An older one still answers den's protocol, so den keeps using
// it, but it is its stop that runs: one from before the KILL escalation leaves
// a hung openfortivpn running. Nothing is reported when no helper is installed.
func checkVPNHelper(in Input, d Deps) []Result {
	if in.Config == nil || len(in.Config.VPNs) == 0 || d.ReadFile == nil {
		return nil
	}
	data, err := d.ReadFile(vpn.HelperPath)
	if err != nil {
		return nil
	}
	if vpn.ScriptOutdated(string(data)) {
		return []Result{{Check: "vpn helper", Status: Warn, Detail: vpn.HelperPath + " is outdated", Fix: vpn.OutdatedScriptFix}}
	}
	return []Result{{Check: "vpn helper", Status: OK, Detail: vpn.HelperPath + " is current"}}
}

// toolNeed is a tool the config depends on: who needs it and how bad its
// absence is.
type toolNeed struct {
	id, why string
	missing Status
}

func (n needs) toolNeeds() []toolNeed {
	// Without these nothing that goes through AWS works, so their absence
	// fails; a missing client only affects the services that use it.
	var out []toolNeed
	if n.aws {
		out = append(out, toolNeed{id: "aws", missing: Fail})
	}
	if len(n.bastions) > 0 {
		out = append(out, toolNeed{id: "session-manager-plugin", why: "SSM tunnels", missing: Fail})
	}
	for _, t := range n.toolList() {
		out = append(out, toolNeed{id: t.name, why: t.why, missing: Warn})
	}
	return out
}

// NeededTools lists the tools the config in in depends on, for describing
// what another platform would need to run it.
func NeededTools(in Input) []string {
	if in.Config == nil {
		return nil
	}
	var ids []string
	for _, n := range collectNeeds(in).toolNeeds() {
		ids = append(ids, n.id)
	}
	return ids
}

// checkTools checks the tools concurrently: each may run a `--version`, and
// some (mongosh starts Node) take a while.
func checkTools(ctx context.Context, needs []toolNeed, env prereq.Env) []Result {
	out := make([]Result, len(needs))
	var wg sync.WaitGroup
	for i, n := range needs {
		wg.Go(func() {
			out[i] = checkTool(ctx, n, env)
		})
	}
	wg.Wait()
	return out
}

func checkTool(ctx context.Context, n toolNeed, env prereq.Env) Result {
	t, known := prereq.Lookup(n.id)
	if !known {
		// A tunnel's command: den can check it is there, not install it.
		t = prereq.Tool{ID: n.id}
	}
	st := prereq.Check(ctx, t, env)
	r := Result{Check: n.id, Tool: &st, Install: st.Steps(), Docs: st.Docs}
	if !st.Found() && len(st.Upgrade) > 0 {
		// Whether this release's package is new enough shows only once it is
		// installed, and an old one fails in ways that do not name its version.
		r.Install = append(r.Install, "# then run den doctor again: some releases' package is older than "+st.MinVersion)
	}
	neededBy := ""
	switch {
	case n.why != "":
		neededBy = " (needed by " + n.why + ")"
	case st.Purpose != "":
		neededBy = " (for " + st.Purpose + ")"
	}
	version := ""
	if st.Version != "" {
		version = " " + st.Version
	}
	switch {
	case !st.Found() && st.Unsupported != "":
		r.Status, r.Detail = Warn, "not available on "+env.Platform.Label()+": "+st.Unsupported
	case !st.Found():
		r.Status, r.Detail = n.missing, n.id+" not found"+neededBy
	case st.TooOld:
		r.Status = Warn
		if st.MinRequired {
			r.Status = n.missing
		}
		r.Detail = fmt.Sprintf("%s%s — %s; %s or newer needed", st.Path, version, st.MinReason, st.MinVersion)
	case st.OffPath && !st.DenFinds:
		r.Status, r.Detail = Warn, "installed at "+st.Path+version+", but not on PATH"+neededBy
	case st.OffPath:
		r.Status, r.Detail = OK, st.Path+version+" (den finds it there)"
	default:
		r.Status, r.Detail = OK, st.Path+version
	}
	switch {
	case r.Status == OK || st.Unsupported != "":
	case st.ManagerMissing:
		r.Fix = "install " + prereq.ManagerName(st.Via) + " first: " + prereq.ManagerURL(st.Via)
	case len(r.Install) == 0 && !known:
		r.Fix = "install " + n.id + neededBy
	case len(r.Install) == 0:
		r.Fix = "see " + st.Docs
	}
	return r
}

func checkSSO(cfg *config.Config, profiles []config.AWSProfile, d Deps) []Result {
	sessions, _ := config.ResolveSSOSessions(profiles, cfg.AWSSessions())
	cacheDir := config.SSOCacheDir(cfg.AWSConfigPath())
	var out []Result
	for _, s := range sessions {
		check := "SSO " + s.Name
		fix := "aws sso login --sso-session " + s.Name + " (or Enter on it in the AWS pane)"
		tok := config.ReadSSOToken(s.Name, s.StartURL, cacheDir)
		switch {
		case tok == nil:
			out = append(out, Result{Check: check, Status: Warn, Detail: "not logged in", Fix: fix})
		case !tok.ExpiresAt.After(d.Now()):
			out = append(out, Result{Check: check, Status: Warn, Detail: "login expired at " + tok.ExpiresAt.Local().Format("Jan 2 15:04"), Fix: fix})
		default:
			out = append(out, Result{Check: check, Status: OK, Detail: "logged in until " + tok.ExpiresAt.Local().Format("Jan 2 15:04")})
		}
	}
	return out
}

func checkPorts(n needs, d Deps) []Result {
	var out []Result
	ports := make([]int, 0, len(n.ports))
	for p := range n.ports {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	var busy []string
	for _, p := range ports {
		users := n.ports[p]
		if !d.PortFree(p) {
			busy = append(busy, fmt.Sprintf(":%d (%s)", p, strings.Join(users, ", ")))
		}
	}
	if len(busy) > 0 {
		out = append(out, Result{Check: "ports in use", Status: Warn,
			Detail: "already taken — by a running den, or by something else: " + strings.Join(busy, "; "),
			Fix:    "stop what holds them (`lsof -i :PORT`), or change local_port"})
	}
	if len(out) == 0 {
		out = append(out, Result{Check: "local ports", Status: OK, Detail: fmt.Sprintf("%d configured, all free", len(ports))})
	}
	return out
}

// checkSecrets reports the secret sources that cannot be read yet. Sources
// failing for the same reason — typically one expired SSO login — share a line.
func checkSecrets(items []secrets.Item) []Result {
	var reasons []string
	byReason := map[string][]string{}
	for _, it := range items {
		st := it.Probe()
		// A missing CLI has its own tool check, with install steps.
		if st.Ready || st.Missing != "" {
			continue
		}
		if _, seen := byReason[st.Reason]; !seen {
			reasons = append(reasons, st.Reason)
		}
		byReason[st.Reason] = append(byReason[st.Reason], it.Name())
	}
	var out []Result
	for _, reason := range reasons {
		names := byReason[reason]
		check := "secret " + names[0]
		if len(names) > 1 {
			check = fmt.Sprintf("%d secrets", len(names))
		}
		out = append(out, Result{Check: check, Status: Warn, Detail: reason + " (" + strings.Join(names, ", ") + ")"})
	}
	return out
}

func checkBastions(ctx context.Context, n needs, d Deps) []Result {
	if d.SSMOnline == nil {
		return nil
	}
	keys := make([]bastion, 0, len(n.bastions))
	for b := range n.bastions {
		keys = append(keys, b)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	// One AWS round trip per bastion, all at once: in sequence they add up.
	out := make([]Result, len(keys))
	var wg sync.WaitGroup
	for i, b := range keys {
		wg.Go(func() {
			out[i] = checkBastion(ctx, b, strings.Join(n.bastions[b], ", "), d)
		})
	}
	wg.Wait()
	return out
}

func checkBastion(ctx context.Context, b bastion, users string, d Deps) Result {
	check := "bastion " + b.instance
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	online, err := d.SSMOnline(c, b.profile, b.region, b.instance)
	switch {
	case err != nil:
		return Result{Check: check, Status: Warn, Detail: fmt.Sprintf("could not ask SSM as %s: %s", b.profile, ExplainAWSError(err)),
			Fix: "fix the credentials, then run den doctor --deep again"}
	case !online:
		return Result{Check: check, Status: Fail, Detail: fmt.Sprintf("SSM agent not online in %s (used by %s)", b.region, users),
			Fix: "start the instance, or check its SSM agent and IAM instance profile"}
	}
	return Result{Check: check, Status: OK, Detail: fmt.Sprintf("online in %s (%s)", b.region, users)}
}

var missingProfile = regexp.MustCompile(`failed to get shared config profile, (\S+)`)

// ExplainAWSError shortens the SDK's nested credential errors to the cause.
func ExplainAWSError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "InvalidGrantException"), strings.Contains(msg, "refresh cached SSO token failed"),
		strings.Contains(msg, "SSOProviderInvalidToken"), strings.Contains(msg, "token has expired"):
		return "SSO login expired — log in to its session"
	case missingProfile.MatchString(msg):
		return "profile " + missingProfile.FindStringSubmatch(msg)[1] + " is not in the AWS config"
	case strings.Contains(msg, "AccessDenied"):
		return "access denied — the profile may not allow ssm:DescribeInstanceInformation"
	}
	if i := strings.LastIndex(msg, ": "); i >= 0 && len(msg)-i > 20 {
		return msg[i+2:]
	}
	return msg
}
