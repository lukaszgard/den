package process

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// StatusEvent is emitted by a supervised process when its state changes.
type StatusEvent struct {
	Timestamp time.Time
	Line      string // raw stdout/stderr line, empty for lifecycle events
	Kind      EventKind
	Err       error
}

// EventKind categorises a StatusEvent.
type EventKind int

const (
	EventLine    EventKind = iota // a new log line arrived
	EventStarted                  // process started successfully
	EventExited                   // process exited (check Err)
)

// Supervisor manages the lifecycle of a single external process.
type Supervisor struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	logs    *RingBuffer
	events  chan StatusEvent
	running bool

	// pgid is the process group the current process leads, or 0 when it was
	// not started in a group of its own.
	pgid int

	// done is closed when the current process exits; exitErr holds that exit
	// error. These exist because the events channel drops on overflow — a
	// chatty process can lose its own EventExited — so anything that must
	// reliably observe termination waits on done instead.
	done    chan struct{}
	exitErr error
}

// StartOptions tunes how StartWith launches a process. The zero value
// reproduces plain Start: inherited environment, inherited working directory,
// no dedicated process group.
type StartOptions struct {
	// Env replaces the child's environment entirely when non-nil.
	Env []string
	// Dir sets the child's working directory when non-empty.
	Dir string
	// ProcessGroup puts the child in its own process group so Stop,
	// StopGraceful and a cancelled context reach the whole tree it spawns.
	ProcessGroup bool
	// OnLine, when set, is called for every stdout/stderr line as it is read.
	// Unlike the events channel it never drops, and unlike the ring buffer it
	// does not slide once full — so it is the only lossless way to observe
	// output. Calls are serialised across the stdout and stderr readers, so
	// implementations need no locking of their own, but they do run on those
	// readers: keep them cheap and non-blocking.
	OnLine func(string)
	// WaitDelay bounds how long the supervisor waits for output pipes to close
	// after the process is done. Zero means wait indefinitely.
	WaitDelay time.Duration
}

// NewSupervisor creates a Supervisor but does not start the process.
func NewSupervisor(bufSize int) *Supervisor {
	done := make(chan struct{})
	close(done) // nothing running yet, so "done" is already true
	return &Supervisor{
		logs:   NewRingBuffer(bufSize),
		events: make(chan StatusEvent, 64),
		done:   done,
	}
}

// Events returns the read-only channel of status events.
func (s *Supervisor) Events() <-chan StatusEvent {
	return s.events
}

// Logs returns a snapshot of buffered log lines.
func (s *Supervisor) Logs() []string {
	return s.logs.Lines()
}

// ResetLogs empties the log buffer, so a process started again does not show
// the output of the one before.
func (s *Supervisor) ResetLogs() {
	s.logs.Reset()
}

// LogSeq changes whenever a log line is added; see RingBuffer.Seq.
func (s *Supervisor) LogSeq() uint64 {
	return s.logs.Seq()
}

// LastLines returns the last n log lines with the "[HH:MM:SS] " timestamp prefix stripped.
func (s *Supervisor) LastLines(n int) []string {
	raw := s.logs.LastN(n)
	out := make([]string, len(raw))
	for i, line := range raw {
		// Strip "[HH:MM:SS] " prefix (11 chars)
		if len(line) > 11 && line[0] == '[' && line[9] == ']' && line[10] == ' ' {
			out[i] = line[11:]
		} else {
			out[i] = line
		}
	}
	return out
}

// Start launches the given command, streaming stdout+stderr into the log buffer
// and emitting StatusEvents. It returns immediately; the process runs in the background.
func (s *Supervisor) Start(ctx context.Context, name string, args ...string) error {
	return s.StartWith(ctx, StartOptions{}, name, args...)
}

// StartWith is Start with control over the child's environment, working
// directory and process group.
func (s *Supervisor) StartWith(ctx context.Context, opts StartOptions, name string, args ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("supervisor: already running")
	}

	cmd := exec.CommandContext(ctx, name, args...)
	if opts.Env != nil {
		cmd.Env = opts.Env
	}
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	// done must exist before cmd.Cancel closes over it.
	done := make(chan struct{})
	if opts.ProcessGroup {
		setProcessGroup(cmd)
		grace := opts.WaitDelay
		if grace <= 0 {
			grace = 3 * time.Second
		}
		// Interrupt the whole tree first so scripts get to run their traps,
		// then force it down if they do not take the hint. Signalling only the
		// direct child would leave grandchildren holding the output pipes
		// open, so cancellation would block until they chose to exit.
		cmd.Cancel = func() error {
			if err := signalGroup(cmd, os.Interrupt); err != nil {
				return killGroup(cmd)
			}
			// cmd.Process is set before exec can call Cancel.
			go reapGroup(groupID(cmd), time.Now().Add(grace))
			return nil
		}
	}
	if opts.WaitDelay > 0 {
		cmd.WaitDelay = opts.WaitDelay
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("supervisor: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("supervisor: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("supervisor: start %s: %w", name, err)
	}

	s.cmd = cmd
	s.pgid = 0
	if opts.ProcessGroup {
		s.pgid = groupID(cmd)
	}
	s.running = true
	s.done = done
	s.exitErr = nil
	s.emit(StatusEvent{Kind: EventStarted})

	// Serialise OnLine: it is driven by two reader goroutines, and making every
	// caller supply its own lock would be an easy mistake to make.
	onLine := opts.OnLine
	if onLine != nil {
		var lineMu sync.Mutex
		deliver := onLine
		onLine = func(line string) {
			lineMu.Lock()
			defer lineMu.Unlock()
			deliver(line)
		}
	}

	// cmd.Wait closes the pipes as soon as it returns, so both readers must
	// finish before it is called or trailing output is silently truncated.
	var streams sync.WaitGroup
	streams.Add(2)
	go func() { defer streams.Done(); s.stream(stdout, onLine) }()
	go func() { defer streams.Done(); s.stream(stderr, onLine) }()
	go s.wait(cmd, done, &streams, opts.WaitDelay)

	return nil
}

// Stop force-kills the process immediately, with its whole group when it was
// started in one. Use StopGraceful when the process should get a chance to
// clean up first.
func (s *Supervisor) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	// Killing the leader alone orphans what it started: aws runs
	// session-manager-plugin as its child, and the plugin would keep the port
	// and the output pipes, so wait would never see the process end. Only while
	// running, which ends as wait reaps the leader: until then its PID, the
	// group's ID, is not free for another process to take.
	if s.running && s.pgid != 0 {
		_ = killGroupID(s.pgid)
	}
	return s.cmd.Process.Kill()
}

// IsRunning reports whether the supervised process is alive.
func (s *Supervisor) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Done returns a channel closed when the current process exits. It is ready
// immediately if nothing has been started.
func (s *Supervisor) Done() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

// ExitErr returns the exit error of the last finished process, or nil if it
// exited cleanly or never ran. Pair it with Done.
func (s *Supervisor) ExitErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitErr
}

// StopGraceful interrupts the process (and its group, when one was requested)
// and gives it up to timeout to exit before force-killing it.
func (s *Supervisor) StopGraceful(timeout time.Duration) error {
	s.mu.Lock()
	cmd, done, running, pgid := s.cmd, s.done, s.running, s.pgid
	s.mu.Unlock()

	if cmd == nil || cmd.Process == nil || !running {
		return nil
	}
	if err := signalGroup(cmd, os.Interrupt); err != nil {
		// Platform cannot interrupt (Windows) — go straight to the kill.
		return killGroup(cmd)
	}
	deadline := time.Now().Add(timeout)
	select {
	case <-done:
	case <-time.After(timeout):
		if pgid == 0 {
			return killGroup(cmd)
		}
	}
	reapGroup(pgid, deadline)
	return nil
}

// reapGroup waits until group pgid is empty or deadline passes, then kills
// whatever is left. The leader exiting is not enough: a non-interactive shell
// runs background jobs with SIGINT ignored, so after an interrupt the shell
// can be gone while what it started keeps running — and keeps its port.
func reapGroup(pgid int, deadline time.Time) {
	for groupAlive(pgid) {
		if time.Now().After(deadline) {
			_ = killGroupID(pgid)
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// WaitForLine blocks until a line matching the predicate appears in stdout/stderr
// or until the context deadline is exceeded. Returns the matching line.
func (s *Supervisor) WaitForLine(ctx context.Context, pred func(string) bool) (string, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case ev := <-s.events:
			if ev.Kind == EventLine && pred(ev.Line) {
				return ev.Line, nil
			}
			if ev.Kind == EventExited {
				return "", fmt.Errorf("process exited before match: %w", ev.Err)
			}
		case <-ticker.C:
			// keep spinning
		}
	}
}

// ---

// maxLineBytes caps one log line. Output that goes on longer without a newline
// (minified JSON, a base64 blob) is split into lines of this size: a
// bufio.Scanner left at its own 64 KiB limit stops reading instead, and the
// process then blocks on its next write to the full pipe.
const maxLineBytes = 64 * 1024

func (s *Supervisor) stream(r io.Reader, onLine func(string)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), 2*maxLineBytes)
	scanner.Split(splitLines)
	for scanner.Scan() {
		// Colour stays, the rest of the escape sequences go: see KeepColours.
		line := KeepColours(lastState(scanner.Text()))
		s.logs.Add(Timestamped(time.Now(), line))
		if onLine != nil {
			onLine(line)
		}
		s.emit(StatusEvent{Kind: EventLine, Line: line})
	}
	// Whatever stopped the scanner, the process must never block writing to a
	// pipe nobody reads.
	_, _ = io.Copy(io.Discard, r)
}

// Timestamped is line as the log buffer keeps it, behind the time it was read.
func Timestamped(t time.Time, line string) string {
	return "[" + t.Format("15:04:05") + "] " + line
}

// splitLines is bufio.ScanLines that hands out a line of maxLineBytes rather
// than asking for more once a line has grown that long. A cut never splits a
// UTF-8 character.
func splitLines(data []byte, atEOF bool) (int, []byte, error) {
	if advance, token, err := bufio.ScanLines(data, atEOF); advance > 0 || token != nil || err != nil {
		return advance, token, err
	}
	if len(data) < maxLineBytes {
		return 0, nil, nil
	}
	n := maxLineBytes
	for i := n - 1; i >= 0 && i >= n-utf8.UTFMax; i-- {
		if utf8.RuneStart(data[i]) {
			if !utf8.FullRune(data[i:n]) {
				n = i
			}
			break
		}
	}
	return n, data[:n], nil
}

// lastState keeps what a line shows once its carriage returns have done their
// work: a progress bar redraws itself with \r, and a raw \r would move the
// cursor in den's own view.
func lastState(line string) string {
	if !strings.Contains(line, "\r") {
		return line
	}
	parts := strings.Split(line, "\r")
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.TrimSpace(parts[i]) != "" {
			return parts[i]
		}
	}
	return ""
}

func (s *Supervisor) wait(cmd *exec.Cmd, done chan struct{}, streams *sync.WaitGroup, delay time.Duration) {
	// Drain stdout/stderr before Wait closes the pipes, but never block here
	// forever: a stray grandchild holding a pipe open must not hang den.
	waitBounded(streams, delay)
	err := cmd.Wait()
	s.mu.Lock()
	s.running = false
	s.exitErr = err
	s.mu.Unlock()
	// Close before emitting: the event channel drops when full, so done is the
	// only signal guaranteed to reach a waiter.
	close(done)
	s.emit(StatusEvent{Kind: EventExited, Err: err})
}

// waitBounded waits for wg, giving up after delay when one is set.
func waitBounded(wg *sync.WaitGroup, delay time.Duration) {
	if delay <= 0 {
		wg.Wait()
		return
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(delay):
	}
}

func (s *Supervisor) emit(ev StatusEvent) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	select {
	case s.events <- ev:
	default:
		// drop if consumer is slow
	}
}

// Drain discards all pending events from the channel (non-blocking).
// Call before restarting a stopped process to clear stale lifecycle events.
func (s *Supervisor) Drain() {
	for {
		select {
		case <-s.events:
		default:
			return
		}
	}
}

// LogWriter returns an io.Writer that appends lines to the internal log buffer.
func (s *Supervisor) LogWriter() io.Writer {
	return &logWriter{logs: s.logs, events: s.events}
}

type logWriter struct {
	logs   *RingBuffer
	events chan StatusEvent
}

func (w *logWriter) Write(p []byte) (int, error) {
	for l := range strings.SplitSeq(strings.TrimRight(string(p), "\n"), "\n") {
		w.logs.Add(Timestamped(time.Now(), l))
		select {
		case w.events <- StatusEvent{Kind: EventLine, Line: l, Timestamp: time.Now()}:
		default:
		}
	}
	return len(p), nil
}
