//go:build !windows

package ssmfwd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/service"
)

// fakeClient puts a client named tool on PATH that runs script, a /bin/sh body.
// tool must be an id of the prereq catalog, which is how a probe finds it.
func fakeClient(t *testing.T, tool, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func psqlProbe(args ...string) LoginProbe {
	return LoginProbe{Command: service.ClientCommand{Tool: "psql", Args: args, Env: []string{"PGPASSWORD=s3cr3t"}}}
}

// The client gets the probe's arguments and environment, as a terminal tab
// would, and exit status 0 is a login that worked.
func TestProbeRunsTheClientWithItsArgsAndEnv(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	fakeClient(t, "psql", `printf '%s\n' "$@" > `+argsFile+`
[ "$PGPASSWORD" = s3cr3t ] || { echo "FATAL: password authentication failed"; exit 2; }`)

	if err := runProbe(context.Background(), psqlProbe("-X", "-c", "select 1", "host=localhost"), "s3cr3t", 5*time.Second); err != nil {
		t.Fatalf("runProbe = %v, want nil", err)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := "-X\n-c\nselect 1\nhost=localhost\n"; string(got) != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

// What the database said is the line that says why, not psql's complaint about
// the address it tried first, and not the description of the connection psql
// puts before it: the log is as wide as the terminal.
func TestProbeReportsTheLineThatSaysWhy(t *testing.T) {
	fakeClient(t, "psql", `echo 'psql: error: connection to server at "localhost" (::1), port 5432 failed: Connection refused' >&2
echo '	Is the server running on that host and accepting TCP/IP connections?' >&2
echo 'connection to server at "localhost" (127.0.0.1), port 5432 failed: FATAL:  PAM authentication failed for user "base-app-rw"' >&2
exit 2`)

	err := runProbe(context.Background(), psqlProbe(), "s3cr3t", 5*time.Second)
	want := `FATAL:  PAM authentication failed for user "base-app-rw"`
	if err == nil || err.Error() != want {
		t.Fatalf("runProbe = %v, want %q", err, want)
	}
}

// The error reaches the views, the log and agents: a client that echoes the
// credential must not carry it there.
func TestProbeErrorNeverCarriesTheSecret(t *testing.T) {
	fakeClient(t, "psql", `echo "ERROR 1045 (28000): Access denied for user 'app' (password $PGPASSWORD)"; exit 1`)

	err := runProbe(context.Background(), psqlProbe(), "s3cr3t", 5*time.Second)
	if err == nil {
		t.Fatal("runProbe = nil, want the refusal")
	}
	if strings.Contains(err.Error(), "s3cr3t") || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("error = %q, want the secret replaced by [redacted]", err)
	}
}

// redis-cli exits 0 when the server answers WRONGPASS, so a probe may judge by
// what the client printed.
func TestProbeSucceededJudgesTheOutput(t *testing.T) {
	pong := func(out string) bool { return strings.Contains(out, "PONG") }
	probe := LoginProbe{Command: service.ClientCommand{Tool: "redis-cli"}, Succeeded: pong}

	fakeClient(t, "redis-cli", `echo "AUTH failed: WRONGPASS invalid username-password pair or user is disabled."; exit 0`)
	err := runProbe(context.Background(), probe, "", 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "WRONGPASS") {
		t.Fatalf("WRONGPASS with exit 0: runProbe = %v, want the WRONGPASS line", err)
	}

	fakeClient(t, "redis-cli", `echo PONG`)
	if err := runProbe(context.Background(), probe, "", 5*time.Second); err != nil {
		t.Fatalf("PONG: runProbe = %v, want nil", err)
	}

	fakeClient(t, "redis-cli", `echo PONG; exit 1`)
	if err := runProbe(context.Background(), probe, "", 5*time.Second); err == nil {
		t.Fatal("PONG with exit 1: runProbe = nil, want a failure")
	}
}

// A machine without the client cannot check, which is not a failed login.
func TestProbeWithoutTheClient(t *testing.T) {
	probe := LoginProbe{Command: service.ClientCommand{Tool: "no-such-client"}}
	if err := runProbe(context.Background(), probe, "", 5*time.Second); !errors.Is(err, errNoClient) {
		t.Fatalf("runProbe = %v, want errNoClient", err)
	}
}

func TestProbeExitWithoutOutput(t *testing.T) {
	fakeClient(t, "psql", `exit 3`)
	err := runProbe(context.Background(), psqlProbe(), "", 5*time.Second)
	if err == nil || err.Error() != "psql exited with exit status 3" {
		t.Fatalf("runProbe = %v, want the exit status", err)
	}
}

// A client that hangs (a server that accepts the connection and never answers)
// must not hold the check for good.
func TestProbeTimesOut(t *testing.T) {
	fakeClient(t, "psql", `sleep 30`)
	start := time.Now()
	err := runProbe(context.Background(), psqlProbe(), "", 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "psql did not answer within 200ms") {
		t.Fatalf("runProbe = %v, want a timeout", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("runProbe took %v after its timeout", d)
	}
}

// A client that reads its input would wait for a person who is not there.
func TestProbeHasNoInput(t *testing.T) {
	fakeClient(t, "psql", `cat`)
	if err := runProbe(context.Background(), psqlProbe(), "", 5*time.Second); err != nil {
		t.Fatalf("runProbe = %v, want nil: stdin should be closed", err)
	}
}

func TestProbeCancelled(t *testing.T) {
	fakeClient(t, "psql", `sleep 30`)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	if err := runProbe(ctx, psqlProbe(), "", 20*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("runProbe = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("runProbe took %v to notice the cancel", d)
	}
}

func TestTellingLine(t *testing.T) {
	cases := []struct{ name, output, want string }{
		{"psql", "psql: error: connection to server at \"localhost\" (::1), port 5432 failed: Connection refused\n\tIs the server running?\nconnection to server at \"localhost\" (127.0.0.1), port 5432 failed: FATAL:  password authentication failed for user \"app\"\n",
			`FATAL:  password authentication failed for user "app"`},
		{"mysql", "mysql: [Warning] Using a password on the command line interface can be insecure.\nERROR 1045 (28000): Access denied for user 'app'@'10.0.0.1' (using password: YES)\n",
			`ERROR 1045 (28000): Access denied for user 'app'@'10.0.0.1' (using password: YES)`},
		{"redis", "AUTH failed: WRONGPASS invalid username-password pair or user is disabled.\n(error) NOAUTH Authentication required.\n",
			"AUTH failed: WRONGPASS invalid username-password pair or user is disabled."},
		{"mongosh", "Current Mongosh Log ID: 123\nMongoServerError: Authentication failed.\n",
			"MongoServerError: Authentication failed."},
		{"nothing telling", "first\nlast words\n\n", "last words"},
		{"empty", "\n \n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tellingLine(c.output); got != c.want {
				t.Errorf("tellingLine = %q, want %q", got, c.want)
			}
		})
	}
}

// A client may print one enormous line; the views and agents get a sentence.
func TestTellingLineIsBounded(t *testing.T) {
	got := tellingLine("FATAL: " + strings.Repeat("x", 5000))
	if n := len([]rune(got)); n > maxLoginErr {
		t.Fatalf("line of %d runes, want at most %d", n, maxLoginErr)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a cut line should say so: %q", got[len(got)-10:])
	}
}

// loginEngine is a fakeEngine whose client den can log in with: tool, handed
// the credential in FAKE_SECRET.
type loginEngine struct {
	*fakeEngine
	tool string
}

func (e *loginEngine) LoginProbe(secret string) LoginProbe {
	return LoginProbe{
		Command: service.ClientCommand{Tool: e.tool, Env: []string{"FAKE_SECRET=" + secret}},
		User:    "app",
		Hint:    "check rds_iam on app\ncheck rds-db:connect",
	}
}

func newLoginTunnel(t *testing.T, tool string) (*Tunnel, <-chan service.StatusUpdate) {
	t.Helper()
	tun := New(Options{Name: "test"}, &loginEngine{fakeEngine: &fakeEngine{ports: []int{freePort(t)}}, tool: tool})
	updates, err := tun.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tun.Stop(context.Background()) }) //nolint:errcheck
	return tun, updates
}

func waitLogin(t *testing.T, tun *Tunnel, want service.LoginState) service.RuntimeState {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if st := tun.State(); st.Login == want {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	st := tun.State()
	t.Fatalf("login = %v (%q), want %v\nlogs:\n%s", st.Login, st.LoginErr, want, strings.Join(tun.Logs(), "\n"))
	return st
}

func waitLog(t *testing.T, tun *Tunnel, want string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(tun.Logs(), "\n"), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no log line %q in:\n%s", want, strings.Join(tun.Logs(), "\n"))
}

// The check runs after CONNECTED and says so from the first update on: an
// agent waiting for the outcome must never read "connected, not checked" for a
// check that is about to start.
func TestLoginCheckedAfterConnect(t *testing.T) {
	fakeAWS(t)
	fakeClient(t, "psql", `[ "$FAKE_SECRET" = token-1 ] || exit 2`)
	tun, updates := newLoginTunnel(t, "psql")

	for u := range updates {
		if u.State.Status == service.StatusConnected {
			if u.State.Login != service.LoginChecking {
				t.Fatalf("first connected update: login = %v, want checking", u.State.Login)
			}
			break
		}
	}
	st := waitLogin(t, tun, service.LoginOK)
	if st.Status != service.StatusConnected || st.LoginErr != "" {
		t.Fatalf("status = %v, login error %q", st.Status, st.LoginErr)
	}
	waitLog(t, tun, "login checked as app")
}

// A refused credential is not a dead tunnel: it stays up, so the user can
// still open a client and see for themselves, and the views say what is wrong.
func TestRefusedLoginKeepsTheTunnelUp(t *testing.T) {
	fakeAWS(t)
	fakeClient(t, "psql", `echo "FATAL:  PAM authentication failed for user \"app\" ($FAKE_SECRET)" >&2; exit 2`)
	tun, _ := newLoginTunnel(t, "psql")

	st := waitLogin(t, tun, service.LoginFailed)
	if st.Status != service.StatusConnected {
		t.Fatalf("status = %v after a refused login, want connected", st.Status)
	}
	if want := `FATAL:  PAM authentication failed for user "app" ([redacted])`; st.LoginErr != want {
		t.Fatalf("login error = %q, want %q", st.LoginErr, want)
	}
	waitLog(t, tun, `login failed: FATAL:  PAM authentication failed for user "app" ([redacted])`)
	waitLog(t, tun, "check rds_iam on app")
	waitLog(t, tun, "check rds-db:connect")
	if _, err := tun.TerminalCommand(); err != nil && !strings.Contains(err.Error(), "no client") {
		t.Fatalf("TerminalCommand after a refused login: %v", err)
	}
}

// r is how a user retries after fixing what the hint named: the new credential
// is checked too, and a login that works clears the warning.
func TestNewCredentialChecksTheLoginAgain(t *testing.T) {
	fakeAWS(t)
	fakeClient(t, "psql", `[ "$FAKE_SECRET" = token-2 ] || { echo "FATAL: refused" >&2; exit 2; }`)
	tun, _ := newLoginTunnel(t, "psql")
	waitLogin(t, tun, service.LoginFailed)

	if err := tun.RefreshToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := waitLogin(t, tun, service.LoginOK)
	if st.LoginErr != "" {
		t.Fatalf("login error = %q after a login that worked", st.LoginErr)
	}
}

// Without the client den cannot tell, and says so rather than failing.
func TestLoginNotCheckedWithoutTheClient(t *testing.T) {
	fakeAWS(t)
	tun, _ := newLoginTunnel(t, "no-such-client")
	waitStatus(t, tun, service.StatusConnected)
	waitLog(t, tun, "no-such-client not found — login not checked")
	if st := tun.State(); st.Login != service.LoginUnchecked {
		t.Fatalf("login = %v without a client, want unchecked", st.Login)
	}
}

func TestEngineWithoutAProbeIsNotChecked(t *testing.T) {
	fakeAWS(t)
	tun := newTunnel(&fakeEngine{ports: []int{freePort(t)}}, false)
	if _, err := tun.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tun.Stop(context.Background()) }) //nolint:errcheck
	if st := waitStatus(t, tun, service.StatusConnected); st.Login != service.LoginUnchecked {
		t.Fatalf("login = %v, want unchecked", st.Login)
	}
}

// A check is part of its connection: Stop ends it, and what it would have
// found must not land on the state Stop leaves.
func TestStopEndsALoginCheck(t *testing.T) {
	fakeAWS(t)
	fakeClient(t, "psql", `sleep 30`)
	tun, _ := newLoginTunnel(t, "psql")
	waitLogin(t, tun, service.LoginChecking)

	start := time.Now()
	if err := tun.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("Stop took %v with a check running", d)
	}
	if st := tun.State(); st.Status != service.StatusIdle || st.Login != service.LoginUnchecked {
		t.Fatalf("after Stop: status %v, login %v; want idle, unchecked", st.Status, st.Login)
	}
}

// A new connection checks its own credential; the last one's verdict is gone
// the moment it drops.
func TestReconnectStartsUnchecked(t *testing.T) {
	dieDir := fakeAWS(t)
	fakeClient(t, "psql", `echo "FATAL: refused" >&2; exit 2`)
	e := &loginEngine{fakeEngine: &fakeEngine{ports: []int{freePort(t)}}, tool: "psql"}
	tun := New(Options{Name: "test", Reconnect: true}, e)
	if _, err := tun.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tun.Stop(context.Background()) }) //nolint:errcheck
	waitLogin(t, tun, service.LoginFailed)

	killSession(t, dieDir, e.ports[0])
	st := waitStatus(t, tun, service.StatusReconnecting)
	if st.Login != service.LoginUnchecked || st.LoginErr != "" {
		t.Fatalf("reconnecting: login %v (%q), want unchecked", st.Login, st.LoginErr)
	}
}
