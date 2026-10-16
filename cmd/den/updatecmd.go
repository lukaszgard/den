package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/update"
)

// denExecutable is the binary `den update install` replaces; tests point it
// at a copy instead of the test binary.
var denExecutable = update.Executable

// Bounds for the network: the AWS CLI may refresh SSO credentials first.
const (
	updateCheckTimeout   = time.Minute
	updateInstallTimeout = 15 * time.Minute
)

// updateCmd implements `den update`: the Updates pane from a shell. It is not
// offered over MCP: an agent must not replace den's binary.
func updateCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for a new den, list releases, install any version",
		Long: `Check whether another release of den is offered, as the Updates panel does at
start: the newest release, unless updates.pin in den.yaml holds den on one, a
release is yanked (marked bad by its maintainers) or skipped on this machine.
updates.channel: prerelease offers release candidates too.

The subcommands list every release, install any of them (an older one too, to
get off a bad release), and keep the skip list of this machine. Releases are
read where the Updates panel reads them: the GitHub release feed, the releases
API with GITHUB_TOKEN or for GitHub Enterprise, or the S3 mirror of
updates.s3_bucket.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u, err := newUpdater(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), updateCheckTimeout)
			defer cancel()
			res, err := u.src.Check(ctx, runtime.GOOS, runtime.GOARCH, version, u.policy)
			if err != nil {
				return err
			}
			report := u.report(res)
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), report)
			}
			writeUpdateReport(cmd.OutOrStdout(), report)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.AddCommand(updateListCmd(), updateInstallCmd(), updateSkipCmd(true), updateSkipCmd(false))
	return cmd
}

// updater is what every `den update` command reads first.
type updater struct {
	cfg       *config.UpdatesConfig
	src       update.Source
	prefsPath string
	prefs     update.Prefs
	policy    update.Policy
}

func newUpdater(warn io.Writer) (updater, error) {
	cfg, err := updatesConfig(warn, cfgFile)
	if err != nil {
		return updater{}, err
	}
	u := updater{cfg: cfg, src: update.NewSource(cfg), prefsPath: update.PrefsFile()}
	if u.prefs, err = update.LoadPrefs(u.prefsPath); err != nil {
		return updater{}, err
	}
	u.policy = update.NewPolicy(cfg, u.prefs)
	return u, nil
}

// updatesConfig reads the updates block of the den.yaml in use, or nil
// without one. The rest of the file need not be valid: a release that rejects
// a den.yaml must still be able to install the one that does not.
func updatesConfig(warn io.Writer, explicit string) (*config.UpdatesConfig, error) {
	path, cfg, problems, err := checkConfig(explicit)
	if err != nil || path == "" {
		return nil, err
	}
	if cfg != nil {
		return cfg.UpdatesSettings(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Updates *config.UpdatesConfig `yaml:"updates"`
	}
	if yaml.Unmarshal(data, &doc) != nil {
		return nil, problems.Err(displayPath(path))
	}
	if err := config.Validate(&config.Config{Updates: doc.Updates}); err != nil {
		return nil, problems.Err(displayPath(path))
	}
	fmt.Fprintf(warn, "warning: %s has errors (den config --check lists them); using its updates block alone\n", displayPath(path))
	return doc.Updates, nil
}

// updateReport is `den update --json`.
type updateReport struct {
	Current   string `json:"current"`
	Offered   string `json:"offered"`
	Direction string `json:"direction"` // upgrade | downgrade | none
	Why       string `json:"why,omitempty"`
	Yanked    string `json:"yanked,omitempty"` // why the running release is yanked
	Channel   string `json:"channel"`
	Pin       string `json:"pin,omitempty"`
	Source    string `json:"source"`
}

func (u updater) report(res update.CheckResult) updateReport {
	r := updateReport{Current: version, Offered: res.Offer.Version, Direction: "none", Why: res.Why,
		Channel: u.policy.Channel, Pin: u.policy.Pin, Source: u.src.Location()}
	if r.Channel == "" {
		r.Channel = update.ChannelStable
	}
	switch {
	case update.SameVersion(res.Offer.Version, version):
	case update.IsVersion(version) && update.Compare(res.Offer.Version, version) < 0:
		r.Direction = "downgrade"
	default:
		r.Direction = "upgrade"
	}
	for _, rel := range res.Index {
		if update.SameVersion(rel.Version, version) {
			r.Yanked = rel.Yanked
		}
	}
	return r
}

func writeUpdateReport(w io.Writer, r updateReport) {
	if r.Yanked != "" {
		fmt.Fprintf(w, "⚠ %s is yanked: %s\n", update.Canonical(r.Current), r.Yanked)
	}
	switch r.Direction {
	case "none":
		fmt.Fprintf(w, "den %s is up to date\n", r.Current)
	case "downgrade":
		fmt.Fprintf(w, "den %s is offered, older than the %s you run\n", r.Offered, r.Current)
	default:
		fmt.Fprintf(w, "den %s is available (you run %s)\n", r.Offered, r.Current)
	}
	if r.Why != "" {
		fmt.Fprintf(w, "  %s\n", r.Why)
	}
	if r.Direction != "none" {
		fmt.Fprintln(w, "  install it with: den update install")
	}
}

// releaseEntry is one release in `den update list --json`.
type releaseEntry struct {
	Version     string     `json:"version"`
	Date        *time.Time `json:"date,omitempty"`
	Kind        string     `json:"kind"` // current | upgrade | downgrade
	Prerelease  bool       `json:"prerelease"`
	Yanked      string     `json:"yanked,omitempty"`
	Skipped     bool       `json:"skipped"`
	Pinned      bool       `json:"pinned"`
	Installable bool       `json:"installable"` // false without an archive for this platform
	URL         string     `json:"url"`
}

func (u updater) entry(rel update.Release) releaseEntry {
	e := releaseEntry{Version: rel.Version, Kind: "upgrade", Prerelease: rel.Prerelease || update.IsPrerelease(rel.Version),
		Yanked: rel.Yanked, Skipped: u.prefs.Skips(rel.Version), Installable: u.src.CanInstall(rel), URL: rel.URL,
		Pinned: u.policy.Pin != "" && update.SameVersion(rel.Version, u.policy.Pin)}
	if !rel.Date.IsZero() {
		d := rel.Date
		e.Date = &d
	}
	switch {
	case update.SameVersion(rel.Version, version):
		e.Kind = "current"
	case update.IsVersion(version) && update.Compare(rel.Version, version) < 0:
		e.Kind = "downgrade"
	}
	return e
}

func (e releaseEntry) note() string {
	var notes []string
	if e.Prerelease {
		notes = append(notes, "rc")
	}
	if e.Pinned {
		notes = append(notes, "pinned")
	}
	if e.Skipped {
		notes = append(notes, "skipped")
	}
	if e.Yanked != "" {
		notes = append(notes, "yanked: "+e.Yanked)
	}
	if !e.Installable {
		notes = append(notes, "no "+runtime.GOOS+"/"+runtime.GOARCH+" build")
	}
	return strings.Join(notes, ", ")
}

func updateListCmd() *cobra.Command {
	var asJSON, all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the published releases of den",
		Long: `List every published release, newest first: its date, whether installing it is
an upgrade or a downgrade, and what marks it (rc, pinned, skipped, yanked, no
build for this platform). Release candidates are listed with --all, or by
default on updates.channel: prerelease.

Without a releases.json at the source (every release before the version
picker), the list comes from one call to the GitHub releases API.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u, err := newUpdater(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), updateCheckTimeout)
			defer cancel()
			rels, err := u.src.Releases(ctx, runtime.GOOS, runtime.GOARCH)
			if err != nil {
				return err
			}
			entries := []releaseEntry{}
			for _, rel := range rels {
				e := u.entry(rel)
				if e.Prerelease && !all && u.policy.Channel != update.ChannelPrerelease && e.Kind != "current" && !e.Pinned {
					continue
				}
				entries = append(entries, e)
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), entries)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "VERSION\tDATE\tKIND\tNOTE")
			for _, e := range entries {
				date := ""
				if e.Date != nil {
					date = e.Date.Format("2006-01-02")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Version, date, e.Kind, e.note())
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.Flags().BoolVar(&all, "all", false, "list release candidates too")
	return cmd
}

func updateInstallCmd() *cobra.Command {
	var yes, skipCurrent bool
	cmd := &cobra.Command{
		Use:   "install [VERSION]",
		Short: "Install a release of den, older ones too",
		Long: `Install VERSION, or the release ` + "`den update`" + ` offers: download it, verify it
against the release's checksums.txt and replace this den binary. An older
VERSION is a downgrade, the way off a bad release; --skip-current then keeps
update checks from offering the version you leave again.

It asks first at a terminal; anywhere else it needs --yes. It does not restart
anything: the next den you start is the new one.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			u, err := newUpdater(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), updateInstallTimeout)
			defer cancel()
			rel, err := u.target(ctx, args)
			if err != nil {
				return err
			}
			if update.SameVersion(rel.Version, version) {
				fmt.Fprintf(out, "den %s is the version you run; nothing to install\n", version)
				return nil
			}
			down := update.IsVersion(version) && update.Compare(rel.Version, version) < 0
			if skipCurrent && !down {
				return fmt.Errorf("--skip-current skips the version a downgrade leaves, and %s is not older than %s", rel.Version, version)
			}
			if !u.src.CanInstall(rel) {
				return fmt.Errorf("%s has no %s build to install", rel.Version, rel.Archive)
			}
			exe, err := denExecutable()
			if err != nil {
				return fmt.Errorf("resolve binary: %w", err)
			}
			plan := u.plan(rel, exe, down, skipCurrent)
			if yes {
				fmt.Fprintln(out, plan)
			}
			if err := confirmAs("Install "+rel.Version+"?", plan, yes); err != nil {
				return err
			}
			if err := u.src.Install(ctx, rel, exe, runtime.GOOS, func(step string) { fmt.Fprintln(out, "✓ "+step) }); err != nil {
				return err
			}
			fmt.Fprintf(out, "Installed %s. The next den you start runs it; den --version says so.\n", rel.Version)
			if skipCurrent {
				u.prefs.Skip(version)
				if err := update.SavePrefs(u.prefsPath, u.prefs); err != nil {
					return fmt.Errorf("installed, but could not skip %s: %w", version, err)
				}
				fmt.Fprintf(out, "%s is skipped in update checks (den update unskip %s undoes it)\n",
					update.Canonical(version), update.Canonical(version))
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "install without asking")
	cmd.Flags().BoolVar(&skipCurrent, "skip-current", false, "on a downgrade, skip the version you leave in update checks")
	return cmd
}

// target is the release to install: VERSION from the list, or what the check
// offers.
func (u updater) target(ctx context.Context, args []string) (update.Release, error) {
	if len(args) == 0 {
		res, err := u.src.Check(ctx, runtime.GOOS, runtime.GOARCH, version, u.policy)
		return res.Offer, err
	}
	want := args[0]
	if !update.IsVersion(want) {
		return update.Release{}, fmt.Errorf("%q is not a version, such as v1.7.1", want)
	}
	rels, err := u.src.Releases(ctx, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return update.Release{}, err
	}
	for _, rel := range rels {
		if update.SameVersion(rel.Version, want) {
			return rel, nil
		}
	}
	return update.Release{}, fmt.Errorf("no release %s; den update list --all shows them", update.Canonical(want))
}

// plan says what an install will do, before it asks.
func (u updater) plan(rel update.Release, exe string, down, skipCurrent bool) string {
	var b strings.Builder
	switch {
	case down:
		fmt.Fprintf(&b, "Install %s, a downgrade from %s, into %s", rel.Version, version, exe)
	case update.IsVersion(version):
		fmt.Fprintf(&b, "Install %s, an upgrade from %s, into %s", rel.Version, version, exe)
	default:
		fmt.Fprintf(&b, "Install %s over this %s build, into %s", rel.Version, version, exe)
	}
	if rel.Yanked != "" {
		fmt.Fprintf(&b, "\n  ⚠ %s is yanked: %s", rel.Version, rel.Yanked)
	}
	if !update.FollowsPolicy(rel.Version) {
		fmt.Fprintf(&b, "\n  ⚠ %s is older than the version picker (%s): it offers the latest release again, "+
			"and knows no skip list or updates.pin", rel.Version, update.PolicySince)
	}
	if u.policy.Pin != "" && !update.SameVersion(rel.Version, u.policy.Pin) {
		fmt.Fprintf(&b, "\n  updates.pin holds den on %s: the next check offers it again", update.Canonical(u.policy.Pin))
	}
	if down {
		fmt.Fprintf(&b, "\n  den.yaml keys and services newer than %s are ignored or rejected by it", rel.Version)
		if skipCurrent {
			fmt.Fprintf(&b, "\n  %s will be skipped in update checks", update.Canonical(version))
		}
	}
	return b.String()
}

func updateSkipCmd(skip bool) *cobra.Command {
	use, short := "skip VERSION", "Keep update checks from offering VERSION on this machine"
	long := `Keep update checks, in the Updates panel and in den update, from offering
VERSION again on this machine, as if it were yanked: for a release that broke
something here. The list is this machine's own, kept in den's state directory
(den paths shows it); den update unskip takes a version off it.`
	if !skip {
		use, short = "unskip VERSION", "Let update checks offer VERSION again"
		long = "Take VERSION off this machine's skip list, so update checks offer it again."
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			v := args[0]
			if !update.IsVersion(v) {
				return fmt.Errorf("%q is not a version, such as v1.7.1", v)
			}
			v = update.Canonical(v)
			path := update.PrefsFile()
			prefs, err := update.LoadPrefs(path)
			if err != nil {
				return err
			}
			var changed bool
			if skip {
				changed = prefs.Skip(v)
			} else {
				changed = prefs.Unskip(v)
			}
			out := cmd.OutOrStdout()
			switch {
			case !changed && skip:
				fmt.Fprintf(out, "%s is already skipped\n", v)
				return nil
			case !changed:
				fmt.Fprintf(out, "%s was not skipped\n", v)
				return nil
			}
			if err := update.SavePrefs(path, prefs); err != nil {
				return err
			}
			if skip {
				fmt.Fprintf(out, "%s is skipped: update checks will not offer it\n", v)
			} else {
				fmt.Fprintf(out, "%s is no longer skipped\n", v)
			}
			return nil
		},
	}
}
