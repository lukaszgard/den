package runbooks

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lukaszgard/den/internal/agent"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/tui/components"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// newForm is the pop-up n opens: the new runbook's name and, when an agent can
// start, what it should be asked to write — and, with two or more to pick from,
// which one. It is not a huh form. The request grows with what is typed, up to
// the room the pane has, and scrolls past it, where a huh group keeps the height
// it was first measured at and cuts the rest; and ↑ or shift+tab takes the
// keyboard back to the name, to change it.
type newForm struct {
	reg *runbook.Registry
	// agents are the ones that can write it; none asks only for the name, and a
	// row to pick one is drawn only for two or more.
	agents  []agent.Agent
	choice  int // the one picked, among agents
	name    textinput.Model
	request textarea.Model
	// onRequest and onAgent are set while the request or the agent row has the
	// keyboard; with neither, the name has it.
	onRequest, onAgent bool
	// err is why the name cannot be used, under it as it is typed.
	err  error
	done bool
	// editing is the runbook E asks the agent to change; the form then has
	// only the request, for the one agent in agents.
	editing string
	// draft is what the form saved last, or the draft it was opened from;
	// its ID is "" until the first save.
	draft runbook.Draft
	// edits counts the changes typed: a save waits for typing to pause, and
	// the tick of an older change is dropped.
	edits int
	// initial is what the form opened with, so text it was filled with is
	// not mistaken for text typed.
	initial string
	// asking is set while esc's question, keep it as a draft, is up.
	asking bool
	// lastRequest is set when E's question opens with the request the
	// runbook's agent was given last, which it says.
	lastRequest bool
	// closed is set once the form is done with and nothing is to be created.
	closed bool
	// top is the first of the request's rows shown once it scrolls.
	top           int
	width, height int
	// light is set once the terminal reported a light background; the zero
	// value is the dark palette, which shows until it replies.
	light bool
}

// requestLimit keeps the request to what fits on an agent's command line.
const requestLimit = 4000

func newNewForm(reg *runbook.Registry, agents []agent.Agent, choice, width, height int, isDark bool) (*newForm, tea.Cmd) {
	t := formTheme(isDark)
	f := &newForm{reg: reg, agents: agents, choice: min(max(choice, 0), max(len(agents)-1, 0)), light: !isDark}

	f.name = textinput.New()
	f.name.Prompt = "> "
	nameStyles := components.PlainInputStyles()
	nameStyles.Focused.Prompt, nameStyles.Blurred.Prompt = t.Focused.TextInput.Prompt, t.Focused.TextInput.Prompt
	nameStyles.Cursor.Color = t.Focused.TextInput.Cursor.GetForeground()
	f.name.SetStyles(nameStyles)

	f.request = textarea.New()
	f.request.ShowLineNumbers = false
	f.request.CharLimit = requestLimit
	// No limit to its lines: den shows the rows that fit and scrolls them.
	f.request.MaxHeight = 0
	f.request.Placeholder = "e.g. show the status of my RDS clusters in prod"
	// enter creates the runbook; a line break is ctrl+j, as in huh's text field.
	f.request.KeyMap.InsertNewline.SetKeys("ctrl+j", "alt+enter")
	style := textarea.StyleState{
		Base:             lipgloss.NewStyle(),
		CursorLine:       lipgloss.NewStyle(),
		CursorLineNumber: lipgloss.NewStyle(),
		EndOfBuffer:      lipgloss.NewStyle(),
		LineNumber:       lipgloss.NewStyle(),
		Placeholder:      lipgloss.NewStyle().Foreground(colorDim),
		Prompt:           t.Focused.TextInput.Prompt,
		Text:             lipgloss.NewStyle(),
	}
	requestStyles := textarea.Styles{Focused: style, Blurred: style}
	requestStyles.Blurred.Prompt = lipgloss.NewStyle().Foreground(colorMuted)
	requestStyles.Cursor = textarea.CursorStyle{
		Color: t.Focused.TextInput.Cursor.GetForeground(), Shape: tea.CursorBlock, Blink: true,
	}
	f.request.SetStyles(requestStyles)
	// "> " on its first row, its wrapped rows under the text: the request
	// reads as one answer, like the name above it.
	f.request.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return "> "
		}
		return "  "
	})

	f.setSize(width, height)
	f.initial = f.text()
	return f, f.name.Focus()
}

// newEditForm is the pop-up E opens: what ag should change in the runbook
// called name. It is the new-runbook form without its name and agent rows, so
// the request grows and scrolls the same way.
func newEditForm(reg *runbook.Registry, ag agent.Agent, name string, width, height int, isDark bool) (*newForm, tea.Cmd) {
	f, _ := newNewForm(reg, []agent.Agent{ag}, 0, width, height, isDark)
	f.editing = name
	f.request.Placeholder = "e.g. ask for the environment as a parameter"
	f.fit()
	return f, f.focusRequest()
}

// fill puts dr back into the form, to go on with it: the name, the request
// and the agent it was typed for, when that one is still offered. The
// keyboard goes to the request once there is a name.
func (f *newForm) fill(dr runbook.Draft) tea.Cmd {
	f.draft = dr
	f.name.SetValue(dr.Name)
	// Room for every row first, as in updateRequest: the textarea would
	// otherwise scroll itself to the cursor at the end.
	f.request.SetHeight(textRows(dr.Request, f.request.Width()) + 2)
	f.request.SetValue(dr.Request)
	f.request.SetHeight(max(f.rows(), 1))
	for i, a := range f.agents {
		if a.Name == dr.Agent {
			f.choice = i
		}
	}
	f.initial = f.text()
	if f.editing != "" || (len(f.agents) > 0 && strings.TrimSpace(dr.Name) != "") {
		return f.focusRequest()
	}
	return f.focusName()
}

// setSize fits the form to a pane of width×height.
func (f *newForm) setSize(width, height int) {
	if f.width == width && f.height == height {
		return
	}
	f.width, f.height = width, height
	w := f.fieldWidth()
	f.name.SetWidth(max(w-lipgloss.Width(f.name.Prompt)-1, 1))
	f.request.SetWidth(w)
	f.request.SetHeight(max(f.rows(), 1))
	f.fit()
}

// fieldWidth is the width a field's text has: the form's, less the bar that
// marks the focused field.
func (f *newForm) fieldWidth() int {
	return formWidth(f.width) - formStyles().Focused.Base.GetHorizontalFrameSize()
}

// values are what was asked for; agentName is the agent picked, "" with none.
func (f *newForm) values() (name, request, agentName string) {
	if len(f.agents) > 0 {
		agentName = f.agents[f.choice].Name
	}
	return strings.TrimSpace(f.name.Value()), strings.TrimSpace(f.request.Value()), agentName
}

// picks reports whether there is an agent to pick: with one, the form uses it
// without a row.
func (f *newForm) picks() bool { return len(f.agents) > 1 }

// cycle moves the agent picked by delta, around the list.
func (f *newForm) cycle(delta int) {
	f.choice = (f.choice + delta + len(f.agents)) % len(f.agents)
	f.fit()
}

// draftSaveDelay is how long typing pauses before the form saves its draft.
var draftSaveDelay = time.Second

// draftSaveMsg asks form to save its draft, unless more was typed since the
// change seq.
type draftSaveMsg struct {
	form *newForm
	seq  int
}

// update drives the form: esc and the question it asks, the draft's save once
// typing pauses, then the fields.
func (f *newForm) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case draftSaveMsg:
		if msg.form == f && msg.seq == f.edits && !f.closed && !f.done {
			_ = f.saveDraft()
		}
		return nil
	case tea.KeyPressMsg:
		if f.asking {
			return f.answer(msg)
		}
		if msg.String() == "esc" {
			if f.worthKeeping() {
				f.asking = true
			} else {
				f.closed = true
			}
			return nil
		}
	}
	before := f.text()
	cmd := f.updateFields(msg)
	if f.text() != before && !f.done {
		f.edits++
		cmd = tea.Batch(cmd, f.saveLater())
	}
	return cmd
}

// answer takes the reply to esc's question: y keeps what was typed as a
// draft, n deletes the draft, esc goes back to the form.
func (f *newForm) answer(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "y", "Y":
		f.asking, f.closed = false, true
		if f.empty() {
			if err := f.reg.DeleteDraft(f.draft.ID); err != nil {
				return warn("the empty draft was not deleted: " + err.Error())
			}
			return flash("nothing to keep: the draft is deleted")
		}
		if err := f.saveDraft(); err != nil {
			return warn("not kept as a draft: " + err.Error())
		}
		return flash("saved as draft")
	case "n", "N":
		f.asking, f.closed = false, true
		if f.draft.ID == "" {
			return nil
		}
		if err := f.reg.DeleteDraft(f.draft.ID); err != nil {
			return warn("the draft was not deleted: " + err.Error())
		}
		return flash("draft discarded")
	case "esc":
		f.asking = false
	}
	return nil
}

// text is what the form holds, as one string to tell a change by.
func (f *newForm) text() string {
	agentName := ""
	if len(f.agents) > 0 {
		agentName = f.agents[f.choice].Name
	}
	return f.name.Value() + "\x00" + f.request.Value() + "\x00" + agentName
}

// empty reports a form with nothing typed into it.
func (f *newForm) empty() bool {
	return strings.TrimSpace(f.name.Value()) == "" && strings.TrimSpace(f.request.Value()) == ""
}

// worthKeeping reports whether esc should ask to keep a draft: something was
// typed, or the form has a draft behind it.
func (f *newForm) worthKeeping() bool {
	return f.reg.KeepsDrafts() && (f.draft.ID != "" || (!f.empty() && f.text() != f.initial))
}

// saveLater saves the draft once typing has paused; a change typed in the
// meantime moves the save after it.
func (f *newForm) saveLater() tea.Cmd {
	if !f.reg.KeepsDrafts() {
		return nil
	}
	seq := f.edits
	return tea.Tick(draftSaveDelay, func(time.Time) tea.Msg { return draftSaveMsg{form: f, seq: seq} })
}

// asDraft is what the form holds as a draft, over the one it saved last.
func (f *newForm) asDraft() runbook.Draft {
	dr := f.draft
	dr.Request = f.request.Value()
	dr.Agent = ""
	if len(f.agents) > 0 {
		dr.Agent = f.agents[f.choice].Name
	}
	if f.editing != "" {
		dr.Kind, dr.Runbook, dr.Name = runbook.DraftEdit, f.editing, ""
	} else {
		dr.Kind, dr.Name = runbook.DraftNew, f.name.Value()
	}
	return dr
}

// saveDraft writes what the form holds as its draft. An empty form writes
// nothing: there is nothing in it to lose.
func (f *newForm) saveDraft() error {
	if f.empty() || !f.reg.KeepsDrafts() {
		return nil
	}
	saved, err := f.reg.SaveDraft(f.asDraft())
	if err != nil {
		return err
	}
	f.draft = saved
	return nil
}

// sent turns the form's draft into the saved request of the runbook called
// name, in folder, which it created or changed: an agent that fails can be
// given it again. Without a request there is nothing to give: the draft goes.
func (f *newForm) sent(name, folder string) error {
	if !f.reg.KeepsDrafts() {
		return nil
	}
	_, request, _ := f.values()
	if request == "" {
		return f.reg.DeleteDraft(f.draft.ID)
	}
	dr := f.asDraft()
	dr.State, dr.Runbook, dr.Folder, dr.Request = runbook.StateCreated, name, folder, request
	saved, err := f.reg.SaveDraft(dr)
	if err != nil {
		return err
	}
	f.draft = saved
	return nil
}

// updateFields hands msg to the field that has the keyboard.
func (f *newForm) updateFields(msg tea.Msg) tea.Cmd {
	if f.editing != "" {
		return f.updateEdit(msg)
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		// Cursor blinks.
		var nameCmd, requestCmd tea.Cmd
		f.name, nameCmd = f.name.Update(msg)
		f.request, requestCmd = f.updateRequest(msg)
		return tea.Batch(nameCmd, requestCmd)
	}
	switch s := k.String(); {
	case s == "enter":
		f.check(true)
		switch {
		case f.err != nil:
			return f.focusName()
		case len(f.agents) > 0 && !f.onRequest && !f.onAgent:
			return f.focusRequest()
		}
		f.done = true
		return nil
	case f.onAgent:
		// Nothing is typed here: any other key is dropped.
		switch s {
		case "left":
			f.cycle(-1)
		case "right":
			f.cycle(1)
		case "up", "shift+tab":
			return f.focusRequest()
		}
		return nil
	case f.onRequest && (s == "shift+tab" || (s == "up" && f.cursorRow() == 0)):
		return f.focusName()
	// ↓ leaves the request only from its last row, as ↑ leaves it from its
	// first: above that the cursor moves down the text.
	case f.onRequest && f.picks() && (s == "tab" || (s == "down" && f.cursorRow() == f.rows()-1)):
		return f.focusAgent()
	case !f.onRequest && len(f.agents) > 0 && s == "down":
		return f.focusRequest()
	case s == "tab":
		// Nowhere to go from here, and the other fields do not take it as text.
		return nil
	}
	var cmd tea.Cmd
	if f.onRequest {
		f.request, cmd = f.updateRequest(msg)
		f.fit()
		return cmd
	}
	f.name, cmd = f.name.Update(msg)
	f.check(false)
	return cmd
}

// updateEdit is update for the edit form: enter starts the agent, and the
// request takes every other key but tab, which has nowhere to go.
func (f *newForm) updateEdit(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "enter":
			f.done = true
			return nil
		case "tab", "shift+tab":
			return nil
		}
	}
	var cmd tea.Cmd
	f.request, cmd = f.updateRequest(msg)
	f.fit()
	return cmd
}

// updateRequest hands msg to the request. The textarea scrolls itself to keep
// its cursor in its height; it is given room for all its rows first, so it
// never does, and den alone picks the rows shown.
func (f *newForm) updateRequest(msg tea.Msg) (textarea.Model, tea.Cmd) {
	if len(f.agents) == 0 {
		return f.request, nil
	}
	f.request.SetHeight(f.rows() + headroom(msg))
	req, cmd := f.request.Update(msg)
	req.SetHeight(max(textRows(req.Value(), req.Width()), 1))
	return req, cmd
}

// headroom is the rows the request may grow by from msg: a key adds at most a
// row or two, text pasted or typed in one go at most a row per character. The
// Bubbles v2 textarea draws every row of its height, so a height of the
// request's whole limit made each key cost thousands of rows.
func headroom(msg tea.Msg) int {
	switch msg := msg.(type) {
	case tea.PasteMsg:
		return 2 + utf8.RuneCountInString(msg.Content)
	case tea.KeyPressMsg:
		return 2 + utf8.RuneCountInString(msg.Text)
	}
	return 2
}

func (f *newForm) focusName() tea.Cmd {
	f.onRequest, f.onAgent = false, false
	f.request.Blur()
	return f.name.Focus()
}

func (f *newForm) focusRequest() tea.Cmd {
	f.onRequest, f.onAgent = true, false
	f.name.Blur()
	// The agent picked may have a description of another length, which leaves
	// the request more or fewer rows: keep its cursor among them.
	f.fit()
	return f.request.Focus()
}

func (f *newForm) focusAgent() tea.Cmd {
	f.onRequest, f.onAgent = false, true
	f.name.Blur()
	f.request.Blur()
	return nil
}

// check says why the name cannot be used, as it is typed. That a name is
// missing waits for enter: the empty field is not a mistake yet.
func (f *newForm) check(submitting bool) {
	name := strings.TrimSpace(f.name.Value())
	if !submitting && (name == "" || f.reg.NewRunbookDir("") == "") {
		// Without runbooks.dirs the description under the title says so.
		f.err = nil
		return
	}
	f.err = f.reg.NewRunbookConflict(name, "")
}

// rows is how many rows the request takes at its width.
func (f *newForm) rows() int {
	return textRows(f.request.Value(), f.request.Width())
}

// cursorRow is the row of the request's cursor among all its rows.
func (f *newForm) cursorRow() int {
	lines := strings.Split(f.request.Value(), "\n")
	row := 0
	for _, l := range lines[:min(f.request.Line(), len(lines))] {
		row += textRows(l, f.request.Width())
	}
	return row + f.request.LineInfo().RowOffset
}

// shown is how many of the request's rows fit in the pane, and whether a
// line under them says that it scrolls: the request grows with what is typed,
// and the pop-up stays inside the pane.
func (f *newForm) shown() (rows int, scrolls bool) {
	total := f.rows()
	// One row of the pane is kept free above and below the pop-up.
	room := max(f.height-lipgloss.Height(f.view(nil, ""))-2, 1)
	if total <= room {
		return total, false
	}
	return max(room-1, 1), true
}

// fit keeps the request's cursor among the rows shown, scrolling them as
// little as it takes.
func (f *newForm) fit() {
	rows, _ := f.shown()
	cursor := f.cursorRow()
	if cursor < f.top {
		f.top = cursor
	}
	if cursor >= f.top+rows {
		f.top = cursor - rows + 1
	}
	f.top = max(min(f.top, f.rows()-rows), 0)
}

// where is the name's description: the folder the runbook goes into, named
// as the name is typed — "the first of runbooks.dirs" left people guessing
// which folder that was.
func (f *newForm) where() string {
	note := ""
	if len(f.agents) == 0 {
		note = "\nWith an agent CLI such as Claude Code, Codex or OpenCode installed, an agent can write it."
	}
	dir := f.reg.NewRunbookDir(f.name.Value())
	switch {
	case dir == "":
		return "No runbooks directory: add one to runbooks.dirs in den.yaml" + note
	case strings.TrimSpace(f.name.Value()) == "":
		dir = filepath.Join(dir, "<name>")
	}
	return "Goes into " + tildePath(dir) + string(filepath.Separator) +
		" — change where with runbooks.dirs in den.yaml" + note
}

// View draws the pop-up.
func (f *newForm) View() string {
	rows, scrolls := f.shown()
	var shown []string
	if len(f.agents) > 0 {
		all := strings.Split(f.request.View(), "\n")
		top := max(min(f.top, len(all)-rows), 0)
		shown = all[top:min(top+rows, len(all))]
	}
	more := ""
	if scrolls {
		total := f.rows()
		top := max(min(f.top, total-rows), 0)
		more = fmt.Sprintf("rows %d–%d of %d · ↑↓ scroll", top+1, top+rows, total)
	}
	return f.view(shown, more)
}

// view draws the pop-up with the request's rows given, and the line saying
// they scroll; with none, it measures what the request has around it.
func (f *newForm) view(requestRows []string, more string) string {
	t := formTheme(!f.light)
	block := func(focused bool, lines ...string) string {
		styles := t.Blurred
		if focused {
			styles = t.Focused
		}
		return styles.Base.Width(formWidth(f.width)).
			Render(strings.Join(lines, "\n"))
	}
	field := func(focused bool, lines ...string) string {
		styles := t.Blurred
		if focused {
			styles = t.Focused
		}
		lines[0] = styles.Title.Render(lines[0])
		return block(focused, lines...)
	}
	description := func(text string) string {
		return t.Focused.Description.Render(formText(text, f.width))
	}

	if f.editing != "" {
		about := f.agents[0].Label() + " changes it in a terminal tab, in its folder" + startsWithGo(f.agents[0]) + "; empty starts it without a request"
		if f.lastRequest {
			about = "Below is the request it was given last: enter sends it again. " + about
		}
		request := append([]string{"What should change in " + f.editing + "?", description(about)},
			requestRows...)
		if more != "" {
			request = append(request, dim.Render(more))
		}
		return f.frame(field(true, request...),
			f.footer(key("enter", "start"), key("ctrl+j", "new line"), key("esc", "cancel")))
	}

	name := []string{"New runbook name", description(f.where()), f.name.View()}
	if f.err != nil {
		name = append(name, lipgloss.NewStyle().Foreground(colorFail).
			Render(ansi.Wrap("✗ "+f.err.Error(), f.fieldWidth(), "")))
	}
	parts := []string{field(!f.onRequest && !f.onAgent, name...)}

	hints := []components.HelpKey{key("enter", "create"), key("esc", "cancel")}
	if len(f.agents) > 0 {
		request := append([]string{"Ask an agent to write it (optional)",
			description("describe the runbook: " + f.agents[f.choice].Name +
				" writes it in a terminal tab" + startsWithGo(f.agents[f.choice]) + "; empty opens run.sh in your editor")},
			requestRows...)
		if more != "" {
			request = append(request, dim.Render(more))
		}
		parts = append(parts, field(f.onRequest, request...))
		if f.picks() {
			parts = append(parts, f.agentRow(block, t))
		}
		switch {
		case f.onAgent:
			hints = []components.HelpKey{key("enter", "create"), key("←/→", "agent"),
				key("↑", "request"), key("esc", "cancel")}
		case f.onRequest:
			hints = []components.HelpKey{key("enter", "create")}
			if f.picks() {
				hints = append(hints, key("tab", "agent"))
			}
			hints = append(hints, key("↑/shift+tab", "name"), key("ctrl+j", "new line"), key("esc", "cancel"))
		default:
			hints[0] = key("enter", "next")
		}
	}
	parts = append(parts, f.footer(hints...))
	return f.frame(parts...)
}

// footer is the form's key line, or esc's question while it is up.
func (f *newForm) footer(keys ...components.HelpKey) string {
	if f.asking {
		return question("Keep as a draft?", "keep", "discard") + "  " + components.Hints(key("esc", "back to it"))
	}
	return components.HintLine(formWidth(f.width), true, keys...)
}

// frame is the pop-up's border around its parts.
func (f *newForm) frame(parts ...string) string {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).
		Padding(1, 2).Render(strings.Join(parts, "\n\n"))
}

// startsWithGo is what a description adds for an agent that takes no first
// prompt: it reads the request with its instructions and waits for the user.
func startsWithGo(a agent.Agent) string {
	if a.Note("request") == "" {
		return ""
	}
	return " (type go there to start)"
}

// agentRow is the row that picks the agent: its name between ‹ ›, and where it
// is among the others. A long name is cut, not wrapped, to stay one row.
func (f *newForm) agentRow(block func(bool, ...string) string, t *huh.Styles) string {
	styles := t.Blurred
	if f.onAgent {
		styles = t.Focused
	}
	count := fmt.Sprintf("  (%d/%d)", f.choice+1, len(f.agents))
	name := ansi.Truncate(f.agents[f.choice].Name, max(f.fieldWidth()-lipgloss.Width("Agent  ‹  ›"+count), 1), "…")
	return block(f.onAgent, styles.Title.Render("Agent")+"  ‹ "+lipgloss.NewStyle().Bold(true).Render(name)+" ›"+dim.Render(count))
}

// textRows is how many rows a textarea width columns wide shows text in.
func textRows(text string, width int) int {
	rows := 0
	for _, line := range strings.Split(text, "\n") {
		rows += wrapRows([]rune(line), width)
	}
	return rows
}

// wrapRows counts the rows bubbles' textarea (v0.20.0) soft-wraps one line
// into, step for step, which it does not export. It must agree with it to the
// row: one too few hides the first row, one too many shows an empty one.
func wrapRows(runes []rune, width int) int {
	rows, row, spaces := 1, 0, 0
	var word []rune
	for _, r := range runes {
		if unicode.IsSpace(r) {
			spaces++
		} else {
			word = append(word, r)
		}
		if spaces > 0 {
			w := ansi.StringWidth(string(word))
			if row+w+spaces > width {
				rows++
				row = 0
			}
			row += w + spaces
			spaces, word = 0, nil
			continue
		}
		last := ansi.StringWidth(string(word[len(word)-1]))
		if w := ansi.StringWidth(string(word)); w+last > width {
			if row > 0 {
				rows++
			}
			row, word = w, nil
		}
	}
	// The last row holds the rest and one space, for the cursor at its end.
	if row+ansi.StringWidth(string(word))+spaces >= width {
		rows++
	}
	return rows
}
