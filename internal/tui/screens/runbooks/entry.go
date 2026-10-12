package runbooks

import (
	"context"
	"fmt"
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/tui/components"

	"charm.land/lipgloss/v2"
)

// entry is one runbook or sequence. The list and the view share it because
// they share the verbs: run it, watch it, cancel it. In the list it may also
// be a draft, a request typed for an agent and kept: it has none of the verbs,
// and enter opens its form again.
type entry struct {
	runner *runbook.Runner
	seq    *runbook.SequenceRunner
	draft  *runbook.Draft
}

// lookup finds the runbook or sequence called name.
func lookup(reg *runbook.Registry, name string) (entry, bool) {
	if r, ok := reg.Lookup(name); ok {
		return entry{runner: r}, true
	}
	if s, ok := reg.LookupSequence(name); ok {
		return entry{seq: s}, true
	}
	return entry{}, false
}

// entries lists the drafts, the newest first, then every runbook, then every
// sequence, in definition order.
func entries(reg *runbook.Registry, drafts []runbook.Draft) []entry {
	var out []entry
	for i := range drafts {
		out = append(out, entry{draft: &drafts[i]})
	}
	for _, r := range reg.Runbooks() {
		out = append(out, entry{runner: r})
	}
	for _, s := range reg.Sequences() {
		out = append(out, entry{seq: s})
	}
	return out
}

func (e entry) isSeq() bool { return e.seq != nil }

func (e entry) isDraft() bool { return e.draft != nil }

// key tells entries apart: a draft of a change to Greet is not Greet.
func (e entry) key() string {
	if e.isDraft() {
		return "draft " + e.draft.ID
	}
	return e.name()
}

// oldFormat reports a runbook still defined by a header script, without a
// folder of its own: m moves it into one.
func (e entry) oldFormat() bool {
	if e.isSeq() || e.isDraft() {
		return false
	}
	rb := e.runner.Runbook()
	return rb.Source != "" && rb.Dir == ""
}

func (e entry) name() string {
	switch {
	case e.isDraft():
		return draftName(*e.draft)
	case e.isSeq():
		return e.seq.Sequence().Name
	}
	return e.runner.Runbook().Name
}

func (e entry) description() string {
	if e.isDraft() {
		line, _, _ := strings.Cut(strings.TrimSpace(e.draft.Request), "\n")
		return line
	}
	if e.isSeq() {
		seq := e.seq.Sequence()
		if seq.Description != "" {
			return seq.Description
		}
		return strings.Join(seq.Steps, " → ")
	}
	return e.runner.Runbook().Description
}

func (e entry) state() runbook.Run {
	if e.isDraft() {
		return runbook.Run{Status: runbook.StatusNeverRun}
	}
	if e.isSeq() {
		return e.seq.State()
	}
	return e.runner.State()
}

func (e entry) tally() runbook.Tally {
	if e.isDraft() {
		return runbook.Tally{}
	}
	if e.isSeq() {
		return e.seq.Tally()
	}
	return e.runner.Tally()
}

func (e entry) isRunning() bool {
	if e.isDraft() {
		return false
	}
	if e.isSeq() {
		return e.seq.IsRunning()
	}
	return e.runner.IsRunning()
}

func (e entry) needsConfirm() bool {
	if e.isDraft() {
		return false
	}
	if e.isSeq() {
		return e.seq.NeedsConfirm()
	}
	return e.runner.Runbook().Confirm
}

func (e entry) params() []config.ParamSpec {
	if e.isDraft() {
		return nil
	}
	if e.isSeq() {
		return e.seq.Params()
	}
	return e.runner.Runbook().Params
}

func (e entry) cancel() {
	if e.isDraft() {
		return
	}
	if e.isSeq() {
		e.seq.Cancel()
		return
	}
	e.runner.Cancel()
}

// start runs the entry in the background with the given parameter answers;
// out, when set, gets every line of the run with the entry's name.
func (e entry) start(ctx context.Context, reg *runbook.Registry, params map[string]string, out func(name, line string)) error {
	if e.isDraft() {
		return errDraft
	}
	var onLine func(string)
	if out != nil {
		name := e.name()
		onLine = func(line string) { out(name, line) }
	}
	if e.isSeq() {
		return e.seq.Start(ctx, runbook.SequenceOptions{Params: params, Origin: runbook.OriginTUI, OnLine: onLine})
	}
	opts := reg.ExecOptionsFor(e.runner.Runbook(), params)
	opts.Origin = runbook.OriginTUI
	opts.OnLine = onLine
	return e.runner.Start(ctx, opts)
}

// where says what the entry runs against: its env, or its step count.
func (e entry) where() string {
	if e.isDraft() {
		return ""
	}
	if e.isSeq() {
		n := len(e.seq.Sequence().Steps)
		return fmt.Sprintf("%d %s", n, plural(n, "step", "steps"))
	}
	return e.runner.Runbook().Env
}

// --- status presentation ---

var (
	colorDim     = lipgloss.Color("#626262")
	colorMuted   = lipgloss.Color("#9B9B9B")
	colorAccent  = lipgloss.Color("#5BC8AF")
	colorWarn    = lipgloss.Color("#F5A623")
	colorRunning = lipgloss.Color("#ECFD65")
	colorOK      = lipgloss.Color("#04B575")
	colorFail    = lipgloss.Color("#FF5F87")

	dim    = lipgloss.NewStyle().Foreground(colorDim)
	muted  = lipgloss.NewStyle().Foreground(colorMuted)
	accent = lipgloss.NewStyle().Foreground(colorAccent)
)

func statusColor(s runbook.RunStatus) color.Color {
	switch s {
	case runbook.StatusRunning, runbook.StatusPending:
		return colorRunning
	case runbook.StatusSucceeded:
		return colorOK
	case runbook.StatusFailed:
		return colorFail
	case runbook.StatusCancelled, runbook.StatusTimedOut:
		return colorWarn
	}
	return colorDim
}

func statusDot(s runbook.RunStatus) string {
	glyph := "○"
	switch s {
	case runbook.StatusRunning, runbook.StatusPending:
		glyph = "◐"
	case runbook.StatusSucceeded:
		glyph = "✓"
	case runbook.StatusFailed:
		glyph = "✗"
	case runbook.StatusCancelled:
		glyph = "⊘"
	case runbook.StatusTimedOut:
		glyph = "⏱"
	}
	return lipgloss.NewStyle().Foreground(statusColor(s)).Render(glyph)
}

// result is what happened and how long it took: "exit 0  2.1s", or the step
// a running sequence is at.
func result(e entry, st runbook.Run) string {
	switch st.Status {
	case runbook.StatusNeverRun:
		return "–"
	case runbook.StatusRunning:
		label := st.Summary()
		if e.isSeq() && st.StepTotal > 0 {
			label = fmt.Sprintf("step %d/%d", st.StepIndex, st.StepTotal)
		}
		return strings.TrimSpace(label + " " + fmtDuration(st.Duration()))
	}
	return strings.TrimSpace(st.Summary() + "  " + fmtDuration(st.Duration()))
}

func fmtDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

// ago says how long ago t was, briefly: "now", "5m ago", "2h ago", "3d ago".
func ago(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// key is one key of a hint line.
func key(k, text string) components.HelpKey { return components.HelpKey{Key: k, Text: text} }

// question is the [y]/[n] line under a pop-up's question.
func question(q, yes, no string) string {
	return lipgloss.NewStyle().Bold(true).Render(q+" ") + components.Hints(key("y", yes), key("n", no))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func truncate(s string, max int) string {
	if max < 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max < 2 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

func pad(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// kindIcon is a TYPE icon: a Nerd Font glyph and the colour it is drawn in.
// Nerd Fonts have no colour glyphs (no COLR, sbix or CBDT tables): an icon
// is drawn in whatever foreground it is given, so the language's colour is
// den's to give, as eza and nvim-web-devicons do.
type kindIcon struct {
	glyph string
	color string // a hex colour
}

// shellIcon is every shell's, and a command's: a terminal in shell green.
var shellIcon = kindIcon{"\uf120", "#89E051"}

// kindIcons are the icons of the TYPE column, by kind. Every glyph is in the
// Basic Multilingual Plane's private use area, which every width table counts
// as one cell: a wider glyph would shift the columns after it. The seti ones
// (perl, pwsh, terraform) have been in Nerd Fonts since v2; kubectl has the
// gear, as its only icon needs Nerd Fonts 3.3.
var kindIcons = map[string]kindIcon{
	"bash": shellIcon, "sh": shellIcon, "zsh": shellIcon, "fish": shellIcon,
	"command": shellIcon, "shell": shellIcon,
	"python":    {"\ue73c", "#FFD43B"},
	"go":        {"\ue627", "#00ADD8"},
	"node":      {"\ue718", "#8CC84B"},
	"ruby":      {"\ue739", "#CC342D"},
	"rust":      {"\ue7a8", "#DEA584"},
	"java":      {"\ue738", "#F89820"},
	"docker":    {"\uf308", "#2496ED"},
	"aws":       {"\ue7ad", "#FF9900"},
	"make":      {"\uf085", "#6D8086"},
	"perl":      {"\ue67e", "#519ABA"},
	"pwsh":      {"\ue683", "#5391FE"},
	"terraform": {"\ue69a", "#844FBA"},
	"seq":       {"\uf0cb", "#5BC8AF"},
	"draft":     {"\uf040", "#9B9B9B"},
}

// otherIcon is the gear of any other kind.
var otherIcon = kindIcon{"\uf013", "#9B9B9B"}

// kind is the TYPE cell: what it runs, with its icon, and a mark when it is
// still a single script that m moves into a folder.
func (e entry) kind() string {
	var cell string
	if e.isDraft() {
		return kindCell("draft", "draft")
	}
	if e.isSeq() {
		cell = kindCell("seq", fmt.Sprintf("seq · %d", len(e.seq.Sequence().Steps)))
	} else {
		kind := e.runner.Runbook().Kind
		cell = kindCell(kind, kind)
	}
	if e.oldFormat() {
		cell += lipgloss.NewStyle().Foreground(colorWarn).Render(" •")
	}
	return cell
}

// kindCell is label after the icon of kind, the icon alone in its colour.
func kindCell(kind, label string) string {
	icon, ok := kindIcons[kind]
	if !ok {
		icon = otherIcon
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(icon.color)).Render(icon.glyph) + " " + label
}

// envs are the env: keys the entry runs with: its own, or those of a
// sequence's steps.
func (e entry) envs(reg *runbook.Registry) []string {
	if e.isDraft() {
		return nil
	}
	if !e.isSeq() {
		if env := e.runner.Runbook().Env; env != "" {
			return []string{env}
		}
		return nil
	}
	var out []string
	for _, step := range e.seq.Sequence().Steps {
		if r, ok := reg.Lookup(step); ok {
			if env := r.Runbook().Env; env != "" && !slices.Contains(out, env) {
				out = append(out, env)
			}
		}
	}
	return out
}

// account is the ENV · PROFILE cell: which environment, and which AWS profile
// it resolves to — the identity the script really uses.
func (e entry) account(reg *runbook.Registry) string {
	envs := e.envs(reg)
	switch len(envs) {
	case 0:
		return "–"
	case 1:
		if spec, ok := reg.Env(envs[0]); ok && spec.AWSProfile != "" {
			return envs[0] + " · " + spec.AWSProfile
		}
		return envs[0]
	}
	return "mixed: " + strings.Join(envs, ", ")
}

// status is the STATUS cell: how the last run ended, or how far it is; for
// a draft, when it was saved last.
func status(e entry, st runbook.Run, now time.Time) string {
	if e.isDraft() {
		return muted.Render("✎ " + ago(e.draft.Updated, now))
	}
	text := st.Summary()
	switch st.Status {
	case runbook.StatusNeverRun:
		text = "never"
	case runbook.StatusRunning:
		text = fmtDuration(st.Duration())
		if e.isSeq() && st.StepTotal > 0 {
			text = fmt.Sprintf("step %d/%d", st.StepIndex, st.StepTotal)
		}
	}
	return statusDot(st.Status) + " " + lipgloss.NewStyle().Foreground(statusColor(st.Status)).Render(text)
}

// runs is the RUNS cell: how many times it has run, and how many of the runs
// it keeps failed.
func runs(t runbook.Tally) string {
	if t.Runs == 0 {
		return dim.Render("–")
	}
	cell := dim.Render(strconv.Itoa(t.Runs))
	if t.Failed > 0 {
		cell += dim.Render(" · ") + lipgloss.NewStyle().Foreground(colorFail).Render(strconv.Itoa(t.Failed)+"✗")
	}
	return cell
}

// lastRun is the LAST RUN cell: when the last run finished and how long it took.
func lastRun(st runbook.Run, now time.Time) string {
	if st.FinishedAt.IsZero() || !st.Status.Terminal() {
		return "–"
	}
	return strings.TrimSuffix(ago(st.FinishedAt, now)+" · "+fmtDuration(st.Duration()), " · ")
}
