//go:build !windows

package ssmfwd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
)

// These tests drive a real Tunnel against a fake `aws` on PATH: the test binary
// re-executed as TestHelperProcess, which listens on the requested local port
// the way session-manager-plugin does and exits when its die file appears.

const helperEnv = "DEN_SSMFWD_FAKE_AWS"

func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	// The local port, as each transport's command spells it: aws ssm
	// (--parameters localPortNumber=), ssh (-L 127.0.0.1:LOCAL:host:port),
	// aws ec2-instance-connect (--local-port) and kubectl (LOCAL:REMOTE).
	var port string
	for i, a := range args {
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		switch {
		case a == "--parameters":
			for kv := range strings.SplitSeq(next, ",") {
				if v, ok := strings.CutPrefix(kv, "localPortNumber="); ok {
					port = v
				}
			}
		case a == "-L":
			if parts := strings.Split(next, ":"); len(parts) == 4 {
				port = parts[1]
			}
		case a == "--local-port":
			port = next
		case strings.Count(a, ":") == 1 && !strings.HasPrefix(a, "-"):
			port = strings.SplitN(a, ":", 2)[0]
		}
	}
	if argsFile := os.Getenv("DEN_SSMFWD_ARGS_FILE"); argsFile != "" {
		_ = os.WriteFile(argsFile, []byte(strings.Join(args, "\n")), 0o644)
	}
	// A session that starts but never serves its port, so every attempt fails
	// the port check.
	if _, err := os.Stat(filepath.Join(os.Getenv("DEN_SSMFWD_DIE_DIR"), port+".nolisten")); err == nil {
		fmt.Println("Waiting for connections...")
		for {
			time.Sleep(time.Second)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Println("listen:", err)
		os.Exit(2)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	fmt.Println("Port " + port + " opened for sessionId fake.")
	fmt.Println("Waiting for connections...")

	dieDir := os.Getenv("DEN_SSMFWD_DIE_DIR")
	dieFile := filepath.Join(dieDir, port)
	deafFile := filepath.Join(dieDir, port+".deaf")
	deaf := false
	for {
		if _, err := os.Stat(dieFile); err == nil {
			fmt.Println("session terminated")
			os.Exit(1)
		}
		// Stop listening but keep running: session-manager-plugin does exactly
		// this when its websocket to AWS drops, and den used to keep reporting
		// the tunnel as connected because the process was still alive.
		if !deaf {
			if _, err := os.Stat(deafFile); err == nil {
				ln.Close()
				deaf = true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// fakeAWS puts an `aws` executable on PATH that re-runs this test binary as the
// helper, and returns the directory where die files end sessions.
func fakeAWS(t *testing.T) (dieDir string) {
	t.Helper()
	return fakeBinary(t, "aws")
}

// fakeBinary is fakeAWS for any transport's program.
func fakeBinary(t *testing.T, name string) (dieDir string) {
	t.Helper()
	if testing.Short() {
		t.Skip("drives real processes through connect/drop/reconnect cycles; skipped with -short")
	}
	bin := t.TempDir()
	dieDir = t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nexec %q -test.run='^TestHelperProcess$' -- \"$@\"\n", exe)
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(helperEnv, "1")
	t.Setenv("DEN_SSMFWD_DIE_DIR", dieDir)
	return dieDir
}

func killSession(t *testing.T, dieDir string, port int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dieDir, fmt.Sprint(port)), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// deafenSession closes the helper's listener without stopping the process.
func deafenSession(t *testing.T, dieDir string, port int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dieDir, fmt.Sprint(port)+".deaf"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// undeafenSession lets a restarted helper listen again.
func undeafenSession(t *testing.T, dieDir string, port int) {
	t.Helper()
	if err := os.Remove(filepath.Join(dieDir, fmt.Sprint(port)+".deaf")); err != nil {
		t.Fatal(err)
	}
}

func reviveSession(t *testing.T, dieDir string, port int) {
	t.Helper()
	if err := os.Remove(filepath.Join(dieDir, fmt.Sprint(port))); err != nil {
		t.Fatal(err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitStatus(t *testing.T, tun *Tunnel, want service.Status) service.RuntimeState {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if st := tun.State(); st.Status == want {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	st := tun.State()
	t.Fatalf("status = %v (err %v), want %v\nlogs:\n%s", st.Status, st.Error, want, strings.Join(tun.Logs(), "\n"))
	return st
}

func newTunnel(e *fakeEngine, reconnect bool) *Tunnel {
	return New(Options{Name: "test", Environment: "int", Reconnect: reconnect}, e)
}

// newFastMonitorTunnel probes the forwards every 50 ms, so tests of the port
// monitor do not wait out the production cadence.
func newFastMonitorTunnel(e *fakeEngine, reconnect bool) *Tunnel {
	return New(Options{
		Name: "test", Environment: "int", Reconnect: reconnect,
		MonitorEvery: 50 * time.Millisecond, MonitorMaxMisses: 2,
	}, e)
}

func TestConnectMintsCredentialAfterTunnelIsUp(t *testing.T) {
	fakeAWS(t)
	e := &fakeEngine{ports: []int{freePort(t)}}
	tun := newTunnel(e, false)

	if _, err := tun.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tun.Stop(context.Background()) }) //nolint:errcheck

	st := waitStatus(t, tun, service.StatusConnected)
	if st.Token != "token-1" || st.TokenExpires == nil {
		t.Fatalf("token = %q expires = %v, want token-1 with an expiry", st.Token, st.TokenExpires)
	}
	if st.ConnectCmd != e.ConnectCommand() {
		t.Fatalf("ConnectCmd = %q", st.ConnectCmd)
	}
	if got := tun.ConnectCommandWithSecret(); got != e.ConnectCommand()+" -a token-1" {
		t.Fatalf("ConnectCommandWithSecret = %q", got)
	}
	if !service.HoldsToken(tun) {
		t.Fatal("tunnel with a Credentialer engine should hold a token")
	}
	if _, err := tun.Start(context.Background()); !errors.Is(err, service.ErrAlreadyRunning) {
		t.Fatalf("second Start err = %v, want ErrAlreadyRunning", err)
	}
}

// A credential minted for a tunnel that is not up would be written to
// ~/.pgpass for a port nobody listens on, and its log line would promise an
// auto-refresh no loop is there to do.
func TestRefreshTokenNeedsAConnectedTunnel(t *testing.T) {
	e := &fakeEngine{ports: []int{freePort(t)}}
	tun := newTunnel(e, false)
	if err := tun.RefreshToken(context.Background()); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("RefreshToken on an idle tunnel: err = %v, want not connected", err)
	}
	if n := e.mintCount(); n != 0 {
		t.Fatalf("an idle tunnel minted %d credentials, want none", n)
	}
}

// r mints a new credential whose log line says when it is auto-refreshed; the
// refresh loop has to follow that credential, not the one it was armed for.
func TestRefreshTokenReschedulesTheAutoRefresh(t *testing.T) {
	fakeAWS(t)
	e := &fakeEngine{ports: []int{freePort(t)}, ttls: []time.Duration{15 * time.Minute, time.Hour}}
	tun := newTunnel(e, false)
	if _, err := tun.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tun.Stop(context.Background()) }) //nolint:errcheck

	st := waitStatus(t, tun, service.StatusConnected)
	waitRefreshAt(t, tun, st.TokenExpires.Add(-refreshLead))

	if err := tun.RefreshToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	st = tun.State()
	if st.Token != "token-2" {
		t.Fatalf("token = %q after r, want token-2", st.Token)
	}
	waitRefreshAt(t, tun, st.TokenExpires.Add(-refreshLead))
}

// waitRefreshAt waits until the refresh loop is armed for want.
func waitRefreshAt(t *testing.T, tun *Tunnel, want time.Time) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := tun.nextRefresh()
		if d := got.Sub(want); d > -time.Second && d < time.Second {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("auto-refresh armed for %v, want %v", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSessionDropWithoutReconnectEndsInError(t *testing.T) {
	dieDir := fakeAWS(t)
	e := &fakeEngine{ports: []int{freePort(t)}}
	tun := newTunnel(e, false)

	if _, err := tun.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tun.Stop(context.Background()) }) //nolint:errcheck
	waitStatus(t, tun, service.StatusConnected)

	killSession(t, dieDir, e.ports[0])
	st := waitStatus(t, tun, service.StatusError)
	if st.Error == nil || !strings.Contains(st.Error.Error(), "SSM session exited") {
		t.Fatalf("error = %v, want SSM session exited", st.Error)
	}
}

// A live process is not a live tunnel. session-manager-plugin can stop serving
// the local port while staying alive, which left den reporting CONNECTED
// indefinitely and refreshing credentials against a tunnel carrying nothing.
func TestSilentForwardDeathIsDetected(t *testing.T) {
	dieDir := fakeAWS(t)
	e := &fakeEngine{ports: []int{freePort(t)}}
	tun := newFastMonitorTunnel(e, false)

	if _, err := tun.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tun.Stop(context.Background()) }) //nolint:errcheck
	waitStatus(t, tun, service.StatusConnected)

	deafenSession(t, dieDir, e.ports[0])

	st := waitStatus(t, tun, service.StatusError)
	if st.Error == nil || !strings.Contains(st.Error.Error(), "stopped accepting connections") {
		t.Fatalf("error = %v, want a port-check failure", st.Error)
	}
}

// The detected drop has to feed the reconnect loop, not just report an error —
// that is what makes a silently dead tunnel self-heal.
func TestSilentForwardDeathReconnects(t *testing.T) {
	dieDir := fakeAWS(t)
	e := &fakeEngine{ports: []int{freePort(t)}}
	tun := newFastMonitorTunnel(e, true)

	if _, err := tun.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tun.Stop(context.Background()) }) //nolint:errcheck
	waitStatus(t, tun, service.StatusConnected)

	deafenSession(t, dieDir, e.ports[0])
	waitStatus(t, tun, service.StatusReconnecting)

	// Let the replacement session serve the port again.
	undeafenSession(t, dieDir, e.ports[0])
	waitStatus(t, tun, service.StatusConnected)
}

func TestSessionDropWithReconnectComesBack(t *testing.T) {
	dieDir := fakeAWS(t)
	e := &fakeEngine{ports: []int{freePort(t)}}
	tun := newTunnel(e, true)

	if _, err := tun.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tun.Stop(context.Background()) }) //nolint:errcheck
	waitStatus(t, tun, service.StatusConnected)

	killSession(t, dieDir, e.ports[0])
	st := waitStatus(t, tun, service.StatusReconnecting)
	if st.ReconnectAt == nil {
		t.Fatal("reconnecting state should carry ReconnectAt for the countdown")
	}
	if _, err := tun.Start(context.Background()); !errors.Is(err, service.ErrAlreadyRunning) {
		t.Fatalf("Start while reconnecting err = %v, want ErrAlreadyRunning", err)
	}
	reviveSession(t, dieDir, e.ports[0])

	st = waitStatus(t, tun, service.StatusConnected)
	if st.Token != "token-2" {
		t.Fatalf("token after reconnect = %q, want a freshly minted token-2", st.Token)
	}
}

func TestCredentialFailureTearsTunnelDown(t *testing.T) {
	fakeAWS(t)
	e := &fakeEngine{ports: []int{freePort(t)}, credErr: errors.New("sso expired")}
	tun := newTunnel(e, false)

	if _, err := tun.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := waitStatus(t, tun, service.StatusError)
	if st.Error == nil || st.Error.Error() != "sso expired" {
		t.Fatalf("error = %v, want the credential error", st.Error)
	}
	<-tun.sup.Done()
	if service.Reachable(fmt.Sprintf("127.0.0.1:%d", e.ports[0]), 200*time.Millisecond) {
		t.Fatal("forward still listening after the credential failed")
	}
}

func TestMultiForwardDropsTogether(t *testing.T) {
	dieDir := fakeAWS(t)
	e := &fakeEngine{ports: []int{freePort(t), freePort(t)}}
	tun := newTunnel(e, false)

	if _, err := tun.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tun.Stop(context.Background()) }) //nolint:errcheck
	waitStatus(t, tun, service.StatusConnected)
	for _, p := range e.ports {
		if !service.Reachable(fmt.Sprintf("127.0.0.1:%d", p), time.Second) {
			t.Fatalf("forward on :%d not listening", p)
		}
	}

	// Losing the second forward must end the connection and stop the first.
	killSession(t, dieDir, e.ports[1])
	waitStatus(t, tun, service.StatusError)
	<-tun.sup.Done()
	if service.Reachable(fmt.Sprintf("127.0.0.1:%d", e.ports[0]), 200*time.Millisecond) {
		t.Fatal("first forward still listening after the second dropped")
	}
}

func TestStopReturnsToIdle(t *testing.T) {
	fakeAWS(t)
	e := &fakeEngine{ports: []int{freePort(t)}}
	tun := newTunnel(e, false)

	if _, err := tun.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, tun, service.StatusConnected)

	if err := tun.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	st := waitStatus(t, tun, service.StatusIdle)
	if st.LocalPort != e.ports[0] {
		t.Fatalf("idle LocalPort = %d, want %d", st.LocalPort, e.ports[0])
	}
	// Stop waits for the connection loop, so by now it has exited and cannot
	// overwrite Idle with an error from the teardown.
	select {
	case <-tun.done:
	default:
		t.Fatal("Stop returned while the connection loop was still running")
	}
	if st := tun.State(); st.Status != service.StatusIdle {
		t.Fatalf("status after teardown = %v (err %v), want idle", st.Status, st.Error)
	}
}

// A Stop during the retry pause must not wait out the pause: the loop has to
// notice the cancellation straight away.
func TestStopDuringRetryIsPrompt(t *testing.T) {
	dieDir := fakeAWS(t)
	e := &fakeEngine{ports: []int{freePort(t)}}
	if err := os.WriteFile(filepath.Join(dieDir, fmt.Sprint(e.ports[0])+".nolisten"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tun := newTunnel(e, false)
	if _, err := tun.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Wait until the first attempt has failed (the port gets the transport's
	// whole start timeout) and the loop is pausing.
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(strings.Join(tun.Logs(), "\n"), "not open (attempt 1/") {
		if time.Now().After(deadline) {
			t.Fatalf("first attempt never failed the port check\nlogs:\n%s", strings.Join(tun.Logs(), "\n"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	start := time.Now()
	if err := tun.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("Stop took %v; it should not wait out the retry pause", took)
	}
	if st := tun.State(); st.Status != service.StatusIdle {
		t.Fatalf("status after Stop = %v (err %v), want idle", st.Status, st.Error)
	}
}

// Every transport runs through the same lifecycle; ssh and kubectl print
// nothing an SSM-style ready check would match, so readiness is the port.
func TestSSHAndKubectlTransportsConnect(t *testing.T) {
	for _, tc := range []struct {
		bin  string
		spec *config.TransportSpec
	}{
		{"ssh", &config.TransportSpec{Type: config.TransportSSH, Host: "bastion.example.com"}},
		{"kubectl", &config.TransportSpec{Type: config.TransportKubectl, Resource: "svc/proxy"}},
	} {
		t.Run(tc.bin, func(t *testing.T) {
			dieDir := fakeBinary(t, tc.bin)
			e := &fakeEngine{ports: []int{freePort(t)}}
			tun := New(Options{Name: "test", Environment: "int", Env: config.EnvMapping{Transport: tc.spec}}, e)
			if _, err := tun.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { tun.Stop(context.Background()) }) //nolint:errcheck
			st := waitStatus(t, tun, service.StatusConnected)
			if st.Token != "token-1" {
				t.Fatalf("token = %q", st.Token)
			}
			killSession(t, dieDir, e.ports[0])
			st = waitStatus(t, tun, service.StatusError)
			if !strings.Contains(st.Error.Error(), tc.bin) {
				t.Fatalf("the drop should name the transport: %v", st.Error)
			}
		})
	}
}
