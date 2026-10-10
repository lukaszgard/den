package prereq

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/platform"
)

// fakeMachine is a machine with the given programs on PATH, files present
// and `--version` answers.
type fakeMachine struct {
	onPath   map[string]string // name → path
	files    []string
	versions map[string]string // path → output
	env      map[string]string
	ran      [][]string
}

func (f *fakeMachine) envFor(p platform.Info) Env {
	return Env{
		Platform: p,
		LookPath: func(name string) (string, error) {
			if path, ok := f.onPath[name]; ok {
				return path, nil
			}
			return "", errors.New("not found")
		},
		Glob: func(pattern string) []string {
			var out []string
			for _, file := range f.files {
				if ok, _ := pathMatch(pattern, file); ok {
					out = append(out, file)
				}
			}
			return out
		},
		Output: func(_ context.Context, name string, args ...string) (string, error) {
			f.ran = append(f.ran, append([]string{name}, args...))
			return f.versions[name], nil
		},
		Getenv: func(k string) string { return f.env[k] },
		Home:   "/home/me",
	}
}

// pathMatch is filepath.Match on forward-slash paths, so tests read the same
// on every OS.
func pathMatch(pattern, name string) (bool, error) {
	re := "^" + strings.NewReplacer(`\*`, `[^/]*`).Replace(regexp.QuoteMeta(pattern)) + "$"
	return regexp.MatchString(re, name)
}

var (
	mac      = platform.Info{OS: platform.MacOS, Arch: "arm64", Managers: []string{"brew"}}
	macBare  = platform.Info{OS: platform.MacOS, Arch: "arm64"}
	ubuntu   = platform.Info{OS: platform.Linux, Distro: platform.Debian, Arch: "amd64", Managers: []string{"apt-get"}}
	fedora   = platform.Info{OS: platform.Linux, Distro: platform.Fedora, Arch: "arm64", Managers: []string{"dnf"}}
	win      = platform.Info{OS: platform.Windows, Arch: "amd64", Managers: []string{"winget"}}
	winScoop = platform.Info{OS: platform.Windows, Arch: "amd64", Managers: []string{"scoop"}}
	alpine   = platform.Info{OS: platform.Linux, Distro: platform.OtherDistro, Arch: "amd64"}
)

func tool(t *testing.T, id string) Tool {
	t.Helper()
	tl, ok := Lookup(id)
	if !ok {
		t.Fatalf("%s is not in the catalog", id)
	}
	return tl
}

func TestLess(t *testing.T) {
	cases := []struct {
		a, b string
		less bool
	}{
		{"3.9.0", "3.10.0", true},
		{"3.10.0", "3.9.0", false},
		{"1.11", "1.11.0", false},
		{"1.11.0", "1.11", false},
		{"5.0.14", "6.0.0", true},
		{"2.27.28", "2.0.0", false},
		{"9.9p2", "9.10", true},
		{"1.14.4", "1.11.0", false},
	}
	for _, c := range cases {
		if got := Less(c.a, c.b); got != c.less {
			t.Errorf("Less(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

// Real `--version` output, from the tools' current releases on macOS and
// Linux, so a changed format is caught here rather than by a user.
func TestVersionParsing(t *testing.T) {
	cases := map[string]map[string]string{
		"aws": {
			"aws-cli/2.27.28 Python/3.13.7 Darwin/27.0.0 source/arm64":   "2.27.28",
			"aws-cli/1.18.69 Python/3.8.10 Linux/5.15.0 botocore/1.17.0": "1.18.69",
		},
		"session-manager-plugin": {"1.2.707.0\n": "1.2.707.0"},
		"psql":                   {"psql (PostgreSQL) 15.7 (Homebrew)": "15.7", "psql (PostgreSQL) 16.4 (Ubuntu 16.4-0ubuntu0.24.04.2)": "16.4"},
		"mysql": {
			"/opt/homebrew/opt/mysql-client/bin/mysql  Ver 9.3.0 for macos15.4 on arm64 (Homebrew)": "9.3.0",
			"mysql  Ver 15.1 Distrib 10.11.6-MariaDB, for debian-linux-gnu (x86_64)":                "15.1",
		},
		"redis-cli":     {"redis-cli 7.2.4": "7.2.4", "valkey-cli 9.0.1": "9.0.1", "redis-cli 5.0.14 (git:a7c01ef4)": "5.0.14"},
		"mongosh":       {"2.3.1\n": "2.3.1"},
		"curl":          {"curl 8.7.1 (x86_64-apple-darwin26.0) libcurl/8.7.1 (SecureTransport)": "8.7.1"},
		"docker":        {"Docker version 26.1.3, build b72abbb6f0": "26.1.3"},
		"ssh":           {"OpenSSH_9.9p2, OpenSSL 3.6.1 27 Jan 2026": "9.9"},
		"kubectl":       {"Client Version: v1.36.3\nKustomize Version: v5.8.1": "1.36.3"},
		"sops":          {"sops 3.9.0": "3.9.0", "sops 3.13.3 (latest)": "3.13.3"},
		"gpg":           {"gpg (GnuPG) 2.4.8\nlibgcrypt 1.11.1": "2.4.8"},
		"vault":         {"Vault v1.14.4 ('ccdd48d1f7b95fc99fd11d67fc1c687576b338de+CHANGES'), built 2023-09-22T21:29:05Z": "1.14.4"},
		"keepassxc-cli": {"2.7.11\n": "2.7.11"},
		"openfortivpn":  {"1.24.1\n": "1.24.1"},
	}
	for id, outputs := range cases {
		re := tool(t, id).VersionRe
		for out, want := range outputs {
			m := re.FindStringSubmatch(out)
			if m == nil || m[1] != want {
				t.Errorf("%s: parsed %v from %q, want %s", id, m, out, want)
			}
		}
	}
}

func TestCheckFoundOnPath(t *testing.T) {
	m := &fakeMachine{
		onPath:   map[string]string{"sops": "/opt/homebrew/bin/sops"},
		versions: map[string]string{"/opt/homebrew/bin/sops": "sops 3.13.3 (latest)"},
	}
	st := Check(context.Background(), tool(t, "sops"), m.envFor(mac))
	if !st.Found() || st.OffPath || st.Version != "3.13.3" || st.TooOld || !st.Usable() {
		t.Fatalf("status = %+v", st)
	}
	// sops phones home on a plain --version; den must not make it.
	if want := []string{"/opt/homebrew/bin/sops", "--version", "--disable-version-check"}; !reflect.DeepEqual(m.ran[0], want) {
		t.Errorf("ran %v, want %v", m.ran[0], want)
	}
}

func TestCheckTooOld(t *testing.T) {
	m := &fakeMachine{
		onPath:   map[string]string{"redis-cli": "/usr/bin/redis-cli", "sops": "/usr/bin/sops"},
		versions: map[string]string{"/usr/bin/redis-cli": "redis-cli 5.0.14", "/usr/bin/sops": "sops 3.8.1"},
	}
	redis := Check(context.Background(), tool(t, "redis-cli"), m.envFor(mac))
	if !redis.TooOld || redis.Usable() {
		t.Fatalf("redis-cli 5 must be too old for IAM auth: %+v", redis)
	}
	if !reflect.DeepEqual(redis.Install, []string{"brew upgrade redis"}) {
		t.Errorf("an old install needs an upgrade, not an install: %v", redis.Install)
	}
	// An old sops still decrypts; only editing needs 3.9.
	sops := Check(context.Background(), tool(t, "sops"), m.envFor(mac))
	if !sops.TooOld || !sops.Usable() {
		t.Errorf("sops 3.8 is too old for editing but still usable: %+v", sops)
	}
}

// The v2 installer puts aws in /usr/local/bin, but a pip --user v1 sits in
// ~/.local/bin, which Ubuntu puts first on PATH: after the install `aws` still
// answers as v1. The fix has to take v1 out first.
func TestCheckTooOldAWSv1IsRemovedFirst(t *testing.T) {
	home := t.TempDir()
	pipx := filepath.Join(home, ".local", "pipx", "venvs", "awscli", "bin", "aws")
	if err := os.MkdirAll(filepath.Dir(pipx), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pipx, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	userBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(userBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(pipx, filepath.Join(userBin, "aws-pipx")); err != nil {
		t.Fatal(err)
	}

	installV2 := tool(t, "aws").Install[KeyDebian][0].Lines[0]
	cases := []struct{ name, path, first string }{
		{"pip --user", filepath.Join(userBin, "aws"), "python3 -m pip uninstall -y awscli"},
		{"pipx", filepath.Join(userBin, "aws-pipx"), "pipx uninstall awscli"},
		{"Debian package", "/usr/bin/aws", "sudo apt-get remove -y awscli"},
		{"elsewhere", "/opt/aws/bin/aws", strings.ReplaceAll(installV2, "{uname}", "x86_64")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &fakeMachine{
				onPath:   map[string]string{"aws": c.path},
				versions: map[string]string{c.path: "aws-cli/1.46.1 Python/3.8.10 Linux/5.15.0 botocore/1.40.1"},
			}
			env := m.envFor(ubuntu)
			env.Home = home
			st := Check(context.Background(), tool(t, "aws"), env)
			if !st.TooOld || len(st.Install) == 0 || st.Install[0] != c.first {
				t.Errorf("aws v1 at %s: steps %q, want them to start with %q", c.path, st.Install, c.first)
			}
		})
	}
}

// Ubuntu 24.04 and older keep an openfortivpn without SAML, so there an old one
// is built from source; Homebrew simply has a newer one.
func TestCheckTooOldWherePackageIsOld(t *testing.T) {
	ofv := tool(t, "openfortivpn")
	machine := func(version string) *fakeMachine {
		return &fakeMachine{
			onPath:   map[string]string{"openfortivpn": "/usr/bin/openfortivpn"},
			versions: map[string]string{"/usr/bin/openfortivpn": version + "\n"},
		}
	}

	old := Check(context.Background(), ofv, machine("1.17.1").envFor(ubuntu))
	if !old.TooOld || old.Usable() {
		t.Fatalf("openfortivpn 1.17.1 has no SAML, so den cannot use it: %+v", old)
	}
	build := strings.Join(old.Install, "\n")
	if old.Via != "apt-get" || !reflect.DeepEqual(old.Install, old.Upgrade) ||
		!strings.Contains(build, "sudo apt-get remove -y openfortivpn") || !strings.Contains(build, "--enable-legacy-pppd") {
		t.Errorf("an old Ubuntu package needs building, not installing again: %q", old.Install)
	}

	if mac := Check(context.Background(), ofv, machine("1.21.0").envFor(mac)); !reflect.DeepEqual(mac.Install, []string{"brew upgrade openfortivpn"}) {
		t.Errorf("Homebrew upgrades it: %q", mac.Install)
	}
	// EPEL 9 has 1.21.0.
	if rhel := Check(context.Background(), ofv, machine("1.21.0").envFor(fedora)); !slices.Contains(rhel.Install, "sudo dnf remove -y openfortivpn") {
		t.Errorf("an old EPEL package needs building too: %q", rhel.Install)
	}
	if cur := Check(context.Background(), ofv, machine("1.24.1").envFor(ubuntu)); cur.TooOld || !cur.Usable() || len(cur.Steps()) != 0 {
		t.Errorf("1.24.1 has SAML: %+v", cur)
	}

	// Not installed yet, apt comes first: the release may well have a new one.
	for name, env := range map[string]Env{"missing": (&fakeMachine{}).envFor(ubuntu), "preview": PreviewEnv(ubuntu)} {
		st := Check(context.Background(), ofv, env)
		if !reflect.DeepEqual(st.Steps(), []string{"sudo apt-get install -y openfortivpn"}) || len(st.Upgrade) == 0 {
			t.Errorf("%s: steps %q, upgrade %q", name, st.Steps(), st.Upgrade)
		}
	}
}

func TestCheckOffPath(t *testing.T) {
	m := &fakeMachine{
		files: []string{
			"/opt/homebrew/opt/mysql-client/bin/mysql",
			"/opt/homebrew/opt/postgresql@14/bin/psql",
			"/opt/homebrew/opt/postgresql@16/bin/psql",
			"/Applications/KeePassXC.app/Contents/MacOS/keepassxc-cli",
		},
		versions: map[string]string{"/opt/homebrew/opt/mysql-client/bin/mysql": "mysql  Ver 9.3.0 for macos15.4"},
	}
	mysql := Check(context.Background(), tool(t, "mysql"), m.envFor(mac))
	if !mysql.OffPath || mysql.Usable() || mysql.PathFix != "brew link --force mysql-client" || mysql.Version != "9.3.0" {
		t.Errorf("keg-only mysql-client: %+v", mysql)
	}
	psql := Check(context.Background(), tool(t, "psql"), m.envFor(mac))
	if psql.Path != "/opt/homebrew/opt/postgresql@16/bin/psql" || psql.PathFix != "brew link --force postgresql@16" {
		t.Errorf("the newest keg should win: %+v", psql)
	}
	kp := Check(context.Background(), tool(t, "keepassxc-cli"), m.envFor(mac))
	if !kp.OffPath || !kp.DenFinds || !kp.Usable() {
		t.Errorf("den finds keepassxc-cli in the app bundle itself: %+v", kp)
	}

	winMachine := &fakeMachine{files: []string{`C:\Program Files/KeePassXC/keepassxc-cli.exe`}}
	winKP := Check(context.Background(), tool(t, "keepassxc-cli"), winMachine.envFor(win))
	if !winKP.OffPath || winKP.DenFinds || !strings.HasPrefix(winKP.PathFix, "add ") {
		t.Errorf("on Windows den does not look in Program Files: %+v", winKP)
	}
}

func TestCheckMissingPerPlatform(t *testing.T) {
	cases := []struct {
		name string
		p    platform.Info
		tool string
		via  string
		want []string
	}{
		{"mac", mac, "sops", "brew", []string{"brew install sops"}},
		{"ubuntu amd64", ubuntu, "sops", "", []string{
			"curl -fsSLO https://github.com/getsops/sops/releases/download/v" + sopsVersion + "/sops_" + sopsVersion + "_amd64.deb",
			"sudo apt-get install -y ./sops_" + sopsVersion + "_amd64.deb",
		}},
		{"fedora arm64", fedora, "sops", "dnf", []string{
			"sudo dnf install -y https://github.com/getsops/sops/releases/download/v" + sopsVersion + "/sops-" + sopsVersion + "-1.aarch64.rpm",
		}},
		{"windows", win, "sops", "winget", []string{"winget install -e --id SecretsOPerationS.SOPS"}},
		{"windows scoop only", winScoop, "aws", "scoop", []string{"scoop install aws"}},
		{"ubuntu ssm arch", ubuntu, "session-manager-plugin", "", []string{
			`curl -fsSLo session-manager-plugin.deb "https://s3.amazonaws.com/session-manager-downloads/plugin/latest/ubuntu_64bit/session-manager-plugin.deb"`,
			"sudo dpkg -i session-manager-plugin.deb",
		}},
		{"fedora aws arch", fedora, "aws", "", []string{
			`curl -fsSLo awscliv2.zip "https://awscli.amazonaws.com/awscli-exe-linux-aarch64.zip"`,
			"unzip -q awscliv2.zip",
			"sudo ./aws/install",
		}},
	}
	for _, c := range cases {
		st := Check(context.Background(), tool(t, c.tool), (&fakeMachine{}).envFor(c.p))
		if st.Found() || st.Via != c.via || !reflect.DeepEqual(st.Install, c.want) || st.ManagerMissing {
			t.Errorf("%s: via %q install %q (manager missing %v), want via %q %q", c.name, st.Via, st.Install, st.ManagerMissing, c.via, c.want)
		}
	}

	vpn := Check(context.Background(), tool(t, "openfortivpn"), (&fakeMachine{}).envFor(win))
	if vpn.Unsupported == "" || len(vpn.Install) != 0 {
		t.Errorf("openfortivpn on Windows: %+v", vpn)
	}
	other := Check(context.Background(), tool(t, "sops"), (&fakeMachine{}).envFor(alpine))
	if len(other.Install) != 0 || other.Docs == "" {
		t.Errorf("an unknown distro gets the docs link only: %+v", other)
	}
	bare := Check(context.Background(), tool(t, "sops"), (&fakeMachine{}).envFor(macBare))
	if !bare.ManagerMissing || bare.Via != "brew" || ManagerURL(bare.Via) != "https://brew.sh" {
		t.Errorf("a Mac without Homebrew must be told to get it: %+v", bare)
	}
}

func TestPreviewLooksAtNothing(t *testing.T) {
	env := PreviewEnv(ubuntu)
	env.LookPath = func(string) (string, error) { t.Fatal("preview looked at PATH"); return "", nil }
	env.Glob = func(string) []string { t.Fatal("preview looked at files"); return nil }
	env.Output = func(context.Context, string, ...string) (string, error) {
		t.Fatal("preview ran a command")
		return "", nil
	}
	st := Check(context.Background(), tool(t, "psql"), env)
	if !reflect.DeepEqual(st.Install, []string{"sudo apt-get install -y postgresql-client"}) {
		t.Errorf("preview install = %v", st.Install)
	}
}

func TestNerdFont(t *testing.T) {
	m := &fakeMachine{files: []string{"/home/me/Library/Fonts/JetBrainsMonoNerdFontMono-Regular.ttf"}}
	if st := Check(context.Background(), tool(t, NerdFont), m.envFor(mac)); !st.Found() {
		t.Errorf("font in ~/Library/Fonts not found: %+v", st)
	}
	m = &fakeMachine{files: []string{"/home/me/.local/share/fonts/JetBrainsMono/JetBrainsMonoNerdFont-Bold.ttf"}}
	if st := Check(context.Background(), tool(t, NerdFont), m.envFor(ubuntu)); !st.Found() {
		t.Errorf("font one folder down not found: %+v", st)
	}
	m = &fakeMachine{env: map[string]string{"TERM_PROGRAM": "ghostty"}}
	if st := Check(context.Background(), tool(t, NerdFont), m.envFor(ubuntu)); !st.Found() {
		t.Errorf("ghostty bundles the symbols: %+v", st)
	}
	if st := Check(context.Background(), tool(t, NerdFont), (&fakeMachine{}).envFor(ubuntu)); st.Found() || len(st.Install) == 0 {
		t.Errorf("no font: %+v", st)
	}
}

func TestCombined(t *testing.T) {
	missing := func(p platform.Info, ids ...string) []Status {
		var out []Status
		for _, id := range ids {
			out = append(out, Check(context.Background(), tool(t, id), (&fakeMachine{}).envFor(p)))
		}
		return out
	}
	got := Combined(missing(mac, "sops", "psql", "redis-cli", "vault", "keepassxc-cli"))
	want := []string{
		"brew install sops libpq redis",
		"brew install --cask keepassxc",
		"brew link --force libpq",
		"# vault",
		"brew tap hashicorp/tap",
		"brew install hashicorp/tap/vault",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mac combined:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Installed but off PATH needs linking, not installing again; a working
	// tool needs nothing.
	m := &fakeMachine{
		onPath: map[string]string{"sops": "/opt/homebrew/bin/sops"},
		files:  []string{"/opt/homebrew/opt/mysql-client/bin/mysql", "/Applications/KeePassXC.app/Contents/MacOS/keepassxc-cli"},
	}
	var found []Status
	for _, id := range []string{"sops", "mysql", "keepassxc-cli"} {
		found = append(found, Check(context.Background(), tool(t, id), m.envFor(mac)))
	}
	if got := Combined(found); !reflect.DeepEqual(got, []string{"brew link --force mysql-client"}) {
		t.Errorf("off-PATH combined = %q", got)
	}

	// The MongoDB repository must be added before mongosh installs, so it
	// cannot join the shared apt line.
	got = Combined(missing(ubuntu, "psql", "mongosh", "redis-cli"))
	if got[0] != "sudo apt-get install -y postgresql-client redis-tools" || !slices.Contains(got, "# mongosh") {
		t.Errorf("ubuntu combined: %q", got)
	}
}

// Every tool must tell every supported platform something: how to install
// it, or why it cannot be.
func TestCatalogIsComplete(t *testing.T) {
	placeholder := regexp.MustCompile(`\{(arch|uname|ssmarch|sops)\}`)
	seen := map[string]bool{}
	for _, tl := range Catalog {
		if seen[tl.ID] {
			t.Errorf("%s is in the catalog twice", tl.ID)
		}
		seen[tl.ID] = true
		if tl.Docs == "" || tl.Purpose == "" {
			t.Errorf("%s needs a docs link and a purpose", tl.ID)
		}
		if tl.Find == nil && tl.VersionRe == nil {
			t.Errorf("%s has no version pattern", tl.ID)
		}
		if len(tl.Upgrade) > 0 && tl.MinVersion == "" {
			t.Errorf("%s: upgrade recipes are only used below a MinVersion, and it has none", tl.ID)
		}
		for _, key := range Keys {
			recipes, unsupported := tl.Install[key], tl.Unsupported[key]
			if len(recipes) == 0 && unsupported == "" {
				t.Errorf("%s: nothing for %s", tl.ID, key)
			}
			for _, r := range slices.Concat(recipes, tl.Upgrade[key]) {
				if r.Manager != "" && !slices.Contains(platform.Managers, r.Manager) {
					t.Errorf("%s: unknown package manager %q", tl.ID, r.Manager)
				}
				for _, arch := range []string{"amd64", "arm64"} {
					for _, line := range expand(r.Lines, arch) {
						if placeholder.MatchString(line) {
							t.Errorf("%s: unexpanded placeholder in %q", tl.ID, line)
						}
					}
				}
			}
		}
	}
	for name, f := range Features {
		for _, id := range f.Tools {
			if !seen[id] {
				t.Errorf("feature %s needs %s, which is not in the catalog", name, id)
			}
		}
	}
}

func TestResolve(t *testing.T) {
	cases := map[string][]string{
		"sops":             {"sops"},
		"rds":              {"aws", "session-manager-plugin", "psql"},
		"rds,ssh":          {"psql", "ssh"},
		"redis, eice":      {"aws", "redis-cli"},
		"sops,vault,vpn":   {"sops", "vault", "openfortivpn"},
		"Documentdb,mysql": {"aws", "session-manager-plugin", "mysql", "mongosh"},
	}
	for in, want := range cases {
		got, err := Resolve(strings.Split(in, ","))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Resolve(%s) = %v, %v; want %v", in, got, err, want)
		}
	}
	all, err := Resolve([]string{"all"})
	if err != nil || len(all) != len(Catalog) {
		t.Errorf("all = %v, %v", all, err)
	}
	if _, err := Resolve([]string{"sops", "kafka"}); err == nil || !strings.Contains(err.Error(), "choose from") {
		t.Errorf("an unknown feature must list the valid ones: %v", err)
	}
}

func TestInstallHint(t *testing.T) {
	cases := []struct {
		id   string
		p    platform.Info
		want string
	}{
		{"sops", mac, "brew install sops"},
		{"sops", macBare, "install Homebrew, then: brew install sops"},
		{"vault", mac, "run `den doctor` for the install steps"},
		{"openfortivpn", win, "openfortivpn has no Windows build; use FortiClient VPN, or run den in WSL on a kernel with PPP (WSL 3.0.1's has none; 2.7.14's has)"},
		{"sops", alpine, "see https://getsops.io/docs/#download"},
		{"sops", ubuntu, "run `den doctor` for the install steps"},
		{"gpg", fedora, "sudo dnf install -y gnupg2"},
		{"autossh", mac, "install autossh"},
	}
	for _, c := range cases {
		if got := installHint(c.id, (&fakeMachine{}).envFor(c.p)); got != c.want {
			t.Errorf("%s on %s: %q, want %q", c.id, c.p.Label(), got, c.want)
		}
	}
	// The real machine: whatever it is, the answer is never empty.
	if InstallHint("sops") == "" {
		t.Error("no hint for this machine")
	}
}

func TestSteps(t *testing.T) {
	cases := []struct {
		name string
		st   Status
		want []string
	}{
		{"fine", Status{Path: "/usr/bin/sops", Install: []string{"brew install sops"}}, nil},
		{"missing", Status{Install: []string{"brew install sops"}}, []string{"brew install sops"}},
		{"too old", Status{Path: "/usr/bin/sops", TooOld: true, Install: []string{"brew upgrade sops"}}, []string{"brew upgrade sops"}},
		{"keg", Status{Path: "/opt/homebrew/opt/libpq/bin/psql", OffPath: true, PathFix: "brew link --force libpq"}, []string{"brew link --force libpq"}},
		{"elsewhere", Status{Path: "/opt/x/bin/psql", OffPath: true, PathFix: "add /opt/x/bin to PATH"}, []string{"# add /opt/x/bin to PATH"}},
		{"den finds it", Status{Path: "/Applications/K.app/cli", OffPath: true, DenFinds: true}, nil},
	}
	for _, c := range cases {
		if got := c.st.Steps(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: steps %q, want %q", c.name, got, c.want)
		}
	}
	if ManagerName("") != "official installer" || ManagerName("scoop") != "Scoop" {
		t.Error("manager names")
	}
}

// A terminal tab runs the client by the path den found, so it has to find what
// Check finds, keg-only installs included, without paying for a --version each
// time a tab opens.
func TestLocate(t *testing.T) {
	m := &fakeMachine{
		onPath: map[string]string{"mysql": "/usr/local/bin/mysql"},
		files: []string{
			"/opt/homebrew/opt/postgresql@14/bin/psql",
			"/opt/homebrew/opt/postgresql@16/bin/psql",
		},
	}
	env := m.envFor(mac)
	if got := locate(tool(t, "mysql"), env); got.path != "/usr/local/bin/mysql" || got.offPath {
		t.Errorf("mysql on PATH: %+v", got)
	}
	if got := locate(tool(t, "psql"), env); got.path != "/opt/homebrew/opt/postgresql@16/bin/psql" || !got.offPath {
		t.Errorf("keg-only psql, newest keg first: %+v", got)
	}
	if got := locate(tool(t, "mongosh"), env); got.path != "" {
		t.Errorf("a missing tool was located: %+v", got)
	}
	if len(m.ran) != 0 {
		t.Errorf("locating a tool ran %v", m.ran)
	}
}

func TestFindAgreesWithCheck(t *testing.T) {
	if path, ok := Find("no-such-tool"); ok || path != "" {
		t.Errorf("Find of a tool outside the catalog = %q, %v", path, ok)
	}
	// Whatever this machine has, Find must point where Check does.
	st := Check(context.Background(), tool(t, "psql"), SystemEnv())
	if path, ok := Find("psql"); ok != st.Found() || path != st.Path {
		t.Errorf("Find(psql) = %q, %v; Check found %q", path, ok, st.Path)
	}
}

func TestRunbookSourcesNeedGit(t *testing.T) {
	ids, err := Resolve([]string{"runbook-sources"})
	if err != nil || !slices.Equal(ids, []string{"git"}) {
		t.Fatalf("Resolve(runbook-sources) = %v, %v", ids, err)
	}
}
