package runbook

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/process"
)

// SequenceRunner executes a sequence's steps in order, stopping at the first
// step that does not succeed.
//
// This is deliberately the entire orchestration model. There are no
// conditionals, no per-step retry policy and no branching: anything that needs
// those belongs inside a script, where a real language is available.
type SequenceRunner struct {
	seq Sequence
	reg *Registry

	mu        sync.RWMutex
	run       Run
	opts      SequenceOptions // of the run in flight, or the last one
	cancel    context.CancelFunc
	cancelled bool
	current   *Runner    // step in flight, for live log interleaving
	done      []string   // output of completed steps
	rec       *Recording // the run in flight, or the last one, on disk
	tally     Tally
	retired   bool // being deleted: no run starts again
}

// SequenceOptions are what one run of a sequence is given.
type SequenceOptions struct {
	// Params answer the parameters of every step, by name.
	Params map[string]string
	// OnLine, when set, receives every output line as it arrives, as
	// ExecOptions.OnLine does.
	OnLine func(string)
	// Origin is where the run was started from, for the history of the
	// sequence and of each step.
	Origin string
}

// NewSequenceRunner creates an idle runner for seq.
func NewSequenceRunner(seq Sequence, reg *Registry) *SequenceRunner {
	return &SequenceRunner{
		seq: seq,
		reg: reg,
		run: Run{Status: StatusNeverRun, StepTotal: len(seq.Steps)},
	}
}

// restore shows the newest run h holds as the last run, until there is a run
// of this session.
func (s *SequenceRunner) restore(h *History) {
	e, ok := h.Latest(s.seq.Name, "")
	kept, counted := h.Tally(s.seq.Name, "")
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok && s.run.Status == StatusNeverRun {
		run := e.Run()
		run.StepTotal = len(s.seq.Steps)
		s.run = run
	}
	if counted {
		s.tally = s.tally.merge(kept)
	}
}

// Tally is how often the sequence has run, the run in flight included.
func (s *SequenceRunner) Tally() Tally {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tally
}

// Sequence returns the definition this runner executes.
func (s *SequenceRunner) Sequence() Sequence { return s.seq }

// State returns a snapshot, splicing the in-flight step's live output onto the
// output already collected from completed steps.
func (s *SequenceRunner) State() Run {
	s.mu.RLock()
	run := s.run
	done := s.done
	cur := s.current
	s.mu.RUnlock()

	if run.Saved != nil {
		return run
	}
	logs := make([]string, 0, len(done)+64)
	logs = append(logs, done...)
	if cur != nil {
		logs = append(logs, cur.State().Logs...)
	}
	run.Logs = logs
	return run
}

// IsRunning reports whether the sequence is in flight.
func (s *SequenceRunner) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.run.Status == StatusRunning
}

// NeedsConfirm reports whether the sequence, or any step in it, must be
// confirmed before running. A sequence is at least as dangerous as its most
// dangerous step.
func (s *SequenceRunner) NeedsConfirm() bool {
	if s.seq.Confirm {
		return true
	}
	for _, name := range s.seq.Steps {
		if r, ok := s.reg.Lookup(name); ok && r.Runbook().Confirm {
			return true
		}
	}
	return false
}

// Params returns the union of every step's parameters, de-duplicated by name so
// the user answers each one once, up front, before step 1 starts.
func (s *SequenceRunner) Params() []config.ParamSpec {
	var out []config.ParamSpec
	seen := make(map[string]bool)
	for _, name := range s.seq.Steps {
		r, ok := s.reg.Lookup(name)
		if !ok {
			continue
		}
		for _, p := range r.Runbook().Params {
			if seen[p.Name] {
				continue
			}
			seen[p.Name] = true
			out = append(out, p)
		}
	}
	return out
}

// Start executes the sequence in the background.
func (s *SequenceRunner) Start(ctx context.Context, opts SequenceOptions) error {
	// Claimed before the goroutine, as Runner.Start does, so a second press
	// cannot start the sequence twice.
	if err := s.begin(opts); err != nil {
		return fmt.Errorf("sequence %q: %w", s.seq.Name, err)
	}
	s.keep(opts)
	go s.execute(ctx, opts)
	return nil
}

// Execute runs every step in order and returns the final state. The sequence
// succeeds only if every step succeeds.
func (s *SequenceRunner) Execute(ctx context.Context, opts SequenceOptions) Run {
	if err := s.begin(opts); errors.Is(err, errDeleting) {
		now := time.Now()
		return Run{Status: StatusFailed, ExitCode: -1, StartedAt: now, FinishedAt: now,
			StepTotal: len(s.seq.Steps), Err: fmt.Errorf("sequence %q: %w", s.seq.Name, err)}
	} else if err != nil {
		return s.State()
	}
	s.keep(opts)
	return s.execute(ctx, opts)
}

// keep starts keeping the run just claimed on disk, outside s.mu as
// Runner.keep does.
func (s *SequenceRunner) keep(opts SequenceOptions) {
	s.mu.RLock()
	run := s.run
	s.mu.RUnlock()
	rec := s.reg.openSequenceRecord(s.seq, run, opts)
	s.mu.Lock()
	s.rec = rec
	s.run.LogPath = rec.Path()
	s.tally.Runs = max(s.tally.Runs, rec.Number())
	s.mu.Unlock()
}

// recording is the run in flight on disk; nil when it is not kept.
func (s *SequenceRunner) recording() *Recording {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rec
}

// begin claims the sequence for one run, or says why it cannot: one is in
// flight, or the sequence is being deleted.
func (s *SequenceRunner) begin(opts SequenceOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.retired:
		return errDeleting
	case s.run.Status == StatusRunning:
		return errors.New("already running")
	}
	s.opts = opts
	s.cancel = nil
	s.cancelled = false
	s.done = nil
	s.rec = nil
	s.run = Run{
		Status:    StatusRunning,
		StartedAt: time.Now(),
		Attempt:   1,
		StepTotal: len(s.seq.Steps),
	}
	s.tally.Runs++
	return nil
}

// retire claims the sequence for its deletion, as Runner.retire does.
func (s *SequenceRunner) retire() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run.Status == StatusRunning {
		return fmt.Errorf("%s is running: delete it once it has finished", s.seq.Name)
	}
	s.retired = true
	return nil
}

// unretire gives the sequence back after a deletion that did not happen.
func (s *SequenceRunner) unretire() {
	s.mu.Lock()
	s.retired = false
	s.mu.Unlock()
}

func (s *SequenceRunner) execute(ctx context.Context, so SequenceOptions) Run {
	onLine := so.OnLine
	// The steps' lines, already redacted by their runners, go to the
	// sequence's own file too, as they arrive.
	rec := s.recording()
	stepLine := func(line string) {
		rec.Line(process.Timestamped(time.Now(), line))
		if onLine != nil {
			onLine(line)
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	s.mu.Lock()
	s.cancel = cancel
	// A Cancel between begin and here had no step to stop yet.
	if s.cancelled {
		cancel()
	}
	s.mu.Unlock()

	for i, name := range s.seq.Steps {
		runner, ok := s.reg.Lookup(name)
		if !ok {
			// Registry construction rejects unknown steps, so reaching here
			// means the registry changed underneath us.
			return s.finish(StatusFailed, -1, fmt.Errorf("step %q not found", name))
		}

		s.beginStep(i+1, name, runner)
		s.appendLine(fmt.Sprintf("── step %d/%d: %s ──", i+1, len(s.seq.Steps), name), onLine)

		opts := s.reg.ExecOptionsFor(runner.Runbook(), so.Params)
		opts.OnLine = stepLine
		opts.Origin = so.Origin
		result := runner.Execute(runCtx, opts)
		if result.Status == StatusRunning {
			// Execute could not claim the runner: the step was started on its
			// own. Finishing with its status would leave the sequence Running
			// for good.
			s.mu.Lock()
			s.current = nil
			s.mu.Unlock()
			s.appendLine(fmt.Sprintf("── stopped at step %d/%d: %s is already running on its own ──",
				i+1, len(s.seq.Steps), name), onLine)
			return s.finish(StatusFailed, -1, fmt.Errorf("step %q is already running", name))
		}

		s.collectStep(runner)

		if result.Status != StatusSucceeded {
			s.appendLine(fmt.Sprintf("── stopped at step %d/%d: %s (%s) ──",
				i+1, len(s.seq.Steps), name, result.Summary()), onLine)
			return s.finish(result.Status, result.ExitCode, result.Err)
		}
	}
	return s.finish(StatusSucceeded, 0, nil)
}

// Cancel interrupts the sequence and the step currently in flight.
func (s *SequenceRunner) Cancel() {
	s.mu.Lock()
	if s.run.Status != StatusRunning {
		s.mu.Unlock()
		return
	}
	s.cancelled = true
	cancel, cur := s.cancel, s.current
	s.mu.Unlock()

	if cur != nil {
		cur.Cancel()
	}
	if cancel != nil {
		cancel()
	}
}

func (s *SequenceRunner) beginStep(index int, name string, r *Runner) {
	s.mu.Lock()
	s.run.StepIndex = index
	s.run.StepName = name
	s.current = r
	s.mu.Unlock()
}

// collectStep freezes the finished step's output into the sequence log so it
// survives the step runner being reused.
func (s *SequenceRunner) collectStep(r *Runner) {
	logs := r.State().Logs
	s.mu.Lock()
	s.done = append(s.done, logs...)
	s.current = nil
	s.mu.Unlock()
}

func (s *SequenceRunner) appendLine(line string, onLine func(string)) {
	s.mu.Lock()
	s.done = append(s.done, line)
	rec := s.rec
	s.mu.Unlock()
	rec.Line(line)
	if onLine != nil {
		onLine(line)
	}
}

func (s *SequenceRunner) finish(status RunStatus, code int, err error) Run {
	s.mu.Lock()
	// A cancelled step reads as a cancelled sequence.
	if s.cancelled && status == StatusFailed {
		status = StatusCancelled
	}
	done := s.run
	done.Status, done.ExitCode, done.FinishedAt, done.Err = status, code, time.Now(), err
	opts, rec := s.opts, s.rec
	s.mu.Unlock()
	// Its files say it is over before the runner does, as a runbook's do.
	s.reg.closeSequenceRecord(rec, s.seq, done, opts)
	kept, counted := rec.Tally()
	s.mu.Lock()
	s.run = done
	s.tally = s.tally.settle(kept, counted, done.Status)
	s.current = nil
	s.mu.Unlock()
	return s.State()
}
