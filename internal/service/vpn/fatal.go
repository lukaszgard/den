package vpn

import "strings"

// Some openfortivpn and pppd output means this machine cannot run the tunnel
// at all, so no retry will help. openfortivpn keeps going anyway when its
// config sets persistent: every few seconds it re-runs the tunnel with a SAML
// session the gateway has already closed, and only "Could not authenticate"
// shows up. den stops it on the first of these lines and names the cause.
const (
	errKernelNoPPP = "this kernel has no PPP support, which pppd needs (WSL 3.0.1's kernel has none) — run `den doctor --for vpn`"
	errNoSAML      = "openfortivpn is older than 1.23.0 and has no SAML login — `den doctor --for vpn` shows how to upgrade it"
)

var fatalMarkers = []struct{ marker, err string }{
	{"The kernel does not support PPP", errKernelNoPPP},
	{"Please load the ppp_generic kernel module", errKernelNoPPP},
	{`Bad key in configuration file: "saml-login"`, errNoSAML},
	{"unrecognized option '--saml-login'", errNoSAML},
}

// fatalLine reports whether line says the tunnel can never come up here, and
// what to tell the user instead.
func fatalLine(line string) (string, bool) {
	for _, f := range fatalMarkers {
		if strings.Contains(line, f.marker) {
			return f.err, true
		}
	}
	return "", false
}
