package runbooks

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/tui/components"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// errDraft is why a draft does not run: it is a request, not a runbook yet.
var errDraft = errors.New("a draft: enter opens it")

// draftName is a draft's row in the list: the name typed for a new runbook,
// or the runbook a change is for, marked so it is not taken for the runbook.
func draftName(d runbook.Draft) string {
	if d.Kind == runbook.DraftEdit {
		return d.Runbook + " (change)"
	}
	if name := strings.TrimSpace(d.Name); name != "" {
		return name
	}
	return "(no name)"
}

// newDrafts are the drafts of new runbooks among drafts: the ones n offers. A
// draft of a change is E's, on its runbook.
func newDrafts(drafts []runbook.Draft) []runbook.Draft {
	var out []runbook.Draft
	for _, d := range drafts {
		if d.Kind == runbook.DraftNew {
			out = append(out, d)
		}
	}
	return out
}

// editDraft is the newest draft of a change to the runbook called name.
func editDraft(drafts []runbook.Draft, name string) (runbook.Draft, bool) {
	for _, d := range drafts {
		if d.Kind == runbook.DraftEdit && d.Runbook == name {
			return d, true
		}
	}
	return runbook.Draft{}, false
}

// draftPicker is what n shows when there are drafts of new runbooks: a new
// one, or a draft to go on with. Without drafts n opens the form at once.
type draftPicker struct {
	drafts []runbook.Draft
	cursor int // 0 is a new runbook, i+1 drafts[i]
	open   bool
}

func (p draftPicker) active() bool { return p.open }

func (p draftPicker) begin(drafts []runbook.Draft) draftPicker {
	return draftPicker{drafts: drafts, open: true}
}

// key moves the cursor, or picks: picked is false while it is still open, and
// draft is nil for a new runbook.
func (p draftPicker) key(msg tea.KeyPressMsg) (_ draftPicker, picked bool, draft *runbook.Draft) {
	switch msg.String() {
	case "up", "k":
		p.cursor = max(p.cursor-1, 0)
	case "down", "j":
		p.cursor = min(p.cursor+1, len(p.drafts))
	case "home", "g":
		p.cursor = 0
	case "end", "G":
		p.cursor = len(p.drafts)
	case "esc":
		p.open = false
	case "enter":
		p.open = false
		if p.cursor > 0 {
			d := p.drafts[p.cursor-1]
			return p, true, &d
		}
		return p, true, nil
	}
	return p, false, nil
}

func (p draftPicker) view(width, height int, now time.Time) string {
	inner := min(width-8, 80)
	row := func(selected bool, label, note string) string {
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("#D0D0D0"))
		if selected {
			cursor = accent.Render("▸ ")
			style = style.Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
		}
		label = truncate(label, max(inner-lipgloss.Width(note)-4, 1))
		gap := max(inner-2-lipgloss.Width(label)-lipgloss.Width(note), 1)
		return cursor + style.Render(label) + strings.Repeat(" ", gap) + dim.Render(note)
	}
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("New runbook"),
		muted.Render("Start a new one, or go on with a draft"),
		"",
		row(p.cursor == 0, "New runbook", ""),
	}
	// The title, its note, the blank lines, the border, the padding and the
	// hint take the rest of the pane.
	room := max(height-12, 1)
	top := max(min(p.cursor-room, len(p.drafts)-room), 0)
	if top > 0 {
		lines = append(lines, dim.Render(fmt.Sprintf("  ↑ %d more", top)))
	}
	end := min(top+room, len(p.drafts))
	for i := top; i < end; i++ {
		d := p.drafts[i]
		label := draftName(d)
		if req, _, _ := strings.Cut(strings.TrimSpace(d.Request), "\n"); req != "" {
			label += " — " + req
		}
		lines = append(lines, row(p.cursor == i+1, label, ago(d.Updated, now)))
	}
	if end < len(p.drafts) {
		lines = append(lines, dim.Render(fmt.Sprintf("  ↓ %d more", len(p.drafts)-end)))
	}
	lines = append(lines, "", components.Hints(key("enter", "open"), key("↑↓", "choose"), key("esc", "cancel")))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).
		Padding(1, 2).Render(strings.Join(lines, "\n"))
}

// draftDeletion asks before D deletes a draft: it is what someone typed.
type draftDeletion struct {
	reg   *runbook.Registry
	draft runbook.Draft
	open  bool
}

func (q draftDeletion) active() bool { return q.open }

func (q draftDeletion) begin(d runbook.Draft) draftDeletion {
	q.draft, q.open = d, true
	return q
}

// key answers: y deletes the draft, anything else keeps it.
func (q draftDeletion) key(msg tea.KeyPressMsg) (draftDeletion, tea.Cmd) {
	q.open = false
	switch msg.String() {
	case "y", "Y":
		if err := q.reg.DeleteDraft(q.draft.ID); err != nil {
			return q, warn("the draft was not deleted: " + err.Error())
		}
		return q, flash("draft deleted")
	}
	return q, flash("kept the draft")
}

func (q draftDeletion) view(width int) string {
	inner := min(width-4, 80) - 6
	lines := []string{
		question(truncate(fmt.Sprintf("Delete the draft %q?", draftName(q.draft)), inner), "delete", "keep"),
	}
	if req := strings.TrimSpace(q.draft.Request); req != "" {
		first, _, _ := strings.Cut(req, "\n")
		lines = append([]string{muted.Render(truncate(first, inner)), ""}, lines...)
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorFail).
		Padding(1, 2).Render(strings.Join(lines, "\n"))
}
