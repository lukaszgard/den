package vpn

import (
	"fmt"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"
)

// pppdMatch is in the arguments of every pppd openfortivpn starts: its fixed
// ppp peer address. Another VPN's pppd has a peer of its own.
const pppdMatch = ":169.254.2.1 noipdefault"

// leftover is a process from an earlier session that keeps a connect from
// coming up: an openfortivpn den could not take over or stop, or the pppd one
// left behind when it was killed.
type leftover struct {
	name, pid string
	age       time.Duration
}

// leftoverProcs lists the openfortivpn processes and orphaned pppds on this
// machine, whoever started them. Either one holds ppp0 and the VPN's DNS, so a
// new openfortivpn beside it cannot come up: its pppd would get ppp1 while den
// watches ppp0.
//
// It reads ps rather than pgrep -f: as the user, macOS hides a root process's
// arguments from pgrep, and only its arguments tell openfortivpn's pppd apart.
// ps is setuid root there and shows them. -A rather than -e, which means
// "environment" to macOS's ps.
func leftoverProcs() []leftover {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,etime=,args=").Output()
	if err != nil {
		return nil
	}
	return parseLeftovers(string(out))
}

// parseLeftovers picks the leftovers out of ps's pid, ppid, etime and args.
func parseLeftovers(out string) []leftover {
	var found []leftover
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, ppid, etime, args := f[0], f[1], f[2], f[3:]
		name := path.Base(args[0])
		switch name {
		case "openfortivpn":
		case "pppd":
			// The reap's match. A pppd whose openfortivpn still runs has it as
			// its parent, and goes when it does.
			if ppid != "1" || !strings.Contains(strings.Join(args, " "), pppdMatch) {
				continue
			}
		default:
			continue
		}
		age, _ := parseEtime(etime)
		found = append(found, leftover{name: name, pid: pid, age: age})
	}
	return found
}

// parseEtime reads ps's elapsed time, "[[dd-]hh:]mm:ss".
func parseEtime(s string) (time.Duration, bool) {
	var days int
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 {
			return 0, false
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	secs := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0, false
		}
		secs = secs*60 + n
	}
	return time.Duration(days)*24*time.Hour + time.Duration(secs)*time.Second, true
}

// describeLeftovers names procs for a message: "openfortivpn (pid 28980) is
// still running", or, for several, "a VPN from an earlier session is still
// running: openfortivpn (pid 28980), pppd (pid 29035)".
func describeLeftovers(procs []leftover) string {
	names := make([]string, len(procs))
	for i, p := range procs {
		names[i] = fmt.Sprintf("%s (pid %s)", p.name, p.pid)
	}
	if len(names) == 1 {
		return names[0] + " is still running"
	}
	return "a VPN from an earlier session is still running: " + strings.Join(names, ", ")
}

func leftoverPIDs(procs []leftover) []string {
	pids := make([]string, len(procs))
	for i, p := range procs {
		pids[i] = p.pid
	}
	return pids
}

// manualCommands returns the commands a person runs on goos to end the
// processes pids: kill by pid, and stop, den's own stop through the privilege
// helper. stop is "" without the helper, since its sudoers rule is what lets
// the helper run as root, and on Windows, where there is none.
func manualCommands(goos, slug string, pids []string, helper bool) (kill, stop string) {
	if goos == "windows" {
		// From a terminal run as Administrator. /F, since a console process
		// has no window to be asked to close.
		kill = "taskkill /F"
		for _, p := range pids {
			kill += " /PID " + p
		}
		return kill, ""
	}
	// TERM first, so openfortivpn gets its chance to log out of the gateway;
	// then KILL, which is what a hung one or its pppd needs.
	list := strings.Join(pids, " ")
	kill = "sudo kill " + list + "; sleep 5; sudo kill -9 " + list + " 2>/dev/null"
	if helper {
		stop = "sudo " + HelperPath + " stop " + slug
	}
	return kill, stop
}
