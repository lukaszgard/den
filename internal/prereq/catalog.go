// Package prereq knows the programs den runs or hands commands to: how to find
// them (including installs that land off PATH), which versions work, and how
// to install them on each platform den supports. den doctor reports from it,
// and the install table in docs/doctor/install.md is generated from it, so the
// two cannot drift apart.
//
// Install commands are printed for people to run; den never runs them.
package prereq

//go:generate go run ../tools/installdoc -o ../../docs/doctor/install.md

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lukaszgard/den/internal/platform"
)

// Recipe is one way to install a tool on one platform.
type Recipe struct {
	// Manager is the package manager the lines use (one of
	// platform.Managers), or "" for an official installer that needs none.
	Manager string
	// Lines are copy-paste shell lines. Comment lines (#) carry steps that
	// cannot be one command, such as adding a vendor repository.
	Lines []string
}

// Place is somewhere an install lands that PATH may not include.
type Place struct {
	Glob string
	// DenFinds is set when den looks here itself, so the tool works from
	// den even though the shell cannot find it.
	DenFinds bool
}

// Tool is one program den depends on.
type Tool struct {
	ID      string // the name on PATH, or a label for things that are not programs
	Purpose string // what den uses it for

	// MinVersion is the oldest version that works and MinReason why.
	// MinRequired makes an older version a failure rather than a warning,
	// for tools where it breaks every use, not just one feature.
	MinVersion  string
	MinReason   string
	MinRequired bool

	VersionArgs []string
	VersionRe   *regexp.Regexp // the first group is the version

	// Places lists where installs land off PATH on the given platform.
	Places func(env Env) []Place
	// Find replaces the PATH lookup for things that are not programs.
	Find func(env Env) string

	// Install holds recipes per platform key (see Key), in order of
	// preference: the first whose package manager is present is shown.
	Install map[string][]Recipe
	// Remove gives, for a too-old install at path, the lines that take it
	// out first. An install that lands elsewhere leaves it in place, and when
	// it comes earlier on PATH it keeps answering. Nil when the recipe
	// replaces it.
	Remove func(path string, env Env) []string
	// Upgrade holds recipes, per platform key, for a version older than
	// MinVersion that Install cannot replace: a distribution release keeps
	// the version it shipped, so upgrading through its package manager
	// changes nothing.
	Upgrade map[string][]Recipe
	// Unsupported says, per platform key, why there is nothing to install
	// there and what to use instead.
	Unsupported map[string]string
	Docs        string
}

// Platform keys for Tool.Install.
const (
	KeyMacOS   = "macos"
	KeyDebian  = "debian"
	KeyFedora  = "fedora"
	KeyWindows = "windows"
)

// Keys are the platform keys every tool must cover.
var Keys = []string{KeyMacOS, KeyDebian, KeyFedora, KeyWindows}

// Key is the Install key for p, or "" for a platform without recipes (Linux
// outside the Debian and Fedora families), where only the docs link is given.
func Key(p platform.Info) string {
	switch {
	case p.OS == platform.MacOS:
		return KeyMacOS
	case p.OS == platform.Windows:
		return KeyWindows
	case p.Distro == platform.Debian:
		return KeyDebian
	case p.Distro == platform.Fedora:
		return KeyFedora
	}
	return ""
}

// sopsVersion is the release the Linux recipes download. Any release at or
// above the sops MinVersion works; bump it when convenient.
const sopsVersion = "3.13.3"

// openfortivpnVersion is the release the Debian build recipe checks out. Any
// release at or above the openfortivpn MinVersion works.
const openfortivpnVersion = "1.24.1"

// openfortivpnFromSource builds openfortivpn on the releases whose own package
// predates SAML: Ubuntu 24.04 and older, Debian 12 and older, RHEL 9 (EPEL).
// The steps are upstream's README's. Those releases all ship pppd 2.4.9, and a
// default build passes ipcp-accept-remote, which pppd before 2.5.0 rejects —
// hence --enable-legacy-pppd. The package goes first so a second, older
// openfortivpn is not left on PATH.
func openfortivpnFromSource(manager, ssl string) Recipe {
	return Recipe{Manager: manager, Lines: []string{
		"# this release's openfortivpn predates SAML: build " + openfortivpnVersion + " from source",
		"sudo " + manager + " remove -y openfortivpn",
		"sudo " + manager + " install -y git gcc automake autoconf " + ssl + " make pkg-config ppp",
		`src=$(mktemp -d) && git clone --depth 1 --branch v` + openfortivpnVersion + ` https://github.com/adrienverge/openfortivpn.git "$src"`,
		`(cd "$src" && ./autogen.sh && ./configure --prefix=/usr/local --sysconfdir=/etc --enable-legacy-pppd && make && sudo make install)`,
		"# installed den's VPN helper? run den vpn install-helper again: it keeps the old openfortivpn path",
	}}
}

func brew(lines ...string) Recipe   { return Recipe{Manager: "brew", Lines: lines} }
func apt(lines ...string) Recipe    { return Recipe{Manager: "apt-get", Lines: lines} }
func dnf(lines ...string) Recipe    { return Recipe{Manager: "dnf", Lines: lines} }
func winget(lines ...string) Recipe { return Recipe{Manager: "winget", Lines: lines} }
func scoop(lines ...string) Recipe  { return Recipe{Manager: "scoop", Lines: lines} }
func manual(lines ...string) Recipe { return Recipe{Lines: lines} }

// brewKegs are the Homebrew locations of formulae that are keg-only (not
// linked onto PATH), for each Homebrew prefix.
func brewKegs(formulae []string, bin string) func(Env) []Place {
	return func(env Env) []Place {
		if env.Platform.OS != platform.MacOS {
			return nil
		}
		var out []Place
		for _, prefix := range []string{"/opt/homebrew", "/usr/local"} {
			for _, f := range formulae {
				out = append(out, Place{Glob: prefix + "/opt/" + f + "/bin/" + bin})
			}
		}
		return out
	}
}

func programFiles(env Env) string {
	if pf := env.Getenv("ProgramFiles"); pf != "" {
		return pf
	}
	return `C:\Program Files`
}

var anyVersion = regexp.MustCompile(`(\d+\.\d+(?:\.\d+)*)`)

// Catalog is every tool den knows, in a stable order.
var Catalog = []Tool{
	{
		ID:          "aws",
		Purpose:     "AWS SSO logins, SSM sessions and IAM tokens",
		MinVersion:  "2.0.0",
		MinReason:   "SSO sessions need AWS CLI v2",
		MinRequired: true,
		Remove:      awsV1Removal,
		VersionArgs: []string{"--version"},
		VersionRe:   regexp.MustCompile(`aws-cli/(\d+\.\d+\.\d+)`),
		Install: map[string][]Recipe{
			KeyMacOS:   {brew("brew install awscli")},
			KeyDebian:  {awsCLILinux},
			KeyFedora:  {awsCLILinux},
			KeyWindows: {winget("winget install -e --id Amazon.AWSCLI"), scoop("scoop install aws")},
		},
		Docs: "https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html",
	},
	{
		ID:          "session-manager-plugin",
		Purpose:     "SSM tunnels (the AWS CLI hands sessions to it)",
		VersionArgs: []string{"--version"},
		VersionRe:   anyVersion,
		Places: func(env Env) []Place {
			switch env.Platform.OS {
			case platform.MacOS:
				return []Place{{Glob: "/usr/local/sessionmanagerplugin/bin/session-manager-plugin"}}
			case platform.Windows:
				return []Place{{Glob: filepath.Join(programFiles(env), "Amazon", "SessionManagerPlugin", "bin", "session-manager-plugin.exe")}}
			}
			return nil
		},
		Install: map[string][]Recipe{
			KeyMacOS: {brew("brew install --cask session-manager-plugin")},
			KeyDebian: {manual(
				`curl -fsSLo session-manager-plugin.deb "https://s3.amazonaws.com/session-manager-downloads/plugin/latest/ubuntu_{ssmarch}/session-manager-plugin.deb"`,
				"sudo dpkg -i session-manager-plugin.deb",
			)},
			KeyFedora:  {dnf("sudo dnf install -y https://s3.amazonaws.com/session-manager-downloads/plugin/latest/linux_{ssmarch}/session-manager-plugin.rpm")},
			KeyWindows: {winget("winget install -e --id Amazon.SessionManagerPlugin")},
		},
		Docs: "https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html",
	},
	{
		ID:          "psql",
		Purpose:     "the connect command of PostgreSQL and Redshift services",
		VersionArgs: []string{"--version"},
		VersionRe:   anyVersion,
		Places: func(env Env) []Place {
			if env.Platform.OS == platform.Windows {
				return []Place{{Glob: filepath.Join(programFiles(env), "PostgreSQL", "*", "bin", "psql.exe")}}
			}
			return brewKegs([]string{"libpq", "postgresql@*"}, "psql")(env)
		},
		Install: map[string][]Recipe{
			KeyMacOS:   {brew("brew install libpq", "brew link --force libpq")},
			KeyDebian:  {apt("sudo apt-get install -y postgresql-client")},
			KeyFedora:  {dnf("sudo dnf install -y postgresql")},
			KeyWindows: {winget("winget install -e --id PostgreSQL.PostgreSQL.18"), scoop("scoop install postgresql")},
		},
		Docs: "https://www.postgresql.org/download/",
	},
	{
		ID:          "mysql",
		Purpose:     "the connect command of RDS/Aurora MySQL services",
		VersionArgs: []string{"--version"},
		VersionRe:   anyVersion,
		Places: func(env Env) []Place {
			if env.Platform.OS == platform.Windows {
				return []Place{{Glob: filepath.Join(programFiles(env), "MySQL", "MySQL Server *", "bin", "mysql.exe")}}
			}
			return brewKegs([]string{"mysql-client", "mysql-client@*"}, "mysql")(env)
		},
		Install: map[string][]Recipe{
			KeyMacOS:   {brew("brew install mysql-client", "brew link --force mysql-client")},
			KeyDebian:  {apt("sudo apt-get install -y default-mysql-client")},
			KeyFedora:  {dnf("sudo dnf install -y mysql")},
			KeyWindows: {winget("winget install -e --id Oracle.MySQL"), scoop("scoop install mysql")},
		},
		Docs: "https://dev.mysql.com/doc/refman/8.4/en/installing.html",
	},
	{
		ID:          "redis-cli",
		Purpose:     "the connect command of ElastiCache and MemoryDB services",
		MinVersion:  "6.0.0",
		MinReason:   "IAM auth needs --user, added in redis-cli 6",
		MinRequired: true,
		VersionArgs: []string{"--version"},
		// Homebrew's valkey provides redis-cli too, and names itself.
		VersionRe: regexp.MustCompile(`(?:redis|valkey)-cli (\d+\.\d+\.\d+)`),
		Install: map[string][]Recipe{
			KeyMacOS:   {brew("brew install redis")},
			KeyDebian:  {apt("sudo apt-get install -y redis-tools")},
			KeyFedora:  {dnf("sudo dnf install -y redis")},
			KeyWindows: {scoop("scoop install redis")},
		},
		Docs: "https://redis.io/docs/latest/operate/oss_and_stack/install/",
	},
	{
		ID:          "mongosh",
		Purpose:     "the connect command of DocumentDB services",
		VersionArgs: []string{"--version"},
		VersionRe:   anyVersion,
		Install: map[string][]Recipe{
			KeyMacOS: {brew("brew install mongosh")},
			KeyDebian: {apt(
				"# add the MongoDB repository first: https://www.mongodb.com/docs/mongodb-shell/install/",
				"sudo apt-get install -y mongodb-mongosh",
			)},
			KeyFedora: {dnf(
				"# add the MongoDB repository first: https://www.mongodb.com/docs/mongodb-shell/install/",
				"sudo dnf install -y mongodb-mongosh",
			)},
			KeyWindows: {winget("winget install -e --id MongoDB.Shell"), scoop("scoop bucket add extras", "scoop install extras/mongosh")},
		},
		Docs: "https://www.mongodb.com/docs/mongodb-shell/install/",
	},
	{
		ID:          "curl",
		Purpose:     "the connect command of OpenSearch and Neptune services",
		VersionArgs: []string{"--version"},
		VersionRe:   regexp.MustCompile(`curl (\d+\.\d+\.\d+)`),
		Install: map[string][]Recipe{
			KeyMacOS:   {brew("brew install curl")},
			KeyDebian:  {apt("sudo apt-get install -y curl")},
			KeyFedora:  {dnf("sudo dnf install -y curl")},
			KeyWindows: {winget("winget install -e --id cURL.cURL"), scoop("scoop install curl")},
		},
		Docs: "https://curl.se/download.html",
	},
	{
		ID:          "docker",
		Purpose:     "docker services (docker compose)",
		VersionArgs: []string{"--version"},
		VersionRe:   regexp.MustCompile(`version (\d+\.\d+\.\d+)`),
		Install: map[string][]Recipe{
			KeyMacOS:   {brew("brew install --cask docker-desktop")},
			KeyDebian:  {dockerLinux},
			KeyFedora:  {dockerLinux},
			KeyWindows: {winget("winget install -e --id Docker.DockerDesktop")},
		},
		Docs: "https://docs.docker.com/get-started/get-docker/",
	},
	{
		ID:          "ssh",
		Purpose:     "user-defined tunnels and the ssh transport",
		VersionArgs: []string{"-V"},
		VersionRe:   regexp.MustCompile(`OpenSSH_(\d+\.\d+)`),
		Install: map[string][]Recipe{
			KeyMacOS:  {brew("brew install openssh")},
			KeyDebian: {apt("sudo apt-get install -y openssh-client")},
			KeyFedora: {dnf("sudo dnf install -y openssh-clients")},
			KeyWindows: {manual(
				"# Windows 10 and later ship OpenSSH; if ssh is missing, in an administrator PowerShell:",
				"Add-WindowsCapability -Online -Name OpenSSH.Client~~~~0.0.1.0",
			), scoop("scoop install openssh")},
		},
		Docs: "https://www.openssh.com/portable.html",
	},
	{
		ID:          "kubectl",
		Purpose:     "the kubectl transport",
		VersionArgs: []string{"version", "--client"},
		VersionRe:   regexp.MustCompile(`Client Version: v(\d+\.\d+\.\d+)`),
		Install: map[string][]Recipe{
			KeyMacOS:   {brew("brew install kubectl")},
			KeyDebian:  {kubectlLinux},
			KeyFedora:  {kubectlLinux},
			KeyWindows: {winget("winget install -e --id Kubernetes.kubectl"), scoop("scoop install kubectl")},
		},
		Docs: "https://kubernetes.io/docs/tasks/tools/",
	},
	{
		ID:         "sops",
		Purpose:    "secrets.sops entries (decrypt and edit)",
		MinVersion: "3.9.0",
		MinReason:  "editing uses `sops edit`, added in sops 3.9",
		// Plain --version asks GitHub whether a newer release exists.
		VersionArgs: []string{"--version", "--disable-version-check"},
		VersionRe:   regexp.MustCompile(`sops (\d+\.\d+\.\d+)`),
		Install: map[string][]Recipe{
			KeyMacOS: {brew("brew install sops")},
			KeyDebian: {manual(
				"curl -fsSLO https://github.com/getsops/sops/releases/download/v{sops}/sops_{sops}_{arch}.deb",
				"sudo apt-get install -y ./sops_{sops}_{arch}.deb",
			)},
			KeyFedora:  {dnf("sudo dnf install -y https://github.com/getsops/sops/releases/download/v{sops}/sops-{sops}-1.{uname}.rpm")},
			KeyWindows: {winget("winget install -e --id SecretsOPerationS.SOPS"), scoop("scoop install sops")},
		},
		Docs: "https://getsops.io/docs/#download",
	},
	{
		ID:          "gpg",
		Purpose:     "sops files encrypted to a PGP key",
		VersionArgs: []string{"--version"},
		VersionRe:   regexp.MustCompile(`gpg \(GnuPG\) (\d+\.\d+\.\d+)`),
		Install: map[string][]Recipe{
			KeyMacOS:   {brew("brew install gnupg")},
			KeyDebian:  {apt("sudo apt-get install -y gnupg")},
			KeyFedora:  {dnf("sudo dnf install -y gnupg2")},
			KeyWindows: {winget("winget install -e --id GnuPG.GnuPG"), scoop("scoop install gpg")},
		},
		Docs: "https://gnupg.org/download/",
	},
	{
		ID:          "vault",
		Purpose:     "secrets.vault entries",
		MinVersion:  "1.11.0",
		MinReason:   "den reads with `vault kv get -mount=`, added in Vault 1.11",
		MinRequired: true,
		VersionArgs: []string{"version"},
		VersionRe:   regexp.MustCompile(`Vault v(\d+\.\d+\.\d+)`),
		Install: map[string][]Recipe{
			KeyMacOS: {brew("brew tap hashicorp/tap", "brew install hashicorp/tap/vault")},
			KeyDebian: {apt(
				"wget -O - https://apt.releases.hashicorp.com/gpg | sudo gpg --dearmor -o /usr/share/keyrings/hashicorp-archive-keyring.gpg",
				`echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com $(grep -oP '(?<=UBUNTU_CODENAME=).*' /etc/os-release || lsb_release -cs) main" | sudo tee /etc/apt/sources.list.d/hashicorp.list`,
				"sudo apt-get update && sudo apt-get install -y vault",
			)},
			KeyFedora: {dnf(
				"# on RHEL/CentOS use https://rpm.releases.hashicorp.com/RHEL/hashicorp.repo instead",
				"wget -O- https://rpm.releases.hashicorp.com/fedora/hashicorp.repo | sudo tee /etc/yum.repos.d/hashicorp.repo",
				"sudo dnf install -y vault",
			)},
			KeyWindows: {winget("winget install -e --id Hashicorp.Vault"), scoop("scoop install vault")},
		},
		Docs: "https://developer.hashicorp.com/vault/install",
	},
	{
		ID:          "keepassxc-cli",
		Purpose:     "secrets.keepassxc entries",
		VersionArgs: []string{"--version"},
		VersionRe:   anyVersion,
		Places: func(env Env) []Place {
			switch env.Platform.OS {
			case platform.MacOS:
				// The keepassxc provider looks inside the app bundle itself.
				return []Place{{Glob: "/Applications/KeePassXC.app/Contents/MacOS/keepassxc-cli", DenFinds: true}}
			case platform.Windows:
				return []Place{{Glob: filepath.Join(programFiles(env), "KeePassXC", "keepassxc-cli.exe")}}
			}
			return nil
		},
		Install: map[string][]Recipe{
			KeyMacOS:   {brew("brew install --cask keepassxc")},
			KeyDebian:  {apt("sudo apt-get install -y keepassxc")},
			KeyFedora:  {dnf("sudo dnf install -y keepassxc")},
			KeyWindows: {winget("winget install -e --id KeePassXCTeam.KeePassXC"), scoop("scoop bucket add extras", "scoop install extras/keepassxc")},
		},
		Docs: "https://keepassxc.org/download/",
	},
	{
		ID:      "openfortivpn",
		Purpose: "vpn entries (FortiGate SSL VPN)",
		// Older releases read the saml-login line of the helper's config as a
		// bad key, drop the whole file, and fail as a wrong password.
		MinVersion:  "1.23.0",
		MinReason:   "den logs in with SAML (--saml-login), added in openfortivpn 1.23.0",
		MinRequired: true,
		VersionArgs: []string{"--version"},
		VersionRe:   anyVersion,
		Install: map[string][]Recipe{
			KeyMacOS:  {brew("brew install openfortivpn")},
			KeyDebian: {apt("sudo apt-get install -y openfortivpn")},
			KeyFedora: {dnf("sudo dnf install -y openfortivpn")},
		},
		Upgrade: map[string][]Recipe{
			KeyDebian: {openfortivpnFromSource("apt-get", "libssl-dev")},
			KeyFedora: {openfortivpnFromSource("dnf", "openssl-devel")},
		},
		Unsupported: map[string]string{
			KeyWindows: "openfortivpn has no Windows build; use FortiClient VPN, or run den in WSL on a kernel with PPP (WSL 3.0.1's has none; 2.7.14's has)",
		},
		Docs: "https://github.com/adrienverge/openfortivpn#installing",
	},
	{
		ID:      NerdFont,
		Purpose: "the TUI's icons (select the font in your terminal afterwards)",
		Find:    findNerdFont,
		Install: map[string][]Recipe{
			KeyMacOS: {brew("brew install --cask font-jetbrains-mono-nerd-font")},
			KeyDebian: {manual(
				"curl -fsSLO https://github.com/ryanoasis/nerd-fonts/releases/latest/download/JetBrainsMono.zip",
				"mkdir -p ~/.local/share/fonts/JetBrainsMono && unzip -o JetBrainsMono.zip -d ~/.local/share/fonts/JetBrainsMono",
				"fc-cache -f",
			)},
			KeyFedora: {manual(
				"curl -fsSLO https://github.com/ryanoasis/nerd-fonts/releases/latest/download/JetBrainsMono.zip",
				"mkdir -p ~/.local/share/fonts/JetBrainsMono && unzip -o JetBrainsMono.zip -d ~/.local/share/fonts/JetBrainsMono",
				"fc-cache -f",
			)},
			KeyWindows: {winget("winget install -e --id DEVCOM.JetBrainsMonoNerdFont"), scoop("scoop bucket add nerd-fonts", "scoop install nerd-fonts/JetBrainsMono-NF")},
		},
		Docs: "https://www.nerdfonts.com/font-downloads",
	},
	{
		ID:          "git",
		Purpose:     "runbook sources: fetching the repositories in runbooks.sources",
		VersionArgs: []string{"--version"},
		VersionRe:   regexp.MustCompile(`git version (\d+\.\d+\.\d+)`),
		Install: map[string][]Recipe{
			KeyMacOS:   {brew("brew install git"), manual("xcode-select --install")},
			KeyDebian:  {apt("sudo apt-get install -y git")},
			KeyFedora:  {dnf("sudo dnf install -y git")},
			KeyWindows: {winget("winget install -e --id Git.Git"), scoop("scoop install git")},
		},
		Docs: "https://git-scm.com/downloads",
	},
	{
		ID:          "claude",
		Purpose:     "writing a runbook from a description (den runbook new --agent claude)",
		VersionArgs: []string{"--version"},
		VersionRe:   regexp.MustCompile(`^(\d+\.\d+\.\d+)`),
		Install: map[string][]Recipe{
			KeyMacOS:   {brew("brew install --cask claude-code"), claudeInstaller},
			KeyDebian:  {claudeInstaller},
			KeyFedora:  {claudeInstaller},
			KeyWindows: {winget("winget install Anthropic.ClaudeCode"), manual("irm https://claude.ai/install.ps1 | iex")},
		},
		Docs: "https://code.claude.com/docs/en/setup",
	},
	{
		ID:          "codex",
		Purpose:     "writing a runbook from a description (den runbook new --agent codex)",
		VersionArgs: []string{"--version"},
		VersionRe:   anyVersion,
		Install: map[string][]Recipe{
			KeyMacOS:   {brew("brew install --cask codex"), codexInstaller},
			KeyDebian:  {codexInstaller},
			KeyFedora:  {codexInstaller},
			KeyWindows: {manual(`powershell -ExecutionPolicy ByPass -c "irm https://chatgpt.com/codex/install.ps1 | iex"`)},
		},
		Docs: "https://github.com/openai/codex",
	},
}

// NerdFont is the catalog ID of the font check, which is not a program.
const NerdFont = "nerd-font"

// The agent CLIs: claude.ai and chatgpt.com host their official installers,
// which is what their docs lead with.
var (
	claudeInstaller = manual("curl -fsSL https://claude.ai/install.sh | bash")
	codexInstaller  = manual("curl -fsSL https://chatgpt.com/codex/install.sh | sh")
)

// awsV1Removal takes out an AWS CLI v1 that the v2 installer leaves in place.
// v2 goes to /usr/local/bin. pip and pipx put v1 in ~/.local/bin, which
// Ubuntu's ~/.profile puts ahead of it, so `aws` keeps answering as v1. Debian's
// awscli package is v1 in /usr/bin: shadowed, but gone is clearer.
func awsV1Removal(path string, env Env) []string {
	userBin := filepath.Join(env.Home, ".local", "bin") + string(filepath.Separator)
	switch {
	case env.Home != "" && strings.HasPrefix(path, userBin):
		if target, err := filepath.EvalSymlinks(path); err == nil && strings.Contains(filepath.ToSlash(target), "/pipx/venvs/") {
			return []string{"pipx uninstall awscli"}
		}
		return []string{"python3 -m pip uninstall -y awscli"}
	case path == "/usr/bin/aws" && env.Platform.Distro == platform.Debian:
		return []string{"sudo apt-get remove -y awscli"}
	}
	return nil
}

var awsCLILinux = manual(
	`curl -fsSLo awscliv2.zip "https://awscli.amazonaws.com/awscli-exe-linux-{uname}.zip"`,
	"unzip -q awscliv2.zip",
	"sudo ./aws/install",
)

// Docker's own script installs the engine with the compose plugin; distro
// packages often lack the plugin or lag far behind.
var dockerLinux = manual(
	"curl -fsSL https://get.docker.com -o get-docker.sh",
	"sudo sh get-docker.sh",
)

var kubectlLinux = manual(
	`curl -fsSLO "https://dl.k8s.io/release/$(curl -fsSL https://dl.k8s.io/release/stable.txt)/bin/linux/{arch}/kubectl"`,
	"sudo install -m 0755 kubectl /usr/local/bin/kubectl",
)

// findNerdFont looks for a font file with "Nerd" in its name where the
// platform keeps user and system fonts. Terminals that bundle the Nerd Font
// symbols need none, so they count as found.
func findNerdFont(env Env) string {
	switch env.Getenv("TERM_PROGRAM") {
	case "ghostty", "WezTerm":
		return env.Getenv("TERM_PROGRAM") + " (bundles the symbols)"
	}
	var dirs []string
	switch env.Platform.OS {
	case platform.MacOS:
		dirs = []string{filepath.Join(env.Home, "Library", "Fonts"), "/Library/Fonts"}
	case platform.Windows:
		dirs = []string{filepath.Join(env.Getenv("LOCALAPPDATA"), "Microsoft", "Windows", "Fonts"), filepath.Join(env.Getenv("WINDIR"), "Fonts")}
	default:
		dirs = []string{filepath.Join(env.Home, ".local", "share", "fonts"), filepath.Join(env.Home, ".fonts"), "/usr/share/fonts", "/usr/local/share/fonts"}
	}
	for _, dir := range dirs {
		// Fonts sit directly in the directory or one family folder down.
		for _, pattern := range []string{filepath.Join(dir, "*Nerd*"), filepath.Join(dir, "*", "*Nerd*")} {
			if m := env.Glob(pattern); len(m) > 0 {
				return m[0]
			}
		}
	}
	return ""
}

// Lookup returns the catalog entry for id.
func Lookup(id string) (Tool, bool) {
	for _, t := range Catalog {
		if t.ID == id {
			return t, true
		}
	}
	return Tool{}, false
}
