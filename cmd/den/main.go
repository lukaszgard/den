package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/log/v2"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	den "github.com/lukaszgard/den"
	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/runbook/source"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/docker"
	"github.com/lukaszgard/den/internal/tui"
	"github.com/lukaszgard/den/internal/update"
)

var (
	cfgFile string
	version = "dev"     // set by goreleaser: -X main.version={{.Version}}
	commit  = "none"    // set by goreleaser: -X main.commit={{.Commit}}
	date    = "unknown" // set by goreleaser: -X main.date={{.Date}}
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// `den run` propagates the script's own exit code so the same
		// definitions are usable from CI and cron.
		if ec, ok := errors.AsType[exitCodeError](err); ok {
			os.Exit(ec.code)
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

// newRootCmd builds the den command tree.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "den",
		Short:   "Developer connectivity tool — RDS, Redis, DocumentDB, Redshift, OpenSearch, Neptune, Docker",
		Long:    "den opens tunnels, manages IAM tokens, and provides a TUI for all your dev services.",
		Version: version,
		RunE:    runTUI,
	}

	// The install instructions in README.md tell people to run `den --version`;
	// cobra only wires that flag up when Version is set. commit/date come from
	// the same goreleaser ldflags, so report all three.
	root.SetVersionTemplate(fmt.Sprintf("den %s (commit %s, built %s)\n", version, commit, date))

	root.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file (default: ./den.yaml, then den.yaml in the config directory; see den paths)")
	// Errors are printed once, below, so that `den run` can exit with the
	// script's code without cobra also printing "Error: exit status N".
	root.SilenceErrors = true
	// Usage is for mistakes in the command line itself, which cobra reports
	// before this runs; an error from running the command (a missing config
	// file, a failed connection) is not helped by a screen of flags.
	root.PersistentPreRun = func(cmd *cobra.Command, _ []string) { cmd.SilenceUsage = true }

	root.AddCommand(
		connectCmd(),
		listCmd(),
		configCmd(),
		createEnvCmd(),
		runCmd(),
		runbookCmd(),
		vpnCmd(),
		schemaCmd(),
		doctorCmd(),
		initCmd(),
		pathsCmd(),
		mcpCmd(),
	)
	return root
}

// runTUI is the default command — show the TUI split-pane dashboard.
func runTUI(cmd *cobra.Command, args []string) error {
	path, cfg, problems, err := checkConfig(cfgFile)
	if err != nil {
		return err
	}
	if path == "" {
		log.Warn("no config file found, showing empty dashboard")
	}
	mgr := service.NewManager()
	logSkipped(applyConfig(cmd.Context(), mgr, cfg))

	opts := tui.Options{ConfigPath: path, Config: cfg, ConfigProblems: problems, Version: version, RunbookHistory: runbook.UserHistory(),
		RunbookSources: source.UserStore(), RunbookTrust: runbook.UserTrust(), RunbookState: runbook.UserStateRoot(),
		RunbookDrafts: runbook.UserDrafts()}
	if path != "" {
		opts.ConfigFile = &tui.ConfigFile{
			Check: func() (*config.Config, config.Problems, error) { return config.LoadChecked(path) },
			WouldStop: func(cfg *config.Config) []string {
				entries, _ := serviceEntries(cfg)
				return mgr.WouldStop(entries)
			},
			Apply: func(ctx context.Context, cfg *config.Config) service.ReconcileResult {
				return applyConfig(ctx, mgr, cfg)
			},
		}
	}
	return runProgram(tui.New(mgr, opts))
}

// The final model runProgram gets back is a *tui.AppModel; without this method
// the restart after an update would be skipped without a word.
var _ interface{ RestartRequested() bool } = (*tui.AppModel)(nil)

func runProgram(m tea.Model) error {
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	// Re-exec only now: Run has left the alt screen and raw mode, and the new
	// den inherits the terminal as it is.
	if app, ok := final.(interface{ RestartRequested() bool }); ok && app.RestartRequested() {
		if err := update.Reexec(); err != nil {
			return fmt.Errorf("restart failed: %w — run den again", err)
		}
	}
	return nil
}

// connectCmd implements `den connect NAME`: the dashboard with that service
// already connecting.
func connectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "connect NAME",
		Short: "Open the dashboard with a configured service connecting",
		Long: "Open the dashboard and connect the service den.yaml calls NAME, as if you had\n" +
			"selected it and pressed c. `den list` shows the names.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, cfg, problems, err := checkConfig(cfgFile)
			if err != nil {
				return err
			}
			mgr := service.NewManager()
			logSkipped(applyConfig(cmd.Context(), mgr, cfg))
			svc, err := findService(mgr, args[0])
			if err != nil {
				return err
			}
			if err := mgr.StartOne(cmd.Context(), svc); err != nil {
				return err
			}
			return runProgram(tui.New(mgr, tui.Options{ConfigPath: path, Config: cfg, ConfigProblems: problems, Version: version, RunbookHistory: runbook.UserHistory(),
				RunbookSources: source.UserStore(), RunbookTrust: runbook.UserTrust(), RunbookState: runbook.UserStateRoot(),
				RunbookDrafts: runbook.UserDrafts()}))
		},
	}
}

// findService returns the service called name, or an error that lists the
// names there are.
func findService(mgr *service.Manager, name string) (service.Service, error) {
	var names []string
	for _, svc := range mgr.Services() {
		if svc.Info().Name == name {
			return svc, nil
		}
		names = append(names, svc.Info().Name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no service %q: den.yaml defines none", name)
	}
	return nil, fmt.Errorf("no service %q (have %s)", name, strings.Join(names, ", "))
}

// listCmd implements `den list`
func listCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List services from config",
		Long: "List the services den.yaml defines. The status is this process's own view, so\n" +
			"it is idle: live connections belong to the TUI (or `den mcp`) that opened them.",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := loadConfig(cfgFile)
			if err != nil {
				return err
			}
			mgr := service.NewManager()
			logSkipped(applyConfig(cmd.Context(), mgr, cfg))
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), serviceSummaries(mgr))
			}
			for _, svc := range mgr.Services() {
				info := svc.Info()
				fmt.Fprintf(cmd.OutOrStdout(), "  %-30s  [%-10s]  %s\n", info.Name, info.Type, svc.State().Status)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

// configCmd implements `den config`
func configCmd() *cobra.Command {
	var asJSON, check, strict bool
	cmd := &cobra.Command{
		Use:   "config [FILE]",
		Short: "Show resolved config, or check it with --check",
		Long: `Shows den.yaml as den reads it: environments merged in, defaults filled.

With --check, checks the file instead and prints every problem with its line and
a hint on how to fix it: mistakes den cannot run with (errors), and things it
runs without but you probably did not mean (warnings): unknown keys, files that
are not there, local ports two services share, unknown AWS profiles, duplicate
names. FILE checks that file instead of the one den finds. The exit code is 1
when there are errors, and with --strict when there are warnings too, so a
pipeline can run it.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !check && (len(args) > 0 || strict) {
				return errors.New("FILE and --strict go with --check")
			}
			file := cfgFile
			if len(args) > 0 {
				file = args[0]
			}
			path, err := config.Discover(file)
			if err != nil {
				return err
			}
			if check {
				return runConfigCheck(cmd.OutOrStdout(), path, asJSON, strict)
			}
			cfg, problems, err := config.LoadChecked(path)
			if err != nil {
				return err
			}
			if err := problems.Err(displayPath(path)); err != nil {
				return err
			}
			printProblems(cmd.ErrOrStderr(), displayPath(path), problems, false)
			if asJSON {
				v, err := configJSON(cfg)
				if err != nil {
					return err
				}
				return writeJSON(cmd.OutOrStdout(), v)
			}
			out, err := yaml.Marshal(cfg)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Config file: %s\n\n%s\n", path, out)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.Flags().BoolVar(&check, "check", false, "check the file and print its problems instead of the config")
	cmd.Flags().BoolVar(&strict, "strict", false, "with --check, exit 1 on warnings too")
	return cmd
}

// createEnvCmd implements `den create-env [--file compose.yml]`
func createEnvCmd() *cobra.Command {
	var composeFile string
	cmd := &cobra.Command{
		Use:   "create-env",
		Short: "Start docker compose environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			if composeFile == "" {
				composeFile = "docker-compose.yml"
			}
			spec := &config.DockerSpec{ComposeFile: composeFile}
			svc := docker.New("Local Stack", spec)

			mgr := service.NewManager()
			mgr.Register(svc)

			ctx := context.Background()
			mgr.StartAll(ctx)

			return runProgram(tui.New(mgr, tui.Options{Version: version}))
		},
	}
	cmd.Flags().StringVarP(&composeFile, "file", "f", "", "docker-compose file (default: docker-compose.yml)")
	return cmd
}

// schemaCmd implements `den schema`.
func schemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema for den.yaml",
		Long: "Print the JSON Schema for den.yaml. Point your editor's YAML language server\n" +
			"at it for completion and validation:\n\n" +
			"  den schema > ~/.config/den/den.schema.json\n" +
			"  # first line of den.yaml:\n" +
			"  # yaml-language-server: $schema=den.schema.json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := cmd.OutOrStdout().Write(den.Schema)
			return err
		},
	}
}
