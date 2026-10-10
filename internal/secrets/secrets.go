// Package secrets defines what the Secrets pane needs from a secret source.
// Providers live in subpackages (sops, ...) and shell out to their vendor CLI,
// so den reuses the user's existing auth and never holds master passwords.
//
// Decrypted values must never reach the log, the disk or a process argv.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/lukaszgard/den/internal/prereq"
)

// Field is one decrypted key/value. Nested documents are flattened to dotted
// keys (db.password, hosts[0]).
type Field struct {
	Key   string
	Value string
	// Path is Key split into the document's own segments, for the tree view:
	// the dotted Key cannot tell a key "client.aacn" from client → aacn. Nil
	// for a field that never nested (a KeePass attribute).
	Path []PathSeg
}

// PathSeg is one step of a Field's path: a map key, or an array position.
type PathSeg struct {
	Key     string
	Index   int
	IsIndex bool // explicit, so a map key "" is not taken for an element
}

// Segments is the field's path, or Key alone when it has none.
func (f Field) Segments() []PathSeg {
	if f.Path == nil {
		return []PathSeg{{Key: f.Key}}
	}
	return f.Path
}

// Status is the result of a cheap readiness check. It never decrypts.
type Status struct {
	Ready  bool
	Reason string // why it is not ready, e.g. "sops not installed"
	// Missing names the CLI that is not installed, when that is the reason.
	Missing string
}

// NotInstalled is the Status of an item whose CLI is missing. The reason
// says how to install it here, so the Secrets pane can do better than
// "not installed".
func NotInstalled(tool string) Status {
	return Status{Reason: tool + " not installed — " + prereq.InstallHint(tool), Missing: tool}
}

// ToolUser is implemented by items that run a CLI den does not ship with,
// so den doctor checks it is installed (and new enough) even while the item
// is ready.
type ToolUser interface {
	Tools() []string
}

// Item is one configured secret source.
type Item interface {
	Name() string
	Description() string
	Provider() string // group heading in the pane, e.g. "sops"
	Location() string // what is read, e.g. the file path
	Details() []Detail
	// AutoReveal reports whether values start in clear rather than masked.
	AutoReveal() bool
	Probe() Status
	Read(ctx context.Context) ([]Field, error)
}

// AWSUser is implemented by items read through an AWS profile (a KMS key,
// Secrets Manager). UsesAWS reports whether the item's Probe depends on an
// AWS login, so that after a login, a logout or an expiry only those items
// are probed again; the rest cannot have changed.
type AWSUser interface {
	UsesAWS() bool
}

// Detail is a non-secret label/value pair shown for the selected item.
type Detail struct {
	Label string
	Value string
}

// Editor is implemented by items the user can edit in place. The command is
// handed the terminal, so the provider's own editor flow (re-encryption
// included) runs unchanged.
type Editor interface {
	EditCmd() *exec.Cmd
	// EditResult interprets the command's exit: whether anything was saved,
	// and the error worth showing, if any.
	EditResult(err error) (changed bool, _ error)
}

// ErrBadPassword is returned (wrapped) by an Unlocker when the password it was
// given is wrong, so the pane can ask again instead of showing a failure.
var ErrBadPassword = errors.New("wrong password")

// ErrLocked means no password was given and none is remembered (any more). It
// matches ErrBadPassword too, so callers that only care about "ask again"
// need one check.
var ErrLocked = fmt.Errorf("%w: locked, password needed", ErrBadPassword)

// Unlocker is implemented by items that need a password den must ask for,
// such as a KeePass master password. The pane prompts only when
// NeedsPassword is true; Read then uses whatever the item remembered.
type Unlocker interface {
	NeedsPassword() bool
	PasswordPrompt() string
	// ReadWithPassword reads with pw, which the caller clears afterwards; the
	// item copies it if it is configured to remember it.
	ReadWithPassword(ctx context.Context, pw []byte) ([]Field, error)
}

// Locker is implemented by items that can forget remembered passwords.
type Locker interface {
	Lock()
}

// Viewer is implemented by items whose values can nest (a YAML file, a JSON
// secret). TreeView reports whether they open as a tree rather than a list of
// dotted keys; items without it open flat.
type Viewer interface {
	TreeView() bool
}

// Toggle is an on/off setting shown as a dot in the details box, like the
// VPN and Tunnel toggles.
type Toggle struct {
	Label string
	On    bool
}

// Toggler is implemented by items with settings worth a toggle dot beyond
// Auto reveal, which every item has.
type Toggler interface {
	Toggles() []Toggle
}

// Browser is implemented by items that stand for a set of entries (a whole
// KeePass database) rather than one secret. The pane lists the entries and
// opens one at a time. pw is nil when the item needs none or remembers one.
type Browser interface {
	Browsing() bool
	ListEntries(ctx context.Context, pw []byte) ([]string, error)
	ReadEntry(ctx context.Context, entry string, pw []byte) ([]Field, error)
}

// LoginCommander is implemented by items whose login den can run for the
// user (for example `vault login -method=oidc`). LoginCmd returns nil when no
// login is configured. The command is handed the terminal.
type LoginCommander interface {
	LoginCmd() *exec.Cmd
}
