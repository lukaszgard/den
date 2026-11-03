package documentdb

import (
	"fmt"
	"slices"
	"strings"

	"github.com/lukaszgard/den/internal/service/ssmfwd"
)

// The Tunnel finds the check by type assertion, so losing the method would
// drop it silently rather than fail to build.
var _ ssmfwd.LoginChecker = (*engine)(nil)

// authCheck is the script the check runs. A refused handshake fails the
// connection, but mongosh can also connect without logging in and say nothing
// about it, so the script fails too when the connection has no user.
const authCheck = `if (!db.runCommand({connectionStatus: 1}).authInfo.authenticatedUsers.length) ` +
	`throw new Error("connected, but not logged in: no authenticated user"); 1`

// LoginProbe is a tab's mongosh running authCheck and exiting. There is no
// secret: the driver signs with the profile.
func (e *engine) LoginProbe(string) ssmfwd.LoginProbe {
	cmd := e.TerminalCommand("")
	cmd.Args = append(slices.Clone(cmd.Args), "--quiet", "--eval", authCheck)
	return ssmfwd.LoginProbe{Command: cmd, Hint: e.loginHint()}
}

// loginHint says what to check when the cluster refuses the signed handshake:
// whose identity mongosh signed with, and the usual causes.
func (e *engine) loginHint() string {
	as, login := "the default AWS credentials", ""
	if p := e.env.CredentialProfile; p != "" {
		as, login = fmt.Sprintf("AWS profile %q", p), " (aws sso login --profile "+p+")"
	}
	return strings.Join([]string{
		fmt.Sprintf("mongosh signed the login with %s; check that:", as),
		"  - the cluster has a $external user for that identity's role ARN, with mechanism MONGODB-AWS",
		"  - no AWS_ACCESS_KEY_ID or AWS_SESSION_TOKEN where den runs: they outrank AWS_PROFILE",
		"  - the profile's credentials have not expired" + login,
		"  - mongosh is 2.5 or newer: older ones cannot sign with MONGODB-AWS",
	}, "\n")
}
