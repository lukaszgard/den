package service

import "testing"

// passwordFake says what a Tunnel says: every tunnel can be asked, and only one
// with a password login answers yes.
type passwordFake struct {
	plainSvc
	login PasswordLogin
	has   bool
}

func (f passwordFake) PasswordLogin() (PasswordLogin, bool) { return f.login, f.has }

// Views and agents decide on this whether to speak of a token or a password, so
// a tunnel logging in with a token must not count as having a password.
func TestPasswordLoginOf(t *testing.T) {
	want := PasswordLogin{Secret: "orders-login", Provider: "sops"}
	cases := []struct {
		name   string
		svc    Service
		want   PasswordLogin
		wantOK bool
	}{
		{"a service that cannot have one", plainSvc{}, PasswordLogin{}, false},
		{"a tunnel logging in with a token", passwordFake{}, PasswordLogin{}, false},
		{"a tunnel logging in with a password", passwordFake{login: want, has: true}, want, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := PasswordLoginOf(c.svc)
			if got != c.want || ok != c.wantOK {
				t.Errorf("PasswordLoginOf = %+v, %v, want %+v, %v", got, ok, c.want, c.wantOK)
			}
		})
	}
}
