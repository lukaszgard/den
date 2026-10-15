package vpn

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
)

func TestSlug(t *testing.T) {
	cases := []struct{ name, want string }{
		{"Office VPN", "office-vpn"},
		{"ACME  Corp / VPN!", "acme-corp-vpn"},
		{"  spaced  ", "spaced"},
		{"vpn2", "vpn2"},
		{"a very long vpn name that goes on and on and on", "a-very-long-vpn-name-that-goes-o"}, // capped at 32
		{"!!!", ""},
	}
	for _, c := range cases {
		if got := Slug(c.name); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestValidSlugRejectsAnythingThatCouldEscapeTheSudoersRule(t *testing.T) {
	for _, bad := range []string{"", "-lead", "has space", "UPPER", "semi;colon", "--flag", "../../etc/passwd", "slash/es", strings.Repeat("x", 33)} {
		if err := ValidSlug(bad); err == nil {
			t.Errorf("ValidSlug(%q) = nil, want an error", bad)
		}
	}
	for _, ok := range []string{"office-vpn", "v", "a1", strings.Repeat("x", 32)} {
		if err := ValidSlug(ok); err != nil {
			t.Errorf("ValidSlug(%q) = %v, want nil", ok, err)
		}
	}
}

func writeConfigFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vpn.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// settingsOf parses a rendered config back into a map for assertions.
func settingsOf(t *testing.T, rendered string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for line := range strings.SplitSeq(rendered, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("rendered config has an unparsable line: %q", line)
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

func TestRenderConfigFromConfigFile(t *testing.T) {
	path := writeConfigFile(t, "host = vpn.example.com\nport = 443\nusername = jane.doe@example.com\nset-dns = 1\nsaml-login = 8020\npersistent = 5\n")

	rendered, err := RenderConfig(&config.VPNSpec{Name: "Office VPN", ConfigFile: path})
	if err != nil {
		t.Fatal(err)
	}
	got := settingsOf(t, rendered)
	want := map[string]string{
		"host": "vpn.example.com", "port": "443", "username": "jane.doe@example.com",
		"set-dns": "1", "saml-login": "8020", "persistent": "5",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestRenderConfigGatewayAndDefaults(t *testing.T) {
	rendered, err := RenderConfig(&config.VPNSpec{Name: "Office VPN", Gateway: "vpn.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	got := settingsOf(t, rendered)
	if got["host"] != "vpn.example.com" || got["port"] != "443" {
		t.Errorf("host/port = %q/%q", got["host"], got["port"])
	}
	// den has always connected with --saml-login; the rendered config must keep that.
	if got["saml-login"] != "8020" {
		t.Errorf("saml-login = %q, want the default 8020", got["saml-login"])
	}

	rendered, err = RenderConfig(&config.VPNSpec{Name: "x", Gateway: "vpn.example.com:10443"})
	if err != nil {
		t.Fatal(err)
	}
	if got := settingsOf(t, rendered); got["port"] != "10443" {
		t.Errorf("port = %q, want 10443 from the gateway", got["port"])
	}
}

func TestRenderConfigGatewayWinsOverConfigFile(t *testing.T) {
	path := writeConfigFile(t, "host = old.example.com\nport = 443\nusername = someone\n")
	rendered, err := RenderConfig(&config.VPNSpec{Name: "x", Gateway: "new.example.com", ConfigFile: path})
	if err != nil {
		t.Fatal(err)
	}
	got := settingsOf(t, rendered)
	if got["host"] != "new.example.com" {
		t.Errorf("host = %q, want the gateway to win", got["host"])
	}
	if got["username"] != "someone" {
		t.Errorf("username = %q, want the config file's value to survive", got["username"])
	}
}

func TestRenderConfigExtraArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want map[string]string
	}{
		{"key=value", []string{"--trusted-cert=abc123"}, map[string]string{"trusted-cert": "abc123"}},
		{"separate value", []string{"--trusted-cert", "abc123"}, map[string]string{"trusted-cert": "abc123"}},
		{"bare flag", []string{"--persistent=5", "--insecure-ssl"}, map[string]string{"persistent": "5", "insecure-ssl": "1"}},
		{"negative spellings", []string{"--no-dns", "--pppd-no-peerdns"}, map[string]string{"set-dns": "0", "pppd-use-peerdns": "0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rendered, err := RenderConfig(&config.VPNSpec{Name: "x", Gateway: "vpn.example.com", ExtraArgs: c.args})
			if err != nil {
				t.Fatal(err)
			}
			got := settingsOf(t, rendered)
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// The rendered config is run as root without a password, so settings that turn
// it into arbitrary root code execution must never be installed.
func TestRenderConfigRejectsRootEscalatingSettings(t *testing.T) {
	for _, key := range []string{"pppd-plugin", "pppd-call", "pppd-log"} {
		t.Run("config file "+key, func(t *testing.T) {
			path := writeConfigFile(t, "host = vpn.example.com\n"+key+" = /tmp/evil\n")
			_, err := RenderConfig(&config.VPNSpec{Name: "x", ConfigFile: path})
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("err = %v, want it to name %s", err, key)
			}
		})
		t.Run("extra_args "+key, func(t *testing.T) {
			_, err := RenderConfig(&config.VPNSpec{Name: "x", Gateway: "vpn.example.com", ExtraArgs: []string{"--" + key + "=/tmp/evil"}})
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("err = %v, want it to name %s", err, key)
			}
		})
	}
}

func TestRenderConfigNeedsAHost(t *testing.T) {
	path := writeConfigFile(t, "username = someone\n")
	if _, err := RenderConfig(&config.VPNSpec{Name: "x", ConfigFile: path}); err == nil {
		t.Fatal("expected an error when neither gateway nor host is set")
	}
}

func TestSudoersFilePinsEveryCommand(t *testing.T) {
	got := SudoersFile("jane", []string{"office-vpn", "other-vpn"})
	want := []string{
		"jane ALL=(root) NOPASSWD: /opt/den/libexec/den-vpn check",
		"jane ALL=(root) NOPASSWD: /opt/den/libexec/den-vpn start office-vpn",
		"jane ALL=(root) NOPASSWD: /opt/den/libexec/den-vpn stop office-vpn",
		"jane ALL=(root) NOPASSWD: /opt/den/libexec/den-vpn status office-vpn",
		"jane ALL=(root) NOPASSWD: /opt/den/libexec/den-vpn start other-vpn",
		"jane ALL=(root) NOPASSWD: /opt/den/libexec/den-vpn stop other-vpn",
		"jane ALL=(root) NOPASSWD: /opt/den/libexec/den-vpn status other-vpn",
	}
	for _, line := range want {
		if !strings.Contains(got, line) {
			t.Errorf("sudoers file is missing:\n  %s\ngot:\n%s", line, got)
		}
	}
	// A bare wrapper rule would let any caller pass openfortivpn its own flags.
	if strings.Contains(got, "NOPASSWD: "+HelperPath+"\n") {
		t.Error("sudoers file grants the wrapper with arbitrary arguments")
	}
}

func TestScriptBakesInTheBinaryAndPrefix(t *testing.T) {
	got := Script("/opt/homebrew/bin/openfortivpn")
	if strings.Contains(got, "@OPENFORTIVPN@") || strings.Contains(got, "@PREFIX@") {
		t.Fatal("script still contains unreplaced placeholders")
	}
	for _, want := range []string{"OPENFORTIVPN='/opt/homebrew/bin/openfortivpn'", "PREFIX=" + HelperPrefix, helperProtocol} {
		if !strings.Contains(got, want) {
			t.Errorf("script is missing %q", want)
		}
	}
}

// The stop sweep used to be gated on the pidfile kill having failed, so a
// session den had lost track of survived every later stop, kept its slot on the
// gateway, and eventually made it refuse new SAML logins. Nothing about that is
// visible locally, so guard it here.
//
// This asserts on the rendered script rather than running it: cmd_stop ends in
// `pkill -x openfortivpn`, which in a test would kill the developer's own VPN.
func TestStopSweepIsNotGatedOnThePidfileKill(t *testing.T) {
	script := Script("/usr/bin/openfortivpn")

	stop := script[strings.Index(script, "cmd_stop()"):]
	stop = stop[:strings.Index(stop, "\ncmd_")]

	for _, sweep := range []string{`pkill -TERM -f "openfortivpn -c $CONF"`, "pkill -TERM -x openfortivpn"} {
		if !strings.Contains(stop, sweep) {
			t.Fatalf("cmd_stop no longer sweeps with %s", sweep)
		}
	}
	if strings.Contains(stop, `[ "$stopped" = "0" ] && pkill`) {
		t.Error("the sweep is gated on $stopped again — orphaned sessions will survive stop")
	}
}

// Killing openfortivpn orphans its pppd child, which keeps its ppp interface up
// and holds a VPN-assigned address for as long as the machine is up. Both stop
// paths have to reap those, and both have to do it narrowly enough that a live
// tunnel's pppd survives.
func TestStopReapsOrphanedPPPD(t *testing.T) {
	paths := map[string]string{"helper script": Script("/usr/bin/openfortivpn")}
	if name, args, err := stopCommand("", "/tmp/x.conf"); err == nil {
		paths["stopCommand"] = name + " " + strings.Join(args, " ")
	}

	for where, body := range paths {
		t.Run(where, func(t *testing.T) {
			if !strings.Contains(body, "169.254.2.1") {
				t.Error("does not look for openfortivpn's pppd at all")
			}
			// ppid==1 is what distinguishes an orphan from a live tunnel's child.
			if !strings.Contains(body, "ppid=") {
				t.Error("kills pppd without checking it is orphaned — this would drop a live tunnel")
			}
			// Without a comm check the sweeping shell can match its own pattern.
			if !strings.Contains(body, "comm=") {
				t.Error("does not verify the target is actually pppd")
			}
		})
	}
}

// openfortivpn logs out of the gateway on TERM, and over a dead connection that
// logout never returns. A stop that only sent TERM left one such process running
// with its ppp interface and split DNS, printed "stopped", and den started a
// second openfortivpn on top of it. Asserted on the text, like the sweep above:
// running it would kill the developer's VPN.
func TestStopEscalatesToKill(t *testing.T) {
	stop := Script("/usr/bin/openfortivpn")
	stop = stop[strings.Index(stop, "cmd_stop()"):]
	stop = stop[:strings.Index(stop, "\ncmd_")]
	paths := map[string]string{"helper script": stop}
	if name, args, err := stopCommand("", "/tmp/x.conf"); err == nil {
		paths["stopCommand"] = name + " " + strings.Join(args, " ")
	}
	if name, args, err := stopCommand("vpn.example.com", ""); err == nil {
		paths["stopCommand with a gateway"] = name + " " + strings.Join(args, " ")
	}

	for where, body := range paths {
		t.Run(where, func(t *testing.T) {
			term := strings.LastIndex(body, "pkill -TERM")
			kill := strings.Index(body, "pkill -KILL")
			reap := strings.Index(body, "169.254.2.1")
			if term < 0 || kill < 0 || reap < 0 {
				t.Fatalf("TERM at %d, KILL at %d, pppd reap at %d; want all three", term, kill, reap)
			}
			if term >= kill || kill >= reap {
				t.Errorf("want TERM, then KILL, then the pppd reap (a KILLed openfortivpn leaves its pppd); got %d, %d, %d", term, kill, reap)
			}
			// openfortivpn needs its chance to log out before it is KILLed.
			if wait := strings.Index(body[term:], "sleep 0.1"); wait < 0 || term+wait > kill {
				t.Error("KILLs without waiting for TERM first")
			}
		})
	}

	// The stop runs as `sh -c <it>`, so a plain -f pattern matches that shell
	// and KILLs it before the pppd reap.
	if body, ok := paths["stopCommand with a gateway"]; ok && strings.Contains(body, "'openfortivpn ") {
		t.Error("the -f pattern matches the stop's own shell; write it as [o]penfortivpn")
	}

	// "stopped" is only true when nothing is left; den logs a failing stop.
	tail := stop[strings.Index(stop, "169.254.2.1"):]
	check := strings.Index(tail, "pgrep -x openfortivpn")
	fail := strings.Index(tail, "did not stop")
	if check < 0 || fail < check || !strings.Contains(tail[fail:], "exit 1") {
		t.Error("cmd_stop does not fail when openfortivpn survives the KILL")
	}
}

func TestTrustIssue(t *testing.T) {
	cases := []struct {
		name     string
		uid      uint32
		mode     fs.FileMode
		wantWord string // "" means trusted
	}{
		{"root owned, not writable by others", 0, 0o755, ""},
		{"root owned file", 0, 0o600, ""},
		{"owned by the user", 501, 0o755, "not owned by root"},
		{"group writable", 0, 0o775, "writable by group"},
		{"world writable", 0, 0o757, "writable by group"},
		{"symlink", 0, 0o777 | fs.ModeSymlink, "symlink"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := trustIssue("/opt/den/libexec/den-vpn", c.uid, c.mode)
			if c.wantWord == "" {
				if got != "" {
					t.Fatalf("trustIssue = %q, want it trusted", got)
				}
				return
			}
			if !strings.Contains(got, c.wantWord) {
				t.Fatalf("trustIssue = %q, want it to mention %q", got, c.wantWord)
			}
		})
	}
}
