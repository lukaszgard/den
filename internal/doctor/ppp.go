package doctor

import (
	"strings"

	"github.com/lukaszgard/den/internal/platform"
)

// WSL 3.0.1 ships kernel 6.18.40.1 without PPP: no driver and no module.
// Rolling back to 2.7.14 (kernel 6.18.33.2) is the only fix so far.
const (
	wslPPPIssue    = "https://github.com/microsoft/WSL/issues/41750"
	wslWithPPP     = "https://github.com/microsoft/WSL/releases/tag/2.7.14"
	pppCheckName   = "kernel PPP"
	pppModuleEntry = "/ppp_generic.ko" // .ko, .ko.zst or .ko.xz in modules.builtin/dep
)

// checkPPP reports whether this Linux kernel can carry the PPP link that pppd
// sets up under openfortivpn. Without it the SAML login succeeds and the
// tunnel then dies with "Couldn't open the /dev/ppp device", which names
// neither the kernel nor the fix. It only reads files.
func checkPPP(d Deps, missing Status) Result {
	r := Result{Check: pppCheckName}
	osrelease, _ := d.ReadFile("/proc/sys/kernel/osrelease")
	release := strings.TrimSpace(string(osrelease))

	if devices, err := d.ReadFile("/proc/devices"); err == nil && hasPPPDriver(string(devices)) {
		r.Status, r.Detail = OK, "PPP driver loaded (kernel "+release+")"
		return r
	}
	modules := "/lib/modules/" + release
	builtin, builtinErr := d.ReadFile(modules + "/modules.builtin")
	dep, depErr := d.ReadFile(modules + "/modules.dep")
	switch {
	case strings.Contains(string(builtin), pppModuleEntry):
		r.Status, r.Detail = OK, "PPP built into kernel "+release
		return r
	case strings.Contains(string(dep), pppModuleEntry):
		r.Status, r.Detail = OK, "ppp_generic module for kernel "+release+", loaded on first use"
		return r
	}

	r.Status = missing
	switch {
	case strings.Contains(strings.ToLower(release), "microsoft"):
		r.Detail = "WSL kernel " + release + " is built without PPP, which openfortivpn's pppd needs (WSL 3.0.1 dropped it: " + wslPPPIssue + ")"
		r.Fix = "roll WSL back to 2.7.14, whose kernel has PPP: " + wslWithPPP + " — or connect with FortiClient on Windows"
	case release != "" && builtinErr != nil && depErr != nil:
		r.Detail = "no modules in " + modules + " for the running kernel: it was upgraded or removed since boot"
		r.Fix = "reboot into the installed kernel"
	default:
		r.Detail = "kernel " + release + " has no PPP driver (ppp_generic), which openfortivpn's pppd needs"
		r.Fix = "sudo modprobe ppp_generic, or boot a kernel built with CONFIG_PPP"
	}
	return r
}

// hasPPPDriver reports whether /proc/devices lists the ppp character driver,
// which is there when PPP is built in or its module is loaded.
func hasPPPDriver(devices string) bool {
	for line := range strings.SplitSeq(devices, "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == "ppp" {
			return true
		}
	}
	return false
}

// needsPPPCheck reports whether checkPPP applies: openfortivpn is wanted and
// this is Linux, where den runs pppd itself. macOS has PPP in its kernel.
func needsPPPCheck(d Deps, wanted bool) bool {
	return wanted && d.Tools.Platform.OS == platform.Linux
}
