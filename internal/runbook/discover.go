package runbook

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lukaszgard/den/internal/config"
)

// headerScanLines caps how far into a file we look for directives. Headers
// belong at the top; scanning further would mean reading whole scripts.
const headerScanLines = 40

// headerRe matches one directive line: "# den:name  Reindex search" or the
// "//" equivalent, so shell, Python and Go files all work.
var headerRe = regexp.MustCompile(`^\s*(?:#|//)\s*den:([a-zA-Z_]+)\s+(.*)$`)

// Warning is a non-fatal discovery problem. Discovery must never fail silently:
// a script that looks registered but is not is worse than an obvious error.
type Warning struct {
	Path string
	Msg  string

	// shadows is the name a runbook den skipped shares with the one it kept:
	// deleting that one lets this one take its place.
	shadows string
}

func (w Warning) String() string {
	if w.Path == "" {
		return w.Msg
	}
	return fmt.Sprintf("%s: %s", w.Path, w.Msg)
}

// Discover scans dirs for runbooks: every folder holding a runbook.yaml is
// one, and so is an executable file carrying a den header block. Folders come
// first in the result, so a folder wins a name collision with a loose script.
//
// A folder without a runbook.yaml, or a file without a "den:name" directive,
// is skipped silently: a runbooks directory may hold shared helpers. Anything
// else that goes wrong produces a Warning.
//
// A relative dir is read against base, den.yaml's directory. A script runs in
// its own directory unless its header names a cwd, which is read against that
// directory too: it is where the helpers a script calls by ./name live.
func Discover(dirs []string, base string) ([]config.RunbookSpec, []Warning) {
	var (
		folders, scripts []config.RunbookSpec
		warnings         []Warning
	)
	for _, dir := range dirs {
		path := resolvePath(dir, base)
		entries, err := os.ReadDir(path)
		if err != nil {
			// A directory that does not exist yet is a normal state — people
			// configure the path before creating it. Anything else (a
			// permission problem, say) is worth saying out loud.
			if !os.IsNotExist(err) {
				warnings = append(warnings, Warning{Path: path, Msg: fmt.Sprintf("cannot scan: %v", err)})
			}
			continue
		}
		for _, e := range entries {
			full := filepath.Join(path, e.Name())
			// Absolute, so the runbook keeps working whatever directory den
			// is launched from later.
			if abs, err := filepath.Abs(full); err == nil {
				full = abs
			}
			if e.IsDir() {
				if strings.HasPrefix(e.Name(), ".") {
					continue // .git, .venv and the like
				}
				if _, err := os.Stat(filepath.Join(full, ManifestName)); err != nil {
					continue // a helpers folder, not a runbook
				}
				spec, err := loadManifest(full)
				if err != nil {
					warnings = append(warnings, Warning{Path: filepath.Join(full, ManifestName), Msg: err.Error() + " — skipped"})
					continue
				}
				folders = append(folders, spec)
				continue
			}
			info, err := e.Info()
			if err != nil {
				warnings = append(warnings, Warning{Path: full, Msg: fmt.Sprintf("cannot stat: %v", err)})
				continue
			}
			if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
				continue // not an executable file
			}
			spec, ok, warns := parseFile(full)
			warnings = append(warnings, warns...)
			if ok {
				spec.Cwd = resolvePath(spec.Cwd, filepath.Dir(full))
				if spec.Cwd == "" {
					spec.Cwd = filepath.Dir(full)
				}
				scripts = append(scripts, spec)
			}
		}
	}
	return append(folders, scripts...), warnings
}

// parseFile reads the header block of one file. ok is false when the file
// carries no den:name and is therefore not a runbook at all.
func parseFile(path string) (config.RunbookSpec, bool, []Warning) {
	var warnings []Warning

	f, err := os.Open(path)
	if err != nil {
		return config.RunbookSpec{}, false, []Warning{{Path: path, Msg: fmt.Sprintf("cannot read: %v", err)}}
	}
	defer f.Close()

	spec := config.RunbookSpec{
		Source:  path,
		Command: shellQuote(path), // the shebang decides the interpreter
	}
	retry := config.RetrySpec{}
	haveRetry := false

	scanner := bufio.NewScanner(f)
	for n := 0; n < headerScanLines && scanner.Scan(); n++ {
		m := headerRe.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		key, val := strings.ToLower(m[1]), strings.TrimSpace(m[2])
		switch key {
		case "name":
			spec.Name = val
		case "description":
			spec.Description = val
		case "env":
			spec.Env = val
		case "cwd":
			spec.Cwd = val
		case "timeout":
			spec.Timeout = val
		case "confirm":
			spec.Confirm = isTruthy(val)
		case "param":
			p, err := parseParam(val)
			if err != nil {
				warnings = append(warnings, Warning{Path: path, Msg: err.Error()})
				continue
			}
			spec.Params = append(spec.Params, p)
		case "retry_until_success":
			retry.UntilSuccess, haveRetry = isTruthy(val), true
		case "retry_interval":
			retry.Interval, haveRetry = val, true
		case "retry_timeout":
			retry.Timeout, haveRetry = val, true
		case "retry_max_attempts":
			if _, err := fmt.Sscanf(val, "%d", &retry.MaxAttempts); err != nil {
				warnings = append(warnings, Warning{Path: path, Msg: fmt.Sprintf("den:retry_max_attempts %q is not a number", val)})
				continue
			}
			haveRetry = true
		default:
			warnings = append(warnings, Warning{Path: path, Msg: fmt.Sprintf("unknown directive den:%s", key)})
		}
	}
	if err := scanner.Err(); err != nil {
		warnings = append(warnings, Warning{Path: path, Msg: fmt.Sprintf("read error: %v", err)})
	}

	if spec.Name == "" {
		// Not a runbook. Any directives we did see were probably a typo'd
		// name, so say so rather than dropping the file without a word.
		if len(warnings) > 0 || spec.Description != "" || len(spec.Params) > 0 {
			warnings = append(warnings, Warning{Path: path, Msg: "has den: directives but no den:name — skipped"})
		}
		return config.RunbookSpec{}, false, warnings
	}
	if haveRetry {
		spec.Retry = &retry
	}
	return spec, true, warnings
}

// paramFlagRe matches the optional key=value / bare-flag modifiers that may
// follow a param name.
var paramFlagRe = regexp.MustCompile(`^(options|default)=(.*)$`)

// parseParam reads "name [options=a,b,c] [default=x] [required] [secret] Description".
func parseParam(val string) (config.ParamSpec, error) {
	fields := strings.Fields(val)
	if len(fields) == 0 {
		return config.ParamSpec{}, fmt.Errorf("den:param needs a name")
	}
	p := config.ParamSpec{Name: fields[0]}
	rest := fields[1:]
	for len(rest) > 0 {
		tok := rest[0]
		switch {
		case tok == "required":
			p.Required = true
		case tok == "secret":
			p.Secret = true
		case paramFlagRe.MatchString(tok):
			m := paramFlagRe.FindStringSubmatch(tok)
			if m[1] == "options" {
				p.Options = splitAndTrim(m[2])
			} else {
				p.Default = m[2]
			}
		default:
			// First non-modifier token begins the description.
			p.Description = strings.Join(rest, " ")
			return p, nil
		}
		rest = rest[1:]
	}
	return p, nil
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "1", "on":
		return true
	}
	return false
}

// shellQuote wraps s for safe use inside `sh -c`, so paths with spaces or
// shell metacharacters run as themselves.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
