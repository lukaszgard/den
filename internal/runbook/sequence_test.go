package runbook

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
)

// testRegistry builds a registry from inline runbook specs only.
func testRegistry(t *testing.T, items []config.RunbookSpec, seqs []config.SequenceSpec) *Registry {
	t.Helper()
	cfg := &config.Config{Runbooks: &config.RunbooksConfig{Items: items, Sequences: seqs}}
	reg, err := NewRegistry(cfg, "", nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg
}

func TestRunnerExitCodes(t *testing.T) {
	cases := []struct {
		command string
		want    RunStatus
		code    int
	}{
		{"exit 0", StatusSucceeded, 0},
		{"exit 3", StatusFailed, 3},
		{"echo hi; exit 0", StatusSucceeded, 0},
	}
	for _, c := range cases {
		reg := testRegistry(t, []config.RunbookSpec{{Name: "X", Command: c.command}}, nil)
		r, _ := reg.Lookup("X")
		got := r.Execute(context.Background(), reg.ExecOptionsFor(r.Runbook(), nil))
		if got.Status != c.want || got.ExitCode != c.code {
			t.Errorf("%q -> %v/%d, want %v/%d", c.command, got.Status, got.ExitCode, c.want, c.code)
		}
	}
}

func TestRunnerCapturesStdoutAndStderr(t *testing.T) {
	reg := testRegistry(t, []config.RunbookSpec{
		{Name: "X", Command: "echo to-stdout; echo to-stderr >&2; exit 1"},
	}, nil)
	r, _ := reg.Lookup("X")

	var streamed []string
	opts := reg.ExecOptionsFor(r.Runbook(), nil)
	opts.OnLine = func(s string) { streamed = append(streamed, s) }
	got := r.Execute(context.Background(), opts)

	joined := strings.Join(streamed, "\n")
	for _, want := range []string{"to-stdout", "to-stderr"} {
		if !strings.Contains(joined, want) {
			t.Errorf("streamed output missing %q, got %q", want, joined)
		}
		if !strings.Contains(strings.Join(got.Logs, "\n"), want) {
			t.Errorf("state logs missing %q", want)
		}
	}
}

func TestRunnerRetryUntilSuccess(t *testing.T) {
	// Fails twice via a counter file, then succeeds.
	dir := t.TempDir()
	reg := testRegistry(t, []config.RunbookSpec{{
		Name: "Flaky",
		Command: `n=$(cat ` + dir + `/c 2>/dev/null || echo 0); n=$((n+1)); ` +
			`echo $n > ` + dir + `/c; echo "try $n"; [ "$n" -ge 3 ]`,
		Retry: &config.RetrySpec{UntilSuccess: true, Interval: "10ms", MaxAttempts: 5},
	}}, nil)
	r, _ := reg.Lookup("Flaky")

	var streamed []string
	opts := reg.ExecOptionsFor(r.Runbook(), nil)
	opts.OnLine = func(s string) { streamed = append(streamed, s) }
	got := r.Execute(context.Background(), opts)
	if got.Status != StatusSucceeded {
		t.Fatalf("status = %v, want succeeded", got.Status)
	}
	if got.Attempt != 3 {
		t.Errorf("attempt = %d, want 3", got.Attempt)
	}
	// One run keeps every attempt, each one marked, in its log and in what
	// den run prints.
	want := []string{"try 1", "── attempt 2 ──", "try 2", "── attempt 3 ──", "try 3"}
	if !inOrder(got.Logs, want) {
		t.Errorf("logs = %q, want the attempts in order: %q", got.Logs, want)
	}
	if !inOrder(streamed, want) {
		t.Errorf("streamed = %q, want the attempts in order: %q", streamed, want)
	}
}

// inOrder reports whether lines holds each of want, in that order, each line
// ending with it (logs carry a timestamp in front).
func inOrder(lines, want []string) bool {
	i := 0
	for _, l := range lines {
		if i < len(want) && strings.HasSuffix(l, want[i]) {
			i++
		}
	}
	return i == len(want) && len(lines) == len(want)
}

func TestRunnerRetryStopsAtMaxAttempts(t *testing.T) {
	reg := testRegistry(t, []config.RunbookSpec{{
		Name:    "Always fails",
		Command: "exit 1",
		Retry:   &config.RetrySpec{UntilSuccess: true, Interval: "1ms", MaxAttempts: 3},
	}}, nil)
	r, _ := reg.Lookup("Always fails")

	got := r.Execute(context.Background(), reg.ExecOptionsFor(r.Runbook(), nil))
	if got.Status != StatusFailed {
		t.Errorf("status = %v, want failed", got.Status)
	}
	if got.Attempt != 3 {
		t.Errorf("attempt = %d, want to stop at 3", got.Attempt)
	}
}

func TestRunnerTimeout(t *testing.T) {
	reg := testRegistry(t, []config.RunbookSpec{
		{Name: "Slow", Command: "sleep 30", Timeout: "150ms"},
	}, nil)
	r, _ := reg.Lookup("Slow")

	start := time.Now()
	got := r.Execute(context.Background(), reg.ExecOptionsFor(r.Runbook(), nil))
	if got.Status != StatusTimedOut {
		t.Errorf("status = %v, want timed out", got.Status)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %s — the timeout did not kill the process tree", elapsed)
	}
}

func TestSequenceStopsAtFirstFailure(t *testing.T) {
	dir := t.TempDir()
	reg := testRegistry(t,
		[]config.RunbookSpec{
			{Name: "one", Command: "echo one >> " + dir + "/log"},
			{Name: "two", Command: "echo two >> " + dir + "/log; exit 4"},
			{Name: "three", Command: "echo three >> " + dir + "/log"},
		},
		[]config.SequenceSpec{{Name: "S", Steps: []string{"one", "two", "three"}}},
	)
	s, ok := reg.LookupSequence("S")
	if !ok {
		t.Fatal("sequence not registered")
	}

	got := s.Execute(context.Background(), SequenceOptions{})
	if got.Status != StatusFailed {
		t.Errorf("status = %v, want failed", got.Status)
	}
	if got.ExitCode != 4 {
		t.Errorf("exit code = %d, want the failing step's 4", got.ExitCode)
	}
	if got.StepIndex != 2 {
		t.Errorf("stopped at step %d, want 2", got.StepIndex)
	}

	logs := strings.Join(got.Logs, "\n")
	if !strings.Contains(logs, "one") || !strings.Contains(logs, "two") {
		t.Errorf("sequence output should include both ran steps: %q", logs)
	}
	if strings.Contains(logs, "three") {
		t.Error("step three must not run after step two failed")
	}
}

func TestSequenceSucceedsWhenAllStepsPass(t *testing.T) {
	reg := testRegistry(t,
		[]config.RunbookSpec{
			{Name: "a", Command: "true"},
			{Name: "b", Command: "true"},
		},
		[]config.SequenceSpec{{Name: "S", Steps: []string{"a", "b"}}},
	)
	s, _ := reg.LookupSequence("S")
	if got := s.Execute(context.Background(), SequenceOptions{}); got.Status != StatusSucceeded {
		t.Errorf("status = %v, want succeeded", got.Status)
	}
}

func TestSequenceMergesStepParams(t *testing.T) {
	reg := testRegistry(t,
		[]config.RunbookSpec{
			{Name: "a", Command: "true", Params: []config.ParamSpec{{Name: "shared"}, {Name: "only_a"}}},
			{Name: "b", Command: "true", Params: []config.ParamSpec{{Name: "shared"}, {Name: "only_b"}}},
		},
		[]config.SequenceSpec{{Name: "S", Steps: []string{"a", "b"}}},
	)
	s, _ := reg.LookupSequence("S")

	got := s.Params()
	if len(got) != 3 {
		t.Fatalf("merged params = %d, want 3 (deduplicated): %+v", len(got), got)
	}
}

func TestSequenceNeedsConfirmIfAnyStepDoes(t *testing.T) {
	reg := testRegistry(t,
		[]config.RunbookSpec{
			{Name: "safe", Command: "true"},
			{Name: "risky", Command: "true", Confirm: true},
		},
		[]config.SequenceSpec{
			{Name: "HasRisky", Steps: []string{"safe", "risky"}},
			{Name: "AllSafe", Steps: []string{"safe"}},
		},
	)
	if s, _ := reg.LookupSequence("HasRisky"); !s.NeedsConfirm() {
		t.Error("a sequence containing a confirm-required step must require confirmation")
	}
	if s, _ := reg.LookupSequence("AllSafe"); s.NeedsConfirm() {
		t.Error("a sequence of safe steps must not require confirmation")
	}
}

// A sequence naming a missing step used to fail the whole registry, and the
// pane then said "No runbooks configured". It now costs only that sequence.
func TestRegistrySkipsASequenceWithAnUnknownStep(t *testing.T) {
	cfg := &config.Config{Runbooks: &config.RunbooksConfig{
		Items: []config.RunbookSpec{{Name: "a", Command: "true"}},
		Sequences: []config.SequenceSpec{
			{Name: "S", Steps: []string{"a", "ghost"}},
			{Name: "a", Steps: []string{"a"}},
			{Name: "Good", Steps: []string{"a"}},
		},
	}}
	reg, err := NewRegistry(cfg, "", nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, ok := reg.Lookup("a"); !ok {
		t.Fatal("the runbook is gone with the broken sequence")
	}
	if _, ok := reg.LookupSequence("Good"); !ok {
		t.Fatal("a valid sequence is gone with the broken one")
	}
	if _, ok := reg.LookupSequence("S"); ok {
		t.Fatal("the sequence with an unknown step was kept")
	}
	warns := fmt.Sprint(reg.Warnings())
	if !strings.Contains(warns, `unknown step "ghost"`) || !strings.Contains(warns, "same name") {
		t.Fatalf("warnings = %s, want both problems named", warns)
	}
}

func TestRegistryInlineShadowsDiscovered(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "dup.sh", "#!/bin/sh\n# den:name Shared Name\n", true)

	cfg := &config.Config{Runbooks: &config.RunbooksConfig{
		Dirs:  []string{dir},
		Items: []config.RunbookSpec{{Name: "Shared Name", Command: "echo inline"}},
	}}
	reg, err := NewRegistry(cfg, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(reg.Runbooks()); n != 1 {
		t.Fatalf("want 1 runbook after shadowing, got %d", n)
	}
	r, _ := reg.Lookup("Shared Name")
	if r.Runbook().Command != "echo inline" {
		t.Error("the reviewed den.yaml entry should win over a discovered file")
	}
	if len(reg.Warnings()) == 0 {
		t.Error("shadowing must produce a warning rather than happening silently")
	}
}

func TestRegistryWarnsOnUnknownEnvInDiscoveredRunbook(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "x.sh", "#!/bin/sh\n# den:name X\n# den:env ghost\n", true)

	cfg := &config.Config{
		Environments: map[string]config.EnvSpec{"real": {AWSProfile: "p"}},
		Runbooks:     &config.RunbooksConfig{Dirs: []string{dir}},
	}
	reg, err := NewRegistry(cfg, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Warnings()) == 0 {
		t.Fatal("an unknown env must warn, not silently inject nothing")
	}
	if !strings.Contains(reg.Warnings()[0].Msg, "ghost") {
		t.Errorf("warning should name the bad env, got %q", reg.Warnings()[0].Msg)
	}
}
