package runbooks

import (
	"path/filepath"
	"strings"

	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// forkQuestion asks before e copies a runbook from a source into your own
// runbooks: its folder in the checkout is replaced by every update, so it is
// not edited in place.
type forkQuestion struct {
	reg  *runbook.Registry
	name string // the runbook asked about; "" when no question is open
	from string // its source
	dest string // the folder the copy goes into; "" when there is none
}

func (q forkQuestion) active() bool { return q.name != "" }

func (q forkQuestion) begin(rb runbook.Runbook) forkQuestion {
	q.name, q.from, q.dest = rb.Name, rb.Remote.Source, q.reg.ForkDir(rb.Name)
	return q
}

// key answers: y copies the runbook and opens the copy in the editor.
func (q forkQuestion) key(msg tea.KeyPressMsg) (forkQuestion, tea.Cmd) {
	name := q.name
	q.name = ""
	switch msg.String() {
	case "y", "Y", "enter":
		local, err := q.reg.Fork(name, "")
		if err != nil {
			return q, warn(err.Error())
		}
		return q, func() tea.Msg { return msgs.OpenRunbookMsg{Name: local, Edit: true} }
	}
	return q, flash("not copied")
}

func (q forkQuestion) view(width int) string {
	inner := min(width-4, 96) - 6
	where := "den.yaml has no runbooks.dirs to copy it into: add one first."
	if q.dest != "" {
		where = "The copy goes into " + tildePath(q.dest) + string(filepath.Separator) + " and is yours: updates leave it alone."
	}
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("Edit a runbook from " + q.from),
		"",
		muted.Render(truncate(q.name+" comes from the runbook source "+q.from+": its folder", inner)),
		muted.Render(truncate("is replaced by every update, so it is not edited in place.", inner)),
		"",
		dim.Render(lipgloss.NewStyle().Width(inner).Render(where)),
		"",
		question("Copy it into your runbooks to change it?", "copy", "keep"),
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Width(min(width-4, 96) + 2).
		Render(strings.Join(lines, "\n"))
}
