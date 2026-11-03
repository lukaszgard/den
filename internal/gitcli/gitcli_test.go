package gitcli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/leakcheck"
)

func TestMain(m *testing.M) { leakcheck.Main(m) }

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func TestEnvNeverPrompts(t *testing.T) {
	env := Env([]string{"PATH=/bin", "GIT_TERMINAL_PROMPT=1", "GIT_ASKPASS=/usr/bin/code-askpass"})
	for _, want := range []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "SSH_ASKPASS_REQUIRE=never", "GIT_SSH_COMMAND=ssh -o BatchMode=yes", "PATH=/bin"} {
		if !slices.Contains(env, want) {
			t.Errorf("env %v lacks %s", env, want)
		}
	}
	if slices.Contains(env, "GIT_TERMINAL_PROMPT=1") {
		t.Error("the inherited GIT_TERMINAL_PROMPT=1 survived")
	}
	if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "GIT_ASKPASS=") }) {
		t.Error("an editor's askpass helper would open a dialog den cannot see")
	}
	own := Env([]string{"GIT_SSH_COMMAND=ssh -i ~/.ssh/work"})
	if !slices.Contains(own, "GIT_SSH_COMMAND=ssh -i ~/.ssh/work") || slices.Contains(own, "GIT_SSH_COMMAND=ssh -o BatchMode=yes") {
		t.Errorf("env %v: a GIT_SSH_COMMAND of the user's own is kept", own)
	}
}

func TestCommandAddsArgsAndEnv(t *testing.T) {
	cmd := Runner{Bin: "/opt/git", Args: []string{"-c", "core.autocrlf=false"}, Env: []string{"GIT_OPTIONAL_LOCKS=0"}}.
		Command(context.Background(), "-C", "/repo", "status")
	if want := []string{"/opt/git", "-c", "core.autocrlf=false", "-C", "/repo", "status"}; !slices.Equal(cmd.Args, want) {
		t.Errorf("args = %v, want %v", cmd.Args, want)
	}
	if !slices.Contains(cmd.Env, "GIT_OPTIONAL_LOCKS=0") || !slices.Contains(cmd.Env, "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("env %v lacks the runner's own or the never-ask settings", cmd.Env)
	}
	if cmd.Stdin != nil {
		t.Error("git must not read den's stdin")
	}
}

// An error is one line: what it was for, the step, and git's last word, which
// is where git says why.
func TestErrorsAreOneLine(t *testing.T) {
	needGit(t)
	dir := t.TempDir()
	_, err := Runner{Label: "source ops"}.Run(context.Background(), "-C", dir, "rev-parse", "HEAD")
	if err == nil {
		t.Fatal("rev-parse outside a repository succeeded")
	}
	if msg := err.Error(); !strings.HasPrefix(msg, "source ops: git rev-parse: ") || strings.Contains(msg, "\n") {
		t.Errorf("err = %q, want one line naming the label and the step", msg)
	}
	_, err = Runner{}.Run(context.Background(), "rev-parse", "--git-dir")
	if err != nil && !strings.HasPrefix(err.Error(), "git rev-parse: ") {
		t.Errorf("err = %q, want the step first without a label", err)
	}
}

func TestErrorFallsBackToTheExitWhenGitSaysNothing(t *testing.T) {
	_, err := Runner{Bin: "false"}.Run(context.Background(), "-C", "/", "fetch")
	if err == nil || err.Error() != "git fetch: exit status 1" {
		t.Errorf("err = %v, want the step and the exit status", err)
	}
}

func TestStream(t *testing.T) {
	needGit(t)
	r := Runner{Label: "x"}
	var got string
	err := r.Stream(context.Background(), func(rd io.Reader) error {
		b, err := io.ReadAll(rd)
		got = string(b)
		return err
	}, "--version")
	if err != nil || !strings.HasPrefix(got, "git version") {
		t.Fatalf("stream = %q, %v", got, err)
	}
	boom := errors.New("boom")
	err = r.Stream(context.Background(), func(io.Reader) error { return boom }, "--version")
	if !errors.Is(err, boom) || !strings.HasPrefix(err.Error(), "x: ") {
		t.Errorf("err = %v, want the consumer's error under the label", err)
	}
	err = Runner{}.Stream(context.Background(), func(io.Reader) error { return boom }, "--version")
	if err != boom {
		t.Errorf("err = %v, want the consumer's error as it is without a label", err)
	}
	if err := r.Stream(context.Background(), func(io.Reader) error { return nil }, "-C", t.TempDir(), "rev-parse", "HEAD"); err == nil {
		t.Error("a failing git must fail the stream")
	}
	if err := (Runner{Bin: "/nonexistent/git"}).Stream(context.Background(), func(io.Reader) error { return nil }, "x"); err == nil {
		t.Error("a missing binary must fail the stream")
	}
}

func TestLastLine(t *testing.T) {
	if got := LastLine("hint: one\n\nfatal: why it failed\n  \n"); got != "fatal: why it failed" {
		t.Errorf("LastLine = %q", got)
	}
	if LastLine(" \n") != "" {
		t.Error("blank output has no last line")
	}
}
