// Package platform holds the small OS-dependent helpers den needs in many
// places: expanding ~ in configured paths, opening a URL in the browser,
// running a configured command line through the system shell, naming the
// shell and the editor a user types into, and where den keeps its state.
package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ExpandHome replaces a leading "~" or "~/" with the user's home directory.
// Any other path, or any path when the home directory is unknown, is returned
// unchanged.
func ExpandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

// TildePath shortens a path in the home directory to ~/…, the way people
// read their own paths.
func TildePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

// OpenURL opens url in the user's default browser without waiting for it.
func OpenURL(url string) error {
	name, args, err := openCommand(runtime.GOOS, url)
	if err != nil {
		return err
	}
	return exec.Command(name, args...).Start()
}

func openCommand(goos, url string) (string, []string, error) {
	switch goos {
	case "darwin":
		return "open", []string{url}, nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}, nil
	case "linux", "freebsd", "openbsd", "netbsd":
		return "xdg-open", []string{url}, nil
	}
	return "", nil, fmt.Errorf("cannot open a browser on %s", goos)
}

// ShellCommand returns the program and arguments that run command line cmd
// through the system shell: sh -c everywhere but Windows, cmd /C there.
// Commands in den.yaml (runbooks, tunnels, login helpers) are written for that
// shell, so den passes them through verbatim.
func ShellCommand(cmd string) (string, []string) {
	return shellCommand(runtime.GOOS, cmd)
}

func shellCommand(goos, cmd string) (string, []string) {
	if goos == "windows" {
		return "cmd", []string{"/C", cmd}
	}
	return "sh", []string{"-c", cmd}
}

// UserShell is the interactive shell to give a user who asked for one: the
// login shell everywhere but Windows, the command interpreter there.
func UserShell() string {
	return userShell(runtime.GOOS, os.Getenv)
}

func userShell(goos string, getenv func(string) string) string {
	if goos == "windows" {
		// SHELL is ignored here: Git Bash sets it to a path only it resolves.
		if comspec := getenv("COMSPEC"); comspec != "" {
			return comspec
		}
		return "cmd.exe"
	}
	if shell := getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

// UserEditor is the editor to open a file in for a user who asked for one:
// $VISUAL, then $EDITOR, as Unix tools have long agreed, then vi, or notepad
// on Windows. It may carry arguments ("code --wait").
func UserEditor() string {
	return userEditor(runtime.GOOS, os.Getenv)
}

func userEditor(goos string, getenv func(string) string) string {
	for _, v := range []string{"VISUAL", "EDITOR"} {
		if e := strings.TrimSpace(getenv(v)); e != "" {
			return e
		}
	}
	if goos == "windows" {
		return "notepad"
	}
	return "vi"
}

// ConfigDir is where a person's own den files live: den.yaml, VPN settings,
// the runbooks they write. $XDG_CONFIG_HOME/den when it is set, on any OS,
// else %APPDATA%\den on Windows and ~/.config/den elsewhere. Empty when no
// home is known.
func ConfigDir() string {
	return configDir(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func configDir(goos string, getenv func(string) string, home func() (string, error)) string {
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" && filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "den")
	}
	if goos == "windows" {
		if appData := getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "den")
		}
	}
	return LegacyConfigDir(home)
}

// LegacyConfigDir is ~/.config/den, where den looked on every OS before it
// honoured XDG_CONFIG_HOME and APPDATA. Discovery still tries it, so a setup
// made then keeps loading. Empty when no home is known.
func LegacyConfigDir(home func() (string, error)) string {
	h, err := home()
	if err != nil || h == "" {
		return ""
	}
	return filepath.Join(h, ".config", "den")
}

// StateDir is where den keeps what it writes for itself and must survive a
// restart, such as runbook run history: $XDG_STATE_HOME/den when it is set, on
// any OS, else %LOCALAPPDATA%\den on Windows and ~/.local/state/den elsewhere.
// Empty when no home is known.
func StateDir() string {
	return stateDir(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func stateDir(goos string, getenv func(string) string, home func() (string, error)) string {
	if xdg := getenv("XDG_STATE_HOME"); xdg != "" && filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "den")
	}
	if goos == "windows" {
		if local := getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "den")
		}
	}
	h, err := home()
	if err != nil || h == "" {
		return ""
	}
	return filepath.Join(h, ".local", "state", "den")
}

// DataDir is where den keeps data it could fetch again but should not have
// to, such as checked-out runbook sources: $XDG_DATA_HOME/den when it is set,
// on any OS, else %LOCALAPPDATA%\den on Windows and ~/.local/share/den
// elsewhere. Empty when no home is known.
func DataDir() string {
	return dataDir(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func dataDir(goos string, getenv func(string) string, home func() (string, error)) string {
	if xdg := getenv("XDG_DATA_HOME"); xdg != "" && filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "den")
	}
	if goos == "windows" {
		if local := getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "den")
		}
	}
	h, err := home()
	if err != nil || h == "" {
		return ""
	}
	return filepath.Join(h, ".local", "share", "den")
}
