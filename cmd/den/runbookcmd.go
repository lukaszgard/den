package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/lukaszgard/den/internal/agent"
	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/runbook/source"
)

// exitCodeError carries a runbook's exit status out to main so `den run` can
// exit with the script's own code — that is what makes these definitions usable
// from CI and cron, not just from the TUI.
type exitCodeError struct{ code int }

func (e exitCodeError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// runCmd implements `den run <name>`.
func runCmd() *cobra.Command {
	var (
		paramFlags []string
		assumeYes  bool
		listOnly   bool
		asJSON     bool
	)
	cmd := &cobra.Command{
		Use:   "run [name]",
		Short: "Run a runbook (one of your scripts, with den's AWS context) or sequence",
		Long: "Run a runbook or sequence defined in den.yaml or discovered in a scanned\n" +
			"directory. Exits with the script's own exit code.\n\n" +
			"DEN_PORT_* variables reflect live connections and are only available inside\n" +
			"the TUI; a standalone `den run` exports none.",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := buildRegistry()
			if err != nil {
				return err
			}
			if listOnly || len(args) == 0 {
				// Discovery problems are shown where someone is looking at the
				// list of runbooks, not before every single run.
				for _, w := range reg.Warnings() {
					fmt.Fprintf(os.Stderr, "warning: %s\n", w)
				}
				if asJSON {
					return writeJSON(cmd.OutOrStdout(), runbookEntries(reg))
				}
				printRunbooks(cmd.OutOrStdout(), reg)
				return nil
			}

			params, err := parseParams(paramFlags)
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			name := args[0]
			if r, ok := reg.Lookup(name); ok {
				return execRunbook(ctx, reg, r, params, assumeYes)
			}
			if s, ok := reg.LookupSequence(name); ok {
				return execSequence(ctx, s, params, assumeYes)
			}
			return fmt.Errorf("no runbook or sequence named %q (try `den run --list`)", name)
		},
	}
	cmd.Flags().StringArrayVar(&paramFlags, "param", nil, "parameter as key=value (repeatable)")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&listOnly, "list", false, "list runbooks and sequences")
	cmd.Flags().BoolVar(&asJSON, "json", false, "with --list, print JSON")
	return cmd
}

func execRunbook(ctx context.Context, reg *runbook.Registry, r *runbook.Runner, params map[string]string, assumeYes bool) error {
	rb := r.Runbook()
	params, err := resolveParams(rb.Params, params)
	if err != nil {
		return err
	}
	if rb.Confirm {
		if err := confirm(describeRunbook(rb), assumeYes); err != nil {
			return err
		}
	}

	opts := reg.ExecOptionsFor(rb, params)
	opts.Origin = runbook.OriginCLI
	opts.OnLine = runLinePrinter(os.Stdout, isTerminal(os.Stdout))

	result := r.Execute(ctx, opts)
	return reportResult(rb.Name, result)
}

func execSequence(ctx context.Context, s *runbook.SequenceRunner, params map[string]string, assumeYes bool) error {
	seq := s.Sequence()
	params, err := resolveParams(s.Params(), params)
	if err != nil {
		return err
	}
	if s.NeedsConfirm() {
		desc := fmt.Sprintf("Sequence: %s\n  steps: %s", seq.Name, strings.Join(seq.Steps, " → "))
		if err := confirm(desc, assumeYes); err != nil {
			return err
		}
	}

	result := s.Execute(ctx, runbook.SequenceOptions{
		Params: params,
		OnLine: runLinePrinter(os.Stdout, isTerminal(os.Stdout)),
		Origin: runbook.OriginCLI,
	})
	return reportResult(seq.Name, result)
}

// runLinePrinter prints a run's lines to w. They carry the colours the script
// printed; into a pipe or a file those would be escape codes between the
// words, so they are kept only when w is a terminal.
func runLinePrinter(w io.Writer, colours bool) func(string) {
	return func(line string) {
		if !colours {
			line = ansi.Strip(line)
		}
		_, _ = fmt.Fprintln(w, line)
	}
}

func reportResult(name string, result runbook.Run) error {
	switch result.Status {
	case runbook.StatusSucceeded:
		return nil
	case runbook.StatusCancelled:
		fmt.Fprintf(os.Stderr, "%s: cancelled\n", name)
		return exitCodeError{code: 130} // conventional code for SIGINT
	case runbook.StatusTimedOut:
		fmt.Fprintf(os.Stderr, "%s: timed out after %s\n", name, result.Duration().Round(1e9))
		return exitCodeError{code: 124} // conventional code for timeout
	default:
		if result.ExitCode > 0 {
			fmt.Fprintf(os.Stderr, "%s: failed (exit %d)\n", name, result.ExitCode)
			return exitCodeError{code: result.ExitCode}
		}
		if result.Err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", name, result.Err)
		}
		return exitCodeError{code: 1}
	}
}

// describeRunbook renders what is about to run, so a confirmation is an
// informed one rather than a reflex.
func describeRunbook(rb runbook.Runbook) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Runbook: %s\n", rb.Name)
	if rb.Description != "" {
		fmt.Fprintf(&b, "  %s\n", rb.Description)
	}
	fmt.Fprintf(&b, "  from:    %s\n", rb.Origin())
	if rb.Remote != nil && rb.Remote.Note != "" {
		fmt.Fprintf(&b, "  note:    %s\n", rb.Remote.Note)
	}
	if rb.Env != "" {
		fmt.Fprintf(&b, "  env:     %s\n", rb.Env)
	}
	if rb.Cwd != "" {
		fmt.Fprintf(&b, "  cwd:     %s\n", rb.Cwd)
	}
	fmt.Fprintf(&b, "  command: %s", strings.TrimSpace(rb.Command))
	return b.String()
}

// confirm gates a dangerous runbook. In a non-TTY it refuses outright rather
// than assuming yes: a pipeline should have to say so explicitly with --yes.
func confirm(description string, assumeYes bool) error {
	return confirmAs("Run this?", description, assumeYes)
}

// confirmAs asks question about what description says, at a terminal only:
// anywhere else the answer has to come from --yes, so the gate fails closed.
func confirmAs(question, description string, assumeYes bool) error {
	if assumeYes {
		return nil
	}
	if !isTerminal(os.Stdin) {
		return fmt.Errorf("this command requires confirmation; pass --yes to run it non-interactively:\n\n%s", description)
	}
	fmt.Printf("%s\n\n%s [y/N] ", description, question)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return fmt.Errorf("cancelled")
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return fmt.Errorf("cancelled")
}

// resolveParams fills in any parameters not supplied on the command line. At a
// terminal it asks; in a pipeline it fails with a message naming what is
// missing, rather than silently running with empty values.
func resolveParams(specs []config.ParamSpec, params map[string]string) (map[string]string, error) {
	var unanswered []config.ParamSpec
	for _, p := range specs {
		if _, ok := params[p.Name]; !ok {
			unanswered = append(unanswered, p)
		}
	}
	if len(unanswered) == 0 {
		return params, nil
	}

	if isTerminal(os.Stdin) {
		answers, err := promptParams(unanswered)
		if err != nil {
			return nil, err
		}
		merged := make(map[string]string, len(params)+len(answers))
		maps.Copy(merged, answers)
		maps.Copy(merged, params)
		return merged, checkParams(specs, merged)
	}
	return params, checkParams(specs, params)
}

// promptParams asks for values at a terminal.
func promptParams(specs []config.ParamSpec) (map[string]string, error) {
	values := make(map[string]*string, len(specs))
	fields := make([]huh.Field, 0, len(specs))
	for _, p := range specs {
		v := new(string)
		*v = p.Default
		values[p.Name] = v

		if len(p.Options) > 0 {
			fields = append(fields, huh.NewSelect[string]().
				Title(p.Name).Description(p.Description).
				Options(huh.NewOptions(p.Options...)...).Value(v))
			continue
		}
		in := huh.NewInput().Title(p.Name).Description(p.Description).Value(v)
		if p.Secret {
			in = in.EchoMode(huh.EchoModePassword)
		}
		if p.Required {
			name := p.Name
			in = in.Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return fmt.Errorf("%s is required", name)
				}
				return nil
			})
		}
		fields = append(fields, in)
	}
	if err := huh.NewForm(huh.NewGroup(fields...)).WithTheme(standaloneTheme()).Run(); err != nil {
		return nil, fmt.Errorf("cancelled")
	}
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[k] = *v
	}
	return out, nil
}

func checkParams(specs []config.ParamSpec, params map[string]string) error {
	if missing := runbook.MissingParams(specs, params); len(missing) > 0 {
		return fmt.Errorf("missing required parameter(s): %s (pass --param %s=...)",
			strings.Join(missing, ", "), missing[0])
	}
	return nil
}

func parseParams(flags []string) (map[string]string, error) {
	out := make(map[string]string, len(flags))
	for _, f := range flags {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid --param %q, expected key=value", f)
		}
		out[k] = v
	}
	return out, nil
}

func printRunbooks(w io.Writer, reg *runbook.Registry) {
	runbooks, sequences := reg.Runbooks(), reg.Sequences()
	if len(runbooks) == 0 && len(sequences) == 0 {
		fmt.Fprintln(w, "No runbooks yet. A runbook is one of your scripts, run with den's AWS profile, region and tunnel ports set.")
		fmt.Fprintln(w, "Point runbooks.dirs in den.yaml at a folder, then: den runbook new <name>")
		return
	}
	if len(runbooks) > 0 {
		fmt.Fprintln(w, "Runbooks:")
		for _, r := range runbooks {
			rb := r.Runbook()
			marker := " "
			if rb.Confirm {
				marker = "!"
			}
			fmt.Fprintf(w, "  %s %-32s %s\n", marker, rb.Name, rb.Description)
		}
	}
	if len(sequences) > 0 {
		fmt.Fprintln(w, "\nSequences:")
		for _, s := range sequences {
			seq := s.Sequence()
			fmt.Fprintf(w, "    %-32s %s\n", seq.Name, strings.Join(seq.Steps, " → "))
		}
	}
	fmt.Fprintln(w, "\n  ! = requires confirmation")
}

// runbookCmd implements `den runbook`: managing runbook folders. Running one
// is `den run`.
func runbookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runbook",
		Short: "Your scripts, run with den's AWS profile, region and tunnels: manage runbook folders",
	}
	cmd.AddCommand(runbookNewCmd(), runbookMigrateCmd(), runbookDeleteCmd(), runbookSourceCmd(), runbookBrowseCmd(),
		runbookSelectCmd(true), runbookSelectCmd(false), runbookForkCmd())
	return cmd
}

func runbookDeleteCmd() *cobra.Command {
	var assumeYes bool
	cmd := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm"},
		Short:   "Delete a runbook or sequence for good, with its runs and its state folder",
		Long: "Delete a runbook or a sequence for good: its folder, script or den.yaml entry,\n" +
			"the runs den kept, its DEN_RUNBOOK_STATE folder and, for a runbook from a\n" +
			"source, the record of your review. A runbook from a source is uninstalled: its\n" +
			"checkout is shared with the source's other runbooks. A running one is refused.\n" +
			"It asks first at a terminal; elsewhere it needs --yes.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := buildRegistry()
			if err != nil {
				return err
			}
			plan, err := reg.PlanDelete(args[0])
			if err != nil {
				return err
			}
			if err := confirmAs("Delete it for good?", describeDeletion(plan), assumeYes); err != nil {
				return err
			}
			if _, err := reg.Delete(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// describeDeletion is what den runbook delete asks about.
func describeDeletion(d runbook.Deletion) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s)", d.Name, d.Kind)
	for _, part := range []struct {
		title string
		lines []string
	}{{"Deletes", d.Gone()}, {"Keeps", d.Kept()}, {"Note", d.Notes()}} {
		if len(part.lines) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s:", part.title)
		for _, l := range part.lines {
			fmt.Fprintf(&b, "\n  - %s", l)
		}
	}
	return b.String()
}

func runbookMigrateCmd() *cobra.Command {
	var all, assumeYes bool
	cmd := &cobra.Command{
		Use:   "migrate [name]",
		Short: "Move a runbook defined by a # den: header script into a folder of its own",
		Long: "Move a runbook that is a single script with a # den: header into a folder of its\n" +
			"own next to it: runbook.yaml from the header, the script as run<ext> without\n" +
			"the den: lines, and README.md from its comment block. --all moves every one.\n" +
			"It asks first at a terminal; elsewhere it needs --yes.",
		Args: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) == 1) {
				return fmt.Errorf("name one runbook, or pass --all")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			path, cfg, err := loadConfig(cfgFile)
			if err != nil {
				return err
			}
			if cfg == nil {
				return errNoConfig()
			}
			reg, err := runbook.NewRegistry(cfg, path, nil)
			if err != nil {
				return err
			}
			var names []string
			if all {
				for _, rb := range reg.HeaderScripts() {
					names = append(names, rb.Name)
				}
			} else {
				names = args
			}
			out := cmd.OutOrStdout()
			if len(names) == 0 {
				fmt.Fprintln(out, "Nothing to migrate: every runbook has its folder.")
				return nil
			}
			var plan strings.Builder
			for _, name := range names {
				r, ok := reg.Lookup(name)
				if !ok {
					return fmt.Errorf("no runbook named %q (try `den run --list`)", name)
				}
				m := runbook.PlanMigration(r.Runbook().Source)
				fmt.Fprintf(&plan, "%s\n  %s\n  → %s/ (%s)\n", name, m.Script, m.Dir, strings.Join(m.Files(), ", "))
			}
			if err := confirmAs("Move them?", strings.TrimSuffix(plan.String(), "\n"), assumeYes); err != nil {
				return err
			}
			for _, name := range names {
				m, err := reg.Migrate(name)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "moved %s into %s\n", name, m.Dir)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "move every runbook still defined by a header script")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func runbookNewCmd() *cobra.Command {
	var dir, agentName string
	cmd := &cobra.Command{
		Use:   "new <name> [what it should do]",
		Short: "Create a runbook folder, and have an agent write it if you like",
		Long: "Create a folder for a new runbook in the first of runbooks.dirs, or in --dir,\n" +
			"from runbooks.template when den.yaml sets one, else from den's own template:\n" +
			"a runbook.yaml naming every key it may set, a run.sh and a README.md.\n\n" +
			"With --agent (or a description of what it should do), den then starts Claude Code or\n" +
			"Codex in the folder, with den's MCP server and its runbook guide, to write it. The\n" +
			"session is yours: the agent asks before it writes or runs anything.\n\n" +
			"  den runbook new \"RDS status\" \"check my RDS instances in eu-staging, in Rust\" --agent claude",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir != "" {
				dir = platform.ExpandHome(dir)
			}
			path, cfg, err := loadConfig(cfgFile)
			if err != nil {
				return err
			}
			request := ""
			if len(args) == 2 {
				request = strings.TrimSpace(args[1])
			}
			// Find the agent before making anything: a missing one must not
			// leave a folder behind.
			var ag agent.Agent
			if agentName != "" || request != "" {
				var specs []config.AgentSpec
				if cfg != nil && cfg.Runbooks != nil {
					specs = cfg.Runbooks.Agents
					if agentName == "" {
						agentName = cfg.Runbooks.Agent
					}
				}
				if ag, err = agent.Select(specs, agentName); err != nil {
					return err
				}
			}

			reg, err := runbook.NewRegistry(cfg, path, nil)
			if err != nil {
				return err
			}
			var folder string
			switch {
			case cfg != nil:
				if folder, err = reg.NewRunbook(args[0], dir); err != nil {
					return err
				}
			case dir != "":
				if folder, err = runbook.Scaffold(dir, args[0], ""); err != nil {
					return err
				}
			default:
				return fmt.Errorf("no config file found: pass --dir, or add runbooks.dirs to den.yaml")
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "created %s\n\n", folder)
			if ag.Kind == "" {
				fmt.Fprintf(out, "  edit   %s\n", filepath.Join(folder, "run.sh"))
				fmt.Fprintf(out, "  run    den run %s\n", shellArg(strings.TrimSpace(args[0])))
				fmt.Fprintln(out, "  or open it in den: Runbooks, then enter")
				return nil
			}
			return runAgent(cmd, reg, ag, path, folder, request)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "directory to create the folder in (default: the first of runbooks.dirs)")
	cmd.Flags().StringVar(&agentName, "agent", "", "have an agent write it: claude, codex, auto or a runbooks.agents name (default: runbooks.agent, else auto)")
	return cmd
}

// runAgent starts the agent in the runbook's folder, attached to this
// terminal, then says what den makes of what it wrote. The agent's exit code
// is den's.
func runAgent(cmd *cobra.Command, reg *runbook.Registry, ag agent.Agent, cfgPath, folder, request string) error {
	den, err := os.Executable()
	if err != nil {
		return err
	}
	if cfgPath != "" {
		if abs, err := filepath.Abs(cfgPath); err == nil {
			cfgPath = abs
		}
	}
	ac := agent.Command(ag, agent.Options{
		Den: den, Config: cfgPath, Dir: folder, Guide: reg.Guide(folder), Request: request,
	})
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "starting %s in it, with den's MCP tools and runbook guide…\n\n", ag.Name)

	c := exec.Command(ac.Path, ac.Args...)
	c.Dir, c.Stdin, c.Stdout, c.Stderr = ac.Dir, os.Stdin, out, cmd.ErrOrStderr()
	runErr := c.Run()

	reg.Rescan()
	v := reg.ValidateFolder(folder)
	fmt.Fprintln(out)
	switch {
	case v.OK:
		fmt.Fprintf(out, "%s is ready (%s)\n  run    den run %s\n  or open it in den: Runbooks, then enter\n", v.Name, v.Kind, shellArg(v.Name))
	default:
		fmt.Fprintf(out, "%s does not load yet:\n", folder)
		for _, e := range v.Errors {
			fmt.Fprintf(out, "  ✗ %s\n", e)
		}
	}
	for _, w := range v.Warnings {
		fmt.Fprintf(out, "  ! %s\n", w)
	}
	if ee, ok := errors.AsType[*exec.ExitError](runErr); ok {
		return exitCodeError{code: ee.ExitCode()}
	}
	return runErr
}

// shellArg quotes s for a shell command line when it needs it.
func shellArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"$`\\|&;<>()*?[]#~!{}") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// buildRegistry loads the config and assembles the runbook registry. The CLI
// passes no PortSource: DEN_PORT_* describes live tunnels, which only exist in
// the TUI process.
func buildRegistry() (*runbook.Registry, error) {
	path, cfg, err := loadConfig(cfgFile)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, errNoConfig()
	}
	reg, err := runbook.NewRegistry(cfg, path, nil)
	if err != nil {
		return nil, err
	}
	reg.SetHistory(runbook.UserHistory(), func(err error) { fmt.Fprintf(os.Stderr, "warning: %v\n", err) })
	useUserRunbookDirs(reg)
	return reg, nil
}

// useUserRunbookDirs points reg at the user's runbook sources, the record of
// which of their runbooks the user has run, and the runbooks' state folders.
func useUserRunbookDirs(reg *runbook.Registry) {
	reg.SetSources(source.UserStore(), runbook.UserTrust())
	reg.SetStateRoot(runbook.UserStateRoot())
}

// isTerminal reports whether f is a real interactive terminal. Checking
// os.ModeCharDevice is not enough: /dev/null is a character device too, which
// would make the confirmation gate fail open exactly when stdin is redirected.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}
