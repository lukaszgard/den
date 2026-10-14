package platform

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// xdg is an absolute XDG directory on the OS running the tests. Windows reads
// "/xdg" as relative, having no drive, and den ignores a relative XDG path.
var xdg = func() string {
	if runtime.GOOS == "windows" {
		return `C:\xdg`
	}
	return "/xdg"
}()

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	cases := map[string]string{
		"~":            home,
		"~/a/b":        filepath.Join(home, "a/b"),
		"/abs/path":    "/abs/path",
		"rel/path":     "rel/path",
		"~other/x":     "~other/x", // another user's home is not ours to guess
		"":             "",
		"dir/~/inside": "dir/~/inside",
	}
	for in, want := range cases {
		if got := ExpandHome(in); got != want {
			t.Errorf("ExpandHome(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShellCommand(t *testing.T) {
	if name, args := shellCommand("linux", "echo hi"); name != "sh" || !reflect.DeepEqual(args, []string{"-c", "echo hi"}) {
		t.Errorf("linux = %s %v", name, args)
	}
	if name, args := shellCommand("windows", "echo hi"); name != "cmd" || !reflect.DeepEqual(args, []string{"/C", "echo hi"}) {
		t.Errorf("windows = %s %v", name, args)
	}
}

func TestUserShell(t *testing.T) {
	for _, tc := range []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"darwin", map[string]string{"SHELL": "/bin/zsh"}, "/bin/zsh"},
		{"linux", nil, "/bin/sh"},
		// Git Bash sets SHELL to a path only it can resolve.
		{"windows", map[string]string{"SHELL": "/usr/bin/bash", "COMSPEC": `C:\Windows\System32\cmd.exe`}, `C:\Windows\System32\cmd.exe`},
		{"windows", nil, "cmd.exe"},
	} {
		getenv := func(key string) string { return tc.env[key] }
		if got := userShell(tc.goos, getenv); got != tc.want {
			t.Errorf("%s with %v = %q, want %q", tc.goos, tc.env, got, tc.want)
		}
	}
}

func TestOpenCommand(t *testing.T) {
	for goos, want := range map[string]string{"darwin": "open", "linux": "xdg-open", "windows": "rundll32"} {
		name, args, err := openCommand(goos, "https://example.com")
		if err != nil || name != want || args[len(args)-1] != "https://example.com" {
			t.Errorf("%s: %s %v %v", goos, name, args, err)
		}
	}
	if _, _, err := openCommand("plan9", "x"); err == nil {
		t.Error("an unsupported OS must be an error, not a silent no-op")
	}
}

func TestUserEditor(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"darwin", map[string]string{"VISUAL": "code --wait", "EDITOR": "vim"}, "code --wait"},
		{"linux", map[string]string{"EDITOR": "nano"}, "nano"},
		{"linux", map[string]string{"VISUAL": "  "}, "vi"},
		{"windows", nil, "notepad"},
	}
	for _, c := range cases {
		if got := userEditor(c.goos, env(c.env)); got != c.want {
			t.Errorf("%s %v: userEditor = %q, want %q", c.goos, c.env, got, c.want)
		}
	}
}

func TestConfigDir(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	home := func() (string, error) { return "/home/u", nil }
	cases := []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"linux", nil, filepath.Join("/home/u", ".config", "den")},
		{"darwin", map[string]string{"XDG_CONFIG_HOME": xdg}, filepath.Join(xdg, "den")},
		// The XDG spec says to ignore a relative path.
		{"linux", map[string]string{"XDG_CONFIG_HOME": "rel"}, filepath.Join("/home/u", ".config", "den")},
		{"windows", map[string]string{"APPDATA": `C:\Users\u\AppData\Roaming`}, filepath.Join(`C:\Users\u\AppData\Roaming`, "den")},
		// XDG wins on Windows too, as for the state and data directories.
		{"windows", map[string]string{"XDG_CONFIG_HOME": xdg, "APPDATA": `C:\a`}, filepath.Join(xdg, "den")},
		// APPDATA means nothing off Windows.
		{"linux", map[string]string{"APPDATA": "/appdata"}, filepath.Join("/home/u", ".config", "den")},
		{"windows", nil, filepath.Join("/home/u", ".config", "den")},
	}
	for _, c := range cases {
		if got := configDir(c.goos, env(c.env), home); got != c.want {
			t.Errorf("%s %v: configDir = %q, want %q", c.goos, c.env, got, c.want)
		}
	}
	noHome := func() (string, error) { return "", errors.New("no home") }
	if got := configDir("linux", env(nil), noHome); got != "" {
		t.Errorf("without a home: configDir = %q, want empty", got)
	}
}

func TestStateDir(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	home := func() (string, error) { return "/home/u", nil }
	cases := []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"linux", nil, filepath.Join("/home/u", ".local", "state", "den")},
		{"darwin", map[string]string{"XDG_STATE_HOME": xdg}, filepath.Join(xdg, "den")},
		// The XDG spec says to ignore a relative path.
		{"linux", map[string]string{"XDG_STATE_HOME": "rel"}, filepath.Join("/home/u", ".local", "state", "den")},
		{"windows", map[string]string{"LOCALAPPDATA": `C:\Users\u\AppData\Local`}, filepath.Join(`C:\Users\u\AppData\Local`, "den")},
	}
	for _, c := range cases {
		if got := stateDir(c.goos, env(c.env), home); got != c.want {
			t.Errorf("%s %v: stateDir = %q, want %q", c.goos, c.env, got, c.want)
		}
	}
}

func TestDataDir(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	home := func() (string, error) { return "/home/u", nil }
	cases := []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"linux", nil, filepath.Join("/home/u", ".local", "share", "den")},
		{"darwin", map[string]string{"XDG_DATA_HOME": xdg}, filepath.Join(xdg, "den")},
		// The XDG spec says to ignore a relative path.
		{"linux", map[string]string{"XDG_DATA_HOME": "rel"}, filepath.Join("/home/u", ".local", "share", "den")},
		{"windows", map[string]string{"LOCALAPPDATA": `C:\Users\u\AppData\Local`}, filepath.Join(`C:\Users\u\AppData\Local`, "den")},
	}
	for _, c := range cases {
		if got := dataDir(c.goos, env(c.env), home); got != c.want {
			t.Errorf("%s %v: dataDir = %q, want %q", c.goos, c.env, got, c.want)
		}
	}
	noHome := func() (string, error) { return "", errors.New("no home") }
	if got := dataDir("linux", env(nil), noHome); got != "" {
		t.Errorf("without a home: dataDir = %q, want empty", got)
	}
}
