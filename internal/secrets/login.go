package secrets

import (
	"cmp"
	"fmt"
	"strings"
)

// maxKeysInError keeps the error of a big file to one readable line.
const maxKeysInError = 10

// Login picks a database login out of a secret's fields. Keys are the dotted
// ones of the flat view (db.password), and empty ones mean username and
// password: what AWS stores for a database, so a secret RDS manages works as
// it is. A secret without the username key gives an empty username, since the
// service may name the user itself.
//
// Errors name keys and never values: they reach the service's log and the
// screen.
func Login(fields []Field, usernameKey, passwordKey string) (username, password string, err error) {
	usernameKey = cmp.Or(usernameKey, "username")
	passwordKey = cmp.Or(passwordKey, "password")
	found := false
	keys := make([]string, 0, len(fields))
	for _, f := range fields {
		keys = append(keys, f.Key)
		if f.Key == usernameKey {
			username = f.Value
		}
		if f.Key == passwordKey {
			password, found = f.Value, true
		}
	}
	if !found {
		return "", "", fmt.Errorf("no key %q (it has: %s)", passwordKey, keyList(keys))
	}
	if password == "" {
		return "", "", fmt.Errorf("key %q is empty", passwordKey)
	}
	return username, password, nil
}

func keyList(keys []string) string {
	if len(keys) == 0 {
		return "no keys"
	}
	if len(keys) > maxKeysInError {
		return strings.Join(keys[:maxKeysInError], ", ") + ", …"
	}
	return strings.Join(keys, ", ")
}
