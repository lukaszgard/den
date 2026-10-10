package source

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

// git runs one git command for a source and returns its output. It never
// waits on a person: den's TUI owns the terminal, so a password, passphrase
// or host-key question would hang den or garble its screen. Credentials come
// from what works without asking — a credential helper, the SSH agent — and
// anything else fails with git's own message.
func (s *Store) git(ctx context.Context, source string, args ...string) ([]byte, error) {
	cmd := s.command(ctx, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return nil, gitError(source, args, &errOut, err)
	}
	return out.Bytes(), nil
}

// gitStream runs git with its output going to consume, for output too big to
// hold: an archive of the repository.
func (s *Store) gitStream(ctx context.Context, source string, consume func(io.Reader) error, args ...string) error {
	cmd := s.command(ctx, args...)
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return gitError(source, args, &errOut, err)
	}
	cerr := consume(out)
	if cerr != nil {
		// Unblock git if it is still writing, then reap it.
		_, _ = io.Copy(io.Discard, out)
	}
	if err := cmd.Wait(); err != nil {
		return gitError(source, args, &errOut, err)
	}
	if cerr != nil {
		return fmt.Errorf("source %s: %w", source, cerr)
	}
	return nil
}

func (s *Store) command(ctx context.Context, args ...string) *exec.Cmd {
	// core.autocrlf off: git for Windows converts text files to CRLF by
	// default, and a run.sh with CRLF fails its shebang (bash\r).
	cmd := exec.CommandContext(ctx, s.gitBin, append([]string{"-c", "core.autocrlf=false"}, args...)...)
	cmd.Env = gitEnv(os.Environ())
	cmd.Stdin = nil
	detach(cmd)
	return cmd
}

// gitEnv is the environment git runs with: environ, with every way of asking
// a person switched off. GIT_ASKPASS is dropped too: an editor's askpass
// helper would open a dialog den cannot see and wait on it.
func gitEnv(environ []string) []string {
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

// gitError is one line: the source, the git step, and git's last word.
func gitError(source string, args []string, stderr *bytes.Buffer, err error) error {
	step := "git"
	if len(args) > 2 && args[0] == "-C" {
		step = "git " + args[2]
	}
	msg := ""
	for line := range strings.SplitSeq(strings.TrimSpace(stderr.String()), "\n") {
		if l := strings.TrimSpace(line); l != "" {
			msg = l
		}
	}
	if msg == "" {
		msg = err.Error()
	}
	return fmt.Errorf("source %s: %s: %s", source, step, msg)
}
