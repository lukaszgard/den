//go:build !windows

package ssmfwd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/service"
)

// passwordEngine logs in with a stored password: a credential with no expiry,
// read again on every call, with a check that runs before any forward opens.
type passwordEngine struct {
	*fakeEngine
	preflightErr error
	reads        atomic.Int32
}

func (e *passwordEngine) Preflight() error { return e.preflightErr }

func (e *passwordEngine) Credential(context.Context, func(string)) (Credential, error) {
	n := e.reads.Add(1)
	return Credential{Secret: fmt.Sprintf("password-%d", n)}, nil
}

func (e *passwordEngine) PasswordLogin() service.PasswordLogin {
	return service.PasswordLogin{Secret: "orders-login", Provider: "sops"}
}

// A login that cannot work should say so at once, not after a session opened
// and the user waited for a bastion.
func TestFailedPreflightOpensNoForward(t *testing.T) {
	fakeAWS(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("DEN_SSMFWD_ARGS_FILE", argsFile)
	e := &passwordEngine{fakeEngine: &fakeEngine{ports: []int{freePort(t)}}, preflightErr: errors.New("SSO login expired")}
	tun := New(Options{Name: "test"}, e)

	if _, err := tun.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := waitStatus(t, tun, service.StatusError)
	if st.Error == nil || st.Error.Error() != "SSO login expired" {
		t.Fatalf("error = %v, want the preflight error", st.Error)
	}
	if _, err := os.Stat(argsFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a session was started: Stat(args) = %v", err)
	}
	// The list cuts a long error short, and the detail view shows the log: with
	// no session there is nothing else in it to say why.
	if logs := strings.Join(tun.Logs(), "\n"); !strings.Contains(logs, "SSO login expired") {
		t.Errorf("the log does not say why: %q", logs)
	}
	if n := e.reads.Load(); n != 0 {
		t.Errorf("the secret was read %d times after a failed preflight", n)
	}
}

// With no expiry there is nothing to count down or renew by itself: no TTL for
// the views and no refresh loop. r still reads the secret again.
func TestCredentialWithoutExpiryIsNotRefreshedByItself(t *testing.T) {
	fakeAWS(t)
	e := &passwordEngine{fakeEngine: &fakeEngine{ports: []int{freePort(t)}}}
	tun := New(Options{Name: "test"}, e)

	if _, err := tun.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tun.Stop(context.Background()) }) //nolint:errcheck

	st := waitStatus(t, tun, service.StatusConnected)
	if st.Token != "password-1" {
		t.Errorf("Token = %q, want the first read", st.Token)
	}
	if st.TokenExpires != nil || st.TokenTTL != 0 {
		t.Errorf("TokenExpires = %v, TokenTTL = %v, want none", st.TokenExpires, st.TokenTTL)
	}
	if at := tun.nextRefresh(); !at.IsZero() {
		t.Errorf("nextRefresh = %v, want no refresh loop", at)
	}

	if err := tun.RefreshToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := tun.State().Token; got != "password-2" {
		t.Errorf("after r: Token = %q, want a new read", got)
	}
	if st := tun.State(); st.TokenExpires != nil {
		t.Errorf("after r: TokenExpires = %v, want none", st.TokenExpires)
	}
	// r must not start a loop either: nothing has an expiry to wake it.
	time.Sleep(50 * time.Millisecond)
	if n := e.reads.Load(); n != 2 {
		t.Errorf("reads = %d, want only the connect and r", n)
	}
}

// A reconnect re-reads the secret, which is how a rotated password arrives
// without the user doing anything.
func TestReconnectReadsTheSecretAgain(t *testing.T) {
	dieDir := fakeAWS(t)
	e := &passwordEngine{fakeEngine: &fakeEngine{ports: []int{freePort(t)}}}
	tun := New(Options{Name: "test", Reconnect: true}, e)

	if _, err := tun.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tun.Stop(context.Background()) }) //nolint:errcheck
	waitStatus(t, tun, service.StatusConnected)

	killSession(t, dieDir, e.ports[0])
	waitStatus(t, tun, service.StatusReconnecting)
	reviveSession(t, dieDir, e.ports[0])

	st := waitStatus(t, tun, service.StatusConnected)
	if st.Token != "password-2" {
		t.Errorf("Token = %q, want it read again", st.Token)
	}
}

func TestPasswordLoginComesFromTheEngine(t *testing.T) {
	withLogin := New(Options{Name: "test"}, &passwordEngine{fakeEngine: &fakeEngine{ports: []int{50110}}})
	login, ok := service.PasswordLoginOf(withLogin)
	if !ok || login.Secret != "orders-login" {
		t.Errorf("PasswordLoginOf = %+v, %v", login, ok)
	}
	plain := New(Options{Name: "test"}, &fakeEngine{ports: []int{50110}})
	if _, ok := service.PasswordLoginOf(plain); ok {
		t.Error("an engine without a password login must not report one")
	}
}
