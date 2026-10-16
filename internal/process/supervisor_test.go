//go:build !windows

package process

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Every tunnel, VPN and runbook runs under a Supervisor, so its guarantees are
// what those services rely on: output is captured and never blocks the child,
// Done fires exactly when the process is gone, and stopping takes the whole
// process tree down — gracefully first when asked.

func waitDone(t *testing.T, s *Supervisor, within time.Duration) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(within):
		t.Fatalf("process still running after %v\nlogs:\n%s", within, strings.Join(s.Logs(), "\n"))
	}
}

func TestCapturesOutputAndExitCode(t *testing.T) {
	s := NewSupervisor(10)
	if err := s.Start(context.Background(), "sh", "-c", "echo out; echo err >&2; exit 3"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 5*time.Second)

	var exitErr *exec.ExitError
	if !errors.As(s.ExitErr(), &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("ExitErr = %v, want exit status 3", s.ExitErr())
	}
	if s.IsRunning() {
		t.Fatal("IsRunning after exit")
	}
	logs := strings.Join(s.Logs(), "\n")
	if !strings.Contains(logs, "] out") || !strings.Contains(logs, "] err") {
		t.Fatalf("logs = %q, want both streams, timestamped", logs)
	}
	last := s.LastLines(2)
	if len(last) != 2 || (last[0] != "out" && last[1] != "out") {
		t.Fatalf("LastLines = %q, want the lines without timestamps", last)
	}
}

func TestDoneIsReadyBeforeStartAndStartIsExclusive(t *testing.T) {
	s := NewSupervisor(10)
	select {
	case <-s.Done():
	default:
		t.Fatal("Done must be closed before anything has started")
	}
	if err := s.Start(context.Background(), "sh", "-c", "sleep 5"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop() //nolint:errcheck
	if err := s.Start(context.Background(), "sh", "-c", "true"); err == nil {
		t.Fatal("a second Start while running must fail")
	}
}

// A chatty process must never block on den: the event channel drops, the ring
// buffer slides, and OnLine still sees every line.
func TestOutputNeverBlocksAndOnLineIsLossless(t *testing.T) {
	const lines = 5000
	var seen atomic.Int64
	s := NewSupervisor(100)
	opts := StartOptions{OnLine: func(string) { seen.Add(1) }}
	script := fmt.Sprintf("i=1; while [ $i -le %d ]; do echo line$i; i=$((i+1)); done", lines)
	if err := s.StartWith(context.Background(), opts, "sh", "-c", script); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 20*time.Second)

	if got := seen.Load(); got != lines {
		t.Fatalf("OnLine saw %d lines, want %d", got, lines)
	}
	logs := s.Logs()
	if len(logs) != 100 || !strings.HasSuffix(logs[len(logs)-1], fmt.Sprintf("line%d", lines)) {
		t.Fatalf("ring buffer holds %d lines ending %q, want the last 100", len(logs), logs[len(logs)-1])
	}
	if s.LogSeq() != lines {
		t.Fatalf("LogSeq = %d, want %d", s.LogSeq(), lines)
	}
}

func TestWaitForLine(t *testing.T) {
	t.Run("match", func(t *testing.T) {
		s := NewSupervisor(10)
		if err := s.Start(context.Background(), "sh", "-c", "echo starting; sleep 0.2; echo ready; sleep 5"); err != nil {
			t.Fatal(err)
		}
		defer s.Stop() //nolint:errcheck
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		line, err := s.WaitForLine(ctx, func(l string) bool { return strings.Contains(l, "ready") })
		if err != nil || line != "ready" {
			t.Fatalf("WaitForLine = %q, %v", line, err)
		}
	})
	t.Run("exit before match", func(t *testing.T) {
		s := NewSupervisor(10)
		if err := s.Start(context.Background(), "sh", "-c", "echo nope; exit 1"); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.WaitForLine(ctx, func(l string) bool { return l == "ready" }); err == nil || !strings.Contains(err.Error(), "exited before match") {
			t.Fatalf("err = %v, want exited before match", err)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		s := NewSupervisor(10)
		if err := s.Start(context.Background(), "sh", "-c", "sleep 5"); err != nil {
			t.Fatal(err)
		}
		defer s.Stop() //nolint:errcheck
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if _, err := s.WaitForLine(ctx, func(string) bool { return true }); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
	})
}

func TestStopGracefulLetsTrapsRun(t *testing.T) {
	s := NewSupervisor(10)
	script := "trap 'echo cleanup; exit 0' INT; echo ready; while :; do sleep 0.05; done"
	if err := s.StartWith(context.Background(), StartOptions{ProcessGroup: true}, "sh", "-c", script); err != nil {
		t.Fatal(err)
	}
	waitForLog(t, s, "ready")
	if err := s.StopGraceful(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 5*time.Second)
	if !strings.Contains(strings.Join(s.Logs(), "\n"), "cleanup") {
		t.Fatal("the INT trap never ran")
	}
}

func TestStopGracefulEscalatesWhenInterruptIsIgnored(t *testing.T) {
	s := NewSupervisor(10)
	script := "trap '' INT; echo ready; while :; do sleep 0.05; done"
	if err := s.StartWith(context.Background(), StartOptions{ProcessGroup: true}, "sh", "-c", script); err != nil {
		t.Fatal(err)
	}
	waitForLog(t, s, "ready")
	start := time.Now()
	if err := s.StopGraceful(300 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 5*time.Second)
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("stop took %v; the kill should follow the 300 ms grace", took)
	}
}

// Cancelling must take down what the command spawned, not just the shell: an
// orphaned ssh or aws would keep its local port and break the next connect.
func TestCancelTakesTheProcessGroupDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := NewSupervisor(10)
	opts := StartOptions{ProcessGroup: true, WaitDelay: 500 * time.Millisecond}
	if err := s.StartWith(ctx, opts, "sh", "-c", "sleep 30 & echo child=$!; wait"); err != nil {
		t.Fatal(err)
	}
	line := waitForLog(t, s, "child=")
	pid, err := strconv.Atoi(line[strings.Index(line, "child=")+len("child="):])
	if err != nil {
		t.Fatalf("bad child line %q", line)
	}
	cancel()
	waitDone(t, s, 5*time.Second)

	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("grandchild %d survived the cancel", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The aws CLI runs session-manager-plugin as its child. Killing aws alone left
// the plugin holding the port and the output pipes, so Done never closed and
// every later connect found the supervisor still running.
func TestStopTakesTheProcessGroupDown(t *testing.T) {
	s := NewSupervisor(10)
	opts := StartOptions{ProcessGroup: true}
	if err := s.StartWith(context.Background(), opts, "sh", "-c", "sleep 30 & echo child=$!; wait"); err != nil {
		t.Fatal(err)
	}
	line := waitForLog(t, s, "child=")
	pid, err := strconv.Atoi(line[strings.Index(line, "child=")+len("child="):])
	if err != nil {
		t.Fatalf("bad child line %q", line)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	waitDone(t, s, 5*time.Second)

	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d survived Stop", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestEnvAndDir(t *testing.T) {
	dir := t.TempDir()
	s := NewSupervisor(10)
	opts := StartOptions{Env: []string{"DEN_TEST=bar"}, Dir: dir}
	if err := s.StartWith(context.Background(), opts, "sh", "-c", `echo "v=$DEN_TEST"; echo "d=$(pwd -P)"`); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 5*time.Second)
	logs := strings.Join(s.LastLines(2), "\n")
	if !strings.Contains(logs, "v=bar") {
		t.Fatalf("env not passed: %q", logs)
	}
	if !strings.Contains(logs, "d=") || !strings.HasSuffix(strings.TrimSpace(logs), strings.TrimPrefix(dir, "/private")) {
		t.Fatalf("dir not applied: %q (want %s)", logs, dir)
	}
}

func TestLogWriterTimestampsAndCounts(t *testing.T) {
	s := NewSupervisor(10)
	fmt.Fprintf(s.LogWriter(), "one\ntwo\n") //nolint:errcheck
	if s.LogSeq() != 2 {
		t.Fatalf("LogSeq = %d, want 2", s.LogSeq())
	}
	if got := s.LastLines(2); got[0] != "one" || got[1] != "two" {
		t.Fatalf("LastLines = %q", got)
	}
}

// waitForLog waits for a log line containing substr and returns it without
// its timestamp.
func waitForLog(t *testing.T, s *Supervisor, substr string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, l := range s.LastLines(50) {
			if strings.Contains(l, substr) {
				return l
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no log line containing %q; logs:\n%s", substr, strings.Join(s.Logs(), "\n"))
	return ""
}

// A line longer than bufio.Scanner's 64 KiB limit used to stop the reader.
// Nothing drained the pipe after that, so the process blocked on its next
// write and ran into its timeout — or hung for good without one.
func TestOverlongLineNeitherStopsOutputNorBlocksTheProcess(t *testing.T) {
	s := NewSupervisor(100)
	var got []string
	opts := StartOptions{OnLine: func(l string) { got = append(got, l) }}
	// 200 KiB without a newline, then 200 KiB more output in ordinary lines:
	// more than a pipe buffer, so the process blocks unless it is read.
	script := `head -c 204800 /dev/zero | tr '\0' x; echo; i=0; while [ $i -lt 2000 ]; do echo "line $i ................................................................................................"; i=$((i+1)); done; echo after`
	if err := s.StartWith(context.Background(), opts, "sh", "-c", script); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 10*time.Second)

	var long int
	for _, l := range got {
		if strings.Trim(l, "x") == "" {
			long += len(l)
		}
		if len(l) > maxLineBytes {
			t.Fatalf("a line of %d bytes was kept, want at most %d", len(l), maxLineBytes)
		}
	}
	if long != 204800 {
		t.Fatalf("got %d bytes of the long line, want all 204800", long)
	}
	if got[len(got)-1] != "after" {
		t.Fatalf("last line = %q, want the output after the long line", got[len(got)-1])
	}
}

// A progress bar redraws itself with carriage returns. Kept raw, a \r moves
// the cursor in den's own view and garbles it; the line shows its last state.
func TestCarriageReturnKeepsTheLastStateOfALine(t *testing.T) {
	s := NewSupervisor(10)
	if err := s.Start(context.Background(), "sh", "-c", `printf '10%%\r50%%\r100%%\r\nplain\r\n'`); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 5*time.Second)
	if got := s.LastLines(2); len(got) != 2 || got[0] != "100%" || got[1] != "plain" {
		t.Fatalf("LastLines = %q, want [100%% plain]", got)
	}
}

// Colour is kept in the log; what would act on den's own screen is not.
func TestOutputKeepsColourAndDropsOtherEscapes(t *testing.T) {
	s := NewSupervisor(10)
	if err := s.Start(context.Background(), "sh", "-c", `printf '\033[2K\033[32mok\033[0m\033]0;title\007 done\033[1A\n'`); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 5*time.Second)
	if got := s.LastLines(1); len(got) != 1 || got[0] != "\x1b[32mok\x1b[0m done" {
		t.Fatalf("LastLines = %q, want the colour kept and the rest gone", got)
	}
}

// A runbook reuses its Supervisor for every run; ResetLogs is what keeps one
// run's output out of the next.
func TestResetLogsStartsTheNextRunClean(t *testing.T) {
	s := NewSupervisor(10)
	if err := s.Start(context.Background(), "sh", "-c", "echo first"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 5*time.Second)
	s.ResetLogs()
	if err := s.Start(context.Background(), "sh", "-c", "echo second"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 5*time.Second)
	if logs := s.Logs(); len(logs) != 1 || !strings.HasSuffix(logs[0], "] second") {
		t.Fatalf("logs = %q, want the second run's line only", logs)
	}
}
