package runbooks

import (
	"fmt"
	"strings"

	"github.com/lukaszgard/den/internal/runbook"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// column is one column of the Runbooks table.
type column struct {
	title string
	// min and max bound the width; a cell is cut to what the column gets.
	// A column whose min and max differ grows with the pane.
	min, max int
	// comfort is the width a growing column should have before the pane keeps
	// a column it could drop; 0 for a fixed column.
	comfort int
	// drop orders the columns a narrow pane gives up, lowest first; 0 never.
	drop int
	cell func(m Model, e entry, st runbook.Run) string
}

// columnGap separates two columns.
const columnGap = 2

// rowIndent is what comes before the first column: a space and the cursor.
const rowIndent = 3

var columns = []column{
	{title: "STATUS", min: 11, max: 11, cell: func(_ Model, e entry, st runbook.Run) string { return status(e, st) }},
	// Two spaces in front: the names have them, for the ! of one that asks
	// before running.
	{title: "  NAME", min: 16, max: 34, comfort: 20, cell: func(_ Model, e entry, _ runbook.Run) string {
		marker := "  "
		if e.needsConfirm() {
			marker = lipgloss.NewStyle().Foreground(colorWarn).Render("!") + " "
		}
		return marker + e.name()
	}},
	{title: "TYPE", min: 11, max: 11, drop: 4, cell: func(_ Model, e entry, _ runbook.Run) string { return e.kind() }},
	{title: "DESCRIPTION", min: 12, max: 60, comfort: 20, drop: 3, cell: func(_ Model, e entry, _ runbook.Run) string { return muted.Render(e.description()) }},
	{title: "ENV · PROFILE", min: 14, max: 40, comfort: 20, cell: func(m Model, e entry, _ runbook.Run) string { return muted.Render(e.account(m.reg)) }},
	{title: "LAST RUN", min: 14, max: 14, drop: 2, cell: func(m Model, _ entry, st runbook.Run) string { return dim.Render(lastRun(st, m.now())) }},
	// comfort: the drop order should keep room for "137 · 2✗", not only the title.
	{title: "RUNS", min: 4, max: 12, comfort: 10, drop: 1, cell: func(_ Model, e entry, _ runbook.Run) string { return runs(e.tally()) }},
	{title: "⌨", min: 3, max: 3, cell: func(m Model, e entry, _ runbook.Run) string {
		if m.terminals == nil {
			return ""
		}
		if n := m.terminals(e.name()); n > 0 {
			return lipgloss.NewStyle().Foreground(colorOK).Render(fmt.Sprintf("⌨ %d", n))
		}
		return ""
	}},
}

// layout gives each column its width for a pane width cells wide: 0 for a
// column the pane is too narrow for. Name and env · profile grow up to what
// their widest cell needs, the description takes the rest.
func (m Model) layout(width int, rows []entry) []int {
	avail := width - rowIndent
	natural := m.naturalWidths(rows)
	widths := make([]int, len(columns))
	for i, c := range columns {
		widths[i] = c.min
	}
	// need is the room the kept columns take at their minimum, or, with
	// comfortable, at the width each growing one should have.
	need := func(comfortable bool) int {
		n, cols := 0, 0
		for i, w := range widths {
			if w == 0 {
				continue
			}
			if c := columns[i]; comfortable && c.comfort > 0 {
				w = max(w, min(natural[i], c.comfort))
			}
			n += w
			cols++
		}
		return n + max(cols-1, 0)*columnGap
	}
	// A pane that cannot give the name, the description and the env their
	// comfortable width gives up a column for them: the run count first.
	for rank := 1; need(true) > avail; rank++ {
		dropped := false
		for i, c := range columns {
			if c.drop == rank {
				widths[i], dropped = 0, true
			}
		}
		if !dropped {
			break // nothing left to drop: cells are cut instead
		}
	}

	// The room left is shared like water: every growing column gets an
	// equal share, one that needs less than its share takes only that, and
	// what it leaves goes to the others. Name, env · profile and description
	// then come out balanced instead of one of them taking it all.
	extra := avail - need(false)
	for extra > 0 {
		var growing []int
		for i, c := range columns {
			if widths[i] > 0 && widths[i] < min(natural[i], c.max) {
				growing = append(growing, i)
			}
		}
		if len(growing) == 0 {
			break
		}
		share := max(extra/len(growing), 1)
		for _, i := range growing {
			grow := min(share, min(natural[i], columns[i].max)-widths[i], extra)
			widths[i] += grow
			extra -= grow
		}
	}
	return widths
}

// naturalWidths is the width of each column's widest cell, title included.
func (m Model) naturalWidths(rows []entry) []int {
	out := make([]int, len(columns))
	for i, c := range columns {
		out[i] = lipgloss.Width(c.title)
		for _, e := range rows {
			out[i] = max(out[i], lipgloss.Width(c.cell(m, e, e.state())))
		}
	}
	return out
}

// header is the table's title row and the rule under it.
func header(widths []int) []string {
	style := lipgloss.NewStyle().Bold(true).Foreground(colorMuted)
	var cells []string
	total := 0
	for i, c := range columns {
		if widths[i] == 0 {
			continue
		}
		cells = append(cells, fit(style.Render(c.title), widths[i]))
		total += widths[i] + columnGap
	}
	line := strings.Repeat(" ", rowIndent) + strings.Join(cells, strings.Repeat(" ", columnGap))
	return []string{line, dim.Render(strings.Repeat(" ", rowIndent) + strings.Repeat("─", max(total-columnGap, 1)))}
}

// tableRow renders one entry under the header's widths.
func (m Model) tableRow(e entry, selected, focused bool, widths []int) string {
	st := e.state()
	cursor := "  "
	if selected {
		cursor = accent.Render("▸ ")
	}
	var cells []string
	for i, c := range columns {
		if widths[i] == 0 {
			continue
		}
		cell := c.cell(m, e, st)
		if c.title == "  NAME" {
			style := lipgloss.NewStyle().Foreground(lipgloss.Color("#D0D0D0"))
			if selected {
				style = style.Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Faint(!focused)
			}
			cell = style.Render(cell)
		}
		cells = append(cells, fit(cell, widths[i]))
	}
	return " " + cursor + strings.Join(cells, strings.Repeat(" ", columnGap))
}

// fit cuts a cell, colours and all, to width cells and pads it to them.
func fit(cell string, width int) string {
	if lipgloss.Width(cell) > width {
		cell = ansi.Truncate(cell, width, "…")
	}
	return cell + strings.Repeat(" ", max(width-lipgloss.Width(cell), 0))
}
