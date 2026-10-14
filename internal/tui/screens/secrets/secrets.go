// Package secrets implements the "Secrets" right-pane: a list of configured
// secret sources that can be decrypted, copied from and edited in place.
//
// Values are masked by default, only the selected row can be revealed, and the
// decrypted fields are dropped as soon as the user leaves the item.
package secrets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lukaszgard/den/internal/secrets"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
)

// clipboardClearAfter is how long a copied value stays on the clipboard.
const clipboardClearAfter = 30 * time.Second

// awsPollEvery is how often the pane, while it is on screen, asks whether the
// AWS logins changed. A login made outside den (`aws sso login` in another
// terminal) or a token running out is not announced by any event.
const awsPollEvery = 5 * time.Second

// mask stands in for every hidden value. Fixed width, so the length of a
// secret is not given away either.
const mask = "••••••••"

// boxHeight is the bordered details box under the item list: room for a
// description, four details and two toggle rows.
const boxHeight = 9

var (
	colorAccent = lipgloss.Color("#E9C46A")
	colorDim    = lipgloss.Color("#626262")
	colorMuted  = lipgloss.Color("#9B9B9B")
	colorOK     = lipgloss.Color("#04B575")
	colorErr    = lipgloss.Color("#FF5F87")
	colorHead   = lipgloss.Color("#6C91BF")
)

// Messages are unexported: the app forwards everything it does not handle
// itself to this pane, which ignores what is not its own.
type (
	readMsg struct {
		seq    int
		item   int // index into Model.items
		fields []secrets.Field
		err    error
	}
	editedMsg struct {
		name string
		err  error
	}
	loginDoneMsg struct {
		name string
		err  error
	}
	listMsg struct {
		seq     int
		item    int
		entries []string
		err     error
	}
	// awsRecheckedMsg is the outcome of one pass of RecheckAWS. statuses is
	// nil when the logins had not changed, so nothing was probed.
	awsRecheckedMsg struct {
		gen      uint64
		stamp    string
		statuses map[int]secrets.Status // by index into Model.items
	}
	// viaTerminal marks a value set on the clipboard through the terminal
	// (OSC 52): den cannot read that clipboard back.
	copiedMsg struct {
		key, sum    string
		viaTerminal bool
	}
	clearClipMsg struct {
		sum         string
		viaTerminal bool
	}
	clipClearedMsg struct{}
)

// Model is the Secrets right-pane model.
type Model struct {
	items    []secrets.Item
	statuses []secrets.Status
	// readFailed marks items whose last decrypt failed. The probe cannot see
	// every failure (a bad KMS grant, a wrong key), so the list learns from
	// the real attempt; the next successful decrypt clears it.
	readFailed map[int]bool
	cursor     int // index into visibleItems()
	width      int
	height     int

	// Search. itemQuery filters the list of files, fieldQuery the keys of
	// the open file; typing is true while the search bar takes keystrokes.
	typing     bool
	itemQuery  string
	fieldQuery string

	// The open item. seq tags each read so a result that arrives after the
	// user moved on is thrown away instead of shown.
	open      bool
	seq       int
	loading   bool
	readErr   string
	fields    []secrets.Field
	fieldCur  int  // index into visibleFields()
	revealed  bool // the selected row is shown in clear
	revealAll bool // every row is shown in clear (auto_reveal, or a)
	tree      bool // values nest under their parent keys (view: tree, or t)

	// Browsing a Browser item (a whole KeePass database): entries lists its
	// entry paths, entry is the one open ("" while the list shows). The
	// password is kept in sessionPW while the database is open, so each entry
	// does not ask again; Hide clears it.
	browsing   bool
	entries    []string
	entryCur   int // index into visibleEntries()
	entryQuery string
	entry      string
	listNote   string // why the entry list is incomplete, if it is
	sessionPW  []byte

	// Password prompt for an Unlocker (KeePassXC). The typed text lives only in
	// pwInput and is reset on submit, cancel and Hide.
	prompting bool
	pwInput   textinput.Model
	promptErr string

	// Following the AWS logins. awsStamp is their state when the items were
	// last probed; rechecking and recheckAgain keep RecheckAWS to one pass at
	// a time, however many events ask for one. gen tells a pass started for
	// this pane from one started for the pane a config reload replaced.
	aws          *secrets.AWSLogins
	awsStamp     string
	gen          uint64
	rechecking   bool
	recheckAgain bool
	nextAWSPoll  time.Time
}

// panes numbers the panes built, for Model.gen.
var panes atomic.Uint64

// New creates the pane and probes every item once.
func New(items []secrets.Item, width, height int) Model {
	m := Model{items: items, width: width, height: height, readFailed: map[int]bool{}, pwInput: newPasswordInput(), gen: panes.Add(1)}
	m.statuses = probeAll(items)
	return m
}

// NewWithAWS is New for items that share logins, so that RecheckAWS can tell
// when an AWS login changed what they can read.
func NewWithAWS(items []secrets.Item, logins *secrets.AWSLogins, width, height int) Model {
	// Taken before the probe: a login in between costs one more pass instead
	// of leaving a stale row.
	stamp := logins.Stamp(time.Now())
	m := New(items, width, height)
	m.aws, m.awsStamp = logins, stamp
	return m
}

// RecheckAWS probes the items read through AWS again, off the UI goroutine,
// if the AWS logins changed since they were last probed. It is cheap to call
// on every AWS event and on a timer: when nothing changed it costs a look at
// a few file times, whatever the number of secrets. Only one pass runs at a
// time and it probes one item after another, so a long list never means a
// goroutine, or a parsed file, per secret.
func (m Model) RecheckAWS() (Model, tea.Cmd) {
	if m.aws == nil {
		return m, nil
	}
	if m.rechecking {
		m.recheckAgain = true
		return m, nil
	}
	m.rechecking = true
	gen, last, logins, items := m.gen, m.awsStamp, m.aws, m.items
	return m, func() tea.Msg {
		msg := awsRecheckedMsg{gen: gen, stamp: logins.Stamp(time.Now())}
		if msg.stamp == last {
			return msg
		}
		msg.statuses = map[int]secrets.Status{}
		for i, it := range items {
			if u, ok := it.(secrets.AWSUser); ok && u.UsesAWS() {
				msg.statuses[i] = it.Probe()
			}
		}
		return msg
	}
}

func newPasswordInput() textinput.Model {
	in := textinput.New()
	// Bubbles v2's own styles colour the prompt and the cursor a light grey,
	// which cannot be seen on a light terminal. The cursor keeps blinking.
	in.SetStyles(components.PlainInputStyles())
	in.EchoMode = textinput.EchoPassword
	in.EchoCharacter = '•'
	in.Prompt = "› "
	in.CharLimit = 1024
	return in
}

// SetSize resizes the pane without losing its state.
func (m Model) SetSize(width, height int) Model {
	m.width, m.height = width, height
	return m
}

// Searching reports whether the search bar is taking keystrokes, so the app
// must not act on them globally.
func (m Model) Searching() bool { return m.typing || m.prompting }

// Shortcuts lists the keys of the pane for the shortcuts pop-up (h or ?). How
// secrets are configured is in their Help topics (docs/help) and docs/secrets,
// not here: the pop-up has to fit the terminal.
func (m Model) Shortcuts() []components.ShortcutGroup {
	return []components.ShortcutGroup{
		{Title: "Secrets", Keys: []components.Shortcut{
			{Keys: "↑ ↓", Text: "Select a secret or a key (or k, j)"},
			{Keys: "enter", Text: "Open it: list its keys, values masked"},
			{Keys: "/", Text: "Filter secrets, or an open one's keys"},
			{Keys: "esc", Text: "Clear the filter, then close the secret"},
		}},
		{Title: "Values", Note: "Dropped when the panel loses focus.", Keys: []components.Shortcut{
			{Keys: "r", Text: "Reveal or hide the selected value"},
			{Keys: "a", Text: "Reveal or hide every value"},
			{Keys: "t", Text: "Nested keys as a tree, or dotted"},
			{Keys: "y", Text: "Copy the value (cleared after 30 s)"},
			{Keys: "e", Text: "Edit a SOPS file in $EDITOR"},
		}},
		{Title: "Access", Keys: []components.Shortcut{
			{Keys: "L", Text: "Run the secret's login command"},
			{Keys: "l", Text: "Forget remembered KeePassXC passwords"},
		}},
	}
}

// Busy reports whether esc belongs to the pane (clearing a filter or closing
// an open item) rather than moving focus back to the menu.
func (m Model) Busy() bool {
	return m.open || m.typing || m.itemQuery != ""
}

// Hide closes the open item and drops its decrypted fields. The app calls it
// whenever the pane loses focus, so plaintext never lingers off screen.
func (m Model) Hide() Model {
	m.typing = false
	if m.prompting {
		m.prompting = false
		m.pwInput.Reset()
		m.pwInput.Blur()
	}
	m.promptErr = ""
	m.clearSession()
	if !m.open {
		return m
	}
	m.open = false
	m.loading = false
	m.readErr = ""
	m.fields = nil
	m.fieldCur = 0
	m.fieldQuery = ""
	m.revealed = false
	m.revealAll = false
	m.tree = false
	m.browsing = false
	m.entries = nil
	m.entryCur = 0
	m.entryQuery = ""
	m.entry = ""
	m.listNote = ""
	m.seq++
	return m
}

// clearSession wipes the password kept while browsing a database. The bytes
// are shared by every copy of the model, so clearing them clears them all.
func (m *Model) clearSession() {
	clear(m.sessionPW)
	m.sessionPW = nil
}

// listing reports whether the open item shows its entry list.
func (m Model) listing() bool { return m.open && m.browsing && m.entry == "" }

// visibleEntries returns the indexes of the entries matching entryQuery.
func (m Model) visibleEntries() []int {
	q := strings.ToLower(m.entryQuery)
	var out []int
	for i, e := range m.entries {
		if q == "" || strings.Contains(strings.ToLower(e), q) {
			out = append(out, i)
		}
	}
	return out
}

// status is what the list shows for item i: the probe, unless the probe
// passed but the last real decrypt failed.
func (m Model) status(i int) secrets.Status {
	st := m.statuses[i]
	if st.Ready && m.readFailed[i] {
		return secrets.Status{Reason: "decrypt failed — enter for details"}
	}
	return st
}

func probeAll(items []secrets.Item) []secrets.Status {
	out := make([]secrets.Status, len(items))
	for i, it := range items {
		out[i] = it.Probe()
	}
	return out
}

// visibleItems returns the indexes of the items matching itemQuery, by name,
// description or location.
func (m Model) visibleItems() []int {
	q := strings.ToLower(m.itemQuery)
	var out []int
	for i, it := range m.items {
		if q == "" ||
			strings.Contains(strings.ToLower(it.Name()), q) ||
			strings.Contains(strings.ToLower(it.Description()), q) ||
			strings.Contains(strings.ToLower(it.Location()), q) {
			out = append(out, i)
		}
	}
	return out
}

// visibleFields returns the indexes of the fields matching fieldQuery. Keys
// only: matching values would tell which masked rows contain the query.
func (m Model) visibleFields() []int {
	q := strings.ToLower(m.fieldQuery)
	var out []int
	for i, f := range m.fields {
		if q == "" || strings.Contains(strings.ToLower(f.Key), q) {
			out = append(out, i)
		}
	}
	return out
}

// selectedIndex is the index into m.items of the highlighted item.
func (m Model) selectedIndex() (int, bool) {
	vis := m.visibleItems()
	if m.cursor < 0 || m.cursor >= len(vis) {
		return 0, false
	}
	return vis[m.cursor], true
}

func (m Model) selected() (secrets.Item, bool) {
	i, ok := m.selectedIndex()
	if !ok {
		return nil, false
	}
	return m.items[i], true
}

func (m Model) selectedField() (secrets.Field, bool) {
	vis := m.visibleFields()
	if m.fieldCur < 0 || m.fieldCur >= len(vis) {
		return secrets.Field{}, false
	}
	return m.fields[vis[m.fieldCur]], true
}

func (m Model) Update(msg tea.Msg, focused bool) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case listMsg:
		badPassword := errors.Is(msg.err, secrets.ErrBadPassword)
		if !badPassword {
			m.readFailed[msg.item] = msg.err != nil
		}
		if !m.listing() || msg.seq != m.seq {
			return m, nil
		}
		m.loading = false
		if badPassword {
			m.clearSession()
			m.promptErr = promptError(msg.err)
			return m.startPrompt()
		}
		m.listNote = ""
		if msg.err != nil {
			if len(msg.entries) == 0 {
				m.readErr = msg.err.Error()
				return m, nil
			}
			m.listNote = msg.err.Error() // a partial list is still worth showing
		}
		m.entries = msg.entries
		m.entryCur = 0
		return m, nil

	case readMsg:
		badPassword := errors.Is(msg.err, secrets.ErrBadPassword)
		// Record the outcome even when the user has moved on: it is still
		// true of the file. A mistyped password says nothing about the
		// secret, so it does not count as a failure.
		if !badPassword {
			m.readFailed[msg.item] = msg.err != nil
		}
		if !m.open || msg.seq != m.seq {
			return m, nil
		}
		m.loading = false
		if badPassword {
			m.clearSession()
			m.promptErr = promptError(msg.err)
			return m.startPrompt()
		}
		if msg.err != nil {
			m.readErr = msg.err.Error()
			return m, nil
		}
		m.fields = msg.fields
		if m.fieldCur >= len(m.visibleFields()) {
			m.fieldCur = 0
		}
		return m, nil

	case editedMsg:
		m.statuses = probeAll(m.items)
		it, ok := m.itemNamed(msg.name)
		if !ok {
			return m, nil
		}
		ed, ok := it.(secrets.Editor)
		if !ok {
			return m, nil
		}
		changed, err := ed.EditResult(msg.err)
		var cmds []tea.Cmd
		switch {
		case err != nil:
			cmds = append(cmds, flash(err.Error(), true))
		case changed:
			cmds = append(cmds, flash(msg.name+" saved", false))
		default:
			cmds = append(cmds, flash("no changes", false))
		}
		// Show what was just saved rather than the stale fields.
		if changed && m.open {
			if i, ok := m.selectedIndex(); ok && m.items[i].Name() == msg.name {
				m = m.startRead()
				cmds = append(cmds, m.readCmd(i))
			}
		}
		return m, tea.Batch(cmds...)

	case loginDoneMsg:
		m.statuses = probeAll(m.items)
		if msg.err != nil {
			return m, flash("login failed: "+msg.err.Error(), true)
		}
		for i, it := range m.items {
			if it.Name() == msg.name {
				delete(m.readFailed, i)
			}
		}
		cmds := []tea.Cmd{flash(msg.name+": logged in", false)}
		// Retry what failed for want of a token.
		if cur, ok := m.selected(); ok && m.open && cur.Name() == msg.name && m.readErr != "" {
			var c tea.Cmd
			m, c = m.fetch(nil)
			cmds = append(cmds, c)
		}
		return m, tea.Batch(cmds...)

	case awsRecheckedMsg:
		if msg.gen != m.gen {
			return m, nil // for the pane a config reload has replaced
		}
		m.rechecking = false
		m.awsStamp = msg.stamp
		for i, st := range msg.statuses {
			m.statuses[i] = st
			// A decrypt that failed under the old login says nothing now.
			if st.Ready {
				delete(m.readFailed, i)
			}
		}
		if m.recheckAgain {
			m.recheckAgain = false
			return m.RecheckAWS()
		}
		return m, nil

	case msgs.TickMsg:
		// The app sends ticks only while the pane is on screen.
		if msg.Time.Before(m.nextAWSPoll) {
			return m, nil
		}
		m.nextAWSPoll = msg.Time.Add(awsPollEvery)
		return m.RecheckAWS()

	case copiedMsg:
		clear := clearClipMsg{sum: msg.sum, viaTerminal: msg.viaTerminal}
		return m, tea.Batch(
			flash(fmt.Sprintf("copied %s — clipboard clears in %s", msg.key, clipboardClearAfter), false),
			tea.Tick(clipboardClearAfter, func(time.Time) tea.Msg { return clear }),
		)

	case clearClipMsg:
		return m, clearClipboard(msg)

	case tea.KeyPressMsg:
		if !focused {
			return m, nil
		}
		return m.handleKey(msg)

	// Pasted text goes where typed text would: into the password, or into
	// the filter.
	case tea.PasteMsg:
		switch {
		case !focused:
		case m.prompting:
			var cmd tea.Cmd
			m.pwInput, cmd = m.pwInput.Update(msg)
			return m, cmd
		case m.typing:
			query, cursor, _ := m.searchTarget()
			*query += msg.Content
			*cursor = 0
			m.revealed = false
		}
		return m, nil

	default:
		if m.prompting { // cursor blink
			var cmd tea.Cmd
			m.pwInput, cmd = m.pwInput.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	// While typing every key is text.
	if m.prompting {
		return m.handlePromptKey(msg)
	}
	if m.typing {
		return m.handleSearchKey(msg)
	}
	switch msg.String() {
	case "/":
		m.typing = true
		return m, nil
	case "l":
		m.clearSession()
		return m, m.lockAll()
	case "L":
		return m, m.loginCmd()
	}
	if m.listing() {
		return m.handleEntriesKey(msg)
	}
	if m.open {
		return m.handleOpenKey(msg)
	}

	switch msg.String() {
	case "esc":
		m.itemQuery = ""
		m.cursor = 0
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.visibleItems())-1 {
			m.cursor++
		}
	case "enter":
		return m.openSelected()
	case "e":
		return m, m.editCmd()
	}
	return m, nil
}

// Click moves the cursor of the list on screen — the files, a database's
// entries or the open file's values — to the row the zone id marks. Not while
// a password is asked for, the search box has the keyboard, or a read is in
// flight or failed: what the click landed on is about to change.
func (m Model) Click(id string) Model {
	if m.typing || m.prompting || m.loading || m.readErr != "" {
		return m
	}
	switch {
	case m.listing():
		if i, ok := components.ZoneIndex(id, "entry"); ok && i < len(m.visibleEntries()) {
			m.entryCur = i
		}
	case m.open:
		if i, ok := components.ZoneIndex(id, "field"); ok && i < len(m.visibleFields()) && i != m.fieldCur {
			m.fieldCur, m.revealed = i, false
		}
	default:
		if i, ok := components.ZoneIndex(id, "item"); ok && i < len(m.visibleItems()) {
			m.cursor = i
		}
	}
	return m
}

// searchTarget is the query, cursor and row count of whichever list is on
// screen, which the search box edits.
func (m *Model) searchTarget() (query *string, cursor *int, count int) {
	switch {
	case m.listing():
		return &m.entryQuery, &m.entryCur, len(m.visibleEntries())
	case m.open:
		return &m.fieldQuery, &m.fieldCur, len(m.visibleFields())
	}
	return &m.itemQuery, &m.cursor, len(m.visibleItems())
}

// handleSearchKey edits the query of whichever list is on screen. Only the
// arrow keys navigate, so j and k can be typed.
func (m Model) handleSearchKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	query, cursor, count := m.searchTarget()
	switch msg.Code {
	case tea.KeyEscape:
		m.typing = false
		*query = ""
		*cursor = 0
	case tea.KeyEnter:
		// Keep the filter: the actions then work on the rows it left.
		m.typing = false
		switch {
		case m.listing():
			return m.openEntry()
		case !m.open:
			return m.openSelected()
		}
	case tea.KeyBackspace:
		if *query == "" {
			m.typing = false
			break
		}
		r := []rune(*query)
		*query = string(r[:len(r)-1])
		*cursor = 0
	case tea.KeyUp:
		if *cursor > 0 {
			*cursor--
		}
	case tea.KeyDown:
		if *cursor < count-1 {
			*cursor++
		}
	default:
		if msg.Text != "" {
			*query += msg.Text
			*cursor = 0
		}
	}
	m.revealed = false
	return m, nil
}

// openSelected decrypts the highlighted item, if it is ready.
func (m Model) openSelected() (Model, tea.Cmd) {
	i, ok := m.selectedIndex()
	if !ok {
		return m, nil
	}
	it := m.items[i]
	m.statuses[i] = it.Probe()
	if st := m.statuses[i]; !st.Ready {
		return m, flash(it.Name()+": "+st.Reason, true)
	}
	m.open = true
	m.fieldCur = 0
	m.fieldQuery = ""
	m.revealAll = it.AutoReveal()
	m.tree = false
	if v, ok := it.(secrets.Viewer); ok {
		m.tree = v.TreeView()
	}
	m.promptErr = ""
	if b, ok := it.(secrets.Browser); ok && b.Browsing() {
		m.browsing = true
		m.entry = ""
		m.entries = nil
		m.entryCur = 0
		m.entryQuery = ""
	}
	if u, ok := it.(secrets.Unlocker); ok && u.NeedsPassword() {
		return m.startPrompt()
	}
	return m.fetch(nil)
}

// fetch loads what the open item shows next: its entry list or the open
// entry when browsing, the item itself otherwise. pw is a freshly typed
// password, or nil to use the session's or the remembered one.
func (m Model) fetch(pw []byte) (Model, tea.Cmd) {
	i, ok := m.selectedIndex()
	if !ok {
		clear(pw)
		return m.Hide(), nil
	}
	if !m.browsing {
		m = m.startRead()
		if pw != nil {
			return m, m.readWithPasswordCmd(i, pw)
		}
		return m, m.readCmd(i)
	}
	if pw != nil {
		m.clearSession()
		m.sessionPW = pw // owned by the model now; Hide clears it
	}
	use := bytes.Clone(m.sessionPW) // the command clears its own copy
	b := m.items[i].(secrets.Browser)
	m = m.startRead()
	seq, entry := m.seq, m.entry
	if entry == "" {
		return m, func() tea.Msg {
			defer clear(use)
			entries, err := b.ListEntries(context.Background(), use)
			return listMsg{seq: seq, item: i, entries: entries, err: err}
		}
	}
	return m, func() tea.Msg {
		defer clear(use)
		fields, err := b.ReadEntry(context.Background(), entry, use)
		return readMsg{seq: seq, item: i, fields: fields, err: err}
	}
}

// openEntry opens the highlighted entry of the list.
func (m Model) openEntry() (Model, tea.Cmd) {
	vis := m.visibleEntries()
	if m.entryCur < 0 || m.entryCur >= len(vis) {
		return m, nil
	}
	m.entry = m.entries[vis[m.entryCur]]
	m.fieldCur = 0
	m.fieldQuery = ""
	if i, ok := m.selectedIndex(); ok {
		m.revealAll = m.items[i].AutoReveal()
	}
	return m.fetch(nil)
}

// handleEntriesKey drives the entry list of a database being browsed.
func (m Model) handleEntriesKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.entryQuery != "" {
			m.entryQuery = ""
			m.entryCur = 0
			return m, nil
		}
		return m.Hide(), nil
	case "backspace":
		return m.Hide(), nil
	case "up", "k":
		if m.entryCur > 0 {
			m.entryCur--
		}
	case "down", "j":
		if m.entryCur < len(m.visibleEntries())-1 {
			m.entryCur++
		}
	case "enter":
		return m.openEntry()
	}
	return m, nil
}

// closeEntry goes back from an entry's fields to the entry list.
func (m Model) closeEntry() Model {
	m.entry = ""
	m.fields = nil
	m.fieldCur = 0
	m.fieldQuery = ""
	m.revealed = false
	m.readErr = ""
	m.loading = false
	m.seq++
	return m
}

// startPrompt asks for the open item's password.
func (m Model) startPrompt() (Model, tea.Cmd) {
	m.prompting = true
	m.loading = false
	m.pwInput.Reset()
	return m, m.pwInput.Focus()
}

// handlePromptKey types into the password field. enter reads with it, esc
// closes the item; the typed text is dropped either way.
func (m Model) handlePromptKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyEscape:
		return m.Hide(), nil
	case tea.KeyEnter:
		pw := []byte(m.pwInput.Value())
		m.pwInput.Reset()
		m.pwInput.Blur()
		m.prompting = false
		m.promptErr = ""
		return m.fetch(pw)
	}
	var cmd tea.Cmd
	m.pwInput, cmd = m.pwInput.Update(msg)
	return m, cmd
}

// promptError is shown above the password field when asking again: nothing
// when the remembered password merely ran out, a hint when the typed one was
// wrong.
func promptError(err error) string {
	if errors.Is(err, secrets.ErrLocked) {
		return ""
	}
	return "wrong password — try again"
}

// readWithPasswordCmd reads item i with pw and clears pw afterwards. Only the
// provider keeps a copy, and only if the item is set to remember it.
func (m Model) readWithPasswordCmd(i int, pw []byte) tea.Cmd {
	seq, it := m.seq, m.items[i]
	return func() tea.Msg {
		defer clear(pw)
		u, ok := it.(secrets.Unlocker)
		if !ok {
			return readMsg{seq: seq, item: i, err: errors.New("no password needed")}
		}
		fields, err := u.ReadWithPassword(context.Background(), pw)
		return readMsg{seq: seq, item: i, fields: fields, err: err}
	}
}

// loginCmd hands the terminal to the selected item's login command, such as
// `vault login -method=oidc`, which opens the browser for OIDC.
func (m Model) loginCmd() tea.Cmd {
	it, ok := m.selected()
	if !ok {
		return nil
	}
	lc, ok := it.(secrets.LoginCommander)
	if !ok {
		return nil
	}
	cmd := lc.LoginCmd()
	if cmd == nil {
		return flash(it.Name()+": no login command configured (login: in den.yaml)", true)
	}
	name := it.Name()
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return loginDoneMsg{name: name, err: err} })
}

// lockAll makes every item that remembers passwords forget them.
func (m Model) lockAll() tea.Cmd {
	locked := false
	for _, it := range m.items {
		if l, ok := it.(secrets.Locker); ok {
			l.Lock()
			locked = true
		}
	}
	if !locked {
		return nil
	}
	return flash("remembered passwords forgotten", false)
}

func (m Model) handleOpenKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// A kept filter is cleared first; the next esc closes the file.
		if m.fieldQuery != "" {
			m.fieldQuery = ""
			m.fieldCur = 0
			m.revealed = false
			return m, nil
		}
		if m.browsing {
			return m.closeEntry(), nil
		}
		return m.Hide(), nil
	case "backspace":
		if m.browsing {
			return m.closeEntry(), nil
		}
		return m.Hide(), nil
	case "up", "k":
		if m.fieldCur > 0 {
			m.fieldCur--
			m.revealed = false
		}
	case "down", "j":
		if m.fieldCur < len(m.visibleFields())-1 {
			m.fieldCur++
			m.revealed = false
		}
	case "r":
		if _, ok := m.selectedField(); ok {
			m.revealed = !m.revealed
		}
	case "a":
		m.revealAll = !m.revealAll
		m.revealed = false
	case "t":
		// The cursor counts values in both views, so the same one stays selected.
		m.tree = !m.tree
	case "y":
		if f, ok := m.selectedField(); ok {
			return m, copyValue(f.Key, f.Value)
		}
	case "e":
		return m, m.editCmd()
	}
	return m, nil
}

// startRead resets the open item for a fresh read and bumps seq so any read
// still in flight is ignored when it lands. revealAll and tree survive, so a
// file re-read after an edit keeps how it was being shown.
func (m Model) startRead() Model {
	m.seq++
	m.loading = true
	m.readErr = ""
	m.fields = nil
	m.revealed = false
	return m
}

func (m Model) readCmd(i int) tea.Cmd {
	seq, it := m.seq, m.items[i]
	return func() tea.Msg {
		fields, err := it.Read(context.Background())
		return readMsg{seq: seq, item: i, fields: fields, err: err}
	}
}

func (m Model) editCmd() tea.Cmd {
	it, ok := m.selected()
	if !ok {
		return nil
	}
	ed, ok := it.(secrets.Editor)
	if !ok {
		return flash(it.Name()+": editing is not supported", true)
	}
	if st := it.Probe(); !st.Ready {
		return flash(it.Name()+": "+st.Reason, true)
	}
	name := it.Name()
	return tea.ExecProcess(ed.EditCmd(), func(err error) tea.Msg {
		return editedMsg{name: name, err: err}
	})
}

func (m Model) itemNamed(name string) (secrets.Item, bool) {
	for _, it := range m.items {
		if it.Name() == name {
			return it, true
		}
	}
	return nil, false
}

// copyValue puts value on the clipboard. Only its hash travels on in the
// message, so the value itself is not kept alive for the clear timer.
func copyValue(key, value string) tea.Cmd {
	sum := digest(value)
	return components.CopyThen(value, func(viaTerminal bool) tea.Msg {
		return copiedMsg{key: key, sum: sum, viaTerminal: viaTerminal}
	})
}

// clearClipboard empties the clipboard, unless the user has since copied
// something else, which is theirs to keep. A value that went through the
// terminal cannot be checked, since the terminal's clipboard cannot be read:
// the secret should not outlive its time, so that one is emptied the same way
// it was set.
func clearClipboard(c clearClipMsg) tea.Cmd {
	if c.viaTerminal {
		return tea.Batch(
			tea.SetClipboard(""),
			func() tea.Msg { return clipClearedMsg{} },
		)
	}
	sum := c.sum
	return func() tea.Msg {
		cur, err := clipboard.ReadAll()
		if err != nil || digest(cur) != sum {
			return nil
		}
		_ = components.WriteClipboard("")
		return clipClearedMsg{}
	}
}

func digest(s string) string {
	h := sha256.Sum256([]byte(s))
	return string(h[:])
}

func flash(text string, warn bool) tea.Cmd {
	return func() tea.Msg {
		m := msgs.StatusFlashMsg{Text: text, Warn: warn}
		if warn {
			m.Hold = 3 * time.Second
		}
		return m
	}
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m Model) View(width, height int, focused bool) string {
	if m.open {
		return m.renderOpen(width, height, focused)
	}
	listHeight := max(height-boxHeight-1, 1)
	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderList(width, listHeight, focused),
		m.renderBox(width, boxHeight),
		components.HintLine(width, focused, hintKey("↑↓", "navigate"), hintKey("enter", "view"), hintKey("/", "filter"),
			hintKey("e", "edit"), hintKey("L", "login"), hintKey("R", "reload"), hintKey("h", "help")),
	)
}

func title(s string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Padding(0, 2).Render(s)
}

func (m Model) renderList(width, height int, focused bool) string {
	rows := []string{"", title("Secrets"), ""}

	vis := m.visibleItems()
	switch {
	case len(m.items) == 0:
		// One row per line: the padding below counts rows.
		rows = append(rows, strings.Split(lipgloss.NewStyle().Foreground(colorDim).Padding(0, 3).
			Render("No secrets configured.\n\nAdd a secrets: section\nto your config file (see the Help panel)."), "\n")...)
	case len(vis) == 0:
		rows = append(rows, lipgloss.NewStyle().Foreground(colorDim).Padding(0, 3).
			Render("No secrets match your search."))
	}

	// Size the name column to the longest name, so the status sits next to it.
	maxName := 0
	for _, it := range m.items {
		maxName = max(maxName, len([]rune(it.Name())))
	}
	maxName = max(min(maxName, width-32), 10)
	group := ""
	for row, i := range vis {
		it := m.items[i]
		if p := it.Provider(); p != group {
			group = p
			rows = append(rows, lipgloss.NewStyle().Foreground(colorHead).Padding(0, 2).Render(strings.ToUpper(p)))
		}
		st := m.status(i)
		dot, label := lipgloss.NewStyle().Foreground(colorOK).Render("●"), lipgloss.NewStyle().Foreground(colorOK).Render("ready")
		if !st.Ready {
			dot = lipgloss.NewStyle().Foreground(colorErr).Render("✕")
			label = lipgloss.NewStyle().Foreground(colorErr).Render(st.Reason)
		}
		line := fmt.Sprintf("  %s %-*s  %s", dot, maxName, truncate(it.Name(), maxName), label)
		if focused && row == m.cursor {
			line = selectedStyle(width).Render(line)
		}
		rows = append(rows, components.Zone(components.ZoneID("item", row), line))
	}
	// The search bar is pinned to the last line.
	bodyH := max(height-1, 0)
	for len(rows) < bodyH {
		rows = append(rows, "")
	}
	rows = append(rows[:bodyH], m.renderSearch(m.itemQuery, len(vis), focused))
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// renderSearch is the bar under a list: the query being typed, a filter that
// is kept, or the prompt to start one.
func (m Model) renderSearch(query string, count int, focused bool) string {
	dim := lipgloss.NewStyle().Foreground(colorDim)
	white := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFDF5"))
	results := dim.Render(fmt.Sprintf("(%d results)", count))
	switch {
	case !focused, m.prompting:
		return ""
	case m.typing:
		return "  " + dim.Render("/ ") + white.Render(query) + "█  " + results
	case query != "":
		return "  " + dim.Render("/ ") + white.Render(query) + "  " + results + "  " + components.Hints(hintKey("esc", "clear"))
	}
	return "  " + components.Hints(hintKey("/", "filter"))
}

func (m Model) renderBox(width, height int) string {
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#333355"))
	innerW, innerH := max(width-2, 4), max(height-2, 1)

	var lines []string
	if it, ok := m.selected(); ok {
		if d := it.Description(); d != "" {
			lines = append(lines, lipgloss.NewStyle().Foreground(colorMuted).Render(truncate(d, innerW)))
		}
		for _, d := range it.Details() {
			label := lipgloss.NewStyle().Foreground(colorHead).Render(fmt.Sprintf("%-12s", d.Label))
			lines = append(lines, label+" "+truncate(d.Value, innerW-13))
		}
		// Same dot as the VPN and Tunnel toggles: green on, grey off.
		lines = append(lines, components.ToggleDot(it.AutoReveal())+" Auto reveal")
		if t, ok := it.(secrets.Toggler); ok {
			for _, tg := range t.Toggles() {
				lines = append(lines, components.ToggleDot(tg.On)+" "+tg.Label)
			}
		}
	}
	for len(lines) < innerH {
		lines = append(lines, "")
	}
	return border.Render(lipgloss.NewStyle().Width(innerW).Height(innerH).Render(strings.Join(lines[:innerH], "\n")))
}

func (m Model) renderOpen(width, height int, focused bool) string {
	it, _ := m.selected()
	head := title(it.Name()) + lipgloss.NewStyle().Foreground(colorDim).Render(it.Provider())
	location := it.Location()
	if m.browsing && m.entry != "" {
		location = m.entry
	}
	loc := lipgloss.NewStyle().Foreground(colorDim).Padding(0, 2).Render(truncate(location, width-4))
	rows := []string{"", head, loc}
	if c := m.countLine(); c != "" {
		rows = append(rows, lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 2).Render(c))
	}
	rows = append(rows, "")

	// Header above; search bar and hint below.
	visible := max(height-len(rows)-2, 1)
	vis := m.visibleFields()

	switch {
	case m.prompting:
		prompt := "Password"
		if u, ok := it.(secrets.Unlocker); ok {
			prompt = u.PasswordPrompt()
		}
		rows = append(rows,
			lipgloss.NewStyle().Foreground(colorHead).Padding(0, 2).Render(prompt),
			"  "+m.pwInput.View())
		if m.promptErr != "" {
			rows = append(rows, "", lipgloss.NewStyle().Foreground(colorErr).Padding(0, 2).Render(m.promptErr))
		}
	case m.loading:
		rows = append(rows, lipgloss.NewStyle().Foreground(colorAccent).Padding(0, 2).Render("decrypting…"))
	case m.readErr != "":
		rows = append(rows, lipgloss.NewStyle().Foreground(colorErr).Padding(0, 2).Width(width-2).Render(m.readErr))
	case m.listing():
		rows = append(rows, m.renderEntries(width, visible, focused)...)
	case len(m.fields) == 0:
		rows = append(rows, lipgloss.NewStyle().Foreground(colorDim).Padding(0, 2).Render("(empty)"))
	case len(vis) == 0:
		rows = append(rows, lipgloss.NewStyle().Foreground(colorDim).Padding(0, 2).Render("No keys match your search."))
	case m.tree:
		rows = append(rows, m.renderTree(vis, width, visible, focused)...)
	default:
		rows = append(rows, m.renderFlat(vis, width, visible, focused)...)
	}
	bodyH := max(height-2, 0)
	for len(rows) < bodyH {
		rows = append(rows, "")
	}
	query, count := m.fieldQuery, len(vis)
	if m.listing() {
		query, count = m.entryQuery, len(m.visibleEntries())
	}
	rows = append(rows[:bodyH], m.renderSearch(query, count, focused))
	view := hintKey("t", "tree")
	if m.tree {
		view = hintKey("t", "flat")
	}
	// t goes last: a narrow pane cuts the hint from the right, and t is also in
	// the help bar and on h.
	hint := []components.HelpKey{hintKey("↑↓", "navigate"), hintKey("r", "reveal"), hintKey("a", "reveal all"), hintKey("y", "copy"),
		hintKey("/", "filter"), hintKey("e", "edit"), hintKey("esc", "close"), hintKey("h", "help"), view}
	switch {
	case m.prompting:
		hint = []components.HelpKey{hintKey("enter", "unlock"), hintKey("esc", "cancel")}
	case m.listing():
		hint = []components.HelpKey{hintKey("↑↓", "navigate"), hintKey("enter", "open entry"), hintKey("/", "filter"),
			hintKey("l", "lock"), hintKey("esc", "close"), hintKey("h", "help")}
	case m.browsing:
		hint = []components.HelpKey{hintKey("↑↓", "navigate"), hintKey("r", "reveal"), hintKey("a", "reveal all"), hintKey("y", "copy"),
			hintKey("/", "filter"), hintKey("esc", "back to entries"), hintKey("h", "help"), view}
	}
	return lipgloss.JoinVertical(lipgloss.Left, append(rows, components.HintLine(width, focused, hint...))...)
}

// renderFlat lists one value per row under its dotted key (db.password).
func (m Model) renderFlat(vis []int, width, visible int, focused bool) []string {
	keyW := 0
	for _, f := range m.fields {
		keyW = max(keyW, len([]rune(f.Key)))
	}
	keyW = min(keyW, width/2)
	start := max(m.fieldCur-visible+1, 0)
	end := min(start+visible, len(vis))
	var rows []string
	for i := start; i < end; i++ {
		f := m.fields[vis[i]]
		line := fmt.Sprintf("  %-*s  %s", keyW, truncate(f.Key, keyW), m.shownValue(i, f.Value, max(width-keyW-8, 8)))
		if focused && i == m.fieldCur {
			line = selectedStyle(width).Render(line)
		}
		rows = append(rows, components.Zone(components.ZoneID("field", i), line))
	}
	return rows
}

// treeRow is one line of the tree view: a heading (a parent key, or "-" for
// an array element that holds keys) or a value. vis indexes visibleFields()
// for a value and is -1 for a heading; parent is the row of the heading the
// row sits under, -1 at the top.
type treeRow struct {
	depth  int
	label  string
	vis    int
	parent int
}

// treeRows lays the visible fields out as the document they came from. The
// headings are worked out from the visible fields alone, so a filtered view
// still shows each match under its parent keys.
func treeRows(fields []secrets.Field, vis []int) []treeRow {
	var rows []treeRow
	var prev []secrets.PathSeg // the previous value's ancestors
	var heads []int            // the row of each heading in prev
	for vi, fi := range vis {
		segs := fields[fi].Segments()
		anc := segs[:len(segs)-1]
		same := 0
		for same < len(anc) && same < len(prev) && anc[same] == prev[same] {
			same++
		}
		heads = heads[:same]
		for d := same; d < len(anc); d++ {
			rows = append(rows, treeRow{depth: d, label: segLabel(anc[d]), vis: -1, parent: last(heads)})
			heads = append(heads, len(rows)-1)
		}
		rows = append(rows, treeRow{depth: len(anc), label: segLabel(segs[len(segs)-1]), vis: vi, parent: last(heads)})
		prev = anc
	}
	return rows
}

// segLabel is a path segment written as YAML writes it.
func segLabel(s secrets.PathSeg) string {
	switch {
	case s.IsIndex:
		return "-"
	case s.Key == "":
		return `"":`
	}
	return s.Key + ":"
}

func last(rows []int) int {
	if len(rows) == 0 {
		return -1
	}
	return rows[len(rows)-1]
}

// renderTree shows the values nested under their parent keys, as in the
// YAML they came from. Headings are never selected: the cursor moves over
// values only, so r, a and y act on one value as in the flat view.
func (m Model) renderTree(vis []int, width, visible int, focused bool) []string {
	rows := treeRows(m.fields, vis)
	// Values line up within each group of siblings, as in hand-written YAML.
	labelW := map[int]int{}
	cur := 0
	for r, row := range rows {
		if row.vis < 0 {
			continue
		}
		labelW[row.parent] = max(labelW[row.parent], 2*row.depth+len([]rune(row.label)))
		if row.vis == m.fieldCur {
			cur = r
		}
	}
	head := lipgloss.NewStyle().Foreground(colorHead)
	start := max(cur-visible+1, 0)
	end := min(start+visible, len(rows))
	var out []string
	for r := start; r < end; r++ {
		row := rows[r]
		label := strings.Repeat("  ", row.depth) + row.label
		if row.vis < 0 {
			out = append(out, "  "+head.Render(truncate(label, width-4)))
			continue
		}
		keyW := min(labelW[row.parent], width/2)
		val := m.shownValue(row.vis, m.fields[vis[row.vis]].Value, max(width-keyW-7, 8))
		line := fmt.Sprintf("  %-*s %s", keyW, truncate(label, keyW), val)
		if focused && row.vis == m.fieldCur {
			line = selectedStyle(width).Render(line)
		}
		out = append(out, components.Zone(components.ZoneID("field", row.vis), line))
	}
	return out
}

// shownValue is the i-th visible value as the pane shows it: the mask, unless
// it is revealed. n caps its width.
func (m Model) shownValue(i int, value string, n int) string {
	if !m.revealAll && (i != m.fieldCur || !m.revealed) {
		return mask
	}
	// Keep a multi-line value (a certificate, say) on its row.
	return truncate(strings.ReplaceAll(value, "\n", "⏎"), n)
}

// countLine says how many keys (or, in a database's entry list, entries) the
// open item has, and how many the filter leaves. Empty while nothing is
// loaded: decrypting, asking for a password, or after an error.
func (m Model) countLine() string {
	if m.prompting || m.loading || m.readErr != "" {
		return ""
	}
	if m.listing() {
		c := countLabel(len(m.visibleEntries()), len(m.entries), "entry", "entries")
		if m.listNote != "" {
			c += " — " + m.listNote
		}
		return c
	}
	return countLabel(len(m.visibleFields()), len(m.fields), "key", "keys")
}

// countLabel renders "12 keys", "1 key" or, when a filter hides some,
// "3 of 12 keys".
func countLabel(shown, total int, one, many string) string {
	noun := many
	if total == 1 {
		noun = one
	}
	if shown != total {
		return fmt.Sprintf("%d of %d %s", shown, total, noun)
	}
	return fmt.Sprintf("%d %s", total, noun)
}

// renderEntries is the entry list of a database being browsed.
func (m Model) renderEntries(width, visible int, focused bool) []string {
	vis := m.visibleEntries()
	dim := lipgloss.NewStyle().Foreground(colorDim).Padding(0, 2)
	switch {
	case len(m.entries) == 0:
		return []string{dim.Render("No entries.")}
	case len(vis) == 0:
		return []string{dim.Render("No entries match your search.")}
	}
	var rows []string
	start := max(m.entryCur-visible+1, 0)
	end := min(start+visible, len(vis))
	for i := start; i < end; i++ {
		line := "  " + truncate(m.entries[vis[i]], width-4)
		if focused && i == m.entryCur {
			line = selectedStyle(width).Render(line)
		}
		rows = append(rows, components.Zone(components.ZoneID("entry", i), line))
	}
	return rows
}

func selectedStyle(width int) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).
		Background(lipgloss.Color("#1a1a3e")).
		Foreground(lipgloss.Color("#FFFDF5")).
		Width(width - 2)
}

// hintKey is one key of a hint line.
func hintKey(k, text string) components.HelpKey { return components.HelpKey{Key: k, Text: text} }

func truncate(s string, n int) string {
	r := []rune(s)
	if n < 1 {
		return ""
	}
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
