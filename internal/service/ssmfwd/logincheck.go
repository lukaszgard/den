package ssmfwd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/prereq"
)

const (
	// loginTimeout bounds one login check. mongosh takes seconds to start, and
	// a server that accepts the connection and never answers must not hold the
	// check for good.
	loginTimeout = 20 * time.Second
	// probeOutputCap bounds what a probe keeps of its client's output; "select
	// 1" prints a line, and anything far longer is not worth holding.
	probeOutputCap = 64 << 10
	// maxLoginErr bounds the error a refused login reports, in runes: it goes
	// to the views and to agents, where one sentence is what fits.
	maxLoginErr = 300
	// probeWaitDelay bounds the wait for a killed client's output pipes, which
	// a child it started could otherwise hold open.
	probeWaitDelay = time.Second
)

// errNoClient means the probe's client is not installed: the login cannot be
// checked here, which is not the same as a login that failed.
var errNoClient = errors.New("client not found")

// loginMarkers are what a client prints when the database refuses it, in the
// order they are looked for: psql's FATAL, redis-cli's WRONGPASS and NOAUTH,
// mongosh's and mysql's wording, then any ERROR.
var loginMarkers = []string{"FATAL", "WRONGPASS", "NOAUTH", "Authentication failed", "Access denied", "ERROR"}

// runProbe runs p's client once, bounded by timeout, and returns nil when it
// logged in. On a refusal the error is the client's line that says why, with
// secret replaced by [redacted].
func runProbe(ctx context.Context, p LoginProbe, secret string, timeout time.Duration) error {
	tool := p.Command.Tool
	path, ok := prereq.Find(tool)
	if !ok {
		return errNoClient
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, path, p.Command.Args...)
	cmd.Env = append(os.Environ(), p.Command.Env...)
	// No stdin: a client that asks for a password would wait for a person who
	// is not there.
	cmd.Stdin = nil
	out := &cappedBuffer{max: probeOutputCap}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = probeWaitDelay
	runErr := cmd.Run()

	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case runCtx.Err() != nil:
		return fmt.Errorf("%s did not answer within %v", tool, timeout)
	}
	output := out.String()
	if runErr == nil && (p.Succeeded == nil || p.Succeeded(output)) {
		return nil
	}
	line := tellingLine(output)
	if line == "" {
		if runErr == nil {
			return fmt.Errorf("%s printed nothing", tool)
		}
		return fmt.Errorf("%s exited with %v", tool, runErr)
	}
	if secret != "" {
		line = strings.ReplaceAll(line, secret, "[redacted]")
	}
	return errors.New(line)
}

// tellingLine picks the line of a client's output that says why a login
// failed: the first one with a marker, else the last one, cut to maxLoginErr.
// psql puts the connection it tried before the server's FATAL, which the log,
// as wide as the terminal, would show instead of the reason; it starts there.
func tellingLine(output string) string {
	var lines []string
	for l := range strings.Lines(output) {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	line := lines[len(lines)-1]
find:
	for _, m := range loginMarkers {
		for _, l := range lines {
			if i := strings.Index(l, m); i >= 0 {
				line = l
				if m == "FATAL" {
					line = l[i:]
				}
				break find
			}
		}
	}
	if r := []rune(line); len(r) > maxLoginErr {
		line = string(r[:maxLoginErr-1]) + "…"
	}
	return line
}

// cappedBuffer keeps the first max bytes written to it and drops the rest,
// while telling the writer everything was taken so the client never blocks.
type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string { return b.buf.String() }
