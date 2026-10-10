package runbooks

import (
	"context"
	"fmt"
	"strings"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/runbook/source"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// What the sources pop-up shows.
const (
	srcList     = iota // the sources
	srcFolders         // the runbooks of one source
	srcRemoving        // the question before removing one
)

// sources is the pop-up s opens over the list: the runbook sources, the
// runbooks of one with what is installed, and adding, updating and removing
// them. Fetching runs off the UI goroutine and reports back with a
// msgs.RunbookSourceDoneMsg; everything else only reads the checkout and
// edits den.yaml, which is quick enough to do at once.
type sources struct {
	reg  *runbook.Registry
	ctx  context.Context
	open bool
	mode int
	// light is set once the terminal reported a light background; the zero
	// value is the dark palette, which shows until it replies.
	light bool

	cursor, folderCursor int
	name                 string // the source the runbooks or the question are about

	form             *huh.Form
	url, as, ref, at *string // the add form's answers

	busy   string   // what runs in the background: "updating ops…"
	result []string // what the last operation did, until the next key
	failed bool
}

func (s sources) active() bool       { return s.open }
func (s sources) awaitingForm() bool { return s.form != nil }

// begin opens the pop-up on the list of sources.
func (s sources) begin() sources {
	s.open, s.mode, s.result, s.failed = true, srcList, nil, false
	return s
}

func (s sources) states() []runbook.SourceState { return s.reg.Sources() }

func (s sources) selected() (runbook.SourceState, bool) {
	states := s.states()
	if s.cursor < 0 || s.cursor >= len(states) {
		return runbook.SourceState{}, false
	}
	return states[s.cursor], true
}

func (s sources) state(name string) (runbook.SourceState, bool) {
	for _, st := range s.states() {
		if st.Spec.Name == name {
			return st, true
		}
	}
	return runbook.SourceState{}, false
}

// update takes a key, or every message while the add form is up.
func (s sources) update(msg tea.Msg, width int) (sources, tea.Cmd) {
	if s.form != nil {
		return s.updateForm(msg)
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return s, nil
	}
	key := k.String()
	s.result, s.failed = nil, false
	switch s.mode {
	case srcRemoving:
		s.mode = srcList
		if key == "y" || key == "Y" {
			if err := s.reg.RemoveSource(s.name); err != nil {
				return s.report(nil, err), nil
			}
			s.cursor = max(s.cursor-1, 0)
			return s.report([]string{"removed " + s.name}, nil), nil
		}
		return s, nil
	case srcFolders:
		return s.folderKey(key)
	}
	states := s.states()
	switch key {
	case "esc":
		s.open = false
	case "up", "k":
		s.cursor = max(s.cursor-1, 0)
	case "down", "j":
		s.cursor = max(min(s.cursor+1, len(states)-1), 0)
	case "enter":
		if st, ok := s.selected(); ok {
			s.mode, s.name, s.folderCursor = srcFolders, st.Spec.Name, 0
		}
	case "u":
		if st, ok := s.selected(); ok && s.busy == "" {
			return s.updateSource(st.Spec.Name)
		}
	case "S":
		if len(states) > 0 && s.busy == "" {
			s.busy = "checking out what den.lock pins…"
			reg, ctx := s.reg, s.ctx
			return s, func() tea.Msg {
				err := reg.SyncSources(ctx)
				return msgs.RunbookSourceDoneMsg{Lines: []string{"checked out what den.lock pins"}, Err: err}
			}
		}
	case "a":
		return s.startForm(width)
	case "d":
		if st, ok := s.selected(); ok {
			s.mode, s.name = srcRemoving, st.Spec.Name
		}
	}
	return s, nil
}

// folderKey handles a key in the runbooks of one source.
func (s sources) folderKey(key string) (sources, tea.Cmd) {
	st, ok := s.state(s.name)
	if !ok {
		s.mode = srcList
		return s, nil
	}
	switch key {
	case "esc", "left":
		s.mode = srcList
	case "up", "k":
		s.folderCursor = max(s.folderCursor-1, 0)
	case "down", "j":
		s.folderCursor = max(min(s.folderCursor+1, len(st.Folders)-1), 0)
	case " ", "space", "enter":
		if s.folderCursor < len(st.Folders) {
			f := st.Folders[s.folderCursor]
			if err := s.reg.SelectSourceRunbook(s.name, f.Folder, !f.Selected); err != nil {
				return s.report(nil, err), nil
			}
			done := "installed "
			if f.Selected {
				done = "uninstalled "
			}
			return s.report([]string{done + f.Name}, nil), nil
		}
	case "u":
		if s.busy == "" {
			return s.updateSource(s.name)
		}
	}
	return s, nil
}

// updateSource fetches name in the background.
func (s sources) updateSource(name string) (sources, tea.Cmd) {
	s.busy = "updating " + name + "…"
	reg, ctx := s.reg, s.ctx
	return s, func() tea.Msg {
		u, err := reg.UpdateSource(ctx, name)
		if err != nil {
			return msgs.RunbookSourceDoneMsg{Err: err}
		}
		lines := u.Lines()
		if u.AsksAgain() {
			lines = append(lines, "New and changed runbooks ask before their next run.")
		}
		return msgs.RunbookSourceDoneMsg{Lines: lines}
	}
}

// done shows what a background operation did; with the pop-up closed, the
// status bar says it.
func (s sources) done(msg msgs.RunbookSourceDoneMsg) (sources, tea.Cmd) {
	s.busy = ""
	if !s.open {
		if msg.Err != nil {
			return s, warn(msg.Err.Error())
		}
		if len(msg.Lines) > 0 {
			return s, flash(strings.TrimSpace(msg.Lines[0]))
		}
		return s, nil
	}
	return s.report(msg.Lines, msg.Err), nil
}

func (s sources) report(lines []string, err error) sources {
	s.result, s.failed = lines, err != nil
	if err != nil {
		s.result = []string{err.Error()}
	}
	return s
}

// startForm asks for a new source: its URL, and optionally its name, ref and
// path.
func (s sources) startForm(width int) (sources, tea.Cmd) {
	s.url, s.as, s.ref, s.at = new(string), new(string), new(string), new(string)
	s.form = huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Repository").
			Description(formText("https://host/owner/repo, git@host:owner/repo.git or host/owner/repo", width)).
			Value(s.url).
			Validate(func(v string) error {
				_, err := config.ParseRepo(v)
				return err
			}),
		huh.NewInput().Title("Name (optional)").Description(formText("prefixes its runbooks; empty: from the repository's name", width)).Value(s.as),
		huh.NewInput().Title("Ref (optional)").Description(formText("tag, branch or commit; empty: the default branch", width)).Value(s.ref),
		huh.NewInput().Title("Path (optional)").Description(formText("the folder holding the runbook folders; empty: the root", width)).Value(s.at),
	)).WithTheme(formThemeFor(!s.light)).WithWidth(formWidth(width)).WithShowHelp(false)
	return s, s.form.Init()
}

func (s sources) updateForm(msg tea.Msg) (sources, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "esc" {
		s.form = nil
		return s, nil
	}
	next, cmd := s.form.Update(msg)
	if f, ok := next.(*huh.Form); ok {
		s.form = f
	}
	switch s.form.State {
	case huh.StateAborted:
		s.form = nil
		return s, nil
	case huh.StateCompleted:
		s.form = nil
		src := config.RunbookSource{
			URL: strings.TrimSpace(*s.url), Name: strings.TrimSpace(*s.as),
			Ref: strings.TrimSpace(*s.ref), Path: strings.TrimSpace(*s.at),
		}
		// den.yaml now, so the source is listed at once; the fetch after.
		u, err := s.reg.AddSource(s.ctx, src, false)
		if err != nil {
			return s.report(nil, err), nil
		}
		for i, st := range s.states() {
			if st.Spec.Name == u.Source {
				s.cursor = i
			}
		}
		return s.updateSource(u.Source)
	}
	return s, cmd
}

// help is the hint line while the pop-up has the keyboard.
func (s sources) help() []components.HelpKey {
	switch {
	case s.form != nil:
		return []components.HelpKey{key("enter", "next field"), key("esc", "cancel")}
	case s.mode == srcRemoving:
		return []components.HelpKey{key("y", "remove"), key("n", "keep")}
	case s.mode == srcFolders:
		return []components.HelpKey{key("space", "install or uninstall"), key("u", "update"), key("esc", "back")}
	}
	return []components.HelpKey{key("enter", "runbooks"), key("u", "update"), key("S", "sync all"), key("a", "add"), key("d", "remove"), key("esc", "close")}
}

func (s sources) view(width, height int) string {
	boxWidth := min(width-4, 110)
	inner := boxWidth - 6
	title := lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	lines := []string{title.Render("Runbook sources")}
	switch {
	case s.form != nil:
		lines = append(lines, dim.Render("A git repository of runbook folders, checked out beside your own runbooks."), "", s.form.View())
	case s.mode == srcRemoving:
		lines = append(lines, "",
			lipgloss.NewStyle().Bold(true).Render("Remove "+s.name+"?"),
			muted.Render(truncate("It leaves den.yaml and den.lock, its runbooks leave the list and its checkout is deleted.", inner)),
			"", question("Remove it?", "remove", "keep"))
	case s.mode == srcFolders:
		lines = append(lines, s.folderLines(inner, height)...)
	default:
		lines = append(lines, s.listLines(inner, height)...)
	}
	if s.busy != "" {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(colorRunning).Render("◐ "+s.busy))
	}
	if len(s.result) > 0 {
		style := muted
		if s.failed {
			style = lipgloss.NewStyle().Foreground(colorWarn)
		}
		lines = append(lines, "")
		for _, l := range s.result {
			lines = append(lines, style.Render(truncate(l, inner)))
		}
	}
	if s.form == nil && s.mode != srcRemoving {
		lines = append(lines, "", components.Hints(s.help()...))
	}
	if limit := max(height-6, 6); len(lines) > limit {
		lines = append(lines[:limit-1], lines[len(lines)-1])
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).
		Padding(1, 2).Width(boxWidth + 2).Render(strings.Join(lines, "\n"))
}

func (s sources) listLines(inner, height int) []string {
	states := s.states()
	if len(states) == 0 {
		return []string{"", muted.Render("No runbook sources yet."),
			dim.Render(truncate("A source is a git repository of runbook folders; its runbooks are listed as <source>/<name>.", inner))}
	}
	lines := []string{dim.Render("Your team's runbooks from git, listed as <source>/<name>. Nothing updates on its own."), ""}
	nameW := 4
	for _, st := range states {
		nameW = max(nameW, len(st.Spec.Name))
	}
	for i, st := range states {
		ref := st.Spec.Ref
		if ref == "" {
			ref = "default branch"
		}
		where := "not checked out"
		if st.CheckedOut && st.Locked != nil {
			where = ref + " @ " + source.Short(st.Locked.Commit)
			installed := 0
			for _, f := range st.Folders {
				if f.Selected {
					installed++
				}
			}
			where += fmt.Sprintf("   %d of %d installed", installed, len(st.Folders))
		}
		row := fmt.Sprintf("%-*s  %s   %s", nameW, st.Spec.Name, st.Repo, where)
		lines = append(lines, cursorRow(i == s.cursor, truncate(row, inner-2)))
		if st.Problem != "" {
			lines = append(lines, lipgloss.NewStyle().Foreground(colorWarn).Render(truncate("    ⚠ "+st.Problem, inner)))
		}
		if len(lines) > height-12 {
			break
		}
	}
	return lines
}

func (s sources) folderLines(inner, height int) []string {
	st, ok := s.state(s.name)
	if !ok {
		return nil
	}
	head := st.Spec.Name + " · " + st.Repo
	if st.Locked != nil {
		head += " @ " + source.Short(st.Locked.Commit)
	}
	lines := []string{muted.Render(truncate(head, inner)), ""}
	if !st.CheckedOut {
		return append(lines, lipgloss.NewStyle().Foreground(colorWarn).Render(truncate("⚠ "+st.Problem, inner)))
	}
	if len(st.Folders) == 0 {
		return append(lines, muted.Render("No runbook folders in it."))
	}
	folderW, nameW := 6, 4
	for _, f := range st.Folders {
		folderW, nameW = max(folderW, len(f.Folder)), max(nameW, len(f.Name))
	}
	start := max(s.folderCursor-(height-16), 0)
	for i := start; i < len(st.Folders) && len(lines) < height-10; i++ {
		f := st.Folders[i]
		mark := "  "
		if f.Selected {
			mark = lipgloss.NewStyle().Foreground(colorOK).Render("✓ ")
		}
		row := fmt.Sprintf("%-*s  %-*s  %s", folderW, f.Folder, nameW, f.Name, f.Description)
		lines = append(lines, cursorRow(i == s.folderCursor, mark+truncate(row, inner-4)))
	}
	if st.Problem != "" {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(colorWarn).Render(truncate("⚠ "+st.Problem, inner)))
	}
	return lines
}

// cursorRow marks the row under the cursor.
func cursorRow(selected bool, text string) string {
	if selected {
		return lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("▸ ") + text
	}
	return "  " + text
}
