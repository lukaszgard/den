package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"

	"github.com/lukaszgard/den/internal/platform"
)

// exposureProblems warns about a file that can hold a secret and sits where
// others can read it. An openfortivpn config may carry `password =`, so a vpn
// config_file is held to this: readable by you alone, and not inside a git
// work tree, where a commit would publish it. A file that is not there is
// checkPath's business.
func exposureProblems(u pathUse, root *yaml.Node) Problems {
	p := u.resolve()
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return nil
	}
	problem := func(code, msg, hint string) Problem {
		return Problem{Severity: SeverityWarning, Line: lineAt(root, u.key), Key: u.key, Code: code, Msg: u.key + ": " + msg, Hint: hint}
	}
	var ps Problems
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		ps = append(ps, problem("path-permissive",
			fmt.Sprintf("%s can be read by other users (mode %04o)", p, fi.Mode().Perm()),
			fmt.Sprintf("run chmod 600 %s: an openfortivpn config may hold a password", p)))
	}
	if repo := gitWorkTree(filepath.Dir(p)); repo != "" {
		ps = append(ps, problem("path-in-repo",
			fmt.Sprintf("%s is inside the git work tree %s", p, repo),
			fmt.Sprintf("keep personal VPN settings in %s, or git-ignore this file: a commit would publish it", platform.TildePath(platform.ConfigDir()))))
	}
	return ps
}

// gitWorkTree is the root of the git work tree dir sits in, found by walking
// up for a .git entry (a folder, or a file in a worktree or submodule); empty
// when there is none. It runs no git: the answer must not depend on one being
// installed.
func gitWorkTree(dir string) string {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
