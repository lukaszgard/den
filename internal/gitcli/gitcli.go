// Package gitcli runs git for den without ever waiting on a person. den's TUI
// owns the terminal, so a password, passphrase or host-key question would
// hang den or garble its screen. Credentials come from what works without
// asking — a credential helper, the SSH agent — and anything else fails with
// git's own message, cut to one line.
//
// Runbook sources (den's own mirrors) and secrets kept in a git working copy
// both run git through it.
package gitcli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// Runner runs git commands. The zero value runs "git" from PATH.
type Runner struct {
	// Bin is the git binary; "git" when empty.
	Bin string
	// Label starts every error, such as "source ops"; errors start with the
	// git step alone when it is empty.
	Label string
	// Args go before every command's own, such as -c core.autocrlf=false.
	Args []string
	// Env is added to the environment, after what keeps git from asking.
	Env []string
}

// Run runs one git command and returns its output.
func (r Runner) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := r.Command(ctx, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return nil, r.error(args, &errOut, err)
	}
	return out.Bytes(), nil
}

// Stream runs git with its output going to consume, for output too big to
// hold: an archive of a repository.
func (r Runner) Stream(ctx context.Context, consume func(io.Reader) error, args ...string) error {
	cmd := r.Command(ctx, args...)
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return r.error(args, &errOut, err)
	}
	cerr := consume(out)
	if cerr != nil {
		// Unblock git if it is still writing, then reap it.
		_, _ = io.Copy(io.Discard, out)
	}
	if err := cmd.Wait(); err != nil {
		return r.error(args, &errOut, err)
	}
	if cerr != nil {
		if r.Label == "" {
			return cerr
		}
		return fmt.Errorf("%s: %w", r.Label, cerr)
	}
	return nil
}

// Command is the git command for args, set up never to ask: no stdin, the
// environment of Env, and (outside Windows) no controlling terminal.
func (r Runner) Command(ctx context.Context, args ...string) *exec.Cmd {
	bin := r.Bin
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, append(slices.Clone(r.Args), args...)...)
	cmd.Env = append(Env(os.Environ()), r.Env...)
	cmd.Stdin = nil
	detach(cmd)
	return cmd
}

// Env is the environment git runs with: environ, with every way of asking a
// person switched off. GIT_ASKPASS is dropped too: an editor's askpass helper
// would open a dialog den cannot see and wait on it.
func Env(environ []string) []string {
	fixed := map[string]string{
		"GIT_TERMINAL_PROMPT": "0",
		"GCM_INTERACTIVE":     "never", // Git Credential Manager's dialogs
		"SSH_ASKPASS_REQUIRE": "never",
	}
	ownSSH := false
	var out []string
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case k == "GIT_SSH_COMMAND" || k == "GIT_SSH":
			// The user's own ssh command (a key, a jump host) is kept;
			// running without a terminal still keeps it from asking.
			ownSSH = true
			out = append(out, kv)
		case k == "GIT_ASKPASS" || fixed[k] != "":
		default:
			out = append(out, kv)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(fixed)) {
		out = append(out, k+"="+fixed[k])
	}
	if !ownSSH {
		out = append(out, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	return out
}

// error is one line: the label, the git step, and git's last word.
func (r Runner) error(args []string, stderr *bytes.Buffer, err error) error {
	step := "git"
	if len(args) > 2 && args[0] == "-C" {
		step = "git " + args[2]
	} else if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		step = "git " + args[0]
	}
	msg := LastLine(stderr.String())
	if msg == "" {
		msg = err.Error()
	}
	if r.Label == "" {
		return fmt.Errorf("%s: %s", step, msg)
	}
	return fmt.Errorf("%s: %s: %s", r.Label, step, msg)
}

// LastLine is the last line of s that is not blank: where git says why.
func LastLine(s string) string {
	msg := ""
	for line := range strings.SplitSeq(strings.TrimSpace(s), "\n") {
		if l := strings.TrimSpace(line); l != "" {
			msg = l
		}
	}
	return msg
}
