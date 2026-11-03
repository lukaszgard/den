package service

import (
	"encoding/json"
	"strings"
	"testing"
)

type loginSvc struct {
	plainSvc
	st RuntimeState
}

func (s loginSvc) State() RuntimeState { return s.st }

// A refused login is the one thing about a connected service that a user or
// an agent must act on, so the summary carries it; the other outcomes are not
// worth a field, and an old verdict must not outlive its state.
func TestSummaryCarriesARefusedLogin(t *testing.T) {
	failed := Summarize(loginSvc{st: RuntimeState{Status: StatusConnected, Login: LoginFailed, LoginErr: "FATAL: refused"}})
	if failed.LoginError != "FATAL: refused" {
		t.Fatalf("LoginError = %q, want the database's answer", failed.LoginError)
	}
	b, _ := json.Marshal(failed)
	if !strings.Contains(string(b), `"login_error":"FATAL: refused"`) {
		t.Errorf("JSON = %s, want login_error", b)
	}

	for _, st := range []RuntimeState{
		{Status: StatusConnected, Login: LoginOK},
		{Status: StatusConnected, Login: LoginChecking, LoginErr: "FATAL: an older answer"},
		{Status: StatusConnected},
	} {
		sum := Summarize(loginSvc{st: st})
		if b, _ := json.Marshal(sum); strings.Contains(string(b), "login_error") {
			t.Errorf("login %v: JSON = %s, want no login_error", st.Login, b)
		}
	}
}
