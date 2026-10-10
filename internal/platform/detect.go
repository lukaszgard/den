package platform

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
)

// The operating systems den gives install instructions for.
const (
	MacOS   = "macos"
	Linux   = "linux"
	Windows = "windows"
)

// The Linux families den gives install instructions for. Each shares one
// package manager and one set of package names.
const (
	Debian      = "debian" // Debian, Ubuntu, Mint, …: apt
	Fedora      = "fedora" // Fedora, RHEL, CentOS, Rocky, Alma, Amazon Linux: dnf
	OtherDistro = "other"
)

// Managers are the package managers den knows install commands for, in the
// order it prefers them.
var Managers = []string{"brew", "apt-get", "dnf", "winget", "scoop"}

// Info describes the machine den runs on, as far as installing tools goes.
type Info struct {
	OS       string   // MacOS, Linux, Windows, or GOOS for anything else
	Distro   string   // on Linux: Debian, Fedora or OtherDistro
	Arch     string   // GOARCH: amd64, arm64, …
	Managers []string // package managers found on PATH, from Managers
}

// Has reports whether manager is available.
func (i Info) Has(manager string) bool { return slices.Contains(i.Managers, manager) }

// Label names the platform for people: "macOS", "Debian/Ubuntu", ….
func (i Info) Label() string {
	switch {
	case i.OS == MacOS:
		return "macOS"
	case i.OS == Windows:
		return "Windows"
	case i.Distro == Debian:
		return "Debian/Ubuntu"
	case i.Distro == Fedora:
		return "Fedora/RHEL"
	case i.OS == Linux:
		return "Linux"
	}
	return i.OS
}

// Detect inspects this machine.
func Detect() Info {
	var release string
	if runtime.GOOS == "linux" {
		for _, p := range []string{"/etc/os-release", "/usr/lib/os-release"} {
			if b, err := os.ReadFile(p); err == nil {
				release = string(b)
				break
			}
		}
	}
	return DetectFrom(runtime.GOOS, runtime.GOARCH, release, exec.LookPath)
}

// DetectFrom is Detect with its inputs supplied: GOOS and GOARCH, the text of
// /etc/os-release (Linux only) and the PATH lookup.
func DetectFrom(goos, goarch, osRelease string, lookPath func(string) (string, error)) Info {
	info := Info{OS: goos, Arch: goarch}
	switch goos {
	case "darwin":
		info.OS = MacOS
	case "linux":
		info.Distro = distroFamily(osRelease)
	}
	for _, m := range Managers {
		if _, err := lookPath(m); err == nil {
			info.Managers = append(info.Managers, m)
		}
	}
	return info
}

// ForOS is the platform named on the command line (macos, debian, fedora,
// windows, optionally with /amd64 or /arm64), with the package manager it
// usually has, for previewing its install instructions from another machine.
// Without an architecture, this machine's is assumed.
func ForOS(name string) (Info, error) {
	arch := runtime.GOARCH
	if n, a, ok := strings.Cut(name, "/"); ok {
		if a != "amd64" && a != "arm64" {
			return Info{}, fmt.Errorf("unknown architecture %q (want amd64 or arm64)", a)
		}
		name, arch = n, a
	}
	switch strings.ToLower(name) {
	case MacOS, "darwin", "mac":
		return Info{OS: MacOS, Arch: arch, Managers: []string{"brew"}}, nil
	case Debian, "ubuntu":
		return Info{OS: Linux, Distro: Debian, Arch: arch, Managers: []string{"apt-get"}}, nil
	case Fedora, "rhel":
		return Info{OS: Linux, Distro: Fedora, Arch: arch, Managers: []string{"dnf"}}, nil
	case Windows:
		return Info{OS: Windows, Arch: arch, Managers: []string{"winget"}}, nil
	}
	return Info{}, fmt.Errorf("unknown platform %q (want macos, debian, fedora or windows)", name)
}

// distroFamily reads ID and ID_LIKE from os-release. ID_LIKE lets
// derivatives nobody listed (Pop!_OS, Amazon Linux) land in their family.
func distroFamily(osRelease string) string {
	var ids []string
	sc := bufio.NewScanner(strings.NewReader(osRelease))
	for sc.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || (key != "ID" && key != "ID_LIKE") {
			continue
		}
		ids = append(ids, strings.Fields(strings.ToLower(strings.Trim(value, `"'`)))...)
	}
	for _, id := range ids {
		switch id {
		case "debian", "ubuntu":
			return Debian
		case "fedora", "rhel", "centos", "rocky", "almalinux":
			return Fedora
		}
	}
	return OtherDistro
}
