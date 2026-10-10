package prereq

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lukaszgard/den/internal/platform"
)

// Env is everything Check looks at, so tests can describe any machine.
type Env struct {
	Platform platform.Info
	LookPath func(file string) (string, error)
	Glob     func(pattern string) []string
	// Output runs a command and returns its combined output.
	Output func(ctx context.Context, name string, args ...string) (string, error)
	Getenv func(key string) string
	Home   string
	// Preview describes another platform: nothing on this machine is looked
	// at, only its install instructions are produced.
	Preview bool
}

// SystemEnv is this machine.
func SystemEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{
		Platform: platform.Detect(),
		LookPath: exec.LookPath,
		Glob: func(pattern string) []string {
			m, _ := filepath.Glob(pattern)
			return m
		},
		Output: func(ctx context.Context, name string, args ...string) (string, error) {
			out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
			return string(out), err
		},
		Getenv: os.Getenv,
		Home:   home,
	}
}

// PreviewEnv describes platform p without looking at this machine.
func PreviewEnv(p platform.Info) Env {
	env := SystemEnv()
	env.Platform = p
	env.Preview = true
	return env
}

// Status is what Check found out about one tool.
type Status struct {
	Tool    string `json:"tool"`
	Purpose string `json:"purpose,omitempty"`
	Path    string `json:"path,omitempty"` // where it was found; empty when missing
	// OffPath is set when it was found only outside PATH; DenFinds when den
	// uses it from there anyway, PathFix how to put it on PATH otherwise.
	OffPath  bool   `json:"off_path,omitempty"`
	DenFinds bool   `json:"den_finds,omitempty"`
	PathFix  string `json:"path_fix,omitempty"`

	Version     string `json:"version,omitempty"`
	MinVersion  string `json:"min_version,omitempty"`
	MinReason   string `json:"-"`
	MinRequired bool   `json:"-"`
	TooOld      bool   `json:"too_old,omitempty"`

	// Unsupported says why this platform has nothing to install.
	Unsupported string `json:"unsupported,omitempty"`
	// Via is the package manager Install uses, or "" for an official
	// installer; Install is empty when there is no recipe for the platform.
	Via     string   `json:"via,omitempty"`
	Install []string `json:"install,omitempty"`
	// Upgrade replaces Install when the version found is older than
	// MinVersion and the package manager has nothing newer (Tool.Upgrade).
	Upgrade []string `json:"upgrade,omitempty"`
	// ManagerMissing is set when Via is not installed either; ManagerURL
	// says where to get it.
	ManagerMissing bool   `json:"manager_missing,omitempty"`
	Docs           string `json:"docs,omitempty"`
}

// Found reports whether the tool is on this machine at all.
func (s Status) Found() bool { return s.Path != "" }

// Usable reports whether den can use the tool as it is.
func (s Status) Usable() bool {
	return s.Found() && (!s.OffPath || s.DenFinds) && (!s.TooOld || !s.MinRequired)
}

// Steps are the lines that make the tool usable: the install lines when it
// is missing or too old, the PATH fix when it is only off PATH, none when
// it is fine.
func (s Status) Steps() []string {
	switch {
	case !s.Found() || s.TooOld:
		return s.Install
	case s.OffPath && !s.DenFinds:
		if strings.HasPrefix(s.PathFix, "add ") {
			return []string{"# " + s.PathFix}
		}
		return []string{s.PathFix}
	}
	return nil
}

// versionTimeout bounds one `--version`. Some CLIs start a runtime first
// (mongosh starts Node), but none should take this long.
const versionTimeout = 5 * time.Second

// Check finds t on the machine env describes and says how to install or
// upgrade it.
func Check(ctx context.Context, t Tool, env Env) Status {
	st := Status{Tool: t.ID, Purpose: t.Purpose, MinVersion: t.MinVersion, MinReason: t.MinReason,
		MinRequired: t.MinRequired, Docs: t.Docs}
	key := Key(env.Platform)
	st.Unsupported = t.Unsupported[key]
	if r, ok := pick(t.Install[key], env); ok {
		st.Via = r.Manager
		st.Install = expand(r.Lines, env.Platform.Arch)
		st.ManagerMissing = r.Manager != "" && !env.Preview && !env.Platform.Has(r.Manager)
	}
	newer, hasNewer := pick(t.Upgrade[key], env)
	if hasNewer {
		st.Upgrade = expand(newer.Lines, env.Platform.Arch)
	}
	if env.Preview {
		return st
	}

	loc := locate(t, env)
	st.Path, st.OffPath, st.DenFinds = loc.path, loc.offPath, loc.denFinds
	if t.Find != nil {
		return st
	}
	if loc.offPath {
		st.PathFix = pathFix(st.Path)
	}
	if st.Found() && t.VersionRe != nil {
		c, cancel := context.WithTimeout(ctx, versionTimeout)
		out, _ := env.Output(c, st.Path, t.VersionArgs...)
		cancel()
		if m := t.VersionRe.FindStringSubmatch(out); m != nil {
			st.Version = m[1]
		}
	}
	if st.Version != "" && t.MinVersion != "" && Less(st.Version, t.MinVersion) {
		st.TooOld = true
		if hasNewer {
			st.Via, st.Install = newer.Manager, st.Upgrade
			st.ManagerMissing = newer.Manager != "" && !env.Platform.Has(newer.Manager)
		} else {
			st.Install = upgrade(st.Install)
		}
		if t.Remove != nil {
			st.Install = append(t.Remove(st.Path, env), st.Install...)
		}
	}
	return st
}

// location is where a tool was found; path is empty when it was not.
type location struct {
	path string
	// offPath is set when it was found only outside PATH, denFinds when den
	// uses it from there anyway.
	offPath, denFinds bool
}

// locate finds t on the machine env describes without running it.
func locate(t Tool, env Env) location {
	if t.Find != nil {
		return location{path: t.Find(env)}
	}
	if p, err := env.LookPath(t.ID); err == nil {
		return location{path: p}
	}
	if t.Places == nil {
		return location{}
	}
	for _, place := range t.Places(env) {
		matches := env.Glob(place.Glob)
		if len(matches) == 0 {
			continue
		}
		// The newest of several versioned installs sorts last.
		slices.Sort(matches)
		return location{path: matches[len(matches)-1], offPath: true, denFinds: place.DenFinds}
	}
	return location{}
}

var systemEnv = sync.OnceValue(SystemEnv)

// Find returns where the catalog tool id is on this machine: on PATH, or in
// one of the places its entry names, such as a keg-only Homebrew install. It
// is for code that runs the tool by that path and so does not need it on
// PATH; unlike Check it never runs the tool.
func Find(id string) (path string, ok bool) {
	t, known := Lookup(id)
	if !known {
		return "", false
	}
	loc := locate(t, systemEnv())
	return loc.path, loc.path != ""
}

// InstallHint is a one-line answer to "how do I install this here": the
// command when one line does it, else a pointer to den doctor, which prints
// every step.
func InstallHint(id string) string { return installHint(id, systemEnv()) }

func installHint(id string, env Env) string {
	t, ok := Lookup(id)
	if !ok {
		return "install " + id
	}
	key := Key(env.Platform)
	if u := t.Unsupported[key]; u != "" {
		return u
	}
	r, ok := pick(t.Install[key], env)
	switch {
	case !ok:
		return "see " + t.Docs
	case len(r.Lines) == 1 && !env.Platform.Has(r.Manager) && r.Manager != "":
		return "install " + ManagerName(r.Manager) + ", then: " + expand(r.Lines, env.Platform.Arch)[0]
	case len(r.Lines) == 1:
		return expand(r.Lines, env.Platform.Arch)[0]
	}
	return "run `den doctor` for the install steps"
}

// pick returns the first recipe whose package manager is present (or which
// needs none), else the first recipe, so a missing manager is named rather
// than the tool left without instructions.
func pick(recipes []Recipe, env Env) (Recipe, bool) {
	if len(recipes) == 0 {
		return Recipe{}, false
	}
	if env.Preview {
		return recipes[0], true
	}
	for _, r := range recipes {
		if r.Manager == "" || env.Platform.Has(r.Manager) {
			return r, true
		}
	}
	return recipes[0], true
}

var managerNames = map[string]string{
	"brew": "Homebrew", "apt-get": "apt", "dnf": "dnf", "winget": "winget", "scoop": "Scoop",
}

var managerURLs = map[string]string{
	"brew":    "https://brew.sh",
	"apt-get": "https://wiki.debian.org/Apt",
	"dnf":     "https://dnf.readthedocs.io",
	"winget":  "https://learn.microsoft.com/windows/package-manager/winget/",
	"scoop":   "https://scoop.sh",
}

// ManagerName is how people call a package manager, or "official installer"
// for recipes that need none.
func ManagerName(manager string) string {
	if n, ok := managerNames[manager]; ok {
		return n
	}
	return "official installer"
}

// ManagerURL is where to get a package manager.
func ManagerURL(manager string) string { return managerURLs[manager] }

// expand fills in the architecture-dependent parts of download URLs, which
// every vendor spells differently.
func expand(lines []string, goarch string) []string {
	uname, ssmArch := goarch, goarch
	switch goarch {
	case "amd64":
		uname, ssmArch = "x86_64", "64bit"
	case "arm64":
		uname = "aarch64"
	}
	r := strings.NewReplacer("{arch}", goarch, "{uname}", uname, "{ssmarch}", ssmArch, "{sops}", sopsVersion)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = r.Replace(l)
	}
	return out
}

// upgrade turns install lines into upgrade lines where the package manager
// needs a different verb; official installers simply overwrite.
func upgrade(lines []string) []string {
	r := strings.NewReplacer(
		"brew install --cask ", "brew upgrade --cask ",
		"brew install ", "brew upgrade ",
		"sudo dnf install -y ", "sudo dnf upgrade -y ",
		"winget install ", "winget upgrade ",
		"scoop install ", "scoop update ",
	)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = r.Replace(l)
	}
	return out
}

var brewOpt = regexp.MustCompile(`^(?:/opt/homebrew|/usr/local)/opt/([^/]+)/bin/`)

// pathFix says how to make a tool found off PATH reachable from the shell.
func pathFix(path string) string {
	if m := brewOpt.FindStringSubmatch(path); m != nil {
		return "brew link --force " + m[1]
	}
	return "add " + filepath.Dir(path) + " to PATH"
}

// Less compares dotted versions numerically: 3.10 is newer than 3.9. Parts
// that are not numbers (a "p2" suffix) compare by their leading digits.
func Less(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(pa), len(pb)) {
		var x, y int
		if i < len(pa) {
			x = leadingInt(pa[i])
		}
		if i < len(pb) {
			y = leadingInt(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

func leadingInt(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}

// simpleInstall matches install lines that can share one command with other
// packages: no flags beyond the manager's usual ones, no bucket or tap path.
var simpleInstall = regexp.MustCompile(`^(sudo apt-get install -y|sudo dnf install -y|brew install|brew install --cask|scoop install) ([^\s/]+)$`)

// Combined merges the Steps of several tools into as few commands as
// possible: a recipe whose first line is a plain package install joins one
// shared line per manager, and everything else follows, under a comment
// naming its tool when it takes several lines. Lines only merge when
// nothing in their recipe has to run before them, such as adding a vendor
// repository.
func Combined(statuses []Status) []string {
	var order []string
	pkgs := map[string][]string{}
	var rest []string
	for _, st := range statuses {
		lines := st.Steps()
		if len(lines) == 0 {
			continue
		}
		if m := simpleInstall.FindStringSubmatch(lines[0]); m != nil {
			if _, seen := pkgs[m[1]]; !seen {
				order = append(order, m[1])
			}
			pkgs[m[1]] = append(pkgs[m[1]], m[2])
			lines = lines[1:]
		}
		switch {
		case len(lines) == 1:
			rest = append(rest, lines[0])
		case len(lines) > 1:
			// Several steps belong together; say whose they are.
			rest = append(rest, "# "+st.Tool)
			rest = append(rest, lines...)
		}
	}
	out := make([]string, 0, len(order)+len(rest))
	for _, prefix := range order {
		out = append(out, prefix+" "+strings.Join(pkgs[prefix], " "))
	}
	return append(out, rest...)
}
