package vpn

import (
	"regexp"
)

var samlURLRegex = regexp.MustCompile(`Authenticate at '(https?://[^']+)'`)

// extractSAMLURL returns the SAML login URL from an openfortivpn log line, if any.
func extractSAMLURL(line string) (string, bool) {
	m := samlURLRegex.FindStringSubmatch(line)
	if len(m) < 2 {
		return "", false
	}
	return m[1], true
}
