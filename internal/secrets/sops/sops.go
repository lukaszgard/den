// Package sops reads and edits SOPS-encrypted files through the sops CLI.
// sops finds the key (KMS, age, PGP) in the file's own metadata, so den only
// supplies the AWS profile a KMS key needs.
package sops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/secrets"
)

// readTimeout bounds a decrypt. KMS normally answers in well under a second;
// a hang usually means a PGP key waiting on a pinentry den cannot show.
const readTimeout = 30 * time.Second

// maxErrLen caps the sops error shown in the pane.
const maxErrLen = 300

// Item is one SOPS-encrypted file.
type Item struct {
	spec config.SOPSSecretSpec
	path string             // spec.File with ~ expanded
	aws  *secrets.AWSLogins // checks the AWS profile of a KMS key; nil skips the check

	// memo is the file's key metadata as last read. Probe runs on the UI
	// goroutine and in the pane's background re-check, hence the lock.
	mu       sync.Mutex
	memo     keyMemo
	keyReads int // how often the file was parsed
}

// keyMemo remembers readMasterKeys for one state of the file.
type keyMemo struct {
	read      bool
	size      int64
	mod       time.Time
	keys      masterKeys
	found, ok bool
}

var (
	_ secrets.Item    = (*Item)(nil)
	_ secrets.Editor  = (*Item)(nil)
	_ secrets.Viewer  = (*Item)(nil)
	_ secrets.AWSUser = (*Item)(nil)
)

// New builds an item from its config entry. aws is the config's shared view
// of the AWS logins, used to check aws_profile; nil skips that check.
func New(spec config.SOPSSecretSpec, aws *secrets.AWSLogins) *Item {
	return &Item{spec: spec, path: platform.ExpandHome(spec.File), aws: aws}
}

func (it *Item) Name() string        { return it.spec.Name }
func (it *Item) Description() string { return it.spec.Description }
func (it *Item) Provider() string    { return "sops" }
func (it *Item) Location() string    { return it.spec.File }
func (it *Item) AutoReveal() bool    { return it.spec.AutoReveal }

// TreeView reports whether the values open as a tree. providers.Items has
// already filled an empty view from secrets.view, so empty here means tree.
func (it *Item) TreeView() bool { return it.spec.View != config.SecretViewFlat }

func (it *Item) Details() []secrets.Detail {
	profile := it.spec.AWSProfile
	if profile == "" {
		profile = "(inherited from the environment)"
	}
	d := []secrets.Detail{
		{Label: "File", Value: it.spec.File},
		{Label: "AWS profile", Value: profile},
	}
	if it.spec.AWSRegion != "" {
		d = append(d, secrets.Detail{Label: "AWS region", Value: it.spec.AWSRegion})
	}
	return d
}

// Tools is what den doctor checks for this item: sops, and gpg when a PGP
// key is the only way into the file.
func (it *Item) Tools() []string {
	k, found, ok := it.masterKeys()
	if ok && found && len(k.PGP) > 0 && len(k.Age)+len(k.KMS)+len(k.Other) == 0 {
		return []string{"sops", "gpg"}
	}
	return []string{"sops"}
}

// Probe checks that sops is installed, the file exists, and that one of the
// keys the file is encrypted to is usable here: an AWS profile with a live
// SSO login for KMS, a matching age key, a PGP secret key in the keyring.
// It reads the file's plaintext metadata only. Failures only a decrypt can
// find (KMS permissions, a revoked key) are left to Read.
func (it *Item) Probe() secrets.Status {
	if _, err := exec.LookPath("sops"); err != nil {
		return secrets.NotInstalled("sops")
	}
	fi, err := os.Stat(it.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return secrets.Status{Reason: "file not found"}
		}
		return secrets.Status{Reason: err.Error()}
	}
	if keys, found, ok := it.keysOf(fi); ok {
		if !found {
			return secrets.Status{Reason: "not encrypted with sops (no sops metadata in the file)"}
		}
		reason, missing := it.keyProblem(keys)
		switch {
		case missing != "":
			return secrets.NotInstalled(missing)
		case reason != "":
			return secrets.Status{Reason: reason}
		}
		return secrets.Status{Ready: true}
	}
	// A file den cannot parse is left to sops; only the profile is checked.
	if reason := it.aws.Check(it.spec.AWSProfile, time.Now()); reason != "" {
		return secrets.Status{Reason: reason}
	}
	return secrets.Status{Ready: true}
}

// UsesAWS reports whether an AWS login decides if the file can be read: it
// has a KMS key, or den cannot read its keys and checks aws_profile alone.
func (it *Item) UsesAWS() bool {
	keys, _, ok := it.masterKeys()
	if !ok {
		return it.spec.AWSProfile != ""
	}
	return len(keys.KMS) > 0
}

// masterKeys is the key metadata of the file as it is now.
func (it *Item) masterKeys() (keys masterKeys, found, ok bool) {
	fi, err := os.Stat(it.path)
	if err != nil {
		return masterKeys{}, false, false
	}
	return it.keysOf(fi)
}

// keysOf is readMasterKeys, remembered for as long as the file keeps the
// size and mod time of fi. Probing again after an AWS login then costs a
// stat instead of parsing the whole document, and what is kept per file is
// its key IDs, never its content.
func (it *Item) keysOf(fi os.FileInfo) (keys masterKeys, found, ok bool) {
	it.mu.Lock()
	defer it.mu.Unlock()
	if m := it.memo; m.read && m.size == fi.Size() && m.mod.Equal(fi.ModTime()) {
		return m.keys, m.found, m.ok
	}
	keys, found, ok = readMasterKeys(it.path)
	it.memo = keyMemo{read: true, size: fi.Size(), mod: fi.ModTime(), keys: keys, found: found, ok: ok}
	it.keyReads++
	return keys, found, ok
}

// Read decrypts the file to JSON on stdout and flattens it. The plaintext only
// ever lives in this process's memory: nothing is written to disk.
func (it *Item) Read(ctx context.Context) ([]secrets.Field, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sops", "--decrypt", "--output-type", "json", it.path)
	cmd.Env = it.env()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	plain := stdout.Bytes()
	defer clear(plain) // best effort: don't leave the whole document in the heap
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("sops: timed out after %s", readTimeout)
		}
		if msg := oneLine(stderr.String()); msg != "" {
			return nil, fmt.Errorf("sops: %s", msg)
		}
		return nil, fmt.Errorf("sops: %w", err)
	}
	fields, err := secrets.FlattenJSON(plain)
	if err != nil {
		// The decoder error can quote a character of the plaintext; keep it out.
		return nil, errors.New("sops: decrypted output is not valid JSON")
	}
	return fields, nil
}

// EditCmd opens the file in $EDITOR through sops, which decrypts to a private
// temp file, waits for the editor and re-encrypts on save.
func (it *Item) EditCmd() *exec.Cmd {
	cmd := exec.Command("sops", "edit", it.path)
	cmd.Env = it.env()
	return cmd
}

// exitUnchanged is sops' exit status when the editor closed without changes.
const exitUnchanged = 200

func (it *Item) EditResult(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() == exitUnchanged {
		return false, nil
	}
	return false, fmt.Errorf("sops edit: %w", err)
}

func (it *Item) env() []string {
	env := os.Environ()
	if it.spec.AWSProfile != "" {
		env = append(env, "AWS_PROFILE="+it.spec.AWSProfile)
	}
	if it.spec.AWSRegion != "" {
		env = append(env, "AWS_REGION="+it.spec.AWSRegion)
	}
	return env
}

// oneLine collapses sops' multi-line error into one line for the pane. The
// useful part (e.g. an expired SSO token) is usually at the end, so the
// whole message is kept up to maxErrLen.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxErrLen {
		s = string(r[:maxErrLen-1]) + "…"
	}
	return s
}
