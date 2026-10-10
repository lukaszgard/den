// Package keepassxc reads entries of a KeePass database through keepassxc-cli.
//
// There is no login session to reuse, so the pane asks for the master
// password and hands it to ReadWithPassword. It reaches keepassxc-cli on
// stdin only, never argv or the environment, and is kept in memory afterwards
// only if the entry sets remember_password, and only for that long.
package keepassxc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/secrets"
)

// DefaultAttributes are shown when an entry does not list its own.
var DefaultAttributes = []string{"UserName", "Password", "URL", "Notes"}

// macCLI is where the KeePassXC app bundle keeps its CLI, which the macOS
// installer does not put on PATH.
const macCLI = "/Applications/KeePassXC.app/Contents/MacOS/keepassxc-cli"

// readTimeout bounds one read. Each keepassxc-cli call re-derives the key,
// which is deliberately slow, and a multi-line value costs one call per
// attribute.
const readTimeout = 60 * time.Second

const maxErrLen = 300

// Item is one entry of a KeePass database.
type Item struct {
	spec     config.KeePassXCSecretSpec
	db       string // spec.Database with ~ expanded
	keyFile  string // spec.KeyFile with ~ expanded
	remember time.Duration
}

var (
	_ secrets.Item     = (*Item)(nil)
	_ secrets.Unlocker = (*Item)(nil)
	_ secrets.Locker   = (*Item)(nil)
	_ secrets.Toggler  = (*Item)(nil)
	_ secrets.Browser  = (*Item)(nil)
)

// New builds an item from its config entry. remember_password was validated
// when the config loaded, so a parse error here just means "don't remember".
func New(spec config.KeePassXCSecretSpec) *Item {
	it := &Item{spec: spec, db: platform.ExpandHome(spec.Database), keyFile: platform.ExpandHome(spec.KeyFile)}
	if spec.RememberPassword != "" {
		if d, err := time.ParseDuration(spec.RememberPassword); err == nil && d > 0 {
			it.remember = d
		}
	}
	return it
}

func (it *Item) Name() string        { return it.spec.Name }
func (it *Item) Description() string { return it.spec.Description }
func (it *Item) Provider() string    { return "keepassxc" }
func (it *Item) Location() string {
	switch {
	case it.spec.Entry != "":
		return it.spec.Database + " › " + it.spec.Entry
	case it.spec.Group != "":
		return it.spec.Database + " › " + strings.Trim(it.spec.Group, "/") + "/"
	}
	return it.spec.Database
}
func (it *Item) AutoReveal() bool { return it.spec.AutoReveal }

func (it *Item) Details() []secrets.Detail {
	d := []secrets.Detail{
		{Label: "Database", Value: it.spec.Database},
	}
	switch {
	case it.spec.Entry != "":
		d = append(d, secrets.Detail{Label: "Entry", Value: it.spec.Entry})
	case it.spec.Group != "":
		d = append(d, secrets.Detail{Label: "Entries", Value: "every entry under " + strings.Trim(it.spec.Group, "/") + "/"})
	default:
		d = append(d, secrets.Detail{Label: "Entries", Value: "every entry in the database"})
	}
	if it.spec.KeyFile != "" {
		d = append(d, secrets.Detail{Label: "Key file", Value: it.spec.KeyFile})
	}
	switch {
	case it.spec.NoPassword:
		d = append(d, secrets.Detail{Label: "Password", Value: "none (key file only)"})
	case it.remember > 0:
		d = append(d, secrets.Detail{Label: "Password", Value: "remembered for " + it.spec.RememberPassword})
	default:
		d = append(d, secrets.Detail{Label: "Password", Value: "asked every time"})
	}
	return d
}

func (it *Item) Toggles() []secrets.Toggle {
	return []secrets.Toggle{{Label: "Remember password", On: it.remember > 0}}
}

// Probe checks the CLI, the database and the key file. It never opens the
// database.
func (it *Item) Probe() secrets.Status {
	if _, err := it.cli(); errors.Is(err, errNotInstalled) {
		return secrets.NotInstalled("keepassxc-cli")
	} else if err != nil {
		return secrets.Status{Reason: err.Error()}
	}
	if _, err := os.Stat(it.db); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return secrets.Status{Reason: "database not found"}
		}
		return secrets.Status{Reason: err.Error()}
	}
	if it.keyFile != "" {
		if _, err := os.Stat(it.keyFile); err != nil {
			return secrets.Status{Reason: "key file not found"}
		}
	}
	return secrets.Status{Ready: true}
}

// cli finds keepassxc-cli: cli_path, then PATH, then the macOS app bundle.
func (it *Item) cli() (string, error) {
	if it.spec.CLIPath != "" {
		p := platform.ExpandHome(it.spec.CLIPath)
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("cli_path not found")
		}
		return p, nil
	}
	if p, err := exec.LookPath("keepassxc-cli"); err == nil {
		return p, nil
	}
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat(macCLI); err == nil {
			return macCLI, nil
		}
	}
	return "", errNotInstalled
}

var errNotInstalled = errors.New("keepassxc-cli not installed")

// Tools is what den doctor checks for this item: keepassxc-cli, unless
// cli_path points at it (Probe checks that path).
func (it *Item) Tools() []string {
	if it.spec.CLIPath != "" {
		return nil
	}
	return []string{"keepassxc-cli"}
}

// NeedsPassword reports whether the pane must ask before reading.
func (it *Item) NeedsPassword() bool {
	if it.spec.NoPassword {
		return false
	}
	pw, ok := cached(it.cacheKey())
	clear(pw)
	return !ok
}

func (it *Item) PasswordPrompt() string {
	p := "Master password for " + filepath.Base(it.db)
	if it.remember > 0 {
		p += " (remembered for " + it.spec.RememberPassword + ")"
	}
	return p
}

// Read uses the remembered password, or none for a key-file-only database.
func (it *Item) Read(ctx context.Context) ([]secrets.Field, error) {
	return it.ReadEntry(ctx, it.spec.Entry, nil)
}

// ReadWithPassword reads with pw and, if the item says so, remembers a copy.
// The caller owns pw and clears it.
func (it *Item) ReadWithPassword(ctx context.Context, pw []byte) ([]secrets.Field, error) {
	return it.ReadEntry(ctx, it.spec.Entry, pw)
}

// Browsing reports whether the item is a whole database (or group) to pick
// entries from, because no entry is configured.
func (it *Item) Browsing() bool { return it.spec.Entry == "" }

// ListEntries lists the entry paths in the database, or under group, in the
// database's own order. Groups, empty-group markers and the Recycle Bin are
// left out.
func (it *Item) ListEntries(ctx context.Context, pw []byte) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	group := strings.Trim(it.spec.Group, "/")
	pos := []string{it.db}
	if group != "" {
		pos = append(pos, group)
	}
	var entries []string
	err := it.withPassword(pw, func(pw []byte) error {
		out, err := it.run(ctx, pw, "ls", []string{"-R", "-f"}, pos...)
		if err != nil {
			return err
		}
		entries = parseList(out, group)
		return nil
	})
	return entries, err
}

// parseList keeps the entries of `ls -R -f` output. Paths under a group come
// back relative to it, so the group is put in front to get paths show accepts.
func parseList(out, group string) []string {
	var entries []string
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasSuffix(line, "/") || line == "[empty]" || strings.HasSuffix(line, "/[empty]") {
			continue
		}
		if group != "" {
			line = group + "/" + line
		}
		// KeePassXC's default name for the bin; deleted entries are noise here.
		if strings.HasPrefix(line, "Recycle Bin/") {
			continue
		}
		entries = append(entries, line)
	}
	return entries
}

// ReadEntry reads one entry. pw is used (and remembered, if configured) when
// given; otherwise the remembered password, or none for no_password.
func (it *Item) ReadEntry(ctx context.Context, entry string, pw []byte) ([]secrets.Field, error) {
	var fields []secrets.Field
	err := it.withPassword(pw, func(pw []byte) error {
		var err error
		fields, err = it.read(ctx, entry, pw)
		return err
	})
	return fields, err
}

// withPassword runs fn with the right password: none for a key-file-only
// database, pw when given (remembered on success if configured), or the
// remembered one, which is forgotten if it no longer works.
func (it *Item) withPassword(pw []byte, fn func(pw []byte) error) error {
	switch {
	case it.spec.NoPassword:
		return fn(nil)
	case pw != nil:
		err := fn(pw)
		if err == nil && it.remember > 0 {
			store(it.cacheKey(), pw, it.remember)
		}
		return err
	}
	remembered, ok := cached(it.cacheKey())
	if !ok {
		return fmt.Errorf("keepassxc: %w", secrets.ErrLocked)
	}
	defer clear(remembered)
	err := fn(remembered)
	if errors.Is(err, secrets.ErrBadPassword) {
		forget(it.cacheKey()) // the database password changed under us
	}
	return err
}

// Lock forgets every remembered KeePassXC password, for all databases.
func (it *Item) Lock() { lockAll() }

func (it *Item) attributes() []string {
	if len(it.spec.Attributes) > 0 {
		return it.spec.Attributes
	}
	return DefaultAttributes
}

// read fetches every attribute in one keepassxc-cli call. Values come one per
// line, so a multi-line value (usually Notes) makes the count disagree; then
// each attribute is fetched on its own, which is exact but re-derives the key
// once per attribute.
func (it *Item) read(ctx context.Context, entry string, pw []byte) ([]secrets.Field, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()

	attrs := it.attributes()
	var args []string
	for _, a := range attrs {
		args = append(args, "-a", a)
	}
	if it.spec.TOTP {
		args = append(args, "-t") // printed after the attributes
	}
	out, err := it.show(ctx, pw, entry, args...)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	want := len(attrs)
	if it.spec.TOTP {
		want++
	}
	if len(lines) == want {
		fields := make([]secrets.Field, 0, want)
		for i, a := range attrs {
			fields = append(fields, secrets.Field{Key: a, Value: lines[i]})
		}
		if it.spec.TOTP {
			fields = append(fields, secrets.Field{Key: "TOTP", Value: lines[len(lines)-1]})
		}
		return fields, nil
	}

	fields := make([]secrets.Field, 0, want)
	for _, a := range attrs {
		v, err := it.show(ctx, pw, entry, "-a", a)
		if err != nil {
			return nil, err
		}
		fields = append(fields, secrets.Field{Key: a, Value: strings.TrimSuffix(v, "\n")})
	}
	if it.spec.TOTP {
		v, err := it.show(ctx, pw, entry, "-t")
		if err != nil {
			return nil, err
		}
		fields = append(fields, secrets.Field{Key: "TOTP", Value: strings.TrimSuffix(v, "\n")})
	}
	return fields, nil
}

// show runs `keepassxc-cli show -s ... <db> <entry>`.
func (it *Item) show(ctx context.Context, pw []byte, entry string, extra ...string) (string, error) {
	return it.run(ctx, pw, "show", append([]string{"-s"}, extra...), it.db, entry)
}

// run runs a keepassxc-cli subcommand with pw on stdin. -q is deliberately not
// used: it silences errors too, leaving a wrong password indistinguishable
// from any other failure. The prompt goes to stderr, so stdout still carries
// only the output.
func (it *Item) run(ctx context.Context, pw []byte, sub string, flags []string, positional ...string) (string, error) {
	cli, err := it.cli()
	if err != nil {
		return "", err
	}
	args := []string{sub}
	if it.keyFile != "" {
		args = append(args, "-k", it.keyFile)
	}
	if it.spec.NoPassword {
		args = append(args, "--no-password")
	}
	args = append(args, flags...)
	args = append(args, positional...)

	cmd := exec.CommandContext(ctx, cli, args...)
	var stdin []byte
	if !it.spec.NoPassword {
		stdin = make([]byte, 0, len(pw)+1)
		stdin = append(append(stdin, pw...), '\n')
	}
	defer clear(stdin)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	raw := stdout.Bytes()
	defer clear(raw)
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("keepassxc: timed out after %s", readTimeout)
		}
		return "", it.describe(stderr.String(), runErr)
	}
	return string(raw), nil
}

// describe turns keepassxc-cli's stderr into one line, without the password
// prompt it always prints first.
func (it *Item) describe(stderr string, runErr error) error {
	msg := strings.Replace(stderr, "Enter password to unlock "+it.db+": ", "", 1)
	if strings.Contains(msg, "Invalid credentials") {
		return fmt.Errorf("keepassxc: %w", secrets.ErrBadPassword)
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if msg == "" {
		return fmt.Errorf("keepassxc: %w", runErr)
	}
	if r := []rune(msg); len(r) > maxErrLen {
		msg = string(r[:maxErrLen-1]) + "…"
	}
	return errors.New("keepassxc: " + msg)
}

func (it *Item) cacheKey() string { return it.db + "\x00" + it.keyFile }

// ── Password cache ────────────────────────────────────────────────────────────
// Shared by every item of the same database (and key file), so unlocking one
// entry unlocks its neighbours for the same period.

type remembered struct {
	pw      []byte
	expires time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*remembered{}
	now     = time.Now // swapped in tests
)

// cached returns a copy of the remembered password, which the caller clears.
func cached(key string) ([]byte, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	r, ok := cache[key]
	if !ok {
		return nil, false
	}
	if !now().Before(r.expires) {
		clear(r.pw)
		delete(cache, key)
		return nil, false
	}
	return bytes.Clone(r.pw), true
}

// store remembers a copy of pw for ttl. Unlocking again does not extend an
// existing period beyond ttl from now.
func store(key string, pw []byte, ttl time.Duration) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if old, ok := cache[key]; ok {
		clear(old.pw)
	}
	cache[key] = &remembered{pw: bytes.Clone(pw), expires: now().Add(ttl)}
}

func forget(key string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if r, ok := cache[key]; ok {
		clear(r.pw)
		delete(cache, key)
	}
}

func lockAll() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	for k, r := range cache {
		clear(r.pw)
		delete(cache, k)
	}
}
