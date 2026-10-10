package service

import (
	"context"
	"testing"
)

// plainSvc has no optional capability.
type plainSvc struct{}

func (plainSvc) Info() ServiceInfo                                  { return ServiceInfo{Name: "plain"} }
func (plainSvc) Start(context.Context) (<-chan StatusUpdate, error) { return nil, nil }
func (plainSvc) Stop(context.Context) error                         { return nil }
func (plainSvc) RefreshToken(context.Context) error                 { return nil }
func (plainSvc) ConnectCommand() string                             { return "client" }
func (plainSvc) State() RuntimeState                                { return RuntimeState{} }
func (plainSvc) Logs() []string                                     { return nil }
func (plainSvc) LogSeq() uint64                                     { return 0 }

// capableSvc has the methods of every capability and answers has for each,
// the way a tunnel does: it has them for any engine, and says no for an
// engine that lacks the behaviour.
type capableSvc struct {
	plainSvc
	has bool
}

func (c capableSvc) HoldsToken() bool                 { return c.has }
func (c capableSvc) HasSecretCommand() bool           { return c.has }
func (c capableSvc) ConnectCommandWithSecret() string { return "client --password=tok" }
func (c capableSvc) HasTerminal() bool                { return c.has }
func (c capableSvc) TerminalCommand() (ClientCommand, error) {
	return ClientCommand{Tool: "client"}, nil
}

// The TUI offers a key when these helpers say yes. Having the methods is not
// enough: a tunnel has them for every engine, so a helper that only checked
// the type would offer t, Y and r on services that cannot honour them.
func TestCapabilityHelpersAskTheService(t *testing.T) {
	cases := []struct {
		name string
		svc  Service
		want bool
	}{
		{"a service without the methods", plainSvc{}, false},
		{"a service with the methods that says no", capableSvc{has: false}, false},
		{"a service with the methods that says yes", capableSvc{has: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HoldsToken(tc.svc); got != tc.want {
				t.Errorf("HoldsToken = %v, want %v", got, tc.want)
			}
			sc, ok := SecretCommand(tc.svc)
			if ok != tc.want || (sc != nil) != tc.want {
				t.Errorf("SecretCommand = %v, %v, want ok=%v", sc, ok, tc.want)
			}
			to, ok := Terminal(tc.svc)
			if ok != tc.want || (to != nil) != tc.want {
				t.Errorf("Terminal = %v, %v, want ok=%v", to, ok, tc.want)
			}
		})
	}
}
