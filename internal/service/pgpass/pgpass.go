// Package pgpass maintains pgpass entries for services whose credential is a
// short-lived Postgres password (RDS IAM tokens, Redshift temporary
// credentials), so psql picks the current one up without it being pasted. The
// file is the one libpq reads: $PGPASSFILE when set, else
// %APPDATA%\postgresql\pgpass.conf on Windows and ~/.pgpass elsewhere.
package pgpass

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Path is the pgpass file libpq reads on this machine. Empty when neither
// $PGPASSFILE nor a home is known.
func Path() string {
	return path(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func path(goos string, getenv func(string) string, home func() (string, error)) string {
	if f := getenv("PGPASSFILE"); f != "" {
		return f
	}
	// libpq on Windows never reads ~/.pgpass: a token written there leaves psql
	// asking for a password.
	if goos == "windows" {
		if appData := getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "postgresql", "pgpass.conf")
		}
	}
	h, err := home()
	if err != nil || h == "" {
		return ""
	}
	if goos == "windows" {
		return filepath.Join(h, "AppData", "Roaming", "postgresql", "pgpass.conf")
	}
	return filepath.Join(h, ".pgpass")
}

// Update writes/replaces the pgpass entry for the given connection in Path.
// It deduplicates by local port — since each service has a unique port, there
// should never be more than one pgpass entry for the same port regardless of
// how dbName or dbUser may have changed over time.
func Update(localPort int, dbName, dbUser, password string) error {
	p := Path()
	if p == "" {
		return errors.New("no home directory for the pgpass file")
	}
	return update(p, localPort, dbName, dbUser, password)
}

func update(path string, localPort int, dbName, dbUser, password string) error {
	var existing []string
	if f, err := os.Open(path); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			existing = append(existing, scanner.Text())
		}
		f.Close()
	}

	// Match by port only — removes stale entries regardless of dbName/dbUser changes.
	portPrefix := fmt.Sprintf("localhost:%d:", localPort)

	var lines []string
	for _, l := range existing {
		if l != "" && !strings.HasPrefix(l, portPrefix) {
			lines = append(lines, l)
		}
	}
	lines = append(lines, Line(localPort, dbName, dbUser, password))

	content := strings.Join(lines, "\n") + "\n"
	// %APPDATA%\postgresql exists only where pgAdmin or the PostgreSQL
	// installer made it.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create pgpass folder: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write pgpass: %w", err)
	}
	// WriteFile only applies the mode on create, not on update — enforce it explicitly
	// so libpq doesn't silently ignore the file due to wrong permissions. On
	// Windows this only clears the read-only bit; libpq checks no mode there.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod pgpass: %w", err)
	}
	return nil
}

// Line formats one pgpass entry. The user is escaped as well as the password:
// Redshift IAM users look like "IAMR:role", and an unescaped colon there would
// shift every following field. An empty dbName becomes the wildcard: libpq reads
// an empty field as the empty name, which no connection asks for.
func Line(localPort int, dbName, dbUser, password string) string {
	if dbName == "" {
		return fmt.Sprintf("localhost:%d:*:%s:%s", localPort, escape(dbUser), escape(password))
	}
	return fmt.Sprintf("localhost:%d:%s:%s:%s", localPort, escape(dbName), escape(dbUser), escape(password))
}

// escape escapes backslashes and colons in a pgpass field value so libpq
// parses them as literals rather than field separators.
func escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `:`, `\:`)
	return s
}
