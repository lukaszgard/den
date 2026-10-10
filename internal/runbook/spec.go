// Package runbook runs user-defined operational scripts.
//
// A runbook is deliberately not a service.Service. A service is a long-lived
// connection whose interesting state is "am I still up"; a runbook runs to
// completion and its interesting state is an exit code. Modelling one as the
// other is what forces docker.Service to report StatusConnected for a finished
// command, and that fudge is exactly what this package avoids.
//
// den executes the command string as written. There is no per-language
// handling: Python is "python3 x.py", Go is "go run ./cmd/x", and a shebang
// script is invoked by path. Anything more elaborate — conditionals, branching,
// value checks — belongs inside the script, where a real language is available.
package runbook

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook/source"
)

// RunStatus is the state of a single execution.
type RunStatus int

const (
	StatusNeverRun RunStatus = iota
	StatusPending            // waiting on confirmation or parameters
	StatusRunning
	StatusSucceeded // exited 0
	StatusFailed    // exited non-zero
	StatusCancelled
	StatusTimedOut
)

func (s RunStatus) String() string {
	switch s {
	case StatusNeverRun:
		return "never run"
	case StatusPending:
		return "pending"
	case StatusRunning:
		return "running"
	case StatusSucceeded:
		return "succeeded"
	case StatusFailed:
		return "failed"
	case StatusCancelled:
		return "cancelled"
	case StatusTimedOut:
		return "timed out"
	default:
		return "unknown"
	}
}

// Terminal reports whether the status represents a finished run.
func (s RunStatus) Terminal() bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusCancelled, StatusTimedOut:
		return true
	}
	return false
}

// Retry re-runs a runbook until it exits 0. Interval and at least one of
// MaxAttempts/Timeout are guaranteed set by config validation.
type Retry struct {
	UntilSuccess bool
	Interval     time.Duration
	MaxAttempts  int // 0 = unlimited, bounded by Timeout instead
	Timeout      time.Duration
}

// Runbook is a resolved, ready-to-execute runbook from either den.yaml or a
// discovered script file.
type Runbook struct {
	Name        string
	Description string
	Command     string
	Cwd         string
	Env         string // key into the config's environments map
	EnvVars     map[string]string
	Params      []config.ParamSpec
	Confirm     bool
	Timeout     time.Duration
	Retry       *Retry

	// Source is the file a discovered runbook came from: its runbook.yaml,
	// or its script. Empty when the runbook was declared inline in den.yaml.
	Source string
	// Dir is the folder of a runbook defined by a runbook.yaml.
	Dir string
	// Kind is what it runs: bash, python, go, node, aws, … or "command" and
	// "shell" for an inline one-line or multi-line command.
	Kind string

	Settings Settings

	// StateDir is a folder of its own that outlives its runs and every
	// update of its source: DEN_RUNBOOK_STATE. Empty when den keeps none.
	StateDir string

	// Remote is set on a runbook from runbooks.sources: where it comes from.
	Remote *Remote
}

// Remote is where a runbook from a runbook source comes from.
type Remote struct {
	Source string // the source's name in den.yaml
	Folder string // its folder below the source's path; "" for one at it
	Repo   string // host/owner/repo
	Commit string // the commit checked out
	// Tree is the git tree of its folder: it changes exactly when something
	// in the folder does.
	Tree string
	// Trusted is set when the user has run this very tree. Until then, and
	// again after an update changed it, the runbook asks before it runs.
	Trusted bool
	// Note says why it asks: new, or changed since the user ran it.
	Note string
}

// Discovered reports whether this runbook came from a scanned directory.
func (r Runbook) Discovered() bool { return r.Source != "" }

// Home is where the runbook lives, and where its terminals open: the folder of
// a runbook.yaml, the directory of a discovered script, and the working
// directory of a runbook declared in den.yaml.
func (r Runbook) Home() string {
	switch {
	case r.Dir != "":
		return r.Dir
	case r.Source != "":
		return filepath.Dir(r.Source)
	}
	return r.Cwd
}

// LogsDir is where the runbook keeps its runs: logs/ in its folder, beside
// its script. "" when den keeps them in its state directory instead: a
// runbook declared in den.yaml or by a single script has no folder of its
// own, and one from a source has a folder that every update replaces.
func (r Runbook) LogsDir() string {
	if r.Dir == "" || r.Remote != nil {
		return ""
	}
	return filepath.Join(r.Dir, LogsFolder)
}

// DefaultHistory is how many finished runs are kept on disk when neither the
// runbook nor runbooks.defaults says otherwise.
const DefaultHistory = 20

// Settings are a runbook's presentation settings, its own over
// runbooks.defaults over den's.
type Settings struct {
	History int    // finished runs kept on disk; 0 keeps the last run, in memory
	About   bool   // show the About tab
	Shell   string // what t opens; "" is the user's shell
	Editor  string // what e opens the script with; "" is the user's editor
	Agent   string // what E changes the runbook with; "" is runbooks.agent
}

func mergeSettings(own, defaults config.RunbookSettings) Settings {
	s := Settings{History: DefaultHistory, About: true}
	for _, layer := range []config.RunbookSettings{defaults, own} {
		if layer.History != nil {
			s.History = *layer.History
		}
		if layer.About != nil {
			s.About = *layer.About
		}
		if layer.Shell != "" {
			s.Shell = layer.Shell
		}
		if layer.Editor != "" {
			s.Editor = layer.Editor
		}
		if layer.Agent != "" {
			s.Agent = layer.Agent
		}
	}
	return s
}

// Origin describes where the runbook was defined, for confirmation prompts.
func (r Runbook) Origin() string {
	if r.Remote != nil {
		return fmt.Sprintf("%s · %s @ %s", r.Remote.Source, r.Remote.Repo, source.Short(r.Remote.Commit))
	}
	if r.Source != "" {
		return r.Source
	}
	return "den.yaml"
}

// Sequence runs named runbooks in order, stopping at the first failure.
type Sequence struct {
	Name        string
	Description string
	Steps       []string
	Confirm     bool
}

// Run is the live or last-known state of one execution.
type Run struct {
	Status     RunStatus
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
	Attempt    int // 1-based; >1 only when retrying
	Logs       []string
	Err        error

	// Sequence progress. StepTotal is 0 for a plain runbook.
	StepIndex int // 1-based index of the step in flight
	StepTotal int
	StepName  string

	// LogPath is the file the run's output is kept in, written as it goes;
	// "" when its runs are not kept.
	LogPath string

	// Saved is the history entry this state was restored from when den
	// started: a run from before, whose output is on disk rather than in Logs.
	Saved *Entry
}

// Duration is how long the run took, or how long it has been going.
func (r Run) Duration() time.Duration {
	if r.StartedAt.IsZero() {
		return 0
	}
	if r.FinishedAt.IsZero() {
		return time.Since(r.StartedAt)
	}
	return r.FinishedAt.Sub(r.StartedAt)
}

// Summary is a short human-readable result, e.g. "exit 0" or "attempt 3".
func (r Run) Summary() string {
	switch r.Status {
	case StatusNeverRun:
		return "never run"
	case StatusPending:
		return "pending"
	case StatusRunning:
		if r.Attempt > 1 {
			return fmt.Sprintf("attempt %d", r.Attempt)
		}
		return "running"
	case StatusSucceeded:
		return "exit 0"
	case StatusFailed:
		return fmt.Sprintf("exit %d", r.ExitCode)
	case StatusCancelled:
		return "cancelled"
	case StatusTimedOut:
		return "timed out"
	}
	return ""
}

// fromSpec converts a validated config spec into a runnable Runbook, its
// settings merged over defaults. Durations are pre-parsed here so nothing
// downstream has to handle a parse error at run time.
func fromSpec(s config.RunbookSpec, defaults config.RunbookSettings) (Runbook, error) {
	rb := Runbook{
		Name:        s.Name,
		Description: s.Description,
		Command:     s.Command,
		Cwd:         s.Cwd,
		Env:         s.Env,
		EnvVars:     s.EnvVars,
		Params:      s.Params,
		Confirm:     s.Confirm,
		Source:      s.Source,
		Dir:         s.Dir,
		Settings:    mergeSettings(s.RunbookSettings, defaults),
	}
	if s.Timeout != "" {
		d, err := time.ParseDuration(s.Timeout)
		if err != nil {
			return Runbook{}, fmt.Errorf("runbook %q: invalid timeout %q: %w", s.Name, s.Timeout, err)
		}
		rb.Timeout = d
	}
	if s.Retry != nil {
		r := &Retry{UntilSuccess: s.Retry.UntilSuccess, MaxAttempts: s.Retry.MaxAttempts}
		if s.Retry.Interval != "" {
			d, err := time.ParseDuration(s.Retry.Interval)
			if err != nil {
				return Runbook{}, fmt.Errorf("runbook %q: invalid retry.interval %q: %w", s.Name, s.Retry.Interval, err)
			}
			r.Interval = d
		}
		if s.Retry.Timeout != "" {
			d, err := time.ParseDuration(s.Retry.Timeout)
			if err != nil {
				return Runbook{}, fmt.Errorf("runbook %q: invalid retry.timeout %q: %w", s.Name, s.Retry.Timeout, err)
			}
			r.Timeout = d
		}
		rb.Retry = r
	}
	rb.Kind = kindOf(rb)
	return rb, nil
}
