package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/runbook/source"
)

// runbookSourceCmd implements `den runbook source`: git repositories of
// runbooks that den checks out and lists beside your own.
func runbookSourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "source",
		Short: "Git repositories of runbooks: add, list, update, sync, remove",
		Long: "A runbook source is a git repository of runbook folders, named in den.yaml's\n" +
			"runbooks.sources. den checks it out at the commit den.lock pins and lists its\n" +
			"runbooks as <source>/<name>. Nothing updates on its own: update moves a source\n" +
			"to the newest commit of its ref, and a runbook it added or changed asks before\n" +
			"its next run.",
	}
	cmd.AddCommand(sourceAddCmd(), sourceListCmd(), sourceUpdateCmd(), sourceSyncCmd(), sourceRemoveCmd())
	return cmd
}

// interruptible is a context that Ctrl-C cancels: a fetch can be slow.
func interruptible() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func sourceAddCmd() *cobra.Command {
	var (
		src     config.RunbookSource
		envMap  []string
		noFetch bool
	)
	cmd := &cobra.Command{
		Use:   "add <url>",
		Short: "Add a git repository of runbooks to den.yaml and check it out",
		Long: "Add a runbook source to runbooks.sources in den.yaml, keeping the rest of the\n" +
			"file as written, then check it out and pin it in den.lock beside den.yaml.\n\n" +
			"  den runbook source add git@ghe.acme.com:ops/den-runbooks.git --ref v1.4.0 \\\n" +
			"      --path runbooks --env-map prod=eu-prod\n\n" +
			"The URL may be https://, ssh://, git@host:owner/repo, host/owner/repo or\n" +
			"owner/repo on GitHub. Credentials come from git (a credential helper or your\n" +
			"SSH key), never from the URL.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src.URL = args[0]
			for _, kv := range envMap {
				from, to, ok := strings.Cut(kv, "=")
				if !ok || from == "" || to == "" {
					return fmt.Errorf("invalid --env-map %q, expected theirs=yours", kv)
				}
				if src.EnvMap == nil {
					src.EnvMap = map[string]string{}
				}
				src.EnvMap[from] = to
			}
			reg, err := buildRegistry()
			if err != nil {
				return err
			}
			ctx, stop := interruptible()
			defer stop()
			out := cmd.OutOrStdout()
			u, err := reg.AddSource(ctx, src, !noFetch)
			if u.Source != "" || err == nil {
				name := u.Source
				if name == "" {
					name = src.Name
				}
				fmt.Fprintf(out, "added %s to den.yaml\n", name)
			}
			if err != nil {
				return err
			}
			if noFetch {
				fmt.Fprintf(out, "check it out with: den runbook source update %s\n", u.Source)
				return nil
			}
			printUpdate(out, u)
			fmt.Fprintf(out, "Its runbooks are listed as %s/<name>: den run --list, or den runbook browse %s\n", u.Source, u.Source)
			return nil
		},
	}
	cmd.Flags().StringVar(&src.Name, "name", "", "the source's name, prefixing its runbooks (default: from the URL)")
	cmd.Flags().StringVar(&src.Ref, "ref", "", "tag, branch or commit to check out (default: the repository's default branch)")
	cmd.Flags().StringVar(&src.Path, "path", "", "folder of the repository holding the runbook folders (default: its root)")
	cmd.Flags().StringArrayVar(&envMap, "env-map", nil, "map an env its runbooks use to one of yours, as theirs=yours (repeatable)")
	cmd.Flags().BoolVar(&noFetch, "no-fetch", false, "only write den.yaml; check it out later with den runbook source update")
	return cmd
}

// printUpdate says what an update did, runbook by runbook.
func printUpdate(w io.Writer, u source.Update) {
	for _, l := range u.Lines() {
		fmt.Fprintln(w, l)
	}
	if u.AsksAgain() {
		fmt.Fprintln(w, "New and changed runbooks ask before their next run.")
	}
}

// sourceEntry is one source in `den runbook source list --json`.
type sourceEntry struct {
	Name       string               `json:"name"`
	URL        string               `json:"url"`
	Repo       string               `json:"repo,omitempty"`
	Ref        string               `json:"ref,omitempty"`
	Path       string               `json:"path,omitempty"`
	Commit     string               `json:"commit,omitempty"`
	Updated    *time.Time           `json:"updated,omitempty"`
	CheckedOut bool                 `json:"checked_out"`
	Problem    string               `json:"problem,omitempty"`
	Runbooks   []sourceRunbookEntry `json:"runbooks"`
}

// sourceRunbookEntry is one runbook folder of a source.
type sourceRunbookEntry struct {
	Folder      string `json:"folder"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Installed   bool   `json:"installed"` // include and exclude let it show
	Listed      bool   `json:"listed"`    // and it loaded
}

func sourceEntries(states []runbook.SourceState) []sourceEntry {
	out := []sourceEntry{}
	for _, st := range states {
		e := sourceEntry{
			Name: st.Spec.Name, URL: st.Spec.URL, Repo: st.Repo, Ref: st.Spec.Ref, Path: st.Spec.Path,
			CheckedOut: st.CheckedOut, Problem: st.Problem, Runbooks: []sourceRunbookEntry{},
		}
		if st.Locked != nil {
			e.Commit = st.Locked.Commit
			e.Updated = &st.Locked.Updated
		}
		for _, f := range st.Folders {
			e.Runbooks = append(e.Runbooks, sourceRunbookEntry{Folder: f.Folder, Name: f.Name, Description: f.Description, Installed: f.Selected, Listed: f.Listed})
		}
		out = append(out, e)
	}
	return out
}

func sourceListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the runbook sources, where each is checked out and what it holds",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, err := buildRegistry()
			if err != nil {
				return err
			}
			states := reg.Sources()
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, sourceEntries(states))
			}
			if len(states) == 0 {
				fmt.Fprintln(out, "No runbook sources yet. Add one: den runbook source add <git url>")
				return nil
			}
			for _, st := range states {
				at := "not checked out"
				if st.Locked != nil && st.CheckedOut {
					at = source.Short(st.Locked.Commit)
				}
				ref := st.Spec.Ref
				if ref == "" {
					ref = "default branch"
				}
				installed := 0
				for _, f := range st.Folders {
					if f.Selected {
						installed++
					}
				}
				fmt.Fprintf(out, "%-16s %s  %s  %s", st.Spec.Name, st.Repo, ref, at)
				if st.CheckedOut {
					fmt.Fprintf(out, "  %d of %d runbooks installed", installed, len(st.Folders))
				}
				fmt.Fprintln(out)
				if st.Problem != "" {
					fmt.Fprintf(out, "  ! %s\n", st.Problem)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func sourceUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update [name...]",
		Short: "Fetch sources and move them to the newest commit of their ref",
		Long: "Fetch each named source, or every one, check out the newest commit of its ref\n" +
			"and pin it in den.lock. A tag or a commit stays put; a branch moves. Runbooks the\n" +
			"update added or changed ask before their next run.",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := buildRegistry()
			if err != nil {
				return err
			}
			names := args
			if len(names) == 0 {
				for _, st := range reg.Sources() {
					names = append(names, st.Spec.Name)
				}
			}
			if len(names) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No runbook sources to update. Add one: den runbook source add <git url>")
				return nil
			}
			ctx, stop := interruptible()
			defer stop()
			var errs []error
			for _, name := range names {
				u, err := reg.UpdateSource(ctx, name)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				printUpdate(cmd.OutOrStdout(), u)
			}
			return errors.Join(errs...)
		},
	}
}

func sourceSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Check out the commits den.lock pins, as after pulling a shared config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, err := buildRegistry()
			if err != nil {
				return err
			}
			ctx, stop := interruptible()
			defer stop()
			err = reg.SyncSources(ctx)
			for _, st := range reg.Sources() {
				if st.Locked != nil && st.CheckedOut {
					fmt.Fprintf(cmd.OutOrStdout(), "%s  at %s\n", st.Spec.Name, source.Short(st.Locked.Commit))
				}
			}
			return err
		},
	}
}

func sourceRemoveCmd() *cobra.Command {
	var assumeYes bool
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a runbook source from den.yaml and den.lock and delete its checkout",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := buildRegistry()
			if err != nil {
				return err
			}
			var src *runbook.SourceState
			for _, st := range reg.Sources() {
				if st.Spec.Name == args[0] {
					src = &st
				}
			}
			if src == nil {
				return fmt.Errorf("no runbook source named %q (den runbook source list)", args[0])
			}
			desc := fmt.Sprintf("Source: %s (%s)\n  its runbooks: %d, listed as %s/<name>\n  removed from den.yaml and den.lock; its checkout is deleted",
				src.Spec.Name, src.Spec.URL, len(src.Folders), src.Spec.Name)
			if err := confirmAs("Remove it?", desc, assumeYes); err != nil {
				return err
			}
			if err := reg.RemoveSource(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func runbookBrowseCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "browse <source>",
		Short: "List every runbook in a source, installed or not",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := buildRegistry()
			if err != nil {
				return err
			}
			for _, st := range reg.Sources() {
				if st.Spec.Name != args[0] {
					continue
				}
				out := cmd.OutOrStdout()
				if asJSON {
					return writeJSON(out, sourceEntries([]runbook.SourceState{st})[0])
				}
				if !st.CheckedOut {
					return fmt.Errorf("source %s: %s", st.Spec.Name, st.Problem)
				}
				ref := st.Spec.Ref
				if ref == "" {
					ref = "default branch"
				}
				fmt.Fprintf(out, "%s · %s @ %s (%s)\n\n", st.Spec.Name, st.Repo, source.Short(st.Locked.Commit), ref)
				for _, f := range st.Folders {
					mark := " "
					if f.Selected {
						mark = "✓"
					}
					fmt.Fprintf(out, "  %s %-20s %-32s %s\n", mark, f.Folder, f.Name, f.Description)
				}
				fmt.Fprintf(out, "\n  ✓ installed · den runbook install %s/<folder> · den runbook uninstall %s/<folder>\n", st.Spec.Name, st.Spec.Name)
				if st.Problem != "" {
					fmt.Fprintf(out, "  ! %s\n", st.Problem)
				}
				return nil
			}
			return fmt.Errorf("no runbook source named %q (den runbook source list)", args[0])
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

// splitSourceRunbook reads "<source>/<runbook>".
func splitSourceRunbook(arg string) (string, string, error) {
	src, rest, ok := strings.Cut(arg, "/")
	if !ok || src == "" || rest == "" {
		return "", "", fmt.Errorf("name it as <source>/<runbook>, e.g. ops/rds-status (den runbook browse <source> lists them)")
	}
	return src, rest, nil
}

// runbookSelectCmd implements `den runbook install` (on) and `uninstall`.
func runbookSelectCmd(on bool) *cobra.Command {
	use, short, done := "install", "Show a runbook of a source: edits its include/exclude in den.yaml", "installed"
	if !on {
		use, short, done = "uninstall", "Hide a runbook of a source: edits its include/exclude in den.yaml", "uninstalled"
	}
	return &cobra.Command{
		Use:   use + " <source>/<runbook>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			srcName, which, err := splitSourceRunbook(args[0])
			if err != nil {
				return err
			}
			reg, err := buildRegistry()
			if err != nil {
				return err
			}
			if err := reg.SelectSourceRunbook(srcName, which, on); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", done, args[0])
			return nil
		},
	}
}

func runbookForkCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "fork <source>/<runbook>",
		Short: "Copy a source's runbook into your runbooks folder, to change it",
		Long: "Copy the folder of a runbook from a source into the first of runbooks.dirs, or\n" +
			"--dir, as a runbook of your own: you can change it, and updates of the source\n" +
			"leave it alone. Its env: is set to the environment env_map gave it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, _, err := splitSourceRunbook(args[0]); err != nil {
				return err
			}
			reg, err := buildRegistry()
			if err != nil {
				return err
			}
			name, err := reg.Fork(args[0], platform.ExpandHome(dir))
			if err != nil {
				return err
			}
			r, _ := reg.Lookup(name)
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "copied %s into %s as %s\n", args[0], r.Runbook().Dir, name)
			fmt.Fprintf(out, "  run    den run %s\n", shellArg(name))
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "directory to copy it into (default: the first of runbooks.dirs)")
	return cmd
}
