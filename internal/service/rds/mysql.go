package rds

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The mysql client has no ~/.pgpass equivalent keyed by host and port, so den
// keeps one option file per service and points the client at it with
// --defaults-extra-file. Rewriting it on every refresh means a new mysql
// session always picks up the current token.

// mysqlOptionFile is where den keeps the option file for the service on localPort.
func mysqlOptionFile(localPort int) string {
	dir := filepath.Join(".config", "den", "mysql")
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, dir)
	}
	return filepath.Join(dir, fmt.Sprintf("%d.cnf", localPort))
}

// mysqlOptions renders the option file. The password is double-quoted because
// '#' starts a comment anywhere on an option-file line; IAM tokens contain
// neither quotes nor backslashes, the only characters quoting would change.
func mysqlOptions(token string) string {
	return fmt.Sprintf("[client]\npassword=\"%s\"\n", token)
}

// writeMySQLOptionFile writes the token to path, readable by the owner only.
func writeMySQLOptionFile(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mysql option file: %w", err)
	}
	if err := os.WriteFile(path, []byte(mysqlOptions(token)), 0o600); err != nil {
		return fmt.Errorf("mysql option file: %w", err)
	}
	// WriteFile keeps the mode of an existing file; the file holds a live
	// credential, so enforce it.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("mysql option file: %w", err)
	}
	return nil
}

// mysqlArgs builds the mysql client's arguments. Three details are load-bearing:
//   - --defaults-extra-file must be the first argument or mysql rejects it.
//   - -h 127.0.0.1, not localhost: the mysql client treats "localhost" as
//     "use the Unix socket" and never touches the tunnel.
//   - --enable-cleartext-plugin: RDS IAM auth sends the token through the
//     cleartext plugin, which the client refuses to use unless told to.
//
// optionFile and password are mutually exclusive ways to supply the token.
func mysqlArgs(optionFile string, localPort int, user, dbName, password string) []string {
	var args []string
	if optionFile != "" {
		args = append(args, "--defaults-extra-file="+optionFile)
	}
	args = append(args,
		"-h", "127.0.0.1",
		"-P", fmt.Sprint(localPort),
		"-u", user,
		"--enable-cleartext-plugin",
		"--ssl-mode=REQUIRED",
	)
	if password != "" {
		args = append(args, "--password='"+password+"'")
	}
	if dbName != "" {
		args = append(args, dbName)
	}
	return args
}

// mysqlCommand is the mysql invocation as a line to copy into a shell.
func mysqlCommand(optionFile string, localPort int, user, dbName, password string) string {
	return "mysql " + strings.Join(mysqlArgs(optionFile, localPort, user, dbName, password), " ")
}
