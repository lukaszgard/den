package runbook

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
)

func TestHistorySaveListLoadPrune(t *testing.T) {
	h := NewHistory(t.TempDir())
	start := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	for i := range 4 {
		e := Entry{Status: StatusSucceeded.String(), StartedAt: start.Add(time.Duration(i) * time.Minute), Origin: OriginCLI}
		if err := h.Save("Assume role (non-prod)", "", 3, e, []string{"run", string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := h.List("Assume role (non-prod)", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("kept %d runs, want the newest 3", len(entries))
	}
	if !entries[0].StartedAt.Equal(start.Add(3*time.Minute)) || !entries[2].StartedAt.Equal(start.Add(time.Minute)) {
		t.Fatalf("entries not newest first: %v .. %v", entries[0].StartedAt, entries[2].StartedAt)
	}
	logs, err := h.Load(entries[0])
	if err != nil || strings.Join(logs, ",") != "run,d" {
		t.Fatalf("Load = %q, %v", logs, err)
	}
	dir := h.dir("Assume role (non-prod)", "")
	if !strings.HasPrefix(filepath.Base(dir), "assume-role-non-prod-") {
		t.Errorf("folder %q, want it named after the runbook", filepath.Base(dir))
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(files) != 6 {
		t.Errorf("%d files on disk, want 3 runs × .log + .json", len(files))
	}
	for _, f := range files {
		if info, _ := os.Stat(f); info.Mode().Perm() != 0o600 {
			t.Errorf("%s is %v, want private to the user", f, info.Mode().Perm())
		}
	}
	if got := entries[0].Run(); got.Status != StatusSucceeded || got.Saved == nil {
		t.Errorf("Entry.Run = %+v, want a finished run pointing back at its entry", got)
	}
}

// A runbook in a folder keeps its runs in that folder's logs/, numbered in
// the order they ran; one without a folder keeps them in den's state
// directory, named the same way.
func TestRunsAreKeptInTheRunbooksOwnFolder(t *testing.T) {
	root := t.TempDir()
	folder := writeFolder(t, root, "assume-role", "name: Assume role\ncommand: echo \"run $DEN_PARAM_N\"\nhistory: 2\nparams:\n  - name: n\n", nil)
	cfg := &config.Config{Runbooks: &config.RunbooksConfig{
		Dirs:  []string{root},
		Items: []config.RunbookSpec{{Name: "Inline", Command: "echo inline"}},
	}}
	reg, _ := NewRegistry(cfg, "", nil)
	state := t.TempDir()
	reg.SetHistory(NewHistory(state), func(err error) { t.Error(err) })
	r, ok := reg.Lookup("Assume role")
	if !ok {
		t.Fatalf("folder runbook not found: %v", reg.Warnings())
	}
	logs := filepath.Join(folder, LogsFolder)
	if got := r.Runbook().LogsDir(); got != logs {
		t.Fatalf("LogsDir = %q, want %q", got, logs)
	}
	for _, n := range []string{"one", "two", "three"} {
		r.Execute(context.Background(), reg.ExecOptionsFor(r.Runbook(), map[string]string{"n": n}))
	}

	// history: 2 keeps the last two, and a new run is numbered past them,
	// never reusing a pruned one's number.
	var names []string
	files, _ := os.ReadDir(logs)
	for _, f := range files {
		names = append(names, f.Name())
	}
	if want := ".gitignore log-02.json log-02.log log-03.json log-03.log"; strings.Join(names, " ") != want {
		t.Fatalf("logs/ holds %q, want %q", names, want)
	}
	runs, _ := reg.Runs("Assume role")
	if len(runs) != 2 || runs[0].LogPath() != filepath.Join(logs, "log-03.log") {
		t.Fatalf("Runs = %+v, want log-03 first", runs)
	}
	if lines, _ := reg.History().Load(runs[0]); len(lines) != 1 || !strings.HasSuffix(lines[0], "run three") {
		t.Errorf("log-03 holds %q, want the third run only", lines)
	}
	if st := r.State(); st.LogPath != runs[0].LogPath() {
		t.Errorf("State().LogPath = %q, want %q", st.LogPath, runs[0].LogPath())
	}
	if ignore, _ := os.ReadFile(filepath.Join(logs, ".gitignore")); !strings.HasSuffix(string(ignore), "\n*\n") {
		t.Errorf(".gitignore = %q, want everything in logs/ ignored", ignore)
	}
	if left, _ := filepath.Glob(filepath.Join(state, "*")); len(left) != 0 {
		t.Errorf("the folder runbook wrote into den's state directory: %q", left)
	}

	inline, _ := reg.Lookup("Inline")
	inline.Execute(context.Background(), reg.ExecOptionsFor(inline.Runbook(), nil))
	if e, ok := reg.History().Latest("Inline", ""); !ok || filepath.Base(e.LogPath()) != "log-01.log" ||
		!strings.HasPrefix(e.LogPath(), state) {
		t.Errorf("den.yaml runbook kept in %q, want log-01.log under %s", e.LogPath(), state)
	}
}

// A logs/ the runbook's own script writes to is shared, not taken over: den
// adds its numbered runs, prunes only those, and leaves its .gitignore alone.
func TestRunsLeaveTheScriptsOwnLogsAlone(t *testing.T) {
	h := NewHistory(t.TempDir())
	logs := filepath.Join(t.TempDir(), LogsFolder)
	if err := os.Mkdir(logs, 0o750); err != nil {
		t.Fatal(err)
	}
	theirs := []string{"app.log", "log-latest.log", "log-07.txt"}
	for _, f := range theirs {
		if err := os.WriteFile(filepath.Join(logs, f), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	for i := range 3 {
		e := Entry{Status: StatusSucceeded.String(), StartedAt: start.Add(time.Duration(i) * time.Minute)}
		if err := h.Save("Job", logs, 1, e, []string{"run"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range append(theirs, "log-03.log", "log-03.json") {
		if _, err := os.Stat(filepath.Join(logs, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(logs, ".gitignore")); err == nil {
		t.Error("den wrote a .gitignore into a logs/ it did not make")
	}
	if runs, _ := h.List("Job", logs); len(runs) != 1 {
		t.Errorf("listed %d runs, want den's one", len(runs))
	}
}

// What reaches disk is what was shown: secret parameter values are masked in
// the output and absent from the entry.
func TestHistoryKeepsSecretsOffDisk(t *testing.T) {
	cfg := &config.Config{Runbooks: &config.RunbooksConfig{Items: []config.RunbookSpec{{
		Name:    "Rotate",
		Command: `echo "token=$DEN_PARAM_TOKEN tier=$DEN_PARAM_TIER"`,
		Params: []config.ParamSpec{
			{Name: "token", Secret: true},
			{Name: "tier", Default: "prod"},
		},
	}}}}
	reg, _ := NewRegistry(cfg, "", nil)
	h := NewHistory(t.TempDir())
	reg.SetHistory(h, func(err error) { t.Error(err) })

	r, _ := reg.Lookup("Rotate")
	opts := reg.ExecOptionsFor(r.Runbook(), map[string]string{"token": "s3cr3t-value"})
	opts.Origin = OriginTUI
	r.Execute(context.Background(), opts)

	e, ok := h.Latest("Rotate", "")
	if !ok {
		t.Fatal("the run was not kept")
	}
	if e.Origin != OriginTUI || e.Params["tier"] != "prod" {
		t.Errorf("entry = %+v, want its origin and public params", e)
	}
	if _, leaked := e.Params["token"]; leaked {
		t.Error("a secret parameter reached the entry")
	}
	data, _ := os.ReadFile(e.log)
	meta, _ := os.ReadFile(strings.TrimSuffix(e.log, logExt) + metaExt)
	for _, b := range [][]byte{data, meta} {
		if strings.Contains(string(b), "s3cr3t-value") {
			t.Fatalf("the secret reached disk:\n%s", b)
		}
	}
	if !strings.Contains(string(data), "token=••••••") {
		t.Fatalf("log = %q, want the masked value", data)
	}
}

func TestHistoryRestoresTheLastRunAndHonoursHistoryZero(t *testing.T) {
	dir := t.TempDir()
	zero := 0
	cfg := &config.Config{Runbooks: &config.RunbooksConfig{
		Items: []config.RunbookSpec{
			{Name: "Kept", Command: "echo kept; exit 3"},
			{Name: "Forgotten", Command: "echo gone", RunbookSettings: config.RunbookSettings{History: &zero}},
		},
		Sequences: []config.SequenceSpec{{Name: "Both", Steps: []string{"Kept"}}},
	}}
	reg, _ := NewRegistry(cfg, "", nil)
	reg.SetHistory(NewHistory(dir), nil)
	for _, name := range []string{"Kept", "Forgotten"} {
		r, _ := reg.Lookup(name)
		r.Execute(context.Background(), reg.ExecOptionsFor(r.Runbook(), nil))
	}
	seq, _ := reg.LookupSequence("Both")
	seq.Execute(context.Background(), SequenceOptions{Origin: OriginCLI})

	// As a fresh den process would see it.
	again, _ := NewRegistry(cfg, "", nil)
	again.SetHistory(NewHistory(dir), nil)
	kept, _ := again.Lookup("Kept")
	st := kept.State()
	if st.Status != StatusFailed || st.ExitCode != 3 || st.Saved == nil {
		t.Fatalf("restored state = %+v, want the failed run from before", st)
	}
	if logs, _ := again.History().Load(*st.Saved); !strings.Contains(strings.Join(logs, "\n"), "kept") {
		t.Fatalf("restored output = %q", logs)
	}
	if forgotten, _ := again.Lookup("Forgotten"); forgotten.State().Status != StatusNeverRun {
		t.Fatal("history: 0 kept a run on disk")
	}
	if s, _ := again.LookupSequence("Both"); s.State().Status != StatusFailed || s.State().StepTotal != 1 {
		t.Fatalf("restored sequence = %+v", s.State())
	}
	if e, _ := again.History().Latest("Kept", ""); e.Origin != OriginCLI {
		t.Errorf("the step run inside the sequence was recorded with origin %q, want the sequence's", e.Origin)
	}

	// A new run of this session replaces what was restored.
	kept.Execute(context.Background(), again.ExecOptionsFor(kept.Runbook(), nil))
	if kept.State().Saved != nil || len(kept.State().Logs) == 0 {
		t.Fatal("a live run still shows the restored one")
	}
}

// Every run used to be kept with the output of the runs before it in the same
// session: the runner's buffer was never emptied, so the second run's .log
// opened with the whole first run.
func TestEachRunKeepsOnlyItsOwnOutput(t *testing.T) {
	cfg := &config.Config{Runbooks: &config.RunbooksConfig{Items: []config.RunbookSpec{{
		Name:    "Count",
		Command: `echo "run $DEN_PARAM_N"`,
		Params:  []config.ParamSpec{{Name: "n"}},
	}}}}
	reg, _ := NewRegistry(cfg, "", nil)
	h := NewHistory(t.TempDir())
	reg.SetHistory(h, func(err error) { t.Error(err) })
	r, _ := reg.Lookup("Count")
	for _, n := range []string{"one", "two"} {
		r.Execute(context.Background(), reg.ExecOptionsFor(r.Runbook(), map[string]string{"n": n}))
	}

	if live := strings.Join(r.State().Logs, "\n"); strings.Contains(live, "run one") || !strings.Contains(live, "run two") {
		t.Fatalf("the second run shows %q, want its own output only", live)
	}
	entries, _ := h.List("Count", "")
	if len(entries) != 2 {
		t.Fatalf("kept %d runs, want 2", len(entries))
	}
	for i, want := range []string{"run two", "run one"} {
		logs, err := h.Load(entries[i])
		if err != nil {
			t.Fatal(err)
		}
		if len(logs) != 1 || !strings.HasSuffix(logs[0], want) {
			t.Errorf("run %d kept %q, want only %q", i, logs, want)
		}
	}
}

// waitFor polls cond until it holds, failing the test after a while.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A run is on disk from its first line, not only once it is over: a den that
// dies mid-run keeps what it printed, and its log can be followed with tail.
func TestHistoryStreamsARunAsItGoes(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "gate")
	cfg := &config.Config{Runbooks: &config.RunbooksConfig{Items: []config.RunbookSpec{{
		Name:    "Slow",
		Command: `echo started; while [ ! -f "` + gate + `" ]; do sleep 0.02; done; echo done`,
	}}}}
	reg, _ := NewRegistry(cfg, "", nil)
	h := NewHistory(t.TempDir())
	reg.SetHistory(h, func(err error) { t.Error(err) })
	r, _ := reg.Lookup("Slow")
	opts := reg.ExecOptionsFor(r.Runbook(), nil)
	opts.Origin = OriginCLI
	if err := r.Start(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	var running Entry
	waitFor(t, "the run's first line on disk", func() bool {
		entries, _ := h.List("Slow", "")
		if len(entries) != 1 {
			return false
		}
		running = entries[0]
		logs, _ := h.Load(running)
		return len(logs) == 1 && strings.HasSuffix(logs[0], "] started")
	})
	if running.RunStatus() != StatusRunning || running.PID != os.Getpid() || running.Origin != OriginCLI {
		t.Fatalf("entry while running = %+v, want running, from this process", running)
	}
	if got := r.State().LogPath; got == "" || got != running.LogPath() {
		t.Fatalf("State().LogPath = %q, want the entry's log %q", got, running.LogPath())
	}

	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, r)
	entries, _ := h.List("Slow", "")
	if len(entries) != 1 {
		t.Fatalf("kept %d entries, want the one run", len(entries))
	}
	done := entries[0]
	if done.RunStatus() != StatusSucceeded || done.PID != 0 || done.LogPath() != running.LogPath() {
		t.Fatalf("entry once done = %+v, want succeeded, with no pid, in the same files", done)
	}
	if logs, _ := h.Load(done); len(logs) != 2 || !strings.HasSuffix(logs[1], "] done") {
		t.Fatalf("log = %q, want both lines", logs)
	}
}

// The file keeps the whole run; only what den holds in memory is bounded.
func TestHistoryKeepsEveryLineOfALongRun(t *testing.T) {
	reg := testRegistry(t, []config.RunbookSpec{{
		Name:    "Chatty",
		Command: `i=0; while [ $i -lt 5200 ]; do echo "line $i"; i=$((i+1)); done`,
	}}, nil)
	h := NewHistory(t.TempDir())
	reg.SetHistory(h, func(err error) { t.Error(err) })
	r, _ := reg.Lookup("Chatty")
	got := r.Execute(context.Background(), reg.ExecOptionsFor(r.Runbook(), nil))
	if len(got.Logs) != runbookLogLines {
		t.Fatalf("held %d lines in memory, want the last %d", len(got.Logs), runbookLogLines)
	}
	e, _ := h.Latest("Chatty", "")
	logs, _ := h.Load(e)
	if len(logs) != 5200 || !strings.HasSuffix(logs[0], "] line 0") || !strings.HasSuffix(logs[5199], "] line 5199") {
		t.Fatalf("kept %d lines (%q … %q), want all 5200", len(logs), first(logs), last(logs))
	}
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

func last(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[len(s)-1]
}

// writeEntry puts run n on disk the way a den process would have left it.
func writeEntry(t *testing.T, h *History, name string, n int, e Entry, log string) {
	t.Helper()
	dir := h.dir(name, "")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, runName(n))
	meta, _ := json.Marshal(e)
	if err := os.WriteFile(base+logExt, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+metaExt, meta, 0o600); err != nil {
		t.Fatal(err)
	}
}

// deadPID is the pid of a process that has come and gone.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// A run whose den died never got its final status. It must read as
// interrupted rather than running forever, and a run another den is still
// going through must be neither restored as this process's last run nor
// pruned from under it.
func TestHistoryTellsARunStillGoingFromOneCutShort(t *testing.T) {
	h := NewHistory(t.TempDir())
	start := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	running := StatusRunning.String()
	writeEntry(t, h, "Job", 2, Entry{Status: StatusSucceeded.String(), StartedAt: start}, "old\n")
	writeEntry(t, h, "Job", 3, Entry{Status: running, StartedAt: start.Add(time.Minute), PID: deadPID(t)}, "cut\n")
	writeEntry(t, h, "Job", 4, Entry{Status: running, StartedAt: start.Add(2 * time.Minute), PID: os.Getpid()}, "going\n")

	entries, _ := h.List("Job", "")
	if len(entries) != 3 {
		t.Fatalf("listed %d runs, want 3", len(entries))
	}
	going, cut := entries[0], entries[1]
	if going.RunStatus() != StatusRunning || going.Interrupted() {
		t.Errorf("the live run reads as %v (interrupted %v)", going.RunStatus(), going.Interrupted())
	}
	if !cut.Interrupted() || cut.RunStatus() != StatusFailed || cut.Run().Summary() != "exit -1" || cut.Error == "" {
		t.Errorf("the run cut short = %+v, want an interrupted failure with a reason", cut)
	}
	if latest, ok := h.Latest("Job", ""); !ok || latest.Running() {
		t.Errorf("Latest = %+v, want the newest run that is over", latest)
	}

	// A crash between the two writes leaves a .log alone: debris once older
	// than every run kept.
	orphan := filepath.Join(h.dir("Job", ""), runName(1)+logExt)
	if err := os.WriteFile(orphan, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.Save("Job", "", 1, Entry{Status: StatusSucceeded.String(), StartedAt: start.Add(3 * time.Minute)}, []string{"new"}); err != nil {
		t.Fatal(err)
	}
	entries, _ = h.List("Job", "")
	var kept []string
	for _, e := range entries {
		logs, _ := h.Load(e)
		kept = append(kept, strings.Join(logs, ""))
	}
	if strings.Join(kept, ",") != "new,going" {
		t.Errorf("after pruning to 1, kept %q, want the newest run and the one still going", kept)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("the orphan log is still there (%v)", err)
	}
}

// A run that started in another den and is still going must not show as the
// last run here: the runbook would read as running, and r would refuse.
func TestRestoreSkipsARunStillGoingElsewhere(t *testing.T) {
	dir := t.TempDir()
	h := NewHistory(dir)
	start := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	writeEntry(t, h, "Job", 1, Entry{Status: StatusFailed.String(), ExitCode: 2, StartedAt: start}, "")
	writeEntry(t, h, "Job", 2, Entry{Status: StatusRunning.String(), StartedAt: start.Add(time.Minute), PID: os.Getpid()}, "")
	reg := testRegistry(t, []config.RunbookSpec{{Name: "Job", Command: "true"}}, nil)
	reg.SetHistory(NewHistory(dir), nil)
	r, _ := reg.Lookup("Job")
	if st := r.State(); st.Status != StatusFailed || st.ExitCode != 2 || r.IsRunning() {
		t.Fatalf("restored %+v, want the failed run that is over", st)
	}
}

// Every run counts, pruned ones too: a run is numbered past the newest kept.
// Failures are counted among the runs kept, so one pruned stops counting.
func TestTallyCountsEveryRunAndTheKeptFailures(t *testing.T) {
	keep, zero := 2, 0
	code := []config.ParamSpec{{Name: "code"}}
	cfg := &config.Config{Runbooks: &config.RunbooksConfig{Items: []config.RunbookSpec{
		{Name: "Kept", Command: `exit "$DEN_PARAM_CODE"`, Params: code, RunbookSettings: config.RunbookSettings{History: &keep}},
		{Name: "Forgotten", Command: `exit "$DEN_PARAM_CODE"`, Params: code, RunbookSettings: config.RunbookSettings{History: &zero}},
	}}}
	dir := t.TempDir()
	reg, _ := NewRegistry(cfg, "", nil)
	reg.SetHistory(NewHistory(dir), func(err error) { t.Error(err) })
	run := func(name, exit string) Tally {
		t.Helper()
		r, _ := reg.Lookup(name)
		r.Execute(context.Background(), reg.ExecOptionsFor(r.Runbook(), map[string]string{"code": exit}))
		return r.Tally()
	}

	for i, c := range []struct {
		exit string
		want Tally
	}{
		{"1", Tally{Runs: 1, Failed: 1}},
		{"0", Tally{Runs: 2, Failed: 1}},
		{"0", Tally{Runs: 3, Failed: 0}}, // the failed run is pruned
	} {
		if got := run("Kept", c.exit); got != c.want {
			t.Fatalf("after run %d: tally %+v, want %+v", i+1, got, c.want)
		}
	}
	// history: 0 keeps nothing on disk: the tally is this session's.
	run("Forgotten", "1")
	if got := run("Forgotten", "0"); got != (Tally{Runs: 2, Failed: 1}) {
		t.Fatalf("history: 0 tally %+v, want this session's 2 runs, 1 failed", got)
	}

	// As a fresh den process would see it.
	again, _ := NewRegistry(cfg, "", nil)
	again.SetHistory(NewHistory(dir), nil)
	if r, _ := again.Lookup("Kept"); r.Tally() != (Tally{Runs: 3}) {
		t.Errorf("restored tally %+v, want 3 runs", r.Tally())
	}
	if r, _ := again.Lookup("Forgotten"); r.Tally() != (Tally{}) {
		t.Errorf("history: 0 restored tally %+v, want none", r.Tally())
	}
}

// A run counts from the moment it starts, and a sequence's run counts for the
// sequence and for each of its steps.
func TestTallyCountsARunInFlightAndASequencesSteps(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "gate")
	reg := testRegistry(t, []config.RunbookSpec{
		{Name: "Wait", Command: `while [ ! -f "` + gate + `" ]; do sleep 0.02; done`},
	}, []config.SequenceSpec{{Name: "Seq", Steps: []string{"Wait"}}})
	reg.SetHistory(NewHistory(t.TempDir()), func(err error) { t.Error(err) })
	seq, _ := reg.LookupSequence("Seq")
	if err := seq.Start(context.Background(), SequenceOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := seq.Tally(); got != (Tally{Runs: 1}) {
		t.Errorf("tally of the sequence in flight %+v, want 1 run", got)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the sequence to finish", func() bool { return !seq.IsRunning() })
	wait, _ := reg.Lookup("Wait")
	if seq.Tally() != (Tally{Runs: 1}) || wait.Tally() != (Tally{Runs: 1}) {
		t.Fatalf("tallies: sequence %+v, step %+v, want 1 run each", seq.Tally(), wait.Tally())
	}
}

// A sequence's own log is written as it goes too, and holds only this run of
// each step.
func TestSequenceStreamsItsOwnLog(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "gate")
	reg := testRegistry(t, []config.RunbookSpec{
		{Name: "A", Command: `echo "A $DEN_PARAM_N"`, Params: []config.ParamSpec{{Name: "n"}}},
		{Name: "B", Command: `while [ ! -f "` + gate + `" ]; do sleep 0.02; done; echo B`},
	}, []config.SequenceSpec{{Name: "Both", Steps: []string{"A", "B"}}})
	h := NewHistory(t.TempDir())
	reg.SetHistory(h, func(err error) { t.Error(err) })
	a, _ := reg.Lookup("A")
	a.Execute(context.Background(), reg.ExecOptionsFor(a.Runbook(), map[string]string{"n": "before"}))

	seq, _ := reg.LookupSequence("Both")
	if err := seq.Start(context.Background(), SequenceOptions{Params: map[string]string{"n": "now"}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the sequence's log to reach step 2", func() bool {
		e, ok := h.firstListed("Both")
		if !ok || e.RunStatus() != StatusRunning {
			return false
		}
		logs, _ := h.Load(e)
		return strings.Contains(strings.Join(logs, "\n"), "── step 2/2: B ──")
	})
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the sequence to finish", func() bool { return !seq.IsRunning() })

	e, _ := h.Latest("Both", "")
	logs, _ := h.Load(e)
	want := []string{"── step 1/2: A ──", "A now", "── step 2/2: B ──", "B"}
	if e.RunStatus() != StatusSucceeded || !inOrder(logs, want) {
		t.Fatalf("sequence log = %q (%v), want %q", logs, e.RunStatus(), want)
	}
}

// The moment a run reads as over, its record on disk says so too. It used to
// be written just after: History read in between listed the run as still
// going, and the TUI did not read it again.
func TestARunIsOverOnDiskBeforeItIsOverInDen(t *testing.T) {
	reg := testRegistry(t, []config.RunbookSpec{{Name: "Quick", Command: "echo hi"}},
		[]config.SequenceSpec{{Name: "Seq", Steps: []string{"Quick"}}})
	h := NewHistory(t.TempDir())
	reg.SetHistory(h, func(err error) { t.Error(err) })
	r, _ := reg.Lookup("Quick")
	seq, _ := reg.LookupSequence("Seq")
	for range 20 {
		if err := r.Start(context.Background(), reg.ExecOptionsFor(r.Runbook(), nil)); err != nil {
			t.Fatal(err)
		}
		for r.IsRunning() {
			runtime.Gosched()
		}
		if e, _ := h.firstListed("Quick"); e.Running() {
			t.Fatal("the runbook's run is over, but its record says it is still going")
		}
		if err := seq.Start(context.Background(), SequenceOptions{}); err != nil {
			t.Fatal(err)
		}
		for seq.IsRunning() {
			runtime.Gosched()
		}
		if e, _ := h.firstListed("Seq"); e.Running() {
			t.Fatal("the sequence's run is over, but its record says it is still going")
		}
	}
}

// firstListed is the newest entry of name, running or not.
func (h *History) firstListed(name string) (Entry, bool) {
	entries, _ := h.List(name, "")
	if len(entries) == 0 {
		return Entry{}, false
	}
	return entries[0], true
}
