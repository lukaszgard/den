package vpn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
)

// The lines are the ones a colleague's den logged: a WSL 3.0.1 kernel without
// PPP, and before that an openfortivpn without SAML. Lines from a login that
// merely failed must not count: a retry can get past those.
func TestFatalLine(t *testing.T) {
	cases := map[string]string{
		"/usr/sbin/pppd: Please load the ppp_generic kernel module.":                                                             errKernelNoPPP,
		"ERROR:  pppd: The kernel does not support PPP, for example, the PPP kernel driver is not included or cannot be loaded.": errKernelNoPPP,
		`WARN:   Bad key in configuration file: "saml-login".`:                                                                   errNoSAML,
		"openfortivpn: unrecognized option '--saml-login'":                                                                       errNoSAML,
		"ERROR:  Could not authenticate to gateway. Please check the password, client certificate, etc.":                         "",
		"Couldn't open the /dev/ppp device: No such device or address":                                                           "",
		"INFO:   Remote gateway has allocated a VPN.":                                                                            "",
	}
	for line, want := range cases {
		got, ok := fatalLine(line)
		if got != want || ok != (want != "") {
			t.Errorf("fatalLine(%q) = %q, %v; want %q", line, got, ok, want)
		}
	}
}

// The helper's log is tailed for the SAML URL and the tunnel-up line; a fatal
// line before the tunnel is up has to reach the waiter, one after it must not.
func TestTailLogFileReportsAFatalLineBeforeTheTunnelIsUp(t *testing.T) {
	for _, c := range []struct {
		name, log string
		wantFatal bool
	}{
		{"before", "INFO:   Remote gateway has allocated a VPN.\n/usr/sbin/pppd: Please load the ppp_generic kernel module.\n", true},
		{"after", "INFO:   Tunnel is up and running.\nERROR:  pppd: The kernel does not support PPP\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSvc(t, &config.VPNSpec{})
			logPath := filepath.Join(t.TempDir(), "vpn.log")
			if err := os.WriteFile(logPath, []byte(c.log), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			readyCh := make(chan struct{})
			fatalCh, fatal := fatalSignal()
			done := make(chan struct{})
			go func() { s.tailLogFile(ctx, logPath, readyCh, fatal); close(done) }()

			select {
			case msg := <-fatalCh:
				if !c.wantFatal || msg != errKernelNoPPP {
					t.Errorf("fatal %q, want it: %v", msg, c.wantFatal)
				}
			case <-time.After(time.Second):
				if c.wantFatal {
					t.Error("the PPP line never reached the waiter")
				}
			}
			cancel()
			<-done
		})
	}
}

// A fatal line ends the attempt: openfortivpn is stopped (a persistent setting
// would rerun it every few seconds against a closed SAML session), the error
// says why, and auto-reconnect leaves it alone.
func TestFatalLineStopsTheTunnelAndIsNotRetried(t *testing.T) {
	s := newSvc(t, &config.VPNSpec{Reconnect: true})
	stopped := false
	s.stopTunnel = func(context.Context) { stopped = true }
	fatalCh, fatal := fatalSignal()
	fatal(errKernelNoPPP)
	fatal(errNoSAML) // only the first one counts

	if s.waitForReady(t.Context(), make(chan struct{}), fatalCh) {
		t.Fatal("a fatal line must not read as connected")
	}
	st := s.State()
	if !stopped || st.Status != service.StatusError || st.Error == nil || !strings.Contains(st.Error.Error(), "no PPP support") {
		t.Errorf("stopped %v, state %+v", stopped, st)
	}
	if _, retry := s.nextRetry(t.Context(), 0); retry {
		t.Error("a machine without PPP must not be retried")
	}
}
