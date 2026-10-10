package ssmfwd

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
)

func TestRefreshDelay(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		exp  time.Time
		want time.Duration
	}{
		{"15-minute IAM token refreshes at 13 min", now.Add(15 * time.Minute), 13 * time.Minute},
		{"hour-long credential refreshes 2 min early", now.Add(time.Hour), 58 * time.Minute},
		{"inside the lead window retries soon", now.Add(time.Minute), minRefreshDelay},
		{"already expired retries soon", now.Add(-time.Minute), minRefreshDelay},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := refreshDelay(c.exp, now); got != c.want {
				t.Fatalf("refreshDelay = %v, want %v", got, c.want)
			}
		})
	}
}

// The note engines log with a minted credential names the time the refresh
// loop will renew it, so a user reading the log knows when the next token comes.
func TestRenewalNote(t *testing.T) {
	now := time.Date(2026, 1, 1, 14, 28, 13, 0, time.Local)
	cases := []struct {
		name string
		exp  time.Time
		want string
	}{
		{"15-minute IAM token", now.Add(15 * time.Minute), "expires 14:43:13, auto-refresh at 14:41:13"},
		{"inside the lead window", now.Add(time.Minute), "expires 14:29:13, auto-refresh at 14:28:43"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := renewalNote(c.exp, now); got != c.want {
				t.Fatalf("renewalNote = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSSMArgs(t *testing.T) {
	env := config.EnvMapping{
		EC2InstanceID: "i-0123",
		AWSProfile:    "devops",
		AWSRegionCode: "eu-central-1",
	}
	f := Forward{RemoteHost: "db.internal", RemotePort: 5432, LocalPort: 50110}

	want := []string{
		"ssm", "start-session",
		"--region", "eu-central-1",
		"--target", "i-0123",
		"--document-name", "AWS-StartPortForwardingSessionToRemoteHost",
		"--parameters", "host=db.internal,portNumber=5432,localPortNumber=50110",
		"--profile", "devops",
	}
	if got := ssmArgs(env, f); !slices.Equal(got, want) {
		t.Fatalf("ssmArgs =\n%q\nwant\n%q", got, want)
	}
}

// termEngine is a fakeEngine whose client den can run in a terminal tab.
type termEngine struct{ *fakeEngine }

func (e termEngine) TerminalCommand(secret string) service.ClientCommand {
	return service.ClientCommand{
		Tool: "client",
		Args: []string{"-p", fmt.Sprint(e.ports[0])},
		Env:  []string{"CLIENT_PASSWORD=" + secret},
	}
}

// A terminal opened before the tunnel is up would start a client with no
// port to reach and no credential to send.
func TestTerminalCommandNeedsAConnectedTunnel(t *testing.T) {
	tun := New(Options{Name: "db"}, termEngine{&fakeEngine{ports: []int{50111}}})
	opener, ok := service.Terminal(tun)
	if !ok {
		t.Fatal("a tunnel whose engine has a terminal command should offer a terminal")
	}

	for _, status := range []service.Status{service.StatusIdle, service.StatusConnecting,
		service.StatusReconnecting, service.StatusError, service.StatusStopping} {
		tun.setState(service.RuntimeState{Status: status, Token: "stale"})
		if cmd, err := opener.TerminalCommand(); err == nil {
			t.Errorf("TerminalCommand while %s = %+v, want an error", status, cmd)
		}
	}

	tun.setState(service.RuntimeState{Status: service.StatusConnected, Token: "tok-1"})
	cmd, err := opener.TerminalCommand()
	if err != nil {
		t.Fatalf("TerminalCommand while connected: %v", err)
	}
	want := service.ClientCommand{Tool: "client", Args: []string{"-p", "50111"}, Env: []string{"CLIENT_PASSWORD=tok-1"}}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("TerminalCommand = %+v, want %+v", cmd, want)
	}
}

func TestNoTerminalWithoutAnEngineCommand(t *testing.T) {
	tun := New(Options{Name: "cache"}, &fakeEngine{ports: []int{50223}})
	if _, ok := service.Terminal(tun); ok {
		t.Fatal("a tunnel whose engine has no terminal command offers a terminal")
	}
	if _, err := tun.TerminalCommand(); err == nil {
		t.Fatal("TerminalCommand without an engine command returned no error")
	}
}
