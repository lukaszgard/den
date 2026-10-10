package runbook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/process"
)

// runbookLogLines is the per-runbook output buffer. Runbooks are far chattier
// than the connection services (which keep 200), and this is what the TUI
// scrolls back through.
const runbookLogLines = 5000

// gracePeriod is how long a cancelled runbook has to exit after SIGINT before
// it is force-killed.
const gracePeriod = 3 * time.Second

// ExecOptions carries everything resolved just before a run: the assembled
// environment, the working directory, and where output should go.
type ExecOptions struct {
	Env      []string
	Dir      string
	Redactor *Redactor
	// OnLine, when set, receives every output line as it arrives, already
	// redacted. The CLI streams it to stdout, the TUI to its Logs panel; the
	// TUI's run view polls State instead.
	OnLine func(string)
	// Origin is where the run was started from (OriginTUI, OriginCLI,
	// OriginMCP), for its history entry.
	Origin string
	// Params are the parameter values the run was given, defaults applied,
	// for its history entry; secret ones are left out of it.
	Params map[string]string
}

// Runner owns the execution state of one runbook. It is safe for concurrent
// use: the TUI reads State on every tick while a run is in flight.
type Runner struct {
	sup *process.Supervisor

	mu        sync.RWMutex
	rb        Runbook
	pending   *Runbook // a new definition that arrived mid-run
	run       Run
	opts      ExecOptions // of the run in flight, or the last one
	cancel    context.CancelFunc
	cancelled bool
	redactor  *Redactor
	rec       *Recording // the run in flight, or the last one, on disk
	tally     Tally
	// retired is set while the runbook is being deleted: no run starts again.
	retired bool

	// openRecord starts keeping a run in the registry's history, and
	// closeRecord gives it its final state; nil keeps nothing.
	openRecord  func(Runbook, Run, ExecOptions) *Recording
	closeRecord func(*Recording, Runbook, Run, ExecOptions)
	// started hears of every run that began, with its definition.
	started func(Runbook)
}

// NewRunner creates an idle Runner for rb.
func NewRunner(rb Runbook) *Runner {
	return &Runner{
		rb:  rb,
		sup: process.NewSupervisor(runbookLogLines),
		run: Run{Status: StatusNeverRun},
	}
}

// Runbook returns the definition this Runner executes.
func (r *Runner) Runbook() Runbook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.rb
}

// restore shows the newest run h holds as the last run, until there is a run
// of this session, and counts the runs h holds. A run restored before is read
// again: its files may have moved, as they do when a runbook gets a folder of
// its own.
func (r *Runner) restore(h *History) {
	rb := r.Runbook()
	e, ok := h.Latest(rb.Name, rb.LogsDir())
	kept, counted := h.Tally(rb.Name, rb.LogsDir())
	r.mu.Lock()
	defer r.mu.Unlock()
	if ok && (r.run.Status == StatusNeverRun || r.run.Saved != nil) {
		r.run = e.Run()
	}
	if counted {
		r.tally = r.tally.merge(kept)
	}
}

// Tally is how often the runbook has run, the run in flight included.
func (r *Runner) Tally() Tally {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tally
}

// latest is the definition the next run will use: one waiting for the run in
// flight to finish, or the current one.
func (r *Runner) latest() Runbook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.pending != nil {
		return *r.pending
	}
	return r.rb
}

// redefine replaces the definition, as a rescan does when a runbook.yaml
// changed. A run in flight keeps the definition it started with; the new one
// applies once it has finished.
func (r *Runner) redefine(rb Runbook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.run.Status == StatusRunning {
		r.pending = &rb
		return
	}
	r.rb, r.pending = rb, nil
}

// State returns a snapshot of the current or last run, with live output and
// secrets already redacted.
func (r *Runner) State() Run {
	r.mu.RLock()
	run := r.run
	red := r.redactor
	r.mu.RUnlock()

	if run.Status == StatusNeverRun || run.Saved != nil {
		return run
	}
	run.Logs = red.Lines(r.sup.Logs())
	return run
}

// keep starts keeping the run just claimed on disk, before its first line.
// It is not done under r.mu: the registry calls into runners while holding
// its own lock.
func (r *Runner) keep(opts ExecOptions) {
	r.mu.RLock()
	open, rb, run := r.openRecord, r.rb, r.run
	r.mu.RUnlock()
	if open == nil {
		return
	}
	rec := open(rb, run, opts)
	r.mu.Lock()
	r.rec = rec
	r.run.LogPath = rec.Path()
	// Its number counts the runs another den started meanwhile, too.
	r.tally.Runs = max(r.tally.Runs, rec.Number())
	r.mu.Unlock()
}

// recording is the run in flight on disk; nil when it is not kept.
func (r *Runner) recording() *Recording {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.rec
}

// IsRunning reports whether a run is in flight.
func (r *Runner) IsRunning() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.run.Status == StatusRunning
}

// Start executes the runbook in the background and returns immediately.
// Use State to follow progress.
func (r *Runner) Start(ctx context.Context, opts ExecOptions) error {
	// Claimed here, not in the goroutine: two quick presses of enter would
	// otherwise both see an idle runner and start two processes.
	if err := r.begin(opts); err != nil {
		return fmt.Errorf("runbook %q: %w", r.Runbook().Name, err)
	}
	r.notifyStarted()
	r.keep(opts)
	go r.execute(ctx, opts)
	return nil
}

func (r *Runner) notifyStarted() {
	if r.started != nil {
		r.started(r.Runbook())
	}
}

// Execute runs the runbook to completion and returns the final state. It
// honours the retry policy, the per-run timeout, and cancellation.
func (r *Runner) Execute(ctx context.Context, opts ExecOptions) Run {
	if err := r.begin(opts); errors.Is(err, errDeleting) {
		// Its last run would read as this one's result: a sequence would
		// carry on past a step that never ran.
		now := time.Now()
		return Run{Status: StatusFailed, ExitCode: -1, StartedAt: now, FinishedAt: now,
			Err: fmt.Errorf("runbook %q: %w", r.Runbook().Name, err)}
	} else if err != nil {
		return r.State()
	}
	r.notifyStarted()
	r.keep(opts)
	return r.execute(ctx, opts)
}

// errDeleting is why a runbook being deleted does not start.
var errDeleting = errors.New("being deleted")

// begin claims the runner for one run, or says why it cannot: one is in
// flight, or the runbook is being deleted.
func (r *Runner) begin(opts ExecOptions) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.retired:
		return errDeleting
	case r.run.Status == StatusRunning:
		return errors.New("already running")
	}
	r.cancel = nil
	r.cancelled = false
	r.redactor = opts.Redactor
	r.opts = opts
	r.run = Run{Status: StatusRunning, StartedAt: time.Now(), Attempt: 1}
	r.rec = nil
	r.tally.Runs++
	// One run, one log: without this every run would show, and keep, the
	// output of the runs before it.
	r.sup.ResetLogs()
	return nil
}

// retire claims the runner for deleting its runbook, so that nothing — r, a
// sequence, den mcp — starts it between the check and the files going. It
// fails while a run is in flight.
func (r *Runner) retire() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.run.Status == StatusRunning {
		return fmt.Errorf("%s is running: delete it once it has finished", r.rb.Name)
	}
	r.retired = true
	return nil
}

// unretire gives the runner back after a deletion that did not happen.
func (r *Runner) unretire() {
	r.mu.Lock()
	r.retired = false
	r.mu.Unlock()
}

func (r *Runner) execute(ctx context.Context, opts ExecOptions) Run {
	// Fixed for the whole run: redefine waits for it to finish.
	rb := r.Runbook()
	// The overall ceiling is the retry timeout when retrying, otherwise the
	// plain per-run timeout.
	overall := rb.Timeout
	if rb.Retry != nil && rb.Retry.Timeout > 0 {
		overall = rb.Retry.Timeout
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var deadline time.Time
	if overall > 0 {
		deadline = time.Now().Add(overall)
		var tcancel context.CancelFunc
		runCtx, tcancel = context.WithDeadline(runCtx, deadline)
		defer tcancel()
	}

	r.mu.Lock()
	r.cancel = cancel
	// A Cancel between begin and here had no process to signal yet.
	if r.cancelled {
		cancel()
	}
	r.mu.Unlock()

	for attempt := 1; ; attempt++ {
		r.setAttempt(attempt)
		if attempt > 1 {
			// The attempts share one log: mark where each one starts.
			r.note(fmt.Sprintf("── attempt %d ──", attempt), opts)
		}

		status, code, err := r.runOnce(runCtx, rb, opts)
		if status == StatusSucceeded || !shouldRetry(rb.Retry, attempt, status, deadline) {
			return r.finish(status, code, err)
		}

		// Wait out the retry interval, but stay cancellable while doing so.
		select {
		case <-runCtx.Done():
			return r.finish(r.terminalFor(runCtx), code, err)
		case <-time.After(rb.Retry.Interval):
		}
	}
}

// runOnce performs a single attempt and reports how it ended.
func (r *Runner) runOnce(ctx context.Context, rb Runbook, opts ExecOptions) (RunStatus, int, error) {
	// Clear stale lifecycle events so a retry does not observe the previous
	// attempt's exit.
	r.sup.Drain()

	start := process.StartOptions{
		Env:          opts.Env,
		Dir:          opts.Dir,
		ProcessGroup: true, // so cancelling kills the whole script tree
		OnLine:       lineSink(opts, r.recording()),
		WaitDelay:    gracePeriod,
	}
	shell, args := platform.ShellCommand(rb.Command)
	if err := r.sup.StartWith(ctx, start, shell, args...); err != nil {
		if term := r.terminalFor(ctx); term != StatusRunning {
			return term, -1, err
		}
		return StatusFailed, -1, err
	}

	// Wait on Done rather than the event channel: events are dropped when the
	// consumer is slow, and a chatty script can lose its own exit event.
	<-r.sup.Done()
	exitErr := r.sup.ExitErr()

	if term := r.terminalFor(ctx); term != StatusRunning {
		return term, exitCodeOf(exitErr), exitErr
	}
	if exitErr != nil {
		return StatusFailed, exitCodeOf(exitErr), exitErr
	}
	return StatusSucceeded, 0, nil
}

// lineSink is where every output line goes besides the log buffer, redacted:
// the run's file, timestamped as the buffer keeps it, and the caller's
// OnLine. It is the lossless path, so the file holds the whole run. nil when
// there is neither.
func lineSink(opts ExecOptions, rec *Recording) func(string) {
	if rec == nil && opts.OnLine == nil {
		return nil
	}
	red, onLine := opts.Redactor, opts.OnLine
	return func(s string) {
		line := red.Line(s)
		rec.Line(process.Timestamped(time.Now(), line))
		if onLine != nil {
			onLine(line)
		}
	}
}

// note adds a line of den's own to the run's output, wherever the script's
// lines go.
func (r *Runner) note(line string, opts ExecOptions) {
	_, _ = io.WriteString(r.sup.LogWriter(), line)
	r.recording().Line(process.Timestamped(time.Now(), line))
	if opts.OnLine != nil {
		opts.OnLine(line)
	}
}

// terminalFor maps an interrupted context to the status that caused it, or
// StatusRunning when the context is still live.
func (r *Runner) terminalFor(ctx context.Context) RunStatus {
	r.mu.RLock()
	cancelled := r.cancelled
	r.mu.RUnlock()

	if cancelled {
		return StatusCancelled
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return StatusTimedOut
	}
	if ctx.Err() != nil {
		return StatusCancelled
	}
	return StatusRunning
}

func shouldRetry(rt *Retry, attempt int, status RunStatus, deadline time.Time) bool {
	if rt == nil || !rt.UntilSuccess {
		return false
	}
	// Cancellation and timeout are decisions, not failures to retry past.
	if status != StatusFailed {
		return false
	}
	if rt.MaxAttempts > 0 && attempt >= rt.MaxAttempts {
		return false
	}
	// No point sleeping through an interval we cannot finish an attempt in.
	if !deadline.IsZero() && time.Now().Add(rt.Interval).After(deadline) {
		return false
	}
	return true
}

// Cancel interrupts a running runbook, giving it a grace period to clean up
// before the process group is killed.
func (r *Runner) Cancel() {
	r.mu.Lock()
	if r.run.Status != StatusRunning {
		r.mu.Unlock()
		return
	}
	r.cancelled = true
	cancel := r.cancel
	r.mu.Unlock()

	_ = r.sup.StopGraceful(gracePeriod)
	if cancel != nil {
		cancel()
	}
}

func (r *Runner) setAttempt(n int) {
	r.mu.Lock()
	r.run.Attempt = n
	r.mu.Unlock()
}

func (r *Runner) finish(status RunStatus, code int, err error) Run {
	r.mu.Lock()
	done := r.run
	done.Status, done.ExitCode, done.FinishedAt, done.Err = status, code, time.Now(), err
	rb, opts, rec, closeRecord := r.rb, r.opts, r.rec, r.closeRecord
	r.mu.Unlock()
	// The run's files say it is over before the runner does: History, read
	// the moment the runner reads as done, would otherwise list the run as
	// still going, and not look again.
	if closeRecord != nil {
		closeRecord(rec, rb, done, opts)
	}
	// Read off the lock: State, on every tick, waits for nothing on disk.
	kept, counted := rec.Tally()
	r.mu.Lock()
	r.run = done
	r.tally = r.tally.settle(kept, counted, done.Status)
	if r.pending != nil {
		r.rb, r.pending = *r.pending, nil
	}
	r.mu.Unlock()
	return r.State()
}

// exitCodeOf extracts the process exit status from a wait error.
// -1 means the process never produced one (it failed to start, or was killed
// by a signal before exiting).
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode()
	}
	return -1
}
