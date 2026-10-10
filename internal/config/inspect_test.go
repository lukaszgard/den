package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// scaffold makes a temp folder with a home/ and a run/ in it, points HOME at
// the first and the working directory at the second, so a path read from where
// den runs, one read against den.yaml's folder (the temp folder itself) and
// one in the home folder can never be mistaken for each other.
func scaffold(t *testing.T) string {
	t.Helper()
	dir := mustEval(t, t.TempDir())
	for _, d := range []string{"home", "run"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", filepath.Join(dir, "home"))
	t.Chdir(filepath.Join(dir, "run"))
	return dir
}

// inspectIn writes yaml as dir/den.yaml and returns what LoadChecked found.
func inspectIn(t *testing.T, dir, yaml string) (*Config, Problems) {
	t.Helper()
	path := filepath.Join(dir, "den.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimPrefix(yaml, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, ps, err := LoadChecked(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, ps
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// onlyProblem is the one problem of a code, failing when there is none or more.
func onlyProblem(t *testing.T, ps Problems, code string) Problem {
	t.Helper()
	var found []Problem
	for _, p := range ps {
		if p.Code == code {
			found = append(found, p)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one %s problem, got %d in:\n%v", code, len(found), ps)
	}
	return found[0]
}

func TestInspectWarnsOncePerMissingPath(t *testing.T) {
	// Each entry names one path that is not there; the line is the one the
	// key is written on.
	tests := []struct {
		name string
		yaml string
		key  string
		line int
		in   string // the resolved path the message shows, under the temp folder
	}{
		{"aws config", "aws:\n  config_path: ~/nope/config\n", "aws.config_path", 2, "/home/nope/config"},
		{"aws credentials", "aws:\n  credentials_path: /nowhere/credentials\n", "aws.credentials_path", 2, "/nowhere/credentials"},
		{"sops", "secrets:\n  sops:\n    - name: s\n      file: ~/s.yaml\n", "secrets.sops[0].file", 4, "/home/s.yaml"},
		{"keepassxc database", "secrets:\n  keepassxc:\n    - name: k\n      database: ~/k.kdbx\n", "secrets.keepassxc[0].database", 4, "/home/k.kdbx"},
		{"keepassxc key", "secrets:\n  keepassxc:\n    - name: k\n      database: ~/db.kdbx\n      key_file: ~/k.key\n", "secrets.keepassxc[0].key_file", 5, "/home/k.key"},
		{"keepassxc cli", "secrets:\n  keepassxc:\n    - name: k\n      database: ~/db.kdbx\n      cli_path: ~/bin/kp\n", "secrets.keepassxc[0].cli_path", 5, "/home/bin/kp"},
		{"vault ca", "secrets:\n  vault:\n    - name: v\n      path: kv/x\n      ca_cert: ~/ca.pem\n", "secrets.vault[0].ca_cert", 5, "/home/ca.pem"},
		{"vault cli", "secrets:\n  vault:\n    - name: v\n      path: kv/x\n      cli_path: ~/bin/vault\n", "secrets.vault[0].cli_path", 5, "/home/bin/vault"},
		{"documentdb ca", "services:\n  - name: d\n    type: documentdb\n    documentdb:\n      documentdb_host: h\n      region: eu\n      environment: int\n      ec2_instance_id: i-0123abcd\n      aws_profile: p\n      credential_profile: p\n      aws_region_code: eu-central-1\n      ca_file: ~/ca.pem\n", "services[0].documentdb.ca_file", 12, "/home/ca.pem"},
		{"docker compose", "services:\n  - name: d\n    type: docker\n    docker:\n      compose_file: stack.yml\n", "services[0].docker.compose_file", 5, "/run/stack.yml"},
		{"docker env", "services:\n  - name: d\n    type: docker\n    docker:\n      compose_file: .\n      env_file: .env.dev\n", "services[0].docker.env_file", 6, "/run/.env.dev"},
		{"vpn", "vpn:\n  - name: v\n    config_file: forti.conf\n", "vpn[0].config_file", 3, "/forti.conf"},
		{"runbook template", "runbooks:\n  template: tpl\n", "runbooks.template", 2, "/tpl"},
		{"runbook cwd", "runbooks:\n  items:\n    - name: r\n      command: ls\n      cwd: work\n", "runbooks.items[0].cwd", 5, "/work"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := scaffold(t)
			touch(t, filepath.Join(dir, "home", "db.kdbx"))
			cfg, ps := inspectIn(t, dir, tt.yaml)
			if cfg == nil {
				t.Fatalf("a missing file is a warning, the config must load: %v", ps)
			}
			p := onlyProblem(t, ps, "path-missing")
			if p.Severity != SeverityWarning || p.Key != tt.key || p.Line != tt.line {
				t.Errorf("problem = %+v, want a warning for %s on line %d", p, tt.key, tt.line)
			}
			want := filepath.Join(dir, tt.in)
			if strings.HasPrefix(tt.in, "/nowhere") {
				want = tt.in
			}
			if !strings.Contains(p.Msg, want) {
				t.Errorf("message %q should show the resolved path %s", p.Msg, want)
			}
			if !strings.Contains(p.Hint, "create it") {
				t.Errorf("hint = %q", p.Hint)
			}
		})
	}
}

func TestInspectNamesWhatRelativePathsStartFrom(t *testing.T) {
	dir := scaffold(t)
	_, ps := inspectIn(t, dir, "runbooks:\n  template: tpl\nvpn:\n  - name: v\n    config_file: forti.conf\nservices:\n  - name: d\n    type: docker\n    docker:\n      compose_file: stack.yml\n")
	for code, want := range map[string]string{"runbooks.template": "den.yaml's folder", "vpn[0].config_file": "den.yaml's folder", "services[0].docker.compose_file": "where den runs"} {
		found := false
		for _, p := range ps {
			if p.Key == code {
				found = true
				if !strings.Contains(p.Hint, want) {
					t.Errorf("%s hint = %q, want %q", code, p.Hint, want)
				}
			}
		}
		if !found {
			t.Errorf("no problem for %s in %v", code, ps)
		}
	}
}

func TestInspectAcceptsPathsThatAreThere(t *testing.T) {
	dir := scaffold(t)
	touch(t, filepath.Join(dir, "home", "s.yaml"))
	touch(t, filepath.Join(dir, "forti.conf"))
	touch(t, filepath.Join(dir, "run", "stack.yml")) // docker reads it from where den runs
	for _, d := range []string{"rb", "work"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	cfg, ps := inspectIn(t, dir, `
secrets:
  sops:
    - name: s
      file: ~/s.yaml
vpn:
  - name: v
    config_file: forti.conf
runbooks:
  dirs: [rb]
  items:
    - name: r
      command: ls
      cwd: work
services:
  - name: d
    type: docker
    docker:
      compose_file: stack.yml
`)
	if cfg == nil || len(ps) != 0 {
		t.Fatalf("every path is there, want no problems: %v", ps)
	}
}

func TestInspectChecksWhatThePathIs(t *testing.T) {
	dir := scaffold(t)
	touch(t, filepath.Join(dir, "plain"))
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0o750); err != nil {
		t.Fatal(err)
	}
	_, ps := inspectIn(t, dir, `
runbooks:
  dirs: [plain]
  template: folder/
secrets:
  sops:
    - name: s
      file: `+dir+`/folder
  vault:
    - name: v
      path: kv/x
      cli_path: `+dir+`/plain
logs:
  file_location: `+dir+`/folder
`)
	if p := onlyProblem(t, ps, "path-not-dir"); p.Key != "runbooks.dirs[0]" {
		t.Errorf("path-not-dir = %+v", p)
	}
	if p := onlyProblem(t, ps, "path-not-executable"); p.Key != "secrets.vault[0].cli_path" {
		t.Errorf("path-not-executable = %+v", p)
	}
	notFile := 0
	for _, p := range ps {
		if p.Code == "path-not-file" {
			notFile++
		}
	}
	if notFile != 2 { // the sops folder and the log folder
		t.Errorf("path-not-file problems = %d, want 2 in %v", notFile, ps)
	}
}

func TestInspectOnlyExpectsTheFirstRunbookFolderToBeMadeLater(t *testing.T) {
	// The first `den runbook new` makes runbooks.dirs[0]; no other is ever made.
	dir := scaffold(t)
	_, ps := inspectIn(t, dir, "runbooks:\n  dirs: [runbooks, more]\n")
	p := onlyProblem(t, ps, "path-missing")
	if p.Key != "runbooks.dirs[1]" || len(ps) != 1 {
		t.Errorf("problems = %v", ps)
	}
	// A first folder that is a file is wrong all the same.
	touch(t, filepath.Join(dir, "plain"))
	_, ps = inspectIn(t, dir, "runbooks:\n  dirs: [plain]\n")
	onlyProblem(t, ps, "path-not-dir")
}

func TestInspectDoesNotRequireALogFolderToExist(t *testing.T) {
	_, ps := inspectIn(t, scaffold(t), "logs:\n  file_location: ./deep/er/den.log\n")
	if len(ps) != 0 {
		t.Errorf("den creates the log folder itself: %v", ps)
	}
}

func TestInspectDoesNotExpandTildeForDocker(t *testing.T) {
	_, ps := inspectIn(t, scaffold(t), "services:\n  - name: d\n    type: docker\n    docker:\n      compose_file: ~/stack.yml\n")
	p := onlyProblem(t, ps, "path-missing")
	if !strings.Contains(p.Hint, "does not expand ~") {
		t.Errorf("hint = %q", p.Hint)
	}
}

func TestVPNConfigFilePathIsReadAgainstDenYaml(t *testing.T) {
	dir := scaffold(t)
	touch(t, filepath.Join(dir, "forti.conf"))
	touch(t, filepath.Join(dir, "home", "home.conf"))
	touch(t, filepath.Join(dir, "run", "here.conf"))
	tests := []struct {
		file, want string
	}{
		{"", ""},
		{"forti.conf", filepath.Join(dir, "forti.conf")},
		{"~/home.conf", filepath.Join(dir, "home", "home.conf")},
		{"/abs/x.conf", "/abs/x.conf"},
		{"gone.conf", filepath.Join(dir, "gone.conf")},        // missing: where it should be
		{"here.conf", filepath.Join(dir, "run", "here.conf")}, // only where den runs: as before
	}
	for _, tt := range tests {
		if got := (VPNSpec{ConfigFile: tt.file, Dir: dir}).ConfigFilePath(); got != tt.want {
			t.Errorf("ConfigFilePath(%q) = %q, want %q", tt.file, got, tt.want)
		}
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const vpnConfigYAML = `
vpn:
  - name: v
    config_file: forti.conf
`

// An openfortivpn config may hold a password, so one that others can read is
// worth a warning, and a 0600 one is not.
func TestInspectWarnsAboutAVPNConfigOthersCanRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no mode bits to read")
	}
	dir := scaffold(t)
	conf := filepath.Join(dir, "forti.conf")
	touch(t, conf)
	if _, ps := inspectIn(t, dir, vpnConfigYAML); len(ps) != 0 {
		t.Fatalf("a 0600 file must pass: %v", ps)
	}
	if err := os.Chmod(conf, 0o644); err != nil {
		t.Fatal(err)
	}
	_, ps := inspectIn(t, dir, vpnConfigYAML)
	p := onlyProblem(t, ps, "path-permissive")
	if p.Severity != SeverityWarning || p.Key != "vpn[0].config_file" || p.Line != 3 ||
		!strings.Contains(p.Msg, conf) || !strings.Contains(p.Msg, "0644") || !strings.Contains(p.Hint, "chmod 600 "+conf) {
		t.Errorf("problem = %+v", p)
	}
}

// A VPN file in a work tree is one commit from being published, whatever its mode.
func TestInspectWarnsAboutAVPNConfigInAGitWorkTree(t *testing.T) {
	dir := scaffold(t)
	touch(t, filepath.Join(dir, "forti.conf"))
	if _, ps := inspectIn(t, dir, vpnConfigYAML); len(ps) != 0 {
		t.Fatalf("outside a repo: %v", ps)
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	_, ps := inspectIn(t, dir, vpnConfigYAML)
	p := onlyProblem(t, ps, "path-in-repo")
	if !strings.Contains(p.Msg, dir) || !strings.Contains(p.Hint, "git-ignore") {
		t.Errorf("problem = %+v", p)
	}
}

// In a worktree or a submodule .git is a file, not a folder.
func TestGitWorkTreeFindsADotGitFile(t *testing.T) {
	dir := mustEval(t, t.TempDir())
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	if got := gitWorkTree(sub); got != "" {
		t.Fatalf("no .git anywhere: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", ".git"), []byte("gitdir: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := gitWorkTree(sub), filepath.Join(dir, "a"); got != want {
		t.Errorf("gitWorkTree = %q, want %q", got, want)
	}
}
