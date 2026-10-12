package runbook

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/runbook/source"
)

// Registry holds every runbook and sequence den knows about, along with their
// run state.
//
// State lives here rather than in the TUI pane on purpose: Bubble Tea
// rebuilds panes on window resize, so a pane that owned its runs would lose
// them — or worse, orphan the processes — every time the terminal changed size.
type Registry struct {
	mu sync.RWMutex

	runners   []*Runner
	byName    map[string]*Runner
	sequences []*SequenceRunner
	seqByName map[string]*SequenceRunner
	warnings  []Warning
	// generation changes whenever the set of runbooks or a definition does,
	// so a view knows to rebuild its rows.
	generation uint64

	cfg     config.RunbooksConfig
	envs    map[string]config.EnvSpec
	cfgPath string
	ports   PortSource

	history     *History
	historyFail func(error)

	// sources is where runbook sources are checked out; nil reads none.
	sources *source.Store
	trust   *Trust
	// stateRoot holds each runbook's DEN_RUNBOOK_STATE folder; "" keeps none.
	stateRoot string
	// drafts keeps requests typed for an agent; nil keeps none.
	drafts *Drafts
}

// NewRegistry merges the inline runbooks from den.yaml with those discovered in
// the scanned directories, then wires up sequences.
//
// cfgPath is the active den.yaml (exported to scripts as DEN_CONFIG). ports
// supplies the live local ports of connected services and may be nil.
//
// Nothing in a runbooks: block fails the registry: a runbook or sequence that
// cannot be used is left out with a Warning, so the rest stays usable.
func NewRegistry(cfg *config.Config, cfgPath string, ports PortSource) (*Registry, error) {
	r := &Registry{
		byName:    make(map[string]*Runner),
		seqByName: make(map[string]*SequenceRunner),
		ports:     ports,
	}
	r.Reload(cfg, cfgPath)
	return r, nil
}

// Reload takes a new den.yaml and rescans. Runbooks keep their runners, and so
// their state, across it; see Rescan.
func (r *Registry) Reload(cfg *config.Config, cfgPath string) {
	// Absolute, as DEN_CONFIG promises scripts, and as the base that relative
	// paths in den.yaml are read against.
	if cfgPath != "" {
		if abs, err := filepath.Abs(cfgPath); err == nil {
			cfgPath = abs
		}
	}
	r.mu.Lock()
	r.cfgPath = cfgPath
	r.cfg = config.RunbooksConfig{}
	r.envs = map[string]config.EnvSpec{}
	if cfg != nil {
		if cfg.Runbooks != nil {
			r.cfg = *cfg.Runbooks
		}
		maps.Copy(r.envs, cfg.Environments)
	}
	r.mu.Unlock()
	r.Rescan()
}

// SetHistory keeps finished runs in h from now on, and restores each
// runbook's last run from it. failed hears about a run that could not be
// saved; it may be nil. Nothing is kept without a call to SetHistory, which
// is how tests stay out of the user's state directory.
func (r *Registry) SetHistory(h *History, failed func(error)) {
	r.mu.Lock()
	r.history, r.historyFail = h, failed
	runners, sequences := r.runners, r.sequences
	r.mu.Unlock()
	for _, run := range runners {
		run.restore(h)
	}
	for _, sr := range sequences {
		sr.restore(h)
	}
}

// SetSources lists the runbooks of runbooks.sources from now on, from their
// trees in store, and remembers in trust which of them the user has run.
// Without a call no source is read: tests, and commands that never run a
// runbook, stay out of the user's data directory.
func (r *Registry) SetSources(store *source.Store, trust *Trust) {
	r.mu.Lock()
	r.sources, r.trust = store, trust
	r.mu.Unlock()
	r.Rescan()
}

// SetStateRoot gives every runbook a folder of its own in root, exported as
// DEN_RUNBOOK_STATE and made when it runs: somewhere to keep a virtualenv or
// a cache that survives its runs and, for a runbook from a source, the
// updates that replace its folder.
func (r *Registry) SetStateRoot(root string) {
	r.mu.Lock()
	r.stateRoot = root
	r.mu.Unlock()
	r.Rescan()
}

// UserStateRoot is where runbooks keep their state in the user's data
// directory, or "" when there is no home.
func UserStateRoot() string {
	dir := platform.DataDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "runbook-state")
}

// History is where finished runs are kept; nil when they are not.
func (r *Registry) History() *History {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.history
}

// Runs lists the kept runs of the runbook or sequence called name, newest
// first, from wherever it keeps them.
func (r *Registry) Runs(name string) ([]Entry, error) {
	logs := ""
	if run, ok := r.Lookup(name); ok {
		logs = run.Runbook().LogsDir()
	}
	return r.History().List(name, logs)
}

// openRecord starts keeping a run of rb as it goes.
func (r *Registry) openRecord(rb Runbook, run Run, opts ExecOptions) *Recording {
	return r.begin(rb.Name, rb.LogsDir(), rb.Settings.History, entryOf(run, opts.Origin, publicParams(rb.Params, opts.Params)))
}

// closeRecord gives a kept run of rb its final state.
func (r *Registry) closeRecord(rec *Recording, rb Runbook, run Run, opts ExecOptions) {
	r.finish(rec, rb.Name, entryOf(run, opts.Origin, publicParams(rb.Params, opts.Params)))
}

// openSequenceRecord starts keeping a run of seq, as long as
// runbooks.defaults says runs are kept: a sequence has no settings of its own.
func (r *Registry) openSequenceRecord(seq Sequence, run Run, opts SequenceOptions) *Recording {
	r.mu.RLock()
	defaults := r.cfg.Defaults
	r.mu.RUnlock()
	keep := mergeSettings(config.RunbookSettings{}, defaults).History
	return r.begin(seq.Name, "", keep, entryOf(run, opts.Origin, r.sequenceParams(seq, opts)))
}

// closeSequenceRecord gives a kept run of seq its final state.
func (r *Registry) closeSequenceRecord(rec *Recording, seq Sequence, run Run, opts SequenceOptions) {
	r.finish(rec, seq.Name, entryOf(run, opts.Origin, r.sequenceParams(seq, opts)))
}

// sequenceParams are the values a run of seq was given that its history may
// hold, its steps' defaults applied.
func (r *Registry) sequenceParams(seq Sequence, opts SequenceOptions) map[string]string {
	var specs []config.ParamSpec
	for _, step := range seq.Steps {
		if runner, ok := r.Lookup(step); ok {
			specs = append(specs, runner.Runbook().Params...)
		}
	}
	return publicParams(specs, ApplyParamDefaults(specs, opts.Params))
}

func (r *Registry) begin(name, logs string, keep int, e Entry) *Recording {
	r.mu.RLock()
	h, failed := r.history, r.historyFail
	r.mu.RUnlock()
	rec, err := h.Begin(name, logs, keep, e)
	if err != nil && failed != nil {
		failed(fmt.Errorf("runbook %q: run not kept in history: %w", name, err))
	}
	return rec
}

func (r *Registry) finish(rec *Recording, name string, e Entry) {
	r.mu.RLock()
	failed := r.historyFail
	r.mu.RUnlock()
	if err := rec.Finish(e); err != nil && failed != nil {
		failed(fmt.Errorf("runbook %q: run not fully kept in history: %w", name, err))
	}
}

// entryOf describes run for its history entry.
func entryOf(run Run, origin string, params map[string]string) Entry {
	e := Entry{
		Status:     run.Status.String(),
		ExitCode:   run.ExitCode,
		StartedAt:  run.StartedAt,
		FinishedAt: run.FinishedAt,
		Attempts:   run.Attempt,
		Origin:     origin,
		Params:     params,
	}
	if run.Err != nil && run.Status != StatusSucceeded {
		e.Error = run.Err.Error()
	}
	return e
}

// publicParams are the values of params a history entry may hold: every one
// not marked secret.
func publicParams(specs []config.ParamSpec, values map[string]string) map[string]string {
	secret := make(map[string]bool)
	for _, p := range specs {
		if p.Secret {
			secret[p.Name] = true
		}
	}
	var out map[string]string
	for k, v := range values {
		if secret[k] {
			continue
		}
		if out == nil {
			out = make(map[string]string)
		}
		out[k] = v
	}
	return out
}

// RescanOn reports whether runbooks.rescan asks for folders to be picked up
// while den runs.
func (r *Registry) RescanOn() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg.RescanOn()
}

// Generation changes whenever Rescan changed what the registry holds.
func (r *Registry) Generation() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.generation
}

// Rescan reads den.yaml's items and the runbook directories again and
// reconciles, the way service.Manager.Reconcile does for services: a runbook
// that is still there keeps its Runner, its last run and its place; a changed
// definition replaces the old one, after the run in flight if there is one; a
// runbook that is gone is dropped, unless it is running, in which case it stays
// until a rescan after it has finished.
func (r *Registry) Rescan() {
	r.mu.RLock()
	cfg, envs, base, cfgPath, store := r.cfg, r.envs, configDir(r.cfgPath), r.cfgPath, r.sources
	trust, stateRoot := r.trust, r.stateRoot
	r.mu.RUnlock()

	specs, warnings := wanted(cfg, envs, base)
	var trusted map[string]Trusted
	if store != nil && len(cfg.Sources) > 0 {
		remote, warns := r.sourceSpecs(cfg, envs, store, cfgPath, specs)
		specs = append(specs, remote...)
		warnings = append(warnings, warns...)
		trusted = trust.All()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	changed := false
	var runners []*Runner
	byName := make(map[string]*Runner, len(specs))
	for _, f := range specs {
		rb, err := fromSpec(f.spec, cfg.Defaults)
		if err != nil {
			// A bad header should not sink the whole app.
			warnings = append(warnings, Warning{Path: f.spec.Source, Msg: err.Error()})
			continue
		}
		rb.Remote = f.remote
		applyTrust(&rb, trusted)
		if stateRoot != "" {
			rb.StateDir = filepath.Join(stateRoot, folderName(rb.Name))
		}
		run, ok := r.byName[rb.Name]
		switch {
		case !ok:
			run, changed = r.newRunner(rb), true
		case !reflect.DeepEqual(run.latest(), rb):
			run.redefine(rb)
			changed = true
		}
		runners = append(runners, run)
		byName[rb.Name] = run
	}
	for _, run := range r.runners {
		if _, kept := byName[run.Runbook().Name]; kept {
			continue
		}
		changed = true
		if run.IsRunning() {
			runners = append(runners, run)
			byName[run.Runbook().Name] = run
		}
	}

	// Sequences are validated here, not in config.Validate, because this is the
	// only place that sees discovered runbooks as well as inline ones. A broken
	// sequence costs only itself: failing the whole registry would empty the
	// pane over one renamed script, with the reason only in den's log.
	var sequences []*SequenceRunner
	seqByName := make(map[string]*SequenceRunner, len(cfg.Sequences))
	for _, spec := range cfg.Sequences {
		if problem := sequenceProblem(spec, byName); problem != "" {
			warnings = append(warnings, Warning{Msg: fmt.Sprintf("sequence %q: %s — skipped", spec.Name, problem)})
			continue
		}
		seq := Sequence{Name: spec.Name, Description: spec.Description, Steps: spec.Steps, Confirm: spec.Confirm}
		sr, ok := r.seqByName[seq.Name]
		if !ok || (!sr.IsRunning() && !reflect.DeepEqual(sr.Sequence(), seq)) {
			sr, changed = NewSequenceRunner(seq, r), true
			sr.restore(r.history)
		}
		sequences = append(sequences, sr)
		seqByName[seq.Name] = sr
	}
	for name, sr := range r.seqByName {
		if _, kept := seqByName[name]; !kept {
			changed = true
			if sr.IsRunning() {
				sequences = append(sequences, sr)
				seqByName[name] = sr
			}
		}
	}

	if changed {
		r.generation++
	}
	r.runners, r.byName = runners, byName
	r.sequences, r.seqByName = sequences, seqByName
	r.warnings = warnings
}

// newRunner makes the runner of a runbook new to the registry, its last run
// restored from history. Called with r.mu held.
func (r *Registry) newRunner(rb Runbook) *Runner {
	run := NewRunner(rb)
	run.openRecord, run.closeRecord = r.openRecord, r.closeRecord
	run.started = r.trustStarted
	run.restore(r.history)
	return run
}

// wanted lists the runbooks cfg describes, in the order a name collision is
// settled by: den.yaml's items, then runbook folders, then loose scripts.
// den.yaml is reviewed and shared; a stray script in a scanned directory is not.
func wanted(cfg config.RunbooksConfig, envs map[string]config.EnvSpec, base string) ([]found, []Warning) {
	var specs []found
	origin := make(map[string]string)
	for _, spec := range cfg.Items {
		// Relative to den.yaml, like everything else it names: the directory
		// den happens to be started in must not change what a runbook does.
		spec.Cwd = resolvePath(spec.Cwd, base)
		if spec.Cwd == "" {
			spec.Cwd = base
		}
		specs = append(specs, found{spec: spec})
		origin[spec.Name] = "den.yaml"
	}

	discovered, warnings := Discover(cfg.Dirs, base)
	for _, spec := range discovered {
		if prev, dup := origin[spec.Name]; dup {
			warnings = append(warnings, Warning{
				Path:    spec.Source,
				Msg:     fmt.Sprintf("name %q already defined by %s — skipped", spec.Name, prev),
				shadows: spec.Name,
			})
			continue
		}
		if spec.Dir != "" {
			// A runbook.yaml is held to the same rules as den.yaml's items.
			if err := config.ValidateRunbook(spec, envs); err != nil {
				warnings = append(warnings, Warning{Path: spec.Source, Msg: err.Error() + " — skipped"})
				continue
			}
			if err := config.CheckRunbookAgent(fmt.Sprintf("runbook %q", spec.Name), spec.Agent, cfg.Agents); err != nil {
				warnings = append(warnings, Warning{Path: spec.Source, Msg: err.Error() + " — E cannot start it"})
			}
		} else if spec.Env != "" {
			// A header script stays as lenient as it always was, but an unknown
			// env is said out loud: it would silently inject no AWS context and
			// the script would run against whatever account the shell has.
			if _, ok := envs[spec.Env]; !ok {
				warnings = append(warnings, Warning{
					Path: spec.Source,
					Msg:  fmt.Sprintf("unknown env %q — no AWS context will be injected", spec.Env),
				})
			}
		}
		specs = append(specs, found{spec: spec})
		origin[spec.Name] = spec.Source
	}
	return specs, warnings
}

// sourceSpecs lists the runbooks of the sources, after everything wanted
// found: a name already taken by one of yours keeps yours.
func (r *Registry) sourceSpecs(cfg config.RunbooksConfig, envs map[string]config.EnvSpec, store *source.Store, cfgPath string, taken []found) ([]found, []Warning) {
	lock, err := source.ReadLock(source.LockPath(cfgPath))
	if err != nil {
		return nil, []Warning{{Path: source.LockPath(cfgPath), Msg: err.Error()}}
	}
	remote, warnings := sourceRunbooks(cfg, envs, store, lock)
	origin := make(map[string]string, len(taken))
	for _, f := range taken {
		origin[f.spec.Name] = f.spec.Source
		if origin[f.spec.Name] == "" {
			origin[f.spec.Name] = "den.yaml"
		}
	}
	var out []found
	for _, f := range remote {
		if prev, dup := origin[f.spec.Name]; dup {
			warnings = append(warnings, Warning{
				Path:    f.spec.Source,
				Msg:     fmt.Sprintf("name %q already defined by %s — skipped", f.spec.Name, prev),
				shadows: f.spec.Name,
			})
			continue
		}
		origin[f.spec.Name] = f.spec.Source
		out = append(out, f)
	}
	return out, warnings
}

// sequenceProblem says why spec cannot run, or "" when it can.
func sequenceProblem(spec config.SequenceSpec, runbooks map[string]*Runner) string {
	if _, dup := runbooks[spec.Name]; dup {
		return "a runbook has the same name"
	}
	for _, step := range spec.Steps {
		if _, ok := runbooks[step]; !ok {
			return fmt.Sprintf("unknown step %q", step)
		}
	}
	return ""
}

// Runbooks returns every runbook runner in definition order.
func (r *Registry) Runbooks() []*Runner {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.runners
}

// Sequences returns every sequence runner in definition order.
func (r *Registry) Sequences() []*SequenceRunner {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sequences
}

// Warnings returns non-fatal discovery problems. Surface these: a script the
// user believes is registered but is not is a silent failure.
func (r *Registry) Warnings() []Warning {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.warnings
}

// Empty reports whether nothing at all is configured.
func (r *Registry) Empty() bool {
	return len(r.Runbooks()) == 0 && len(r.Sequences()) == 0
}

// Lookup finds a runbook by name.
func (r *Registry) Lookup(name string) (*Runner, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	run, ok := r.byName[name]
	return run, ok
}

// LookupSequence finds a sequence by name.
func (r *Registry) LookupSequence(name string) (*SequenceRunner, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seq, ok := r.seqByName[name]
	return seq, ok
}

// ExecOptionsFor assembles the environment and working directory for one run.
func (r *Registry) ExecOptionsFor(rb Runbook, params map[string]string) ExecOptions {
	r.mu.RLock()
	env := r.envs[rb.Env]
	cfgPath := r.cfgPath
	ports := r.ports
	r.mu.RUnlock()

	var live map[string]int
	if ports != nil {
		live = ports()
	}
	// Defaults are resolved here, once, so every caller — the CLI, a sequence
	// step, the TUI — sees the same effective values.
	resolved := ApplyParamDefaults(rb.Params, params)
	if rb.StateDir != "" {
		// A failure shows in the run: the script cannot write there.
		_ = os.MkdirAll(rb.StateDir, 0o700)
	}
	return ExecOptions{
		Env:      BuildEnv(rb, env, cfgPath, resolved, live),
		Dir:      rb.Cwd,
		Redactor: NewRedactor(rb, resolved),
		Params:   resolved,
	}
}

// Agent is the agent runbooks.agent names: a kind such as claude, the name
// of a runbooks.agents entry, or "" for auto.
func (r *Registry) Agent() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg.Agent
}

// Agents are the runbooks.agents entries, in the order den.yaml lists them.
func (r *Registry) Agents() []config.AgentSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]config.AgentSpec(nil), r.cfg.Agents...)
}

// Env is the environment an env: key names, for showing which account a
// runbook runs against.
func (r *Registry) Env(name string) (config.EnvSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	env, ok := r.envs[name]
	return env, ok
}

// ContextVars are the variables den sets for rb on top of the environment it
// inherited — its connection context, the live ports, env_vars and the
// parameter defaults — as a terminal opened for rb gets them and the About tab
// lists them. A parameter without a default has no value outside a run.
func (r *Registry) ContextVars(rb Runbook) map[string]string {
	r.mu.RLock()
	env, cfgPath, ports := r.envs[rb.Env], r.cfgPath, r.ports
	r.mu.RUnlock()
	var live map[string]int
	if ports != nil {
		live = ports()
	}
	params := make(map[string]string)
	for _, p := range rb.Params {
		if p.Default != "" && !p.Secret {
			params[p.Name] = p.Default
		}
	}
	return ContextVars(rb, env, cfgPath, params, live)
}

// ContextEnv is ContextVars as KEY=value entries, for a terminal's
// environment.
func (r *Registry) ContextEnv(rb Runbook) []string { return envList(r.ContextVars(rb)) }

// EditTarget is the file e opens for rb: the script its command runs when
// that is a file of its own, else its runbook.yaml, its header script, or
// den.yaml for a runbook declared there.
func (r *Registry) EditTarget(rb Runbook) string {
	if script := entryScript(rb); script != "" {
		return script
	}
	if rb.Source != "" {
		return rb.Source
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfgPath
}

// entryScript finds the file a runbook.yaml's command runs: the first word of
// its first line that names a file in the runbook's working directory, so
// "./run.sh", "python3 snapshot.py" and "bash -e deploy.sh" all lead to it.
func entryScript(rb Runbook) string {
	if rb.Dir == "" {
		return ""
	}
	first, _, _ := strings.Cut(strings.TrimSpace(rb.Command), "\n")
	for _, word := range strings.Fields(first) {
		word = strings.Trim(word, `"'`)
		if word == "" || strings.HasPrefix(word, "-") || strings.ContainsAny(word, "$=|&;<>") {
			continue
		}
		path := word
		if !filepath.IsAbs(path) {
			path = filepath.Join(rb.Cwd, path)
		}
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return ""
}

// ApplyParamDefaults fills in declared defaults for parameters the caller did
// not supply. Supplied values always win.
func ApplyParamDefaults(specs []config.ParamSpec, params map[string]string) map[string]string {
	out := make(map[string]string, len(params)+len(specs))
	for _, p := range specs {
		if p.Default != "" {
			out[p.Name] = p.Default
		}
	}
	maps.Copy(out, params)
	return out
}

// MissingParams lists the required parameters that have neither a value in
// params nor a default.
func MissingParams(specs []config.ParamSpec, params map[string]string) []string {
	var missing []string
	for _, p := range specs {
		if !p.Required {
			continue
		}
		if v, ok := params[p.Name]; (!ok || strings.TrimSpace(v) == "") && p.Default == "" {
			missing = append(missing, p.Name)
		}
	}
	return missing
}

// AnyRunning reports whether any runbook or sequence is in flight. Callers use
// it to avoid discarding a registry that still owns live processes.
func (r *Registry) AnyRunning() bool {
	for _, run := range r.Runbooks() {
		if run.IsRunning() {
			return true
		}
	}
	for _, seq := range r.Sequences() {
		if seq.IsRunning() {
			return true
		}
	}
	return false
}

// StopAll cancels everything in flight. Call it on shutdown so scripts are not
// left orphaned when den exits.
func (r *Registry) StopAll() {
	for _, seq := range r.Sequences() {
		seq.Cancel()
	}
	for _, run := range r.Runbooks() {
		run.Cancel()
	}
}

// configDir is the directory relative paths in den.yaml are read against: the
// file's own, or den's working directory when there is no file.
func configDir(cfgPath string) string {
	if cfgPath == "" {
		return ""
	}
	return filepath.Dir(cfgPath)
}

// resolvePath expands ~ and reads a relative path against base. An empty path
// stays empty, so callers can tell "not set" apart.
func resolvePath(p, base string) string {
	p = platform.ExpandHome(p)
	if p == "" || filepath.IsAbs(p) || base == "" {
		return p
	}
	return filepath.Join(base, p)
}
