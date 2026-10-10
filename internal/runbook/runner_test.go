package runbook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
)

// waitIdle waits until r has finished its run.
func waitIdle(t *testing.T, r *Runner) Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for r.IsRunning() {
		if time.Now().After(deadline) {
			t.Fatalf("runbook %q still running", r.Runbook().Name)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return r.State()
}

// Two presses of enter in quick succession used to both pass the running
// check, because the run only became Running inside the goroutine Start
// launched: two processes then shared one Supervisor.
func TestStartTwiceStartsOneRun(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "starts")
	r := NewRunner(Runbook{Name: "Once", Command: "echo x >> " + marker + "; sleep 0.3"})

	if err := r.Start(context.Background(), ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(context.Background(), ExecOptions{}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second Start = %v, want already running", err)
	}
	if got := waitIdle(t, r); got.Status != StatusSucceeded {
		t.Fatalf("status = %v, want succeeded", got.Status)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "x"); n != 1 {
		t.Fatalf("the command ran %d times, want once", n)
	}
}

// A cancel that lands between Start and the process starting must still stop
// the run, rather than being lost because there was no process to signal yet.
func TestCancelRightAfterStartStopsTheRun(t *testing.T) {
	r := NewRunner(Runbook{Name: "Slow", Command: "sleep 5"})
	if err := r.Start(context.Background(), ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	r.Cancel()
	if got := waitIdle(t, r); got.Status != StatusCancelled {
		t.Fatalf("status = %v, want cancelled", got.Status)
	}
}

// A step whose runbook is already running on its own cannot be claimed by the
// sequence. The sequence used to finish with the step's Running status and
// stay "running" for good.
func TestSequenceFailsWhenAStepIsAlreadyRunning(t *testing.T) {
	reg := testRegistry(t, []config.RunbookSpec{{Name: "Slow", Command: "sleep 2"}},
		[]config.SequenceSpec{{Name: "Seq", Steps: []string{"Slow"}}})
	step, _ := reg.Lookup("Slow")
	if err := step.Start(context.Background(), ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	defer func() { step.Cancel(); waitIdle(t, step) }()

	seq, _ := reg.LookupSequence("Seq")
	got := seq.Execute(context.Background(), SequenceOptions{})
	if got.Status != StatusFailed || seq.IsRunning() {
		t.Fatalf("sequence status = %v (running %v), want failed", got.Status, seq.IsRunning())
	}
	if !strings.Contains(strings.Join(got.Logs, "\n"), "already running") {
		t.Fatalf("logs = %q, want the reason", got.Logs)
	}
}
