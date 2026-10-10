package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	vpnsvc "github.com/lukaszgard/den/internal/service/vpn"
)

// vpnCmd groups the commands that manage the VPN privilege helper. Everything
// privileged happens here, in a terminal where sudo can ask for a password —
// never inside the TUI.
func vpnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vpn",
		Short: "Manage the VPN privilege helper",
		Long: "Manage the helper that lets den bring the VPN up and down without an admin\n" +
			"prompt. Without it, openfortivpn needs root and den falls back to the\n" +
			"system's admin dialog on every connect and disconnect.",
	}
	cmd.AddCommand(vpnInstallHelperCmd(), vpnUninstallHelperCmd(), vpnStatusCmd())
	return cmd
}

func vpnInstallHelperCmd() *cobra.Command {
	var (
		nameFilter string
		assumeYes  bool
	)
	cmd := &cobra.Command{
		Use:   "install-helper",
		Short: "Install the root-owned VPN helper and its sudoers rule",
		Long: "Install a root-owned helper plus a sudoers rule pinned to it, so den can\n" +
			"start and stop the VPN without asking for your password.\n\n" +
			"Everything that will be written is printed first. You are asked for your\n" +
			"password once, by sudo, in this terminal.\n\n" +
			"Re-run this after changing a vpn: entry in den.yaml — the helper runs the\n" +
			"copy of the settings made here, not the file in your config.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(*cobra.Command, []string) error {
			return installVPNHelper(nameFilter, assumeYes)
		},
	}
	cmd.Flags().StringVar(&nameFilter, "name", "", "only install this VPN entry (default: every vpn: entry)")
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func vpnUninstallHelperCmd() *cobra.Command {
	var assumeYes bool
	cmd := &cobra.Command{
		Use:          "uninstall-helper",
		Short:        "Remove the VPN helper and its sudoers rule",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(*cobra.Command, []string) error {
			return uninstallVPNHelper(assumeYes)
		},
	}
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func vpnStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "status",
		Short:        "Show whether the VPN helper is installed and usable",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return vpnHelperStatus(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

// vpnProfile is one den.yaml vpn: entry as the helper will know it.
type vpnProfile struct {
	name string
	slug string
	conf string // rendered openfortivpn config
}

// loadVPNProfiles renders every configured VPN (or just one) for the helper.
func loadVPNProfiles(nameFilter string) ([]vpnProfile, error) {
	cfgPath, err := config.Discover(cfgFile)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	if len(cfg.VPNs) == 0 {
		return nil, fmt.Errorf("no vpn: entries in %s", cfgPath)
	}

	seen := map[string]string{}
	var profiles []vpnProfile
	for i := range cfg.VPNs {
		spec := cfg.VPNs[i]
		if nameFilter != "" && spec.Name != nameFilter {
			continue
		}
		slug := vpnsvc.Slug(spec.Name)
		if err := vpnsvc.ValidSlug(slug); err != nil {
			return nil, fmt.Errorf("vpn %q: %w", spec.Name, err)
		}
		if other, dup := seen[slug]; dup {
			return nil, fmt.Errorf("vpn %q and %q both map to the profile name %q — rename one", other, spec.Name, slug)
		}
		seen[slug] = spec.Name

		conf, err := vpnsvc.RenderConfig(&spec)
		if err != nil {
			return nil, fmt.Errorf("vpn %q: %w", spec.Name, err)
		}
		profiles = append(profiles, vpnProfile{name: spec.Name, slug: slug, conf: conf})
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("no vpn: entry named %q in %s", nameFilter, cfgPath)
	}
	return profiles, nil
}

func installVPNHelper(nameFilter string, assumeYes bool) error {
	if err := vpnHelperSupported(); err != nil {
		return err
	}
	profiles, err := loadVPNProfiles(nameFilter)
	if err != nil {
		return err
	}
	binPath, err := exec.LookPath("openfortivpn")
	if err != nil {
		return fmt.Errorf("openfortivpn not found in PATH — install it first (brew install openfortivpn)")
	}
	me, err := user.Current()
	if err != nil {
		return err
	}

	slugs := make([]string, len(profiles))
	for i, p := range profiles {
		slugs[i] = p.slug
	}
	script := vpnsvc.Script(binPath)
	sudoers := vpnsvc.SudoersFile(me.Username, slugs)

	var plan strings.Builder
	plan.WriteString("den will install, as root:\n\n")
	fmt.Fprintf(&plan, "  %s  (root:wheel 0755)\n", vpnsvc.HelperPath)
	fmt.Fprintf(&plan, "      runs: %s -c <profile config> (detached)\n", binPath)
	for _, p := range profiles {
		fmt.Fprintf(&plan, "\n  %s  (root:wheel 0600)   ← vpn %q\n", vpnsvc.HelperConfPath(p.slug), p.name)
		plan.WriteString(indent(p.conf, "      "))
	}
	fmt.Fprintf(&plan, "\n  %s  (root:wheel 0440)\n", vpnsvc.SudoersPath)
	plan.WriteString(indent(sudoers, "      "))
	plan.WriteString("\nAfterwards den connects and disconnects the VPN without a password prompt.\n")
	plan.WriteString("Undo at any time with: den vpn uninstall-helper\n")
	fmt.Fprintf(&plan, "\nNote: %s stays writable by your user, so this also means anything running\n", binPath)
	plan.WriteString("as you could replace it and gain root without a password. See docs/vpn/README.md.")

	if err := confirm(plan.String(), assumeYes); err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "den-vpn-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	scriptSrc := filepath.Join(tmp, "den-vpn")
	if err := os.WriteFile(scriptSrc, []byte(script), 0o600); err != nil {
		return err
	}
	sudoersSrc := filepath.Join(tmp, "den-vpn.sudoers")
	if err := os.WriteFile(sudoersSrc, []byte(sudoers), 0o600); err != nil {
		return err
	}

	fmt.Println("\nsudo will now ask for your password (once).")
	if err := runSudo("-v"); err != nil {
		return fmt.Errorf("sudo authentication failed: %w", err)
	}

	// Check the sudoers syntax before anything is installed: a malformed file
	// in /etc/sudoers.d breaks sudo itself, for every command.
	if err := runSudo("visudo", "-cf", sudoersSrc); err != nil {
		return fmt.Errorf("generated sudoers file is invalid, nothing was installed: %w", err)
	}

	dirs := []string{
		vpnsvc.HelperPrefix,
		filepath.Join(vpnsvc.HelperPrefix, "libexec"),
		filepath.Join(vpnsvc.HelperPrefix, "etc"),
		filepath.Join(vpnsvc.HelperPrefix, "var"),
		filepath.Join(vpnsvc.HelperPrefix, "var", "log"),
		filepath.Join(vpnsvc.HelperPrefix, "var", "run"),
	}
	if err := runSudo(append([]string{"install", "-d", "-o", "0", "-g", "0", "-m", "755"}, dirs...)...); err != nil {
		return err
	}
	if err := runSudo("install", "-o", "0", "-g", "0", "-m", "755", scriptSrc, vpnsvc.HelperPath); err != nil {
		return err
	}
	for _, p := range profiles {
		confSrc := filepath.Join(tmp, p.slug+".conf")
		if err := os.WriteFile(confSrc, []byte(p.conf), 0o600); err != nil {
			return err
		}
		if err := runSudo("install", "-o", "0", "-g", "0", "-m", "600", confSrc, vpnsvc.HelperConfPath(p.slug)); err != nil {
			return err
		}
	}
	// The rule lands last, once everything it points at exists.
	if err := runSudo("install", "-o", "0", "-g", "0", "-m", "440", sudoersSrc, vpnsvc.SudoersPath); err != nil {
		return err
	}

	ok, why := vpnsvc.Available(context.Background())
	if !ok {
		return fmt.Errorf("installed, but den still cannot use the helper: %s", why)
	}
	fmt.Printf("\n✓ helper installed and verified — den will no longer prompt for the VPN\n")
	for _, p := range profiles {
		fmt.Printf("  %s → profile %q\n", p.name, p.slug)
	}
	return nil
}

func uninstallVPNHelper(assumeYes bool) error {
	if err := vpnHelperSupported(); err != nil {
		return err
	}
	if _, err := os.Stat(vpnsvc.HelperPath); err != nil && os.IsNotExist(err) {
		fmt.Println("helper is not installed; nothing to do")
		return nil
	}

	plan := fmt.Sprintf("den will remove, as root:\n\n  %s\n  %s\n\n"+
		"VPN connect and disconnect will ask for your admin password again.", vpnsvc.SudoersPath, vpnsvc.HelperPrefix)
	if vpnRunning() {
		plan += "\n\nA VPN tunnel is currently up. It stays up; the next disconnect will prompt."
	}
	if err := confirm(plan, assumeYes); err != nil {
		return err
	}
	if err := runSudo("rm", "-f", vpnsvc.SudoersPath); err != nil {
		return err
	}
	if err := runSudo("rm", "-rf", vpnsvc.HelperPrefix); err != nil {
		return err
	}
	fmt.Println("✓ helper removed")
	return nil
}

func vpnHelperStatus(ctx context.Context, w io.Writer) error {
	// The settings files come first: they are worth knowing on a machine that
	// cannot run the helper too.
	if err := writeVPNEntries(w); err != nil {
		fmt.Fprintf(w, "vpn entries: %v\n\n", err)
	}
	if err := vpnHelperSupported(); err != nil {
		return err
	}
	fmt.Fprintf(w, "helper:  %s\n", vpnsvc.HelperPath)
	fmt.Fprintf(w, "sudoers: %s\n", vpnsvc.SudoersPath)

	ok, why := vpnsvc.Available(ctx)
	if ok {
		fmt.Fprintln(w, "status:  usable — den connects and disconnects without a prompt")
	} else {
		fmt.Fprintf(w, "status:  not usable — %s\n", why)
	}

	profiles, err := loadVPNProfiles("")
	if err != nil {
		fmt.Fprintf(w, "\nprofiles: %v\n", err)
		return nil
	}
	fmt.Fprintln(w, "\nprofiles:")
	for _, p := range profiles {
		state := "not installed"
		if _, err := os.Stat(vpnsvc.HelperConfPath(p.slug)); err == nil {
			state = "installed"
			if ok {
				name, args := "sudo", []string{"-n", vpnsvc.HelperPath, "status", p.slug}
				if out, err := exec.CommandContext(ctx, name, args...).Output(); err == nil {
					state += ", " + strings.TrimSpace(string(out))
				}
			}
		}
		fmt.Fprintf(w, "  %-24s %-16s %s\n", p.name, p.slug, state)
	}
	return nil
}

// writeVPNEntries lists the vpn: entries of den.yaml with the settings file
// each reads, if any, and whether it is there: the first thing to check when a
// connect fails with "read config_file".
func writeVPNEntries(w io.Writer) error {
	cfgPath, err := config.Discover(cfgFile)
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "vpn entries in %s:\n", platform.TildePath(cfgPath))
	if len(cfg.VPNs) == 0 {
		fmt.Fprintf(w, "  none: add one with a gateway, such as\n    vpn:\n      - name: office\n        gateway: vpn.example.com\n\n")
		return nil
	}
	for _, v := range cfg.VPNs {
		detail := "gateway only, no config_file"
		if p := v.ConfigFilePath(); p != "" {
			detail = "config_file " + platform.TildePath(p) + " (missing)"
			if _, err := os.Stat(p); err == nil {
				detail = "config_file " + platform.TildePath(p) + " (exists)"
			}
		}
		fmt.Fprintf(w, "  %-24s %s\n", v.Name, detail)
	}
	fmt.Fprintln(w)
	return nil
}

// vpnHelperSupported mirrors the platforms the VPN service itself supports.
func vpnHelperSupported() error {
	if _, err := exec.LookPath("sudo"); err != nil {
		return fmt.Errorf("sudo not found — the VPN helper is only supported on macOS and Linux")
	}
	return nil
}

// vpnRunning reports whether an openfortivpn process is alive right now.
func vpnRunning() bool {
	out, _ := exec.Command("pgrep", "-x", "openfortivpn").Output()
	return len(strings.TrimSpace(string(out))) > 0
}

// runSudo runs one privileged step, echoing it first so nothing happens that
// was not shown.
func runSudo(args ...string) error {
	fmt.Printf("  $ sudo %s\n", strings.Join(args, " "))
	cmd := exec.Command("sudo", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// indent prefixes every line of text, for printing file contents in the plan.
func indent(text, prefix string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n") + "\n"
}
