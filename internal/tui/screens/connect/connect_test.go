package connect

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/lukaszgard/den/internal/service"
)

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
