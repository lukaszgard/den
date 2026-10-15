package vpn

import (
	"reflect"
	"testing"
	"time"
)

// psPrints is the body of a fake ps that prints lines as
// `ps -A -o pid=,ppid=,etime=,args=` does.
func psPrints(lines ...string) string {
	body := "printf '%s\\n'"
	for _, l := range lines {
		body += " " + shellQuote(l)
	}
	return body
}

// pgrep -f cannot see the arguments of a root pppd on macOS, which is how a
// pppd 3 days old went unnoticed while every connect started another
// openfortivpn beside it. ps can: both the hung openfortivpn and the orphaned
// pppd must be found, and nothing that merely looks like them.
func TestLeftoverProcs(t *testing.T) {
	fakePATH(t, map[string]string{"ps": psPrints(
		"28980     1    13:05:09 /opt/homebrew/bin/openfortivpn -c /opt/den/etc/office-vpn.conf",
		// openfortivpn's own pppd: it goes when its parent does.
		"28990 28980       04:10 /usr/sbin/pppd 230400 :169.254.2.1 noipdefault ipcp-accept-local",
		"29035     1 03-13:40:22 /usr/sbin/pppd 230400 :169.254.2.1 noipdefault ipcp-accept-local noaccomp",
		// Another VPN's pppd, orphaned or not, has another peer.
		"30000     1    01:00:00 /usr/sbin/pppd /dev/ttys003 115200 call other-vpn",
		"31000   500       00:07 vim openfortivpn.conf",
	)})

	got := leftoverProcs()
	want := []leftover{
		{name: "openfortivpn", pid: "28980", age: 13*time.Hour + 5*time.Minute + 9*time.Second},
		{name: "pppd", pid: "29035", age: 3*24*time.Hour + 13*time.Hour + 40*time.Minute + 22*time.Second},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("leftoverProcs() = %+v\nwant %+v", got, want)
	}
}

func TestParseEtime(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"00:07":       7 * time.Second,
		"04:10":       4*time.Minute + 10*time.Second,
		"13:05:09":    13*time.Hour + 5*time.Minute + 9*time.Second,
		"03-13:40:22": 3*24*time.Hour + 13*time.Hour + 40*time.Minute + 22*time.Second,
	} {
		if got, ok := parseEtime(in); !ok || got != want {
			t.Errorf("parseEtime(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "7", "a:b", "1:2:3:4", "x-01:02:03"} {
		if _, ok := parseEtime(bad); ok {
			t.Errorf("parseEtime(%q) reads as a time", bad)
		}
	}
}

// The helper's stop is only offered with the helper: without its sudoers rule
// the command is not one the user can run as written.
func TestManualCommands(t *testing.T) {
	pids := []string{"28980", "29035"}
	unixKill := "sudo kill 28980 29035; sleep 5; sudo kill -9 28980 29035 2>/dev/null"
	for _, c := range []struct {
		goos       string
		helper     bool
		kill, stop string
	}{
		{"darwin", true, unixKill, "sudo /opt/den/libexec/den-vpn stop office-vpn"},
		{"darwin", false, unixKill, ""},
		{"linux", true, unixKill, "sudo /opt/den/libexec/den-vpn stop office-vpn"},
		{"linux", false, unixKill, ""},
		{"windows", true, "taskkill /F /PID 28980 /PID 29035", ""},
		{"windows", false, "taskkill /F /PID 28980 /PID 29035", ""},
	} {
		kill, stop := manualCommands(c.goos, "office-vpn", pids, c.helper)
		if kill != c.kill || stop != c.stop {
			t.Errorf("manualCommands(%s, helper=%v) =\n  %q\n  %q\nwant\n  %q\n  %q", c.goos, c.helper, kill, stop, c.kill, c.stop)
		}
	}
}
