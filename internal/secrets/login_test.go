package secrets

import (
	"strings"
	"testing"
)

func TestLoginTakesTheNamedKeys(t *testing.T) {
	fields := []Field{
		{Key: "username", Value: "app"},
		{Key: "password", Value: "s3cret"},
		{Key: "db.user", Value: "nested-user"},
		{Key: "db.pass", Value: "nested-pass"},
	}
	cases := []struct {
		name                     string
		usernameKey, passwordKey string
		wantUser, wantPass       string
	}{
		{"default keys, as AWS stores a database secret", "", "", "app", "s3cret"},
		{"dotted keys of a nested file", "db.user", "db.pass", "nested-user", "nested-pass"},
		{"one default, one named", "", "db.pass", "app", "nested-pass"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			user, pass, err := Login(fields, c.usernameKey, c.passwordKey)
			if err != nil {
				t.Fatal(err)
			}
			if user != c.wantUser || pass != c.wantPass {
				t.Errorf("Login = %q, %q, want %q, %q", user, pass, c.wantUser, c.wantPass)
			}
		})
	}
}

// A secret may hold only the password: the service's db_user then names the
// user, so a missing username is for the caller to judge.
func TestLoginWithoutAUsernameKeyIsNotAnError(t *testing.T) {
	user, pass, err := Login([]Field{{Key: "password", Value: "s3cret"}}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if user != "" || pass != "s3cret" {
		t.Errorf("Login = %q, %q", user, pass)
	}
}

// The error lands in the service's log and on screen, so it helps by naming
// the keys there are, and must never carry a value.
func TestLoginErrorsNameKeysAndNeverValues(t *testing.T) {
	const value = "VALUE-THAT-MUST-NOT-LEAK"
	cases := []struct {
		name    string
		fields  []Field
		wantErr []string
	}{
		{
			name:    "no such key",
			fields:  []Field{{Key: "user", Value: value}, {Key: "pass", Value: value}},
			wantErr: []string{`"password"`, "user", "pass"},
		},
		{
			name:    "empty password",
			fields:  []Field{{Key: "username", Value: value}, {Key: "password", Value: ""}},
			wantErr: []string{`"password"`, "empty"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := Login(c.fields, "", "")
			if err == nil {
				t.Fatal("want an error")
			}
			for _, want := range c.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %s", err, want)
				}
			}
			if strings.Contains(err.Error(), value) {
				t.Errorf("error %q carries a value", err)
			}
		})
	}
}
