package runbooks

import (
	"github.com/lukaszgard/den/internal/agent"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
)

// editAgentValue is what picks the agent E starts for rb: its own agent
// setting, else runbooks.agent.
func editAgentValue(reg *runbook.Registry, rb runbook.Runbook) string {
	if rb.Settings.Agent != "" {
		return rb.Settings.Agent
	}
	return reg.Agent()
}

// editHint is the agent as the key line names it, "claude opus".
func editHint(reg *runbook.Registry, rb runbook.Runbook) string {
	return agent.Describe(reg.Agents(), editAgentValue(reg, rb))
}

// EditWithAgent opens the question E asks, what the runbook's agent should
// change in it, or says why it cannot: a sequence has no folder, a source's
// runbook is copied first, and one that is not in a folder of its own would
// have an agent rewrite den.yaml or a script's neighbours.
func (v View) EditWithAgent() (View, tea.Cmd) {
	e, ok := lookup(v.reg, v.name)
	if !ok {
		return v, nil
	}
	if e.isSeq() {
		return v, flash("a sequence has no folder of its own: open one of its steps")
	}
	rb := e.runner.Runbook()
	switch {
	case rb.Remote != nil:
		v.fork = v.fork.begin(rb)
		return v, nil
	case e.oldFormat():
		return v, warn("E needs a folder of its own: press m to move it into one first")
	case rb.Dir == "":
		return v, warn("defined in den.yaml: an agent edits a runbook folder; move it into one by hand")
	}
	ag, err := agent.Pick(v.reg.Agents(), editAgentValue(v.reg, rb))
	if err != nil {
		return v, warn(err.Error())
	}
	var cmd tea.Cmd
	v.ask, cmd = newEditForm(v.reg, ag, v.name, v.width, v.height, !v.light)
	return v, cmd
}

// updateAsk drives the question: enter starts the agent with the request, esc
// drops it, asking first to keep what was typed as a draft.
func (v View) updateAsk(msg tea.Msg) (View, tea.Cmd) {
	cmd := v.ask.update(msg)
	if v.ask.closed {
		v.ask = nil
		return v, cmd
	}
	if !v.ask.done {
		return v, cmd
	}
	_, request, _ := v.ask.values()
	name := v.name
	v.ask = nil
	return v, func() tea.Msg {
		return msgs.OpenRunbookTerminalMsg{Name: name, AgentEdit: true, Request: request}
	}
}

// SaveDraft writes the draft of E's open question at once, for a den about to
// quit.
func (v View) SaveDraft() {
	if v.ask != nil {
		_ = v.ask.saveDraft()
	}
}
