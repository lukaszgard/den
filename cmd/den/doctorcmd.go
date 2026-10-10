package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"

	"github.com/lukaszgard/den/internal/ai"
	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/doctor"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/prereq"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/runbook/source"
	"github.com/lukaszgard/den/internal/secrets"
	"github.com/lukaszgard/den/internal/secrets/providers"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/awsauth"
)

// doctorCmd implements `den doctor`, also reachable as `den check-install`.
func doctorCmd() *cobra.Command {
	var deep, asJSON, explain, yes bool
	var features []string
	var previewOS string
	cmd := &cobra.Command{
		Use:     "doctor",
		Aliases: []string{"check-install"},
		Short:   "Check what den needs and say how to install what is missing",
		Long: "Check what den needs outside itself and say how to fix what is missing:\n" +
			"the config, the AWS CLI and Session Manager plugin, the AWS profiles den.yaml\n" +
			"names, SSO logins, local ports, and the client tools the services and secrets\n" +
			"use: where each is installed, its version, and the install, upgrade or PATH\n" +
			"command for this OS and package manager. den prints these commands; it never\n" +
			"runs them. Exits 1 when a check fails.\n\n" +
			"--for checks only what the named features need, with or without a config:\n" +
			"`den doctor --for sops` before writing den.yaml, or in an onboarding script.\n" +
			"Features: " + strings.Join(prereq.FeatureNames(), ", ") + ".\n" +
			"Tunnelled services (rds, redis, …) assume the ssm transport unless ssh, eice or\n" +
			"kubectl is named too.\n\n" +
			"--os shows the install steps for another platform (macos, debian, fedora,\n" +
			"windows, optionally /amd64 or /arm64) without checking this machine, for the\n" +
			"features named by --for or, without it, for everything den.yaml uses.\n\n" +
			"--deep also asks AWS whether each bastion's SSM agent is online (read-only,\n" +
			"uses your AWS credentials).\n\n" +
			"--explain asks Claude to explain the problems in plain language. den first\n" +
			"prints exactly what it would send — names, hosts, IDs and paths replaced by\n" +
			"placeholders — and sends it only if you agree (or pass --yes). The answer\n" +
			"comes back with your real names restored locally. Needs ANTHROPIC_API_KEY\n" +
			"or `ant auth login`.",
		Example: "  den doctor\n" +
			"  den check-install --for sops\n" +
			"  den doctor --for rds,redis --json\n" +
			"  den doctor --for sops,vault --os windows",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var tools []string
			if len(features) > 0 {
				var err error
				if tools, err = prereq.Resolve(features); err != nil {
					return err
				}
			}
			if previewOS != "" {
				return previewInstall(cmd, previewOS, features, tools, asJSON)
			}
			deps := doctor.SystemDeps()
			deps.SSMOnline = ssmOnline
			var results []doctor.Result
			if len(tools) > 0 {
				in := doctor.Input{For: tools}
				if slices.Contains(tools, "openfortivpn") {
					// The VPN entry and its settings files are the one part of the
					// config --for looks at.
					loadDoctorConfig(&in)
				}
				results = doctor.Run(cmd.Context(), in, deps)
			} else {
				results = runDoctor(cmd.Context(), deep, deps)
			}
			if asJSON {
				if err := writeJSON(cmd.OutOrStdout(), results); err != nil {
					return err
				}
			} else {
				printResults(cmd.OutOrStdout(), results, deps.Tools.Platform.Label())
			}
			if explain {
				if err := explainResults(cmd, results, yes); err != nil {
					return err
				}
			}
			if doctor.Failed(results) {
				return exitCodeError{code: 1}
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&features, "for", nil, "check only what these features need (comma-separated; see above)")
	cmd.Flags().StringVar(&previewOS, "os", "", "show the install steps for another platform instead of checking this one")
	cmd.Flags().BoolVar(&deep, "deep", false, "also check each bastion is online in SSM (calls AWS)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.Flags().BoolVar(&explain, "explain", false, "ask Claude to explain the problems (shows what it sends first)")
	cmd.Flags().BoolVar(&yes, "yes", false, "with --explain, send without asking")
	_ = cmd.RegisterFlagCompletionFunc("for", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return prereq.FeatureNames(), cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("os", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"macos", "debian", "fedora", "windows"}, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

// previewInstall prints what another platform needs, without looking at
// this machine: the tools for the named features, or else the ones the
// config uses.
func previewInstall(cmd *cobra.Command, osName string, features, tools []string, asJSON bool) error {
	p, err := platform.ForOS(osName)
	if err != nil {
		return err
	}
	what := strings.Join(features, ", ")
	if len(tools) == 0 {
		path, cfg, err := loadConfig(cfgFile)
		if err != nil {
			return err
		}
		if cfg == nil {
			return errors.New("--os needs --for, or a den.yaml to read what is needed")
		}
		what = path
		in := doctor.Input{Config: cfg, Secrets: providers.Items(cfg, secrets.NewAWSLogins(cfg.AWSConfigPath()))}
		mgr := service.NewManager()
		applyConfig(cmd.Context(), mgr, cfg)
		for _, svc := range mgr.Services() {
			in.Services = append(in.Services, service.Summarize(svc))
		}
		tools = doctor.NeededTools(in)
	}
	env := prereq.PreviewEnv(p)
	statuses := make([]prereq.Status, 0, len(tools))
	for _, id := range tools {
		t, ok := prereq.Lookup(id)
		if !ok {
			t = prereq.Tool{ID: id, Purpose: "a tunnel command in den.yaml"}
		}
		statuses = append(statuses, prereq.Check(cmd.Context(), t, env))
	}
	if asJSON {
		return writeJSON(cmd.OutOrStdout(), statuses)
	}
	printGuide(cmd.OutOrStdout(), p, what, statuses)
	return nil
}

// printGuide prints install steps per tool, then merged into one block.
func printGuide(w io.Writer, p platform.Info, what string, statuses []prereq.Status) {
	w = colourWriter(w)
	bold := lipgloss.NewStyle().Bold(true)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
	fmt.Fprintf(w, "What den needs for %s on %s (%s):\n", what, p.Label(), p.Arch)
	for _, st := range statuses {
		fmt.Fprintf(w, "\n%s", bold.Render(st.Tool))
		if st.Purpose != "" {
			fmt.Fprintf(w, " — %s", st.Purpose)
		}
		fmt.Fprintln(w)
		switch {
		case st.Unsupported != "":
			fmt.Fprintf(w, "  not available: %s\n", st.Unsupported)
		case len(st.Install) == 0 && st.Docs == "":
			fmt.Fprintln(w, "  install it with your package manager")
		case len(st.Install) == 0:
			fmt.Fprintf(w, "  see %s\n", st.Docs)
		default:
			fmt.Fprintf(w, "  %s\n", dim.Render("via "+prereq.ManagerName(st.Via)))
			for _, line := range st.Install {
				fmt.Fprintf(w, "  %s\n", line)
			}
		}
		if st.MinVersion != "" {
			fmt.Fprintf(w, "  %s\n", dim.Render(st.MinVersion+" or newer: "+st.MinReason))
		}
		if len(st.Upgrade) > 0 {
			fmt.Fprintf(w, "  %s\n", dim.Render("if "+prereq.ManagerName(st.Via)+" installs one older than "+st.MinVersion+":"))
			for _, line := range st.Upgrade {
				fmt.Fprintf(w, "  %s\n", line)
			}
		}
		if st.Docs != "" && len(st.Install) > 0 {
			fmt.Fprintf(w, "  %s\n", dim.Render("docs: "+st.Docs))
		}
	}
	if combined := prereq.Combined(statuses); len(statuses) > 1 && len(combined) > 0 {
		fmt.Fprintf(w, "\n%s\n", bold.Render("All in one go:"))
		for _, line := range combined {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}
}

// explainResults shows the redacted report, asks before sending it, and
// prints Claude's explanation with the real names put back.
func explainResults(cmd *cobra.Command, results []doctor.Result, yes bool) error {
	out := colourWriter(cmd.OutOrStdout())
	var problems []doctor.Result
	for _, r := range results {
		if r.Status != doctor.OK {
			problems = append(problems, r)
		}
	}
	if len(problems) == 0 {
		fmt.Fprintln(out, "\nNothing to explain: every check passed.")
		return nil
	}
	_, cfg, _ := loadConfig(cfgFile)
	red := ai.NewRedactor(sensitiveTerms(cfg))
	report := red.Redact(doctorReport(problems))
	var model string
	if cfg != nil && cfg.AI != nil {
		model = cfg.AI.Model
	}
	claude := ai.NewClaude(model)

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
	fmt.Fprintf(out, "\nThis is exactly what would be sent to Anthropic (%s):\n\n", claude.Model())
	for line := range strings.SplitSeq(strings.TrimRight(report, "\n"), "\n") {
		fmt.Fprintln(out, "  "+dim.Render(line))
	}
	fmt.Fprintln(out)
	if !yes {
		if !isTerminal(os.Stdin) {
			return errors.New("--explain needs a terminal to ask before sending; pass --yes to send without asking")
		}
		fmt.Fprint(out, "Send it? [y/N] ")
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Fprintln(out, "Not sent.")
			return nil
		}
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
	defer cancel()
	answer, err := claude.Explain(ctx, report)
	if err != nil {
		return fmt.Errorf("--explain: %w", err)
	}
	fmt.Fprintf(out, "\n%s\n", red.Restore(answer))
	return nil
}

// doctorReport is the plain-text form of the problems the doctor found.
func doctorReport(problems []doctor.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "den %s on %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
	for _, r := range problems {
		fmt.Fprintf(&b, "[%s] %s: %s\n", r.Status, r.Check, r.Detail)
		if r.Fix != "" {
			fmt.Fprintf(&b, "  suggested fix: %s\n", r.Fix)
		}
		if len(r.Install) > 0 {
			fmt.Fprintf(&b, "  den suggests running: %s\n", strings.Join(r.Install, "; "))
		}
	}
	return b.String()
}

// sensitiveTerms lists the names from den.yaml and the AWS config that say
// something about the user's organisation, for redaction before --explain.
func sensitiveTerms(cfg *config.Config) map[string][]string {
	terms := map[string][]string{}
	add := func(kind string, values ...string) { terms[kind] = append(terms[kind], values...) }
	if cfg == nil {
		return terms
	}
	for _, s := range cfg.Services {
		add("service", s.Name)
		if s.DocumentDB != nil {
			add("path", s.DocumentDB.CAFile)
		}
		if s.Docker != nil {
			add("path", s.Docker.ComposeFile, s.Docker.EnvFile)
		}
		if t := s.Target(); t != nil {
			add("profile", t.AWSProfile, t.CredentialProfile)
			add("instance", t.EC2InstanceID)
			if t.Transport != nil {
				add("host", t.Transport.Host, t.Transport.Resource, t.Transport.Context, t.Transport.EndpointID)
			}
		}
	}
	for name, env := range cfg.Environments {
		add("environment", name)
		add("profile", env.AWSProfile, env.CredentialProfile)
		add("instance", env.EC2InstanceID)
	}
	// The config check names the files den.yaml points at, which say what the
	// organisation calls its projects.
	if a := cfg.AWS; a != nil {
		add("path", a.ConfigPath, a.CredentialsPath)
	}
	if r := cfg.Runbooks; r != nil {
		add("path", r.Dirs...)
		add("path", r.Template)
		for _, it := range r.Items {
			add("path", it.Cwd)
		}
	}
	for _, v := range cfg.VPNs {
		add("path", v.ConfigFile)
	}
	if l := cfg.Logs; l != nil {
		add("path", l.FileLocation)
	}
	for _, t := range cfg.Tunnels {
		add("tunnel", t.Name)
		if f := strings.Fields(t.Command); len(f) > 0 {
			add("host", f[len(f)-1])
		}
	}
	for _, v := range cfg.VPNs {
		add("vpn", v.Name, v.Gateway)
	}
	if cfg.AWS != nil {
		for _, s := range cfg.AWS.Sessions {
			add("session", s.Session, s.Name)
		}
	}
	if profiles, err := config.LoadAWSProfiles(cfg.AWSConfigPath()); err == nil {
		for _, p := range profiles {
			if p.Kind == "sso-session" {
				add("session", p.Name)
			} else {
				add("profile", p.Name)
			}
		}
	}
	if s := cfg.Secrets; s != nil {
		for _, it := range s.SOPS {
			add("secret", it.Name)
			add("path", it.File)
			add("profile", it.AWSProfile)
		}
		for _, it := range s.AWSSecretsManager {
			add("secret", it.Name, it.SecretID)
			add("profile", it.AWSProfile)
		}
		for _, it := range s.KeePassXC {
			add("secret", it.Name)
			add("path", it.Database, it.KeyFile, it.CLIPath)
		}
		for _, it := range s.Vault {
			add("secret", it.Name, it.Path)
			add("path", it.CACert, it.CLIPath)
		}
	}
	if u := cfg.Updates; u != nil {
		add("bucket", u.S3Bucket)
		add("profile", u.AWSProfile)
	}
	return terms
}

// runDoctor gathers the doctor's input the way the rest of den loads it, but
// keeps going when the config is broken: saying so is the doctor's job.
func runDoctor(ctx context.Context, deep bool, deps doctor.Deps) []doctor.Result {
	in := doctor.Input{Deep: deep}
	loadDoctorConfig(&in)
	if in.Config != nil {
		mgr := service.NewManager()
		applyConfig(ctx, mgr, in.Config)
		for _, svc := range mgr.Services() {
			in.Services = append(in.Services, service.Summarize(svc))
		}
		in.Secrets = providers.Items(in.Config, secrets.NewAWSLogins(in.Config.AWSConfigPath()))
		in.Sources = sourceChecks(in.Config, in.ConfigPath)
	}
	return doctor.Run(ctx, in, deps)
}

// loadDoctorConfig fills in the config part of the input: which den.yaml is in
// use and where from, and what loading it found.
func loadDoctorConfig(in *doctor.Input) {
	in.ConfigDir = platform.ConfigDir()
	path, source, err := config.DiscoverSource(cfgFile)
	switch {
	case err != nil && cfgFile != "":
		in.LoadErr = err
	case err == nil:
		in.ConfigPath, in.ConfigSource = path, source
		cfg, problems, err := config.LoadChecked(path)
		if err == nil {
			err = problems.Err(path)
		}
		if err != nil {
			in.LoadErr = err
		} else {
			in.Config, in.Problems = cfg, problems
		}
	}
}

// splitLines cuts every hint at its newlines.
func splitLines(hints []string) []string {
	var out []string
	for _, h := range hints {
		out = append(out, strings.Split(h, "\n")...)
	}
	return out
}

// sourceChecks describes where each runbook source stands on this machine,
// from den.lock and the checkouts; it fetches nothing.
func sourceChecks(cfg *config.Config, cfgPath string) []doctor.SourceCheck {
	if len(cfg.RunbookSources()) == 0 {
		return nil
	}
	reg, err := runbook.NewRegistry(cfg, cfgPath, nil)
	if err != nil {
		return nil
	}
	reg.SetSources(source.UserStore(), nil)
	var out []doctor.SourceCheck
	for _, st := range reg.Sources() {
		c := doctor.SourceCheck{Name: st.Spec.Name, Ref: st.Spec.Ref, Problem: st.Problem}
		if st.Locked != nil {
			c.Commit = st.Locked.Commit
		}
		out = append(out, c)
	}
	return out
}

// ssmOnline asks SSM whether an instance's agent is online.
func ssmOnline(ctx context.Context, profile, region, instanceID string) (bool, error) {
	cfg, err := awsauth.LoadConfig(ctx, profile, region)
	if err != nil {
		return false, err
	}
	out, err := ssm.NewFromConfig(cfg).DescribeInstanceInformation(ctx, &ssm.DescribeInstanceInformationInput{
		Filters: []ssmtypes.InstanceInformationStringFilter{{Key: aws.String("InstanceIds"), Values: []string{instanceID}}},
	})
	if err != nil {
		return false, err
	}
	for _, info := range out.InstanceInformationList {
		if aws.ToString(info.InstanceId) == instanceID {
			return info.PingStatus == ssmtypes.PingStatusOnline, nil
		}
	}
	return false, nil
}

// printResults prints one line per check with how to fix it underneath, then,
// when several tools need work, all their steps merged into one block for
// the platform named by where.
func printResults(w io.Writer, results []doctor.Result, where string) {
	w = colourWriter(w)
	mark := map[doctor.Status]string{
		doctor.OK:   lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575")).Render("✓"),
		doctor.Warn: lipgloss.NewStyle().Foreground(lipgloss.Color("#F5A623")).Render("⚠"),
		doctor.Fail: lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Render("✗"),
	}
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
	width := 0
	for _, r := range results {
		width = max(width, len(r.Check))
	}
	counts := map[doctor.Status]int{}
	var tools []prereq.Status
	for _, r := range results {
		counts[r.Status]++
		fmt.Fprintf(w, "%s %-*s  %s\n", mark[r.Status], width, r.Check, r.Detail)
		if r.Status == doctor.OK {
			continue
		}
		hints := r.Install
		if r.Fix != "" {
			hints = append([]string{r.Fix}, hints...)
		}
		// A fix may span lines (a snippet to paste): the arrow goes on the first.
		hints = splitLines(hints)
		for i, h := range hints {
			arrow := "→ "
			if i > 0 {
				arrow = "  "
			}
			fmt.Fprintf(w, "  %*s  %s\n", width, "", dim.Render(arrow+h))
		}
		if r.Tool != nil && len(r.Tool.Steps()) > 0 {
			tools = append(tools, *r.Tool)
		}
	}
	fmt.Fprintf(w, "\n%d ok, %d warnings, %d failed\n", counts[doctor.OK], counts[doctor.Warn], counts[doctor.Fail])
	if len(tools) < 2 {
		return
	}
	fmt.Fprintf(w, "\nTo fix the tools above in one go (%s):\n\n", where)
	managers := map[string]bool{}
	for _, t := range tools {
		if t.ManagerMissing && !managers[t.Via] {
			managers[t.Via] = true
			fmt.Fprintf(w, "  %s\n", dim.Render("# install "+prereq.ManagerName(t.Via)+" first: "+prereq.ManagerURL(t.Via)))
		}
	}
	for _, line := range prereq.Combined(tools) {
		if strings.HasPrefix(line, "#") {
			line = dim.Render(line)
		}
		fmt.Fprintf(w, "  %s\n", line)
	}
}

// colourWriter is w with the colours lipgloss renders cut down to what w can
// show: all of them on a capable terminal, none in a pipe or a file. Lip Gloss
// v2 always renders full colour and leaves that to whoever prints. The profile
// is detected once, here: detecting it per write reads terminfo (and asks tmux)
// for every line the report prints.
func colourWriter(w io.Writer) io.Writer { return colorprofile.NewWriter(w, os.Environ()) }
