package runbooks

import (
	"fmt"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// migration asks before m moves a runbook defined by a header script into a
// folder of its own; the list and the view share it.
type migration struct {
	reg  *runbook.Registry
	name string // the runbook being asked about; "" when no question is open
	plan runbook.Migration
}

func (g migration) active() bool { return g.name != "" }

// begin opens the question for e, or says why e cannot move.
func (g migration) begin(e entry) (migration, tea.Cmd) {
	switch {
	case e.isSeq():
		return g, warn("a sequence lives in den.yaml: it has no script to move")
	case e.runner.Runbook().Dir != "":
		return g, flash("already in its own folder")
	case e.runner.Runbook().Source == "":
		return g, warn("defined in den.yaml: move it into a folder by hand, or keep it there")
	case e.isRunning():
		return g, warn("running: move it once it has finished")
	}
	g.name, g.plan = e.name(), runbook.PlanMigration(e.runner.Runbook().Source)
	return g, nil
}

// key answers the question: y moves the runbook, anything else keeps it.
func (g migration) key(msg tea.KeyPressMsg) (migration, tea.Cmd) {
	name := g.name
	g.name = ""
	switch msg.String() {
	case "y", "Y", "enter":
		m, err := g.reg.Migrate(name)
		if err != nil {
			return g, warn(err.Error())
		}
		return g, flash(fmt.Sprintf("moved %s into %s/", name, tildePath(m.Dir)))
	}
	return g, flash("not moved")
}

func (g migration) view(width int) string {
	inner := min(width-4, 96) - 6
	code := lipgloss.NewStyle().Foreground(colorRunning)
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("Move into its own folder"),
		"",
		muted.Render("Runbook: ") + lipgloss.NewStyle().Bold(true).Render(g.name),
		muted.Render("Script:  ") + code.Render(truncate(tildePath(g.plan.Script), inner-9)),
		muted.Render("Folder:  ") + code.Render(truncate(tildePath(g.plan.Dir)+"/", inner-9)),
		"",
		muted.Render("   " + runbook.ManifestName + "   what its # den: lines say"),
		muted.Render("   " + g.plan.Run + strings.Repeat(" ", max(len(runbook.ManifestName)-len(g.plan.Run), 0)) + "   the script, without those lines"),
		muted.Render("   README.md" + strings.Repeat(" ", max(len(runbook.ManifestName)-len("README.md"), 0)) + "   the comment block at its top"),
		"",
		dim.Render(truncate("The script itself is removed; it runs inside the folder from then on.", inner)),
		"",
		question("Move it?", "yes", "no"),
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Width(min(width-4, 96) + 2).
		Render(strings.Join(lines, "\n"))
}

func warn(text string) tea.Cmd {
	return func() tea.Msg { return msgs.StatusFlashMsg{Text: text, Warn: true, Hold: 4 * time.Second} }
}
