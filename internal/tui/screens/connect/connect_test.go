package connect

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/testkeys"
)

type fakeSvc struct{ name, typ string }

func (f fakeSvc) Info() service.ServiceInfo {
	return service.ServiceInfo{Name: f.name, Type: f.typ}
}
func (fakeSvc) Start(context.Context) (<-chan service.StatusUpdate, error) { return nil, nil }
func (fakeSvc) Stop(context.Context) error                                 { return nil }
func (fakeSvc) RefreshToken(context.Context) error                         { return nil }
func (fakeSvc) ConnectCommand() string                                     { return "" }
func (fakeSvc) State() service.RuntimeState                                { return service.RuntimeState{} }
func (fakeSvc) Logs() []string                                             { return nil }
func (fakeSvc) LogSeq() uint64                                             { return 0 }

func TestFilterLightsUpTheMatch(t *testing.T) {
	m := New([]service.Service{fakeSvc{"orders-db", service.TypeRDS}, fakeSvc{"cache", service.TypeRedis}}, 100, 20)
	m, _ = m.Update(testkeys.Press("/"), true)
	for _, r := range "ORD" {
		m, _ = m.Update(testkeys.Press(string(r)), true)
	}
	v := m.View(100, 20, true)
	if !strings.Contains(v, components.MatchStyle.Render("ord")) || strings.Contains(ansi.Strip(v), "cache") {
		t.Fatalf("the filter should keep orders-db with its match lit up:\n%q", v)
	}
}

// A refused login is what the row has to say: a token counting down would
// read as a working connection.
func TestRowSaysTheLoginFailed(t *testing.T) {
	expires := time.Now().Add(14 * time.Minute)
	st := service.RuntimeState{Status: service.StatusConnected, TokenExpires: &expires, Login: service.LoginFailed, LoginErr: "FATAL: refused"}
	if got := ansi.Strip(statusExtra(st)); got != "connected · login failed" {
		t.Fatalf("statusExtra = %q, want the refused login", got)
	}
	st.Login = service.LoginOK
	if got := ansi.Strip(statusExtra(st)); !strings.HasPrefix(got, "token: 1") {
		t.Fatalf("statusExtra = %q after a login that worked, want the countdown", got)
	}
}
