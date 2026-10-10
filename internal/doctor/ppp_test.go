package doctor

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
)

// A colleague on WSL 3.0.1 finished the SAML login and then lost the tunnel to
// "Couldn't open the /dev/ppp device": that kernel has no PPP at all. Doctor
// has to say so before a connect does, with the fix for that kind of machine.
func TestCheckPPP(t *testing.T) {
	const (
		wsl     = "6.18.40.1-microsoft-standard-WSL2"
		devices = "Character devices:\n  1 mem\n  4 tty\n\nBlock devices:\n  7 loop\n"
	)
	cases := []struct {
		name          string
		files         map[string]string
		status        Status
		detail, fixed string
	}{
		{"driver loaded", map[string]string{
			"/proc/sys/kernel/osrelease": "6.18.33.2-microsoft-standard-WSL2\n",
			"/proc/devices":              "Character devices:\n  1 mem\n108 ppp\n\nBlock devices:\n  7 loop\n",
		}, OK, "PPP driver loaded", ""},
		{"module on disk", map[string]string{
			"/proc/sys/kernel/osrelease":                "6.8.0-45-generic\n",
			"/proc/devices":                             devices,
			"/lib/modules/6.8.0-45-generic/modules.dep": "kernel/drivers/net/ppp/ppp_generic.ko.zst: kernel/lib/crc-ccitt.ko.zst\n",
		}, OK, "loaded on first use", ""},
		{"built in", map[string]string{
			"/proc/sys/kernel/osrelease":                   "5.15.0-1-custom\n",
			"/proc/devices":                                devices,
			"/lib/modules/5.15.0-1-custom/modules.builtin": "kernel/drivers/net/ppp/ppp_generic.ko\n",
		}, OK, "built into kernel", ""},
		{"WSL 3.0.1", map[string]string{
			"/proc/sys/kernel/osrelease":               wsl + "\n",
			"/proc/devices":                            devices,
			"/lib/modules/" + wsl + "/modules.builtin": "kernel/drivers/net/tun.ko\n",
		}, Warn, "WSL kernel " + wsl + " is built without PPP", "roll WSL back to 2.7.14"},
		{"modules gone since boot", map[string]string{
			"/proc/sys/kernel/osrelease": "5.4.0-150-generic\n",
			"/proc/devices":              devices,
		}, Warn, "no modules in /lib/modules/5.4.0-150-generic", "reboot"},
		{"kernel without ppp", map[string]string{
			"/proc/sys/kernel/osrelease":                 "5.4.0-150-generic\n",
			"/proc/devices":                              devices,
			"/lib/modules/5.4.0-150-generic/modules.dep": "kernel/drivers/net/tun.ko:\n",
		}, Warn, "kernel 5.4.0-150-generic has no PPP driver", "sudo modprobe ppp_generic"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := checkPPP(kernel(c.files), Warn)
			if r.Status != c.status || !strings.Contains(r.Detail, c.detail) || !strings.Contains(r.Fix, c.fixed) {
				t.Errorf("got %s %q, fix %q; want %s %q, fix containing %q", r.Status, r.Detail, r.Fix, c.status, c.detail, c.fixed)
			}
		})
	}
}

// The check runs wherever openfortivpn is wanted on Linux: for a vpn entry in
// den.yaml, and for `den doctor --for vpn`, where a problem fails the run.
func TestPPPCheckRunsForTheVPNOnLinux(t *testing.T) {
	wsl := map[string]string{"/proc/sys/kernel/osrelease": "6.18.40.1-microsoft-standard-WSL2\n"}
	cfg := awsHome(t, "", "")
	cfg.VPNs = []config.VPNSpec{{Name: "office", Gateway: "vpn.example.com"}}
	in := Input{ConfigPath: "den.yaml", Config: cfg}

	if got := find(t, Run(context.Background(), in, kernel(wsl)), pppCheckName); got.Status != Warn {
		t.Errorf("a vpn entry on a kernel without PPP: %+v", got)
	}
	forVPN := Run(context.Background(), Input{For: []string{"openfortivpn"}}, kernel(wsl))
	if got := find(t, forVPN, pppCheckName); got.Status != Fail || !Failed(forVPN) {
		t.Errorf("den doctor --for vpn must fail without PPP: %+v", got)
	}

	cfg.VPNs = nil
	for _, r := range Run(context.Background(), in, kernel(wsl)) {
		if r.Check == pppCheckName {
			t.Errorf("no vpn entry, no PPP check: %+v", r)
		}
	}
	mac := fakeMachine(macWithBrew, nil, nil, nil)
	for _, r := range Run(context.Background(), Input{For: []string{"openfortivpn"}}, mac) {
		if r.Check == pppCheckName {
			t.Errorf("macOS has PPP in its kernel; nothing to check: %+v", r)
		}
	}
}

// kernel is an Ubuntu machine whose /proc and /lib/modules hold files.
func kernel(files map[string]string) Deps {
	d := fakeMachine(platform.Info{OS: platform.Linux, Distro: platform.Debian, Arch: "amd64", Managers: []string{"apt-get"}}, nil, nil, nil)
	d.ReadFile = func(name string) ([]byte, error) {
		if s, ok := files[name]; ok {
			return []byte(s), nil
		}
		return nil, os.ErrNotExist
	}
	return d
}
