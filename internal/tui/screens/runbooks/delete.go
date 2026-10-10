package runbooks

import (
	"fmt"
	"strings"

	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// deletion asks before D deletes a runbook or sequence for good, listing all
// that goes and all that stays; the list and the view share it.
type deletion struct {
	reg   *runbook.Registry
	plan  runbook.Deletion
	open  bool
	terms int // its terminals with a program still running, which stop
}

func (q deletion) active() bool { return q.open }

// begin opens the question for e, or says why e cannot be deleted. terms is
// how many of its terminals still run a program.
func (q deletion) begin(e entry, terms int) (deletion, tea.Cmd) {
	plan, err := q.reg.PlanDelete(e.name())
	if err != nil {
		return q, warn(err.Error())
	}
	q.plan, q.open, q.terms = plan, true, terms
	return q, nil
}

// key answers. Only y deletes, not enter as the other questions allow:
// nothing brings a deleted runbook back, so a key pressed from habit must
// not be the one that does it.
func (q deletion) key(msg tea.KeyPressMsg) (deletion, tea.Cmd) {
	q.open = false
	switch msg.String() {
	case "y", "Y":
		name := q.plan.Name
		return q, func() tea.Msg { return msgs.DeleteRunbookMsg{Name: name} }
	}
	return q, flash("kept " + q.plan.Name)
}

func (q deletion) view(width int) string {
	inner := min(width-4, 96) - 6
	fail := lipgloss.NewStyle().Foreground(colorFail)
	wrap := lipgloss.NewStyle().Width(inner - 4)
	list := func(mark lipgloss.Style, sign string, items []string) []string {
		var out []string
		for _, it := range items {
			out = append(out, mark.Render("  "+sign+" ")+indentWrapped(wrap.Render(it), "    "))
		}
		return out
	}
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(colorFail).Render("Delete " + truncate(q.plan.Name, inner-7)),
		"",
		muted.Render("Deletes"),
	}
	lines = append(lines, list(fail, "✗", q.plan.Gone())...)
	if kept := q.plan.Kept(); len(kept) > 0 {
		lines = append(lines, "", muted.Render("Keeps"))
		lines = append(lines, list(muted, "·", kept)...)
	}
	notes := q.plan.Notes()
	if q.terms > 0 {
		notes = append([]string{fmt.Sprintf("%s still running in its terminals %s stopped first",
			plural(q.terms, "a program", fmt.Sprintf("%d programs", q.terms)), plural(q.terms, "is", "are"))}, notes...)
	}
	if len(notes) > 0 {
		lines = append(lines, "")
		lines = append(lines, list(lipgloss.NewStyle().Foreground(colorWarn), "!", notes)...)
	}
	lines = append(lines, "", question("Delete it for good?", "delete", "keep"))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorFail).
		Padding(1, 2).
		Width(min(width-4, 96) + 2).
		Render(strings.Join(lines, "\n"))
}

// indentWrapped indents every line of a wrapped paragraph after its first.
func indentWrapped(text, indent string) string {
	return strings.ReplaceAll(text, "\n", "\n"+indent)
}
