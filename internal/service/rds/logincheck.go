package rds

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/lukaszgard/den/internal/service/ssmfwd"
)

// The login check (ssmfwd.LoginChecker) runs the client a tab runs, with the
// same connection parts and the credential handed over the same way, plus what
// makes it answer once and exit. Options go before the positional arguments:
// BSD getopt on macOS stops at the first argument that is not an option.

// psqlProbeArgs makes psql log in, answer "select 1" and exit: no psqlrc, no
// password prompt (-w; nobody is there to answer), one bare value.
func psqlProbeArgs(conninfo string) []string {
	return []string{"-X", "-w", "-A", "-t", "-c", "select 1", conninfo}
}

// mysqlProbeArgs turns a tab's mysql arguments, which end with the database
// when there is one, into a one-shot query.
func mysqlProbeArgs(args []string, dbName string) []string {
	if dbName != "" {
		args = args[:len(args)-1]
	}
	args = append(slices.Clone(args), "--batch", "-e", "select 1")
	if dbName != "" {
		args = append(args, dbName)
	}
	return args
}

// LoginProbe logs in with the IAM token: psql reads it from PGPASSWORD as a tab
// does, mysql from the option file Credential has just written.
func (e *engine) LoginProbe(token string) ssmfwd.LoginProbe {
	tab := e.TerminalCommand(token)
	p := ssmfwd.LoginProbe{Command: tab, User: e.dbUser(), Hint: e.iamHint()}
	if e.mysql() {
		p.Command.Args = mysqlProbeArgs(tab.Args, e.dbName())
	} else {
		p.Command.Args = psqlProbeArgs(tab.Args[0])
	}
	return p
}

// iamHint says what to check when RDS refuses an IAM token, which it does with
// no reason ("PAM authentication failed"): who the token was minted as, for
// whom and where, and the usual causes. All of it is known without an API call.
func (e *engine) iamHint() string {
	as := "the default AWS credentials"
	profile := e.env.CredentialProfile
	if profile != "" {
		as = fmt.Sprintf("AWS profile %q", profile)
		if e.spec.CredentialProfile == "" {
			as += " (aws_profile: credential_profile is not set)"
		}
	}
	user := e.dbUser()
	// Short lines: the log is as wide as the terminal and cuts the rest.
	lines := []string{
		"the IAM token was minted as " + as,
		fmt.Sprintf("for user %q on %s:%d; check that:", user, e.spec.RDSHost, e.remotePort()),
	}
	if e.mysql() {
		lines = append(lines, "  - the user logs in with IAM:",
			fmt.Sprintf("    CREATE USER '%s' IDENTIFIED WITH AWSAuthenticationPlugin AS 'RDS';", user))
	} else {
		lines = append(lines, fmt.Sprintf("  - the user has the rds_iam role: GRANT rds_iam TO %q;", user))
	}
	lines = append(lines,
		"  - the profile may call rds-db:connect on",
		fmt.Sprintf("    arn:aws:rds-db:%s:<account>:dbuser:<resource-id>/%s", cmp.Or(e.env.AWSRegionCode, "<region>"), user),
		"  - the profile's credentials have not expired"+ssoLogin(profile))
	return strings.Join(lines, "\n")
}

func ssoLogin(profile string) string {
	if profile == "" {
		return ""
	}
	return " (aws sso login --profile " + profile + ")"
}

// LoginProbe logs in with the stored password, from the environment as a tab
// does.
func (e *passwordEngine) LoginProbe(password string) ssmfwd.LoginProbe {
	tab := e.TerminalCommand(password)
	p := ssmfwd.LoginProbe{Command: tab, User: e.username(), Hint: e.passwordHint()}
	if e.base.mysql() {
		p.Command.Args = mysqlProbeArgs(tab.Args, e.base.dbName())
	} else {
		p.Command.Args = psqlProbeArgs(tab.Args[0])
	}
	return p
}

// passwordHint says what to check when RDS refuses a stored password: a secret
// that is out of date, or a user RDS holds to IAM auth.
func (e *passwordEngine) passwordHint() string {
	lines := []string{
		fmt.Sprintf("the password came from secret %q (%s) for user %q; check that:", e.secret.Name(), e.secret.Provider(), e.username()),
		"  - the secret holds the user's current password (r reads it again)",
	}
	if e.base.mysql() {
		lines = append(lines, "  - the user has a password, not IDENTIFIED WITH AWSAuthenticationPlugin, which accepts IAM tokens only")
	} else {
		lines = append(lines, "  - the user does not have the rds_iam role: with it, RDS accepts IAM tokens only")
	}
	return strings.Join(lines, "\n")
}

// The Tunnel finds the check by type assertion, so losing the method would
// drop it silently rather than fail to build.
var (
	_ ssmfwd.LoginChecker = (*engine)(nil)
	_ ssmfwd.LoginChecker = (*passwordEngine)(nil)
)
