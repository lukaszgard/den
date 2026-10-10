package runbooks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// flow walks one run through its parameters and its confirmation, in that
// order so the confirmation shows the real values, and then starts it. The
// list and the runbook view both use it, as a pop-up over what they show.
type flow struct {
	reg *runbook.Registry
	ctx context.Context
	// out receives every line of the runs the flow starts, with the name of
	// the runbook or sequence; nil sends them nowhere but the run's own log.
	out func(name, line string)
	// light is set once the terminal reported a light background; the zero
	// value is the dark palette, which shows until it replies.
	light bool

	e       entry
	form    *huh.Form
	values  map[string]*string
	params  map[string]string
	confirm bool
}

// active reports that the flow has the keyboard.
func (f flow) active() bool { return f.form != nil || f.confirm }

// awaitingForm reports that the parameter form is up: it needs every message,
// not only keys, since its cursor and validation run on its own commands.
func (f flow) awaitingForm() bool { return f.form != nil }

// begin starts e's run, after asking for its parameters and confirmation when
// it has them.
func (f flow) begin(e entry, width, height int) (flow, tea.Cmd) {
	if e.isRunning() {
		return f, func() tea.Msg {
			return msgs.StatusFlashMsg{Text: "already running — press x to cancel", Warn: true, Hold: 3 * time.Second}
		}
	}
	f.e, f.params, f.confirm = e, nil, false
	if specs := e.params(); len(specs) > 0 {
		f.form, f.values = buildForm(specs, width, height, !f.light)
		return f, f.form.Init()
	}
	if e.needsConfirm() {
		f.confirm = true
		return f, nil
	}
	return f.start()
}

// update drives the form or the confirmation. esc abandons the run.
func (f flow) update(msg tea.Msg) (flow, tea.Cmd) {
	if f.form != nil {
		if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "esc" {
			return f.reset(), flash("cancelled")
		}
		next, cmd := f.form.Update(msg)
		if form, ok := next.(*huh.Form); ok {
			f.form = form
		}
		switch f.form.State {
		case huh.StateCompleted:
			f.params = collect(f.values)
			f.form, f.values = nil, nil
			if f.e.needsConfirm() {
				f.confirm = true
				return f, nil
			}
			return f.start()
		case huh.StateAborted:
			return f.reset(), flash("cancelled")
		}
		return f, cmd
	}
	if k, ok := msg.(tea.KeyPressMsg); ok && f.confirm {
		switch k.String() {
		case "y", "Y", "enter":
			return f.start()
		case "n", "N", "esc", "q":
			return f.reset(), flash("cancelled")
		}
	}
	return f, nil
}

func (f flow) start() (flow, tea.Cmd) {
	e, params := f.e, f.params
	f = f.reset()
	if err := e.start(f.ctx, f.reg, params, f.out); err != nil {
		return f, flash(err.Error())
	}
	return f, flash("started " + e.name())
}

func (f flow) reset() flow {
	return flow{reg: f.reg, ctx: f.ctx, out: f.out}
}

// view is the pop-up of the flow's current step, for a screen width wide.
func (f flow) view(width, height int) string {
	if f.form != nil {
		return f.paramsBox(width)
	}
	if f.confirm {
		return f.confirmBox(width, height)
	}
	return ""
}

func (f flow) paramsBox(width int) string {
	head := lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("Parameters — " + f.e.name())
	hint := components.Hints(key("enter", "next"), key("esc", "cancel"))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Width(min(width-4, 96) + 2).
		Render(head + "\n\n" + f.form.View() + "\n" + hint)
}

func (f flow) confirmBox(width, height int) string {
	warn := lipgloss.NewStyle().Bold(true).Foreground(colorWarn)
	code := lipgloss.NewStyle().Foreground(colorRunning)
	inner := min(width-4, 96) - 6

	lines := []string{warn.Render("Confirm before running"), ""}
	if f.e.isSeq() {
		seq := f.e.seq.Sequence()
		lines = append(lines,
			muted.Render("Sequence: ")+lipgloss.NewStyle().Bold(true).Render(seq.Name),
			muted.Render("Steps:"))
		for i, step := range seq.Steps {
			lines = append(lines, code.Render(fmt.Sprintf("   %d. %s", i+1, step)))
		}
	} else {
		rb := f.e.runner.Runbook()
		lines = append(lines, muted.Render("Runbook: ")+lipgloss.NewStyle().Bold(true).Render(rb.Name))
		if rb.Description != "" {
			lines = append(lines, muted.Render("         "+truncate(rb.Description, inner-9)))
		}
		lines = append(lines, muted.Render("From:    ")+code.Render(truncate(rb.Origin(), inner-9)))
		if rb.Remote != nil && rb.Remote.Note != "" {
			lines = append(lines, muted.Render("Why:     ")+lipgloss.NewStyle().Foreground(colorWarn).Render(truncate(rb.Remote.Note, inner-9)))
		}
		if rb.Env != "" {
			lines = append(lines, muted.Render("Env:     ")+code.Render(rb.Env))
		}
		if rb.Cwd != "" {
			lines = append(lines, muted.Render("Cwd:     ")+code.Render(truncate(rb.Cwd, inner-9)))
		}
		lines = append(lines, "", muted.Render("Command:"))
		cmdLines := strings.Split(strings.TrimSpace(rb.Command), "\n")
		// The box must fit the screen; a long inline script is shown in part.
		if most := max(height-len(lines)-10, 3); len(cmdLines) > most {
			cmdLines = append(cmdLines[:most-1], fmt.Sprintf("… %d more lines", len(cmdLines)-most+1))
		}
		for _, cl := range cmdLines {
			lines = append(lines, code.Render("   "+truncate(cl, inner-3)))
		}
	}
	if len(f.params) > 0 {
		lines = append(lines, "", muted.Render("Parameters:"))
		for _, p := range f.e.params() {
			v := f.params[p.Name]
			if p.Secret && v != "" {
				v = "••••••"
			}
			lines = append(lines, code.Render(fmt.Sprintf("   %s = %s", p.Name, truncate(v, inner-len(p.Name)-6))))
		}
	}
	lines = append(lines, "", question("Run this?", "yes", "no"))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorWarn).
		Padding(1, 2).
		Width(min(width-4, 96) + 2).
		Render(strings.Join(lines, "\n"))
}

// help is the help line while the flow has the keyboard.
func (f flow) help() []components.HelpKey {
	if f.form != nil {
		return []components.HelpKey{key("enter", "next field"), key("esc", "cancel")}
	}
	return []components.HelpKey{key("y", "run"), key("n", "cancel")}
}

func flash(text string) tea.Cmd {
	return func() tea.Msg { return msgs.StatusFlashMsg{Text: text} }
}
