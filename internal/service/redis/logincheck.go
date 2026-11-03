package redis

import (
	"fmt"
	"slices"
	"strings"

	"github.com/lukaszgard/den/internal/service/ssmfwd"
)

// The Tunnel finds the check by type assertion, so losing the method would
// drop it silently rather than fail to build.
var _ ssmfwd.LoginChecker = (*engine)(nil)

// LoginProbe is a tab's redis-cli sending one PING. redis-cli exits 0 when the
// cache answers WRONGPASS or NOAUTH, so only a PONG is a login.
func (e *engine) LoginProbe(token string) ssmfwd.LoginProbe {
	cmd := e.TerminalCommand(token)
	cmd.Args = append(slices.Clone(cmd.Args), "PING")
	return ssmfwd.LoginProbe{
		Command:   cmd,
		Succeeded: func(out string) bool { return strings.Contains(out, "PONG") },
		User:      e.spec.UserID,
		Hint:      e.loginHint(),
	}
}

// loginHint says what to check when the cache refuses: who the token was
// minted as and for which user, and the usual causes.
func (e *engine) loginHint() string {
	if e.spec.CacheName == "" || e.spec.UserID == "" {
		return "the cache was reached without a credential (cache_name and user_id are not set);\n" +
			"one that requires AUTH answers NOAUTH: set both to log in with IAM"
	}
	as, login := "the default AWS credentials", ""
	if p := e.env.CredentialProfile; p != "" {
		as, login = fmt.Sprintf("AWS profile %q", p), " (aws sso login --profile "+p+")"
	}
	kind, action, group := "cache", "elasticache:Connect", "the cache's user group"
	if e.spec.MemoryDB {
		kind, action, group = "MemoryDB cluster", "memorydb:Connect", "the cluster's ACL"
	}
	// Short lines: the log is as wide as the terminal and cuts the rest.
	return strings.Join([]string{
		"the IAM token was minted as " + as,
		fmt.Sprintf("for user %q of %s %q; check that:", e.spec.UserID, kind, e.spec.CacheName),
		fmt.Sprintf("  - the profile may call %s on the %s and on the user", action, kind),
		fmt.Sprintf("  - the user is in %s, uses IAM authentication, and its name equals its ID", group),
		"  - the profile's credentials have not expired" + login,
	}, "\n")
}
