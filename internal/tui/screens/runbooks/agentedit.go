package runbooks

import (
	"fmt"
	"strings"

	"github.com/lukaszgard/den/internal/agent"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
// have an agent rewrite den.yaml or a script's neighbours. The question is
// filled with the draft whose ID is draftID, else the newest draft of a
// change to the runbook.
func (v View) EditWithAgent(draftID string) (View, tea.Cmd) {
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
	if dr, ok := v.editDraft(draftID); ok {
		cmd = v.ask.fill(dr)
	} else if saved, ok := v.reg.SavedRequest(v.name, ""); ok {
		// After den restarted, the agent's tab and its restart question are
		// gone: this is how an agent that failed gets its request again.
		cmd = v.ask.fill(runbook.Draft{Request: saved.Request, Agent: saved.Agent})
		v.ask.lastRequest = true
	}
	return v, cmd
}

// editDraft is the draft E's question opens with: the one whose ID is id,
// else the newest draft of a change to the runbook on screen.
func (v View) editDraft(id string) (runbook.Draft, bool) {
	drafts := v.reg.Drafts()
	for _, d := range drafts {
		if id != "" && d.ID == id {
			return d, true
		}
	}
	return editDraft(drafts, v.name)
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
	var kept tea.Cmd
	if e, ok := lookup(v.reg, name); ok && !e.isSeq() {
		if err := v.ask.sent(name, e.runner.Runbook().Dir); err != nil {
			kept = warn("the request is not kept for a restart: " + err.Error())
		}
	}
	v.ask = nil
	return v, tea.Batch(kept, func() tea.Msg {
		return msgs.OpenRunbookTerminalMsg{Name: name, AgentEdit: true, Request: request}
	})
}

// SaveDraft writes the draft of E's open question at once, for a den about to
// quit.
func (v View) SaveDraft() {
	if v.ask != nil {
		_ = v.ask.saveDraft()
	}
}

// AskResend asks, before the agent of the tab tabID restarts, whether to send
// it request again; the answer comes back as an OpenRunbookTerminalMsg.
func (v View) AskResend(tabID int, request string) View {
	v.resend = resendQuestion{name: v.name, tabID: tabID, request: request}
	return v
}

// resendQuestion asks whether a restarted agent gets the request it was
// started with again, or starts empty.
type resendQuestion struct {
	name    string
	tabID   int // 0 when no question is open
	request string
}

// resendKeys are the answers to the question.
var resendKeys = []components.HelpKey{key("y", "send it"), key("n", "start empty"), key("esc", "cancel")}

func (q resendQuestion) active() bool { return q.tabID != 0 }

// key answers: y (or enter, which asked for the restart) sends the request,
// n starts the agent without it, esc restarts nothing. Any other key keeps
// the question.
func (q resendQuestion) key(msg tea.KeyPressMsg) (resendQuestion, tea.Cmd) {
	name, tabID := q.name, q.tabID
	answer := func(resend bool) (resendQuestion, tea.Cmd) {
		return resendQuestion{}, func() tea.Msg {
			return msgs.OpenRunbookTerminalMsg{Name: name, TabID: tabID, Answered: true, Resend: resend}
		}
	}
	switch msg.String() {
	case "y", "Y", "enter":
		return answer(true)
	case "n", "N":
		return answer(false)
	case "esc":
		return resendQuestion{}, nil
	}
	return q, nil
}

func (q resendQuestion) view(width int) string {
	inner := min(width-4, 96) - 6
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("Restart the agent"),
		"",
		muted.Render("It was started with:"),
	}
	req := strings.Split(strings.TrimSpace(q.request), "\n")
	for i, line := range req {
		if i == 3 {
			lines = append(lines, dim.Render(fmt.Sprintf("  … %d more %s", len(req)-3, plural(len(req)-3, "line", "lines"))))
			break
		}
		lines = append(lines, "  "+truncate(line, inner-2))
	}
	lines = append(lines, "", lipgloss.NewStyle().Bold(true).Render("Send the request again? ")+components.Hints(resendKeys...))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Width(min(width-4, 96) + 2).
		Render(strings.Join(lines, "\n"))
}
