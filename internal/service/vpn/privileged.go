package vpn

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// supportedOS returns nil if the current platform supports VPN, otherwise an
// actionable error. Linux additionally requires pkexec to be installed.
func supportedOS() error {
	switch runtime.GOOS {
	case "darwin":
		return nil
	case "linux":
		if _, err := exec.LookPath("pkexec"); err != nil {
			return fmt.Errorf("pkexec not found — install policykit (apt install policykit-1 / dnf install polkit)")
		}
		return nil
	default:
		return fmt.Errorf("VPN service not supported on %s", runtime.GOOS)
	}
}

// darwinStartScript builds the AppleScript needed to launch openfortivpn in
// the background as root, redirecting all output to logPath. osascript returns
// as soon as the user approves the password dialog; the openfortivpn process
// keeps running, reparented to launchd.
func darwinStartScript(gateway, configFile string, extraArgs []string, logPath string) string {
	cmdArgs := []string{"openfortivpn"}
	if gateway != "" {
		cmdArgs = append(cmdArgs, gateway)
	}
	cmdArgs = append(cmdArgs, "--saml-login")
	if configFile != "" {
		cmdArgs = append(cmdArgs, "-c", configFile)
	}
	cmdArgs = append(cmdArgs, extraArgs...)
	inner := fmt.Sprintf("%s < /dev/null > %s 2>&1 &", shellJoin(cmdArgs), shellQuote(logPath))
	return fmt.Sprintf(`do shell script %q with administrator privileges`, inner)
}

// linuxStartCommand returns the pkexec invocation to run openfortivpn as root
// on Linux. pkexec streams stdout/stderr in real time, so no log-file dance is
// needed and the existing supervisor path can consume it directly.
func linuxStartCommand(gateway, configFile string, extraArgs []string) (string, []string) {
	args := []string{"openfortivpn"}
	if gateway != "" {
		args = append(args, gateway)
	}
	args = append(args, "--saml-login")
	if configFile != "" {
		args = append(args, "-c", configFile)
	}
	args = append(args, extraArgs...)
	return "pkexec", args
}

// helperCommand returns the sudo invocation for one privilege-helper action.
// `-n` is what keeps this promise-free: without the sudoers rule sudo fails
// immediately instead of prompting, and den falls back to the admin dialog.
func helperCommand(action, slug string) (string, []string) {
	return "sudo", []string{"-n", HelperPath, action, slug}
}

// stopCommand returns the OS-appropriate command to terminate the running
// openfortivpn process. On macOS a second osascript prompt appears.
// When gateway is set it matches on the full command pattern; otherwise it
// falls back to process-name match (-x) which is safe when only one instance runs.
// reapOrphanedPPPD kills pppd processes left behind by an openfortivpn that was
// killed rather than shut down cleanly. openfortivpn normally terminates its
// pppd child itself; when it cannot, pppd reparents to init and keeps its ppp
// interface up, holding a VPN-assigned address indefinitely. Five such
// interfaces had accumulated here before this existed.
//
// The match is deliberately narrow: openfortivpn's fixed ppp peer address, and
// a parent of init. A pppd belonging to a running openfortivpn has that process
// as its parent, so a live tunnel — or another VPN's pppd — is never touched.
const reapOrphanedPPPD = `for p in $(pgrep -f ':169.254.2.1 noipdefault' 2>/dev/null); do ` +
	`[ "$(ps -o comm= -p $p 2>/dev/null | sed 's|.*/||')" = pppd ] || continue; ` +
	`if [ "$(ps -o ppid= -p $p 2>/dev/null | tr -d ' ')" = "1" ]; then kill -TERM $p 2>/dev/null; fi; done`

func stopCommand(gateway, configFile string) (string, []string, error) {
	var killArgs []string
	if gateway != "" {
		killArgs = []string{"-TERM", "-f", "openfortivpn " + gateway}
	} else {
		killArgs = []string{"-TERM", "-x", "openfortivpn"}
	}
	switch runtime.GOOS {
	case "darwin":
		// Reap after the pkill so openfortivpn gets its chance to stop pppd
		// itself; only what it leaves behind is swept.
		inner := "pkill " + strings.Join(killArgs, " ") + "; sleep 1; " + reapOrphanedPPPD
		return "osascript", []string{"-e", fmt.Sprintf(`do shell script %q with administrator privileges`, inner)}, nil
	case "linux":
		inner := "pkill " + strings.Join(killArgs, " ") + "; sleep 1; " + reapOrphanedPPPD
		return "pkexec", []string{"sh", "-c", inner}, nil
	default:
		return "", nil, fmt.Errorf("VPN stop not supported on %s", runtime.GOOS)
	}
}

// shellJoin joins arguments into a single shell-safe string for embedding in
// an AppleScript `do shell script` literal.
func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

// shellQuote single-quotes s for use in a /bin/sh command line.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
