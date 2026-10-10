package platform

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestDistroFamily(t *testing.T) {
	cases := map[string]string{
		"ubuntu":    "NAME=\"Ubuntu\"\nID=ubuntu\nID_LIKE=debian\nVERSION_ID=\"24.04\"\n",
		"debian":    "PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nID=debian\n",
		"mint":      "NAME=\"Linux Mint\"\nID=linuxmint\nID_LIKE=\"ubuntu debian\"\n",
		"fedora":    "NAME=\"Fedora Linux\"\nID=fedora\n",
		"rhel":      "NAME=\"Red Hat Enterprise Linux\"\nID=\"rhel\"\nID_LIKE=\"fedora\"\n",
		"rocky":     "NAME=\"Rocky Linux\"\nID=\"rocky\"\nID_LIKE=\"rhel centos fedora\"\n",
		"amazon":    "NAME=\"Amazon Linux\"\nID=\"amzn\"\nID_LIKE=\"fedora\"\n",
		"arch":      "NAME=\"Arch Linux\"\nID=arch\n",
		"alpine":    "NAME=\"Alpine Linux\"\nID=alpine\n",
		"no file":   "",
		"malformed": "this is not os-release\n",
	}
	want := map[string]string{
		"ubuntu": Debian, "debian": Debian, "mint": Debian,
		"fedora": Fedora, "rhel": Fedora, "rocky": Fedora, "amazon": Fedora,
		"arch": OtherDistro, "alpine": OtherDistro, "no file": OtherDistro, "malformed": OtherDistro,
	}
	for name, release := range cases {
		if got := distroFamily(release); got != want[name] {
			t.Errorf("%s: family = %q, want %q", name, got, want[name])
		}
	}
}

func TestDetectFrom(t *testing.T) {
	onPath := func(names ...string) func(string) (string, error) {
		return func(file string) (string, error) {
			if slices.Contains(names, file) {
				return "/usr/bin/" + file, nil
			}
			return "", errors.New("not found")
		}
	}

	mac := DetectFrom("darwin", "arm64", "", onPath("brew"))
	if mac.OS != MacOS || mac.Distro != "" || !mac.Has("brew") || mac.Label() != "macOS" {
		t.Errorf("mac = %+v (%s)", mac, mac.Label())
	}

	// Linuxbrew counts, and managers keep den's order of preference.
	ubuntu := DetectFrom("linux", "amd64", "ID=ubuntu\n", onPath("apt-get", "brew"))
	if ubuntu.OS != Linux || ubuntu.Distro != Debian || !reflect.DeepEqual(ubuntu.Managers, []string{"brew", "apt-get"}) {
		t.Errorf("ubuntu = %+v", ubuntu)
	}
	if ubuntu.Label() != "Debian/Ubuntu" {
		t.Errorf("ubuntu label = %q", ubuntu.Label())
	}

	win := DetectFrom("windows", "amd64", "", onPath("scoop"))
	if win.OS != Windows || win.Has("winget") || !win.Has("scoop") || win.Label() != "Windows" {
		t.Errorf("windows = %+v", win)
	}

	arch := DetectFrom("linux", "amd64", "ID=arch\n", onPath())
	if arch.Label() != "Linux" || len(arch.Managers) != 0 {
		t.Errorf("arch = %+v (%s)", arch, arch.Label())
	}
}

func TestForOS(t *testing.T) {
	for name, want := range map[string]string{
		"macos": "macOS", "darwin": "macOS", "debian": "Debian/Ubuntu", "Ubuntu": "Debian/Ubuntu",
		"fedora": "Fedora/RHEL", "rhel": "Fedora/RHEL", "windows": "Windows",
	} {
		info, err := ForOS(name)
		if err != nil || info.Label() != want || len(info.Managers) != 1 {
			t.Errorf("ForOS(%q) = %+v, %v; want %s with its package manager", name, info, err, want)
		}
	}
	if _, err := ForOS("plan9"); err == nil {
		t.Error("an unknown platform must be an error")
	}
	if info, err := ForOS("debian/amd64"); err != nil || info.Arch != "amd64" || info.Distro != Debian {
		t.Errorf("debian/amd64 = %+v, %v", info, err)
	}
	if _, err := ForOS("debian/sparc"); err == nil {
		t.Error("an unknown architecture must be an error")
	}
}
