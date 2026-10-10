package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/lukaszgard/den/internal/platform"
)

// Inspect is the part of the check that looks outside den.yaml: the files it
// points at, and the AWS files its profiles live in. Check cannot do this, it
// reads bytes and nothing else, which keeps it hermetic.
//
// Everything here is a warning. A config that ran yesterday must still load,
// and a file that is missing only breaks the services that use it. baseDir is
// den.yaml's directory, the base the paths den reads against it start from.
func Inspect(cfg *Config, root *yaml.Node, baseDir string) Problems {
	if cfg == nil {
		return nil
	}
	var ps Problems
	ps = append(ps, pathProblems(cfg, root, baseDir)...)
	ps = append(ps, portProblems(cfg, root)...)
	ps = append(ps, awsProblems(cfg, root)...)
	ps = append(ps, nameProblems(cfg, root)...)
	ps = append(ps, formatProblems(cfg, root)...)
	return ps
}

// What a path must be.
type pathKind int

const (
	kindFile pathKind = iota
	kindDir
	kindExec
)

// pathUse is one path den.yaml names, with the rules den reads it by.
type pathUse struct {
	key  string
	raw  string
	kind pathKind
	// home says whether den expands a leading ~ before using the path.
	home bool
	// base is what a relative path is read against; "" is the directory den runs in.
	base string
	// create says den makes the path itself when it is missing, so only the
	// wrong kind of thing there is a problem.
	create bool
	// beside makes a relative path fall back to the directory den runs in when
	// nothing is at base: how a vpn config_file is read.
	beside bool
}

// resolve is the path den opens, absolute so the message can show it.
func (u pathUse) resolve() string {
	p := u.raw
	if u.home {
		p = platform.ExpandHome(p)
	}
	if filepath.IsAbs(p) {
		return p
	}
	if u.base != "" {
		joined := filepath.Join(u.base, p)
		if !u.beside {
			return joined
		}
		if _, err := os.Stat(joined); err == nil {
			return joined
		}
		if abs, err := filepath.Abs(p); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
		return joined // missing either way: report where it should be
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// pathProblems checks every path den.yaml names.
func pathProblems(cfg *Config, root *yaml.Node, baseDir string) Problems {
	var uses []pathUse
	add := func(key, raw string, kind pathKind, home bool, base string) {
		if raw != "" {
			uses = append(uses, pathUse{key: key, raw: raw, kind: kind, home: home, base: base})
		}
	}
	// `den runbook new` makes the first runbooks.dirs folder, so that one is not
	// a mistake while it is not there yet. The others are never made.
	addDir := func(key, raw string, base string, create bool) {
		if raw != "" {
			uses = append(uses, pathUse{key: key, raw: raw, kind: kindDir, home: true, base: base, create: create})
		}
	}
	if a := cfg.AWS; a != nil {
		add("aws.config_path", a.ConfigPath, kindFile, true, "")
		add("aws.credentials_path", a.CredentialsPath, kindFile, true, "")
	}
	if s := cfg.Secrets; s != nil {
		for i, it := range s.SOPS {
			add(fmt.Sprintf("secrets.sops[%d].file", i), it.File, kindFile, true, "")
		}
		for i, it := range s.KeePassXC {
			k := fmt.Sprintf("secrets.keepassxc[%d]", i)
			add(k+".database", it.Database, kindFile, true, "")
			add(k+".key_file", it.KeyFile, kindFile, true, "")
			add(k+".cli_path", it.CLIPath, kindExec, true, "")
		}
		for i, it := range s.Vault {
			k := fmt.Sprintf("secrets.vault[%d]", i)
			add(k+".ca_cert", it.CACert, kindFile, true, "")
			add(k+".cli_path", it.CLIPath, kindExec, true, "")
		}
	}
	for i, svc := range cfg.Services {
		if svc.DocumentDB != nil {
			add(fmt.Sprintf("services[%d].documentdb.ca_file", i), svc.DocumentDB.CAFile, kindFile, true, "")
		}
		if svc.Docker != nil {
			// docker compose gets these as written: no ~, and the directory den
			// runs in is the base.
			k := fmt.Sprintf("services[%d].docker", i)
			add(k+".compose_file", svc.Docker.ComposeFile, kindFile, false, "")
			add(k+".env_file", svc.Docker.EnvFile, kindFile, false, "")
		}
	}
	var ps Problems
	for i, v := range cfg.VPNs {
		if v.ConfigFile != "" {
			u := pathUse{key: fmt.Sprintf("vpn[%d].config_file", i), raw: v.ConfigFile, kind: kindFile, home: true, base: baseDir, beside: true}
			ps = append(ps, checkPath(u, root, baseDir)...)
			ps = append(ps, exposureProblems(u, root)...)
		}
	}
	if r := cfg.Runbooks; r != nil {
		for i, d := range r.Dirs {
			addDir(fmt.Sprintf("runbooks.dirs[%d]", i), d, baseDir, i == 0)
		}
		add("runbooks.template", r.Template, kindDir, true, baseDir)
		for i, it := range r.Items {
			add(fmt.Sprintf("runbooks.items[%d].cwd", i), it.Cwd, kindDir, true, baseDir)
		}
	}
	if l := cfg.Logs; l != nil && l.FileLocation != "" {
		// den makes the missing folders of a log file itself; what it cannot do
		// is write into a folder.
		u := pathUse{key: "logs.file_location", raw: l.FileLocation, kind: kindFile, home: true}
		if p := u.resolve(); isDir(p) {
			ps = append(ps, Problem{
				Severity: SeverityWarning, Line: lineAt(root, u.key), Key: u.key, Code: "path-not-file",
				Msg:  fmt.Sprintf("%s: %s is a folder, not a file", u.key, p),
				Hint: "name the log file itself, such as " + filepath.Join(l.FileLocation, "den.log"),
			})
		}
	}
	for _, u := range uses {
		ps = append(ps, checkPath(u, root, baseDir)...)
	}
	return ps
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// checkPath is what is wrong with one path, if anything.
func checkPath(u pathUse, root *yaml.Node, baseDir string) Problems {
	p := u.resolve()
	problem := func(code, msg, hint string) Problems {
		return Problems{{Severity: SeverityWarning, Line: lineAt(root, u.key), Key: u.key, Code: code, Msg: u.key + ": " + msg, Hint: hint}}
	}
	fi, err := os.Stat(p)
	if err != nil {
		if u.create && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		msg := p + " does not exist"
		if !errors.Is(err, fs.ErrNotExist) {
			msg = fmt.Sprintf("cannot read %s: %v", p, err)
		}
		return problem("path-missing", msg, missingHint(u, baseDir))
	}
	switch {
	case u.kind == kindDir && !fi.IsDir():
		return problem("path-not-dir", p+" is a file, not a folder", "point it at a folder")
	case u.kind == kindFile && fi.IsDir():
		return problem("path-not-file", p+" is a folder, not a file", "point it at a file")
	case u.kind == kindExec && (fi.IsDir() || fi.Mode()&0o111 == 0):
		return problem("path-not-executable", p+" is not an executable file", "point it at the program itself, or run chmod +x on it")
	}
	return nil
}

// missingHint says how to fix a path that is not there, and what a relative
// one is read against: the surprise is usually the base.
func missingHint(u pathUse, baseDir string) string {
	switch {
	case u.raw != "" && u.raw[0] == '~' && !u.home:
		return "write the full path: den does not expand ~ here"
	case filepath.IsAbs(u.raw) || (u.home && filepath.IsAbs(platform.ExpandHome(u.raw))):
		return "create it, or correct the path"
	case u.base != "":
		return fmt.Sprintf("create it, or correct the path (relative paths start from %s, den.yaml's folder)", baseDir)
	}
	cwd, _ := os.Getwd()
	return fmt.Sprintf("create it, or correct the path (relative paths start from %s, where den runs)", cwd)
}
