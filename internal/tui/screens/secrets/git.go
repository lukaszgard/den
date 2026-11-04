package secrets

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/secrets"
	"github.com/lukaszgard/den/internal/secrets/gitfile"
	"github.com/lukaszgard/den/internal/tui/components"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// GitChecker is what the pane asks about secrets kept in a git working copy:
// gitfile.Git in den, a fake in the tests.
type GitChecker interface {
	// Repo is the working copy file is in, or "" when none. It starts no
	// process: it runs for every secret when the pane is built.
	Repo(file string) string
	// Check is offline: it compares with the refs the repository has.
	Check(ctx context.Context, repo string, files []gitfile.File) []gitfile.FileState
	Fetch(ctx context.Context, st gitfile.FileState) error
	FastForward(ctx context.Context, st gitfile.FileState) (gitfile.Moved, error)
}

// gitRequest is one pass of the git check: always over every secret in git,
// offline, after fetching one working copy first when f or u asked for it.
type gitRequest struct {
	fetch  string // the working copy to fetch first; "" for none
	item   int    // the item f or u was pressed on, which hears the outcome; -1 for none
	update bool   // u: offer the fast-forward once fetched
}

var localCheck = gitRequest{item: -1}

// gitQuestion is a question about git on screen, in place of the details
// box (or under the location of an open file).
type gitQuestion struct {
	edit bool // e on an outdated file; otherwise the fast-forward
	item int
	st   gitfile.FileState
}

type (
	// gitCheckedMsg is the outcome of one pass of recheckGit.
	gitCheckedMsg struct {
		gen      uint64
		req      gitRequest
		states   map[int]gitfile.FileState // by index into Model.items
		fetchErr error                     // why the fetch req asked for failed
	}
	gitUpdatedMsg struct {
		gen   uint64
		item  int
		repo  string
		moved gitfile.Moved
		err   error
	}
)

// WithGit makes the pane follow the secrets kept in a git working copy:
// their state against the upstream, f to fetch, u to update. Without it
// (tests, mostly) the pane knows nothing of git.
func (m Model) WithGit(c GitChecker) Model {
	m.git = c
	m.gitRepos = make([]string, len(m.items))
	m.gitStates = map[int]gitfile.FileState{}
	for i, it := range m.items {
		if g, ok := it.(secrets.GitTracked); ok {
			if path, spec := g.GitFile(); spec.On() {
				m.gitRepos[i] = c.Repo(path)
			}
		}
	}
	return m
}

// gitRepo is item i's working copy, or "" when it is not in one.
func (m Model) gitRepo(i int) string {
	if i < 0 || i >= len(m.gitRepos) {
		return ""
	}
	return m.gitRepos[i]
}

// gitActions reports whether f and u apply to item i: it is in a working
// copy that tracks it, or (until the first check says otherwise) may.
func (m Model) gitActions(i int) bool {
	if m.gitRepo(i) == "" {
		return false
	}
	st, checked := m.gitStates[i]
	return !checked || (st.State != gitfile.NotTracked && st.State != gitfile.Unknown)
}

// Asking reports whether a question about git waits for an answer, so tab
// does not leave the pane while it is on screen.
func (m Model) Asking() bool { return m.ask != nil }

// recheckGit runs a pass of the git check off the UI goroutine: every secret
// in git, grouped by working copy, so ten secrets in one repository cost one
// check and at most one fetch. Only one pass runs at a time; a request made
// meanwhile runs once it ends.
func (m Model) recheckGit(req gitRequest) (Model, tea.Cmd) {
	m.gitStarted = true
	if m.git == nil {
		return m, nil
	}
	if m.gitBusy {
		// A request for a fetch is the person's; a local check is already
		// part of every pass.
		if m.gitNext == nil || req.item >= 0 {
			m.gitNext = &req
		}
		return m, nil
	}
	type group struct {
		repo  string
		items []int
		files []gitfile.File
	}
	var groups []*group
	byRepo := map[string]*group{}
	for i, it := range m.items {
		repo := m.gitRepo(i)
		if repo == "" {
			continue
		}
		path, spec := it.(secrets.GitTracked).GitFile()
		g := byRepo[repo]
		if g == nil {
			g = &group{repo: repo}
			byRepo[repo] = g
			groups = append(groups, g)
		}
		g.items = append(g.items, i)
		g.files = append(g.files, gitfile.File{Path: path, Branch: spec.Branch})
	}
	if len(groups) == 0 {
		return m, nil
	}
	m.gitBusy = true
	gen, c := m.gen, m.git
	return m, func() tea.Msg {
		ctx := context.Background()
		msg := gitCheckedMsg{gen: gen, req: req, states: map[int]gitfile.FileState{}}
		for _, g := range groups {
			states := c.Check(ctx, g.repo, g.files)
			if g.repo == req.fetch {
				states, msg.fetchErr = fetchThenCheck(ctx, c, g.repo, g.files, states)
			}
			for j, st := range states {
				msg.states[g.items[j]] = st
			}
		}
		return msg
	}
}

// fetchThenCheck fetches each upstream the files of one working copy are
// compared with, once, and checks them again. A file whose upstream could
// not be fetched is marked check failed; its readiness is not touched.
func fetchThenCheck(ctx context.Context, c GitChecker, repo string, files []gitfile.File, states []gitfile.FileState) (_ []gitfile.FileState, fetchErr error) {
	key := func(st gitfile.FileState) string { return st.Remote + "\x00" + st.RemoteRef + "\x00" + st.UpstreamRef }
	failed := map[string]error{}
	done := map[string]bool{}
	for _, st := range states {
		if st.Remote == "" || done[key(st)] {
			continue
		}
		done[key(st)] = true
		if err := c.Fetch(ctx, st); err != nil {
			failed[key(st)], fetchErr = err, err
		}
	}
	if len(done) == 0 {
		return states, nil // nothing to fetch: the states say why
	}
	states = c.Check(ctx, repo, files)
	for j, st := range states {
		if err := failed[key(st)]; err != nil {
			states[j].State, states[j].Err = gitfile.CheckFailed, err.Error()
		}
	}
	return states, fetchErr
}

func (m Model) gitChecked(msg gitCheckedMsg) (Model, tea.Cmd) {
	if msg.gen != m.gen {
		return m, nil // for the pane a config reload has replaced
	}
	m.gitBusy = false
	m.gitStates = msg.states
	var cmds []tea.Cmd
	if i := msg.req.item; i >= 0 && i < len(m.items) {
		var c tea.Cmd
		m, c = m.gitOutcome(i, msg)
		cmds = append(cmds, c)
	}
	if next := m.gitNext; next != nil {
		m.gitNext = nil
		var c tea.Cmd
		m, c = m.recheckGit(*next)
		cmds = append(cmds, c)
	}
	return m, tea.Batch(cmds...)
}

// gitOutcome tells the person who pressed f or u on item i what came of it,
// and asks before a fast-forward.
func (m Model) gitOutcome(i int, msg gitCheckedMsg) (Model, tea.Cmd) {
	name := m.items[i].Name()
	st := msg.states[i]
	switch {
	case st.State == gitfile.Unknown: // the file is not there
		return m, flash(name+": "+m.status(i).Reason, true)
	case st.State == gitfile.CheckFailed:
		return m, flash(name+": "+st.Err, true)
	case st.State == gitfile.NotTracked:
		return m, flash(name+" is not in git", true)
	case st.State == gitfile.NoUpstream:
		return m, flash(name+": no upstream — "+st.Note, true)
	case msg.fetchErr != nil:
		return m, flash(st.Name+": "+msg.fetchErr.Error(), true)
	case !msg.req.update:
		if st.State == gitfile.UpToDate {
			return m, flash(name+" is up to date", false)
		}
		return m, flash(name+": "+stateSummary(st), st.State >= gitfile.Outdated)
	case st.State == gitfile.UpToDate:
		return m, flash(name+" is up to date", false)
	case st.State != gitfile.Outdated && st.State != gitfile.Diverged:
		return m, flash(name+": "+st.Upstream+" has nothing newer ("+st.State.String()+" here)", false)
	}
	if why := st.Refusal(); why != "" {
		return m, flash(name+" not updated: "+why, true)
	}
	m.ask = &gitQuestion{item: i, st: st}
	return m, nil
}

// stateSummary is st in a few words, for a flash.
func stateSummary(st gitfile.FileState) string {
	switch st.State {
	case gitfile.Outdated:
		return fmt.Sprintf("%s has a newer version (%s)", st.Upstream, plural(st.Behind, "commit", "commits"))
	case gitfile.Diverged:
		return "changed both here and on " + st.Upstream
	case gitfile.NotPushed:
		return fmt.Sprintf("%s not pushed to %s", plural(st.Ahead, "commit", "commits"), st.Upstream)
	}
	return st.State.String()
}

// gitKey is f (update false) or u on the selected secret: fetch its working
// copy, check again, and for u offer the fast-forward.
func (m Model) gitKey(update bool) (Model, tea.Cmd) {
	i, ok := m.selectedIndex()
	if !ok || !m.gitActions(i) {
		return m, nil
	}
	repo := m.gitRepo(i)
	name := filepath.Base(repo)
	if st, ok := m.gitStates[i]; ok && st.Name != "" {
		name = st.Name
	}
	m, c := m.recheckGit(gitRequest{fetch: repo, item: i, update: update})
	return m, tea.Batch(flash("fetching "+name+"…", false), c)
}

// answerGit takes the key pressed while a question about git is on screen.
func (m Model) answerGit(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	q := *m.ask
	m.ask = nil
	k := msg.String()
	switch {
	case q.edit && k == "e":
		return m, m.editCmd()
	case q.edit && k == "u":
		return m.gitKey(true)
	case q.edit:
		return m, nil
	case k == "y" || k == "Y":
		gen, c, repo := m.gen, m.git, m.gitRepo(q.item)
		return m, tea.Batch(flash("updating "+q.st.Name+"…", false), func() tea.Msg {
			moved, err := c.FastForward(context.Background(), q.st)
			return gitUpdatedMsg{gen: gen, item: q.item, repo: repo, moved: moved, err: err}
		})
	}
	return m, flash(m.items[q.item].Name()+" not updated", false)
}

// gitUpdated follows a fast-forward: every secret is probed and checked
// again, and the open file, if the update reached it, is read again.
func (m Model) gitUpdated(msg gitUpdatedMsg) (Model, tea.Cmd) {
	if msg.gen != m.gen {
		return m, nil
	}
	m.statuses = probeAll(m.items)
	m, check := m.recheckGit(localCheck)
	if msg.err != nil {
		return m, tea.Batch(check, flash(m.items[msg.item].Name()+" not updated: "+msg.err.Error(), true))
	}
	cmds := []tea.Cmd{check, flash(msg.moved.String(), false)}
	for i := range m.items {
		if m.gitRepo(i) == msg.repo {
			delete(m.readFailed, i) // a decrypt of the old version says nothing now
		}
	}
	if i, ok := m.selectedIndex(); ok && m.open && !m.browsing && m.gitRepo(i) == msg.repo {
		m = m.startRead()
		cmds = append(cmds, m.readCmd(i))
	}
	return m, tea.Batch(cmds...)
}

// edit is e: sops edit, unless the upstream has a newer version of the file,
// which editing would turn into a conflict an encrypted file cannot merge.
func (m Model) edit() (Model, tea.Cmd) {
	if i, ok := m.selectedIndex(); ok {
		if st := m.gitStates[i]; st.State == gitfile.Outdated {
			m.ask = &gitQuestion{edit: true, item: i, st: st}
			return m, nil
		}
	}
	return m, m.editCmd()
}

// ── View ──────────────────────────────────────────────────────────────────────

// gitBadge is the list's word on item i's git state; "" when it has none.
func (m Model) gitBadge(i int) string {
	if m.gitRepo(i) == "" {
		return ""
	}
	st, ok := m.gitStates[i]
	if !ok || st.State == gitfile.Unknown {
		return ""
	}
	icon, color := "", colorDim
	switch st.State {
	case gitfile.Diverged:
		icon, color = "⇅ ", colorErr
	case gitfile.Outdated:
		icon, color = "↓ ", colorAccent
	case gitfile.NotCommitted:
		icon, color = "✎ ", colorAccent
	case gitfile.NotPushed:
		icon, color = "↑ ", colorAccent
	case gitfile.UpToDate:
		icon, color = "✓ ", colorOK
	case gitfile.CheckFailed:
		icon, color = "! ", colorErr
	case gitfile.NoUpstream:
		icon = "– "
	}
	return lipgloss.NewStyle().Foreground(color).Render(icon + st.State.String())
}

// gitDetails are the details box's rows for item i: where the file is in
// git and against what, then what is worth knowing about its state.
func (m Model) gitDetails(i int, now time.Time) []secrets.Detail {
	repo := m.gitRepo(i)
	if repo == "" {
		return nil
	}
	st, ok := m.gitStates[i]
	switch {
	case !ok:
		return []secrets.Detail{{Label: "Git", Value: filepath.Base(repo) + " · checking…"}}
	case st.State == gitfile.Unknown:
		return nil // the file is not there: its status says so
	}
	if st.Repo == "" { // git could not even read the working copy
		return []secrets.Detail{{Label: "Git", Value: filepath.Base(repo)}, {Label: "Error", Value: st.Err}}
	}
	where := st.Name
	switch {
	case st.State == gitfile.NotTracked:
		return []secrets.Detail{{Label: "Git", Value: where + " · not tracked: untracked or ignored"}}
	case st.Branch == "":
		where += " · detached HEAD"
	case st.Upstream == "":
		where += " · " + st.Branch
	case st.RefBranch != st.Branch:
		where += " · on " + st.Branch + ", compared with " + st.Upstream
	default:
		where += " · " + st.Branch + " → " + st.Upstream
	}
	if st.FetchedAt.IsZero() {
		where += " · never fetched — " + components.Hints(hintKey("f", "fetch"))
	} else {
		where += " · fetched " + ago(st.FetchedAt, now)
	}
	rows := []secrets.Detail{{Label: "Git", Value: where}}
	switch st.State {
	case gitfile.Outdated, gitfile.Diverged:
		v := plural(st.Behind, "newer commit", "newer commits")
		if c := st.Last; c.Short != "" {
			v += fmt.Sprintf(" · %s %s (%s, %s)", c.Short, c.Subject, c.Author, ago(c.When, now))
		}
		if st.State == gitfile.Diverged {
			v += " · changed here too"
		}
		rows = append(rows, secrets.Detail{Label: "Upstream", Value: v})
	case gitfile.NotPushed:
		rows = append(rows, secrets.Detail{Label: "Local", Value: plural(st.Ahead, "commit", "commits") + " not pushed"})
	case gitfile.NotCommitted:
		rows = append(rows, secrets.Detail{Label: "Local", Value: "changed, not committed"})
	case gitfile.NoUpstream:
		rows = append(rows, secrets.Detail{Label: "Upstream", Value: st.Note})
	case gitfile.CheckFailed:
		rows = append(rows, secrets.Detail{Label: "Error", Value: st.Err})
	}
	return rows
}

// gitBanner is the line under an open file's location: what git says of the
// file whose values are on screen. "" when there is nothing to say.
func (m Model) gitBanner(i int, width int) string {
	if m.gitRepo(i) == "" {
		return ""
	}
	st := m.gitStates[i]
	text, color := "", colorAccent
	var keys []components.HelpKey
	switch st.State {
	case gitfile.Outdated:
		text = fmt.Sprintf("↓ %s has a newer version of this file (%s)", st.Upstream, plural(st.Behind, "commit", "commits"))
		keys = []components.HelpKey{hintKey("u", "update")}
	case gitfile.Diverged:
		text, color = "⇅ changed both here and on "+st.Upstream+": resolve it in git", colorErr
	case gitfile.NotCommitted:
		text = "✎ changed here, not committed"
	case gitfile.NotPushed:
		text = fmt.Sprintf("↑ %s not pushed to %s", plural(st.Ahead, "commit", "commits"), st.Upstream)
	case gitfile.CheckFailed:
		text, color = "! git check failed: "+st.Err, colorErr
		keys = []components.HelpKey{hintKey("f", "retry")}
	default:
		return ""
	}
	line := lipgloss.NewStyle().Foreground(color).Render(text)
	if len(keys) > 0 {
		line += "   " + components.Hints(keys...)
	}
	return "  " + ansi.Truncate(line, max(width-4, 1), "…")
}

// askText is the question about git on screen.
func (m Model) askText() string {
	q := m.ask
	if q.edit {
		return q.st.Upstream + " has a newer version of this file. Editing the old one makes a change git cannot merge."
	}
	return fmt.Sprintf("Fast-forward %s (%s) by %s? This updates the whole branch, not only this file.",
		q.st.Name, q.st.Branch, plural(q.st.BranchBehind, "commit", "commits"))
}

// askKeys are the keys that answer the question, for the hint line.
func (m Model) askKeys() []components.HelpKey {
	if m.ask.edit {
		return []components.HelpKey{hintKey("u", "update first"), hintKey("e", "edit anyway"), hintKey("esc", "cancel")}
	}
	return []components.HelpKey{hintKey("y", "update"), hintKey("n", "cancel")}
}

// renderAsk is the question wrapped to width, indented like the rows around it.
func (m Model) renderAsk(width int) []string {
	body := lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Width(max(width-4, 10)).Render(m.askText())
	var lines []string
	for l := range strings.SplitSeq(body, "\n") {
		lines = append(lines, "  "+l)
	}
	return lines
}

// renderAskBox is the question in a box as tall as the details box it
// stands in for.
func (m Model) renderAskBox(width, height int) string {
	innerW, innerH := max(width-2, 4), max(height-2, 1)
	body := lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Width(innerW).Render(m.askText())
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).
		Render(lipgloss.NewStyle().Width(innerW).Height(innerH).MaxHeight(innerH).Render(body))
}

// ago says how long ago t was, briefly: "now", "5m ago", "2h ago", "3d ago".
func ago(t, now time.Time) string {
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

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
