package secrets

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/secrets"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/tui/testkeys"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type fakeItem struct {
	name   string
	fields []secrets.Field
	status secrets.Status
	auto   bool
	err    *error // shared, so a test can fix the item after building the pane
}

func (f fakeItem) Name() string              { return f.name }
func (f fakeItem) Description() string       { return "" }
func (f fakeItem) Provider() string          { return "sops" }
func (f fakeItem) Location() string          { return "/x/" + f.name + ".enc.yaml" }
func (f fakeItem) Details() []secrets.Detail { return nil }
func (f fakeItem) Probe() secrets.Status     { return f.status }
func (f fakeItem) AutoReveal() bool          { return f.auto }
func (f fakeItem) Read(context.Context) ([]secrets.Field, error) {
	if f.err != nil && *f.err != nil {
		return nil, *f.err
	}
	return f.fields, nil
}

// open presses enter on the first item and delivers the read result.
func open(t *testing.T) Model {
	t.Helper()
	item := fakeItem{
		name:   "e2e",
		status: secrets.Status{Ready: true},
		fields: []secrets.Field{
			{Key: "db.password", Value: "hunter2"},
			{Key: "api.token", Value: "tok-123"},
		},
	}
	m := New([]secrets.Item{item}, 100, 30)
	m, cmd := m.Update(testkeys.Press("enter"), true)
	if cmd == nil {
		t.Fatal("enter did not start a read")
	}
	m, _ = m.Update(cmd(), true)
	return m
}

func TestValuesMaskedUntilRevealed(t *testing.T) {
	m := open(t)
	view := ansi.Strip(m.View(100, 30, true))
	if strings.Contains(view, "hunter2") || strings.Contains(view, "tok-123") {
		t.Fatal("a value is visible before reveal")
	}
	if !strings.Contains(view, "db.password") {
		t.Fatal("keys should be listed")
	}

	m, _ = m.Update(testkeys.Press("r"), true)
	view = ansi.Strip(m.View(100, 30, true))
	if !strings.Contains(view, "hunter2") || strings.Contains(view, "tok-123") {
		t.Fatal("reveal must show the selected value only")
	}

	// Moving away hides it again.
	m, _ = m.Update(testkeys.Press("down"), true)
	if strings.Contains(ansi.Strip(m.View(100, 30, true)), "tok-123") {
		t.Fatal("moving the cursor must re-mask")
	}
}

func TestHideDropsValues(t *testing.T) {
	m := open(t).Hide()
	if m.fields != nil || m.open {
		t.Fatal("Hide must drop the decrypted fields")
	}
	m = open(t)
	m, _ = m.Update(testkeys.Press("esc"), true)
	if m.fields != nil || m.open {
		t.Fatal("esc must drop the decrypted fields")
	}
}

func TestStaleReadIgnored(t *testing.T) {
	item := fakeItem{name: "e2e", status: secrets.Status{Ready: true},
		fields: []secrets.Field{{Key: "k", Value: "v"}}}
	m := New([]secrets.Item{item}, 100, 30)
	m, cmd := m.Update(testkeys.Press("enter"), true)
	m, _ = m.Update(testkeys.Press("esc"), true) // leave before sops answers
	m, _ = m.Update(cmd(), true)
	if m.fields != nil {
		t.Fatal("a read landing after the user left must be discarded")
	}
}

func TestNotReadyItemDoesNotOpen(t *testing.T) {
	item := fakeItem{name: "e2e", status: secrets.Status{Reason: "sops not installed"}}
	m := New([]secrets.Item{item}, 100, 30)
	m, _ = m.Update(testkeys.Press("enter"), true)
	if m.open {
		t.Fatal("an item that is not ready must not open")
	}
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), "sops not installed") {
		t.Fatal("the reason should be shown in the list")
	}
}

func autoItem(auto bool) fakeItem {
	return fakeItem{
		name:   "e2e",
		auto:   auto,
		status: secrets.Status{Ready: true},
		fields: []secrets.Field{
			{Key: "db.password", Value: "hunter2"},
			{Key: "api.token", Value: "tok-123"},
		},
	}
}

func openItem(t *testing.T, items ...secrets.Item) Model {
	t.Helper()
	m := New(items, 100, 30)
	m, cmd := m.Update(testkeys.Press("enter"), true)
	if cmd == nil {
		t.Fatal("enter did not start a read")
	}
	m, _ = m.Update(cmd(), true)
	return m
}

func typeText(m Model, s string) Model {
	for _, r := range s {
		m, _ = m.Update(testkeys.Press(string(r)), true)
	}
	return m
}

func TestAutoRevealShowsEverythingOnOpen(t *testing.T) {
	m := openItem(t, autoItem(true))
	v := ansi.Strip(m.View(100, 30, true))
	if !strings.Contains(v, "hunter2") || !strings.Contains(v, "tok-123") {
		t.Fatal("auto_reveal must show every value on open")
	}

	m, _ = m.Update(testkeys.Press("a"), true)
	v = ansi.Strip(m.View(100, 30, true))
	if strings.Contains(v, "hunter2") || strings.Contains(v, "tok-123") {
		t.Fatal("a must mask everything again")
	}

	// Closing and reopening starts from the config again.
	m, _ = m.Update(testkeys.Press("esc"), true)
	m, cmd := m.Update(testkeys.Press("enter"), true)
	m, _ = m.Update(cmd(), true)
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), "tok-123") {
		t.Fatal("reopening must start revealed again")
	}
}

func TestWithoutAutoRevealEverythingStartsMasked(t *testing.T) {
	m := openItem(t, autoItem(false))
	v := ansi.Strip(m.View(100, 30, true))
	if strings.Contains(v, "hunter2") || strings.Contains(v, "tok-123") {
		t.Fatal("values must start masked")
	}
	m, _ = m.Update(testkeys.Press("a"), true)
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), "tok-123") {
		t.Fatal("a must reveal every value")
	}
}

func TestKeySearchFiltersKeysNotValues(t *testing.T) {
	m := openItem(t, autoItem(false))
	m, _ = m.Update(testkeys.Press("/"), true)
	if !m.Searching() {
		t.Fatal("/ must open the search bar")
	}
	m = typeText(m, "tok") // only in the api.token key... and its value
	v := ansi.Strip(m.View(100, 30, true))
	if strings.Contains(v, "db.password") || !strings.Contains(v, "api.token") {
		t.Fatalf("filter by key failed:\n%s", v)
	}

	// A string found only in a value must match nothing.
	m, _ = m.Update(testkeys.Press("esc"), true)
	m, _ = m.Update(testkeys.Press("/"), true)
	m = typeText(m, "hunter")
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), "No keys match") {
		t.Fatal("search must never match values")
	}
}

func TestEnterKeepsFilterForActions(t *testing.T) {
	m := openItem(t, autoItem(false))
	m, _ = m.Update(testkeys.Press("/"), true)
	m = typeText(m, "token")
	m, _ = m.Update(testkeys.Press("enter"), true)
	if m.Searching() {
		t.Fatal("enter must close the search bar")
	}
	if f, ok := m.selectedField(); !ok || f.Key != "api.token" {
		t.Fatalf("selected field = %+v, want api.token", f)
	}
	m, _ = m.Update(testkeys.Press("r"), true)
	v := ansi.Strip(m.View(100, 30, true))
	if !strings.Contains(v, "tok-123") || strings.Contains(v, "db.password") {
		t.Fatal("r must reveal the filtered row, with the filter still applied")
	}

	// esc clears the kept filter first, then closes the file.
	m, _ = m.Update(testkeys.Press("esc"), true)
	if !m.open || m.fieldQuery != "" {
		t.Fatal("first esc must only clear the filter")
	}
	m, _ = m.Update(testkeys.Press("esc"), true)
	if m.open {
		t.Fatal("second esc must close the file")
	}
}

func TestSearchTypesJAndK(t *testing.T) {
	m := openItem(t, fakeItem{name: "e2e", status: secrets.Status{Ready: true},
		fields: []secrets.Field{{Key: "jwt.key", Value: "x"}, {Key: "other", Value: "y"}}})
	m, _ = m.Update(testkeys.Press("/"), true)
	m = typeText(m, "jk")
	if m.fieldQuery != "jk" {
		t.Fatalf("query = %q, j and k must be typed, not navigate", m.fieldQuery)
	}
	m, _ = m.Update(testkeys.Press("backspace"), true)
	m, _ = m.Update(testkeys.Press("backspace"), true)
	m, _ = m.Update(testkeys.Press("backspace"), true)
	if m.Searching() {
		t.Fatal("backspace on an empty query must close the bar")
	}
}

func TestListSearchFiltersFiles(t *testing.T) {
	a := fakeItem{name: "eu staging", status: secrets.Status{Ready: true}}
	b := fakeItem{name: "us prod", status: secrets.Status{Ready: true},
		fields: []secrets.Field{{Key: "k", Value: "v"}}}
	m := New([]secrets.Item{a, b}, 100, 30)
	m, _ = m.Update(testkeys.Press("/"), true)
	m = typeText(m, "prod")
	v := ansi.Strip(m.View(100, 30, true))
	if strings.Contains(v, "eu staging") || !strings.Contains(v, "us prod") {
		t.Fatalf("list filter failed:\n%s", v)
	}
	// enter opens the one match.
	m, cmd := m.Update(testkeys.Press("enter"), true)
	if !m.open || cmd == nil {
		t.Fatal("enter in the list search must open the match")
	}
	if it, _ := m.selected(); it.Name() != "us prod" {
		t.Fatalf("opened %q", it.Name())
	}
}

func TestFailedDecryptTurnsItemRed(t *testing.T) {
	readErr := errors.New("sops: could not load AWS config: profile, typo")
	item := fakeItem{name: "e2e", status: secrets.Status{Ready: true}, err: &readErr,
		fields: []secrets.Field{{Key: "k", Value: "v"}}}
	m := New([]secrets.Item{item}, 100, 30)
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), "ready") {
		t.Fatal("the probe passed, so the item starts ready")
	}

	m, cmd := m.Update(testkeys.Press("enter"), true)
	m, _ = m.Update(cmd(), true)
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), "could not load AWS config") {
		t.Fatal("the open file must show the sops error")
	}
	m, _ = m.Update(testkeys.Press("esc"), true)
	if v := ansi.Strip(m.View(100, 30, true)); !strings.Contains(v, "decrypt failed") || strings.Contains(v, "ready") {
		t.Fatalf("after a failed decrypt the list must show it:\n%s", v)
	}

	// Once the cause is fixed, a successful decrypt turns it green again.
	readErr = nil
	m, cmd = m.Update(testkeys.Press("enter"), true)
	m, _ = m.Update(cmd(), true)
	m, _ = m.Update(testkeys.Press("esc"), true)
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), "ready") {
		t.Fatal("a successful decrypt must clear the failure")
	}
}

func TestAutoRevealToggleRow(t *testing.T) {
	for _, auto := range []bool{true, false} {
		m := New([]secrets.Item{autoItem(auto)}, 100, 30)
		v := ansi.Strip(m.View(100, 30, true))
		want := ansi.Strip(components.ToggleDot(auto)) + " Auto reveal"
		if !strings.Contains(v, want) {
			t.Errorf("auto_reveal=%v: box must show %q like the VPN/Tunnel toggles", auto, want)
		}
	}
}

// lockedItem is an Unlocker that accepts "pw" and remembers it when remember
// is set, like the KeePassXC provider.
type lockedItem struct {
	fakeItem
	remember bool
	cached   *bool
	gotPW    *string
	locks    *int
}

func (l lockedItem) NeedsPassword() bool    { return !*l.cached }
func (l lockedItem) PasswordPrompt() string { return "Master password for team.kdbx" }
func (l lockedItem) Lock()                  { *l.cached = false; *l.locks++ }
func (l lockedItem) Read(ctx context.Context) ([]secrets.Field, error) {
	if !*l.cached {
		return nil, secrets.ErrBadPassword
	}
	return l.fields, nil
}
func (l lockedItem) ReadWithPassword(_ context.Context, pw []byte) ([]secrets.Field, error) {
	*l.gotPW = string(pw)
	if string(pw) != "pw" {
		return nil, fmt.Errorf("keepassxc: %w", secrets.ErrBadPassword)
	}
	if l.remember {
		*l.cached = true
	}
	return l.fields, nil
}

func newLocked(remember bool) lockedItem {
	cached, got, locks := false, "", 0
	return lockedItem{
		name: "jenkins", status: secrets.Status{Ready: true},
		fields:   []secrets.Field{{Key: "Password", Value: "s3cr3t"}},
		remember: remember, cached: &cached, gotPW: &got, locks: &locks,
	}
}

// run delivers cmd's message, if any, back to the model.
func run(m Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return m
	}
	if msg := cmd(); msg != nil {
		if _, isRead := msg.(readMsg); isRead {
			m, _ = m.Update(msg, true)
		}
	}
	return m
}

func TestLockedItemPromptsBeforeReading(t *testing.T) {
	item := newLocked(false)
	m := New([]secrets.Item{item}, 100, 30)
	m, _ = m.Update(testkeys.Press("enter"), true)
	if !m.prompting || m.loading {
		t.Fatal("a locked item must prompt, not read")
	}
	if !m.Searching() {
		t.Fatal("while prompting the app must not act on typed keys (q, h, R)")
	}
	v := ansi.Strip(m.View(100, 30, true))
	if !strings.Contains(v, "Master password for team.kdbx") || !strings.Contains(v, "enter: unlock") {
		t.Fatalf("prompt not shown:\n%s", v)
	}

	m = typeText(m, "pw")
	if strings.Contains(ansi.Strip(m.View(100, 30, true)), "pw\n") || strings.Contains(ansi.Strip(m.View(100, 30, true)), "› pw") {
		t.Fatal("the password must be masked")
	}
	m, cmd := m.Update(testkeys.Press("enter"), true)
	if m.prompting || m.pwInput.Value() != "" {
		t.Fatal("enter must submit and clear the field")
	}
	m = run(m, cmd)
	if *item.gotPW != "pw" {
		t.Fatalf("read with %q", *item.gotPW)
	}
	if len(m.fields) != 1 {
		t.Fatalf("fields = %v", m.fields)
	}

	// Not remembered: the next open asks again.
	m, _ = m.Update(testkeys.Press("esc"), true)
	m, _ = m.Update(testkeys.Press("enter"), true)
	if !m.prompting {
		t.Fatal("without remember_password every open must prompt")
	}
}

func TestPromptTypesHAndQ(t *testing.T) {
	m := New([]secrets.Item{newLocked(false)}, 100, 30)
	m, _ = m.Update(testkeys.Press("enter"), true)
	// Searching is what keeps the root model from reading h as "shortcuts"
	// and q as "quit" while the prompt has the keyboard.
	if !m.Searching() {
		t.Fatal("a password prompt must report that it takes the keyboard")
	}
	m = typeText(m, "hq/l")
	if m.typing || m.pwInput.Value() != "hq/l" {
		t.Fatalf("keys must be typed into the password: typing=%v value=%q", m.typing, m.pwInput.Value())
	}
}

func TestWrongPasswordPromptsAgain(t *testing.T) {
	item := newLocked(true)
	m := New([]secrets.Item{item}, 100, 30)
	m, _ = m.Update(testkeys.Press("enter"), true)
	m = typeText(m, "nope")
	m, cmd := m.Update(testkeys.Press("enter"), true)
	m = run(m, cmd)
	if !m.prompting || !strings.Contains(ansi.Strip(m.View(100, 30, true)), "wrong password") {
		t.Fatal("a wrong password must prompt again with a message")
	}
	if m.readFailed[0] {
		t.Fatal("a mistyped password must not mark the secret as failed")
	}
}

func TestRememberedPasswordSkipsPromptAndLockForgets(t *testing.T) {
	item := newLocked(true)
	m := New([]secrets.Item{item}, 100, 30)
	m, _ = m.Update(testkeys.Press("enter"), true)
	m = typeText(m, "pw")
	m, cmd := m.Update(testkeys.Press("enter"), true)
	m = run(m, cmd)
	m, _ = m.Update(testkeys.Press("esc"), true)

	m, cmd = m.Update(testkeys.Press("enter"), true)
	if m.prompting {
		t.Fatal("a remembered password must not prompt")
	}
	m = run(m, cmd)
	if len(m.fields) != 1 {
		t.Fatal("read with the remembered password failed")
	}

	m, _ = m.Update(testkeys.Press("esc"), true)
	m, _ = m.Update(testkeys.Press("l"), true)
	if *item.locks != 1 {
		t.Fatal("l must lock")
	}
	m, _ = m.Update(testkeys.Press("enter"), true)
	if !m.prompting {
		t.Fatal("after l the password must be asked again")
	}
}

func TestEscAndHideDropTypedPassword(t *testing.T) {
	m := New([]secrets.Item{newLocked(false)}, 100, 30)
	m, _ = m.Update(testkeys.Press("enter"), true)
	m = typeText(m, "half")
	m, _ = m.Update(testkeys.Press("esc"), true)
	if m.prompting || m.open || m.pwInput.Value() != "" {
		t.Fatal("esc must cancel and drop the typed password")
	}
	m, _ = m.Update(testkeys.Press("enter"), true)
	m = typeText(m, "half")
	m = m.Hide()
	if m.prompting || m.pwInput.Value() != "" {
		t.Fatal("Hide must drop the typed password")
	}
}

func TestToggleRowsFromProvider(t *testing.T) {
	m := New([]secrets.Item{toggler{name: "k", status: secrets.Status{Ready: true}}}, 100, 30)
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), ansi.Strip(components.ToggleDot(true))+" Remember password") {
		t.Fatal("provider toggles must render as dots")
	}
}

type toggler struct{ fakeItem }

func (toggler) Toggles() []secrets.Toggle {
	return []secrets.Toggle{{Label: "Remember password", On: true}}
}

// browseItem is a whole database: it lists entries and reads one at a time,
// accepting the password "pw" and remembering nothing.
type browseItem struct {
	lockedItem
	reads *[]string // "list:<pw>" or "<entry>:<pw>" per call
}

func (b browseItem) Browsing() bool { return true }
func (b browseItem) ListEntries(_ context.Context, pw []byte) ([]string, error) {
	*b.reads = append(*b.reads, "list:"+string(pw))
	if string(pw) != "pw" {
		return nil, fmt.Errorf("keepassxc: %w", secrets.ErrBadPassword)
	}
	return []string{"CI/Jenkins", "AMS/Postgres INT", "AMS/Kafka/E2E"}, nil
}
func (b browseItem) ReadEntry(_ context.Context, entry string, pw []byte) ([]secrets.Field, error) {
	*b.reads = append(*b.reads, entry+":"+string(pw))
	if string(pw) != "pw" {
		return nil, fmt.Errorf("keepassxc: %w", secrets.ErrLocked)
	}
	return []secrets.Field{{Key: "Password", Value: "secret-of-" + entry}}, nil
}

func newBrowse() browseItem {
	var reads []string
	return browseItem{lockedItem: newLocked(false), reads: &reads}
}

// deliver runs cmd and feeds a list or read result back to the model.
func deliver(m Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case listMsg, readMsg:
		m, _ = m.Update(msg, true)
	}
	return m
}

func unlockBrowse(t *testing.T, b browseItem) Model {
	t.Helper()
	m := New([]secrets.Item{b}, 100, 30)
	m, _ = m.Update(testkeys.Press("enter"), true)
	if !m.prompting {
		t.Fatal("a locked database must prompt first")
	}
	m = typeText(m, "pw")
	m, cmd := m.Update(testkeys.Press("enter"), true)
	return deliver(m, cmd)
}

func TestBrowseListsEntriesThenOpensOne(t *testing.T) {
	b := newBrowse()
	m := unlockBrowse(t, b)
	v := ansi.Strip(m.View(100, 30, true))
	for _, e := range []string{"CI/Jenkins", "AMS/Postgres INT", "AMS/Kafka/E2E"} {
		if !strings.Contains(v, e) {
			t.Fatalf("entry %q not listed:\n%s", e, v)
		}
	}

	m, _ = m.Update(testkeys.Press("down"), true)
	m, cmd := m.Update(testkeys.Press("enter"), true)
	m = deliver(m, cmd)
	if m.entry != "AMS/Postgres INT" || len(m.fields) != 1 {
		t.Fatalf("entry = %q fields = %v", m.entry, m.fields)
	}
	if got := (*b.reads)[len(*b.reads)-1]; got != "AMS/Postgres INT:pw" {
		t.Fatalf("entry read = %q, want it read with the session password, no second prompt", got)
	}
	if strings.Contains(ansi.Strip(m.View(100, 30, true)), "secret-of-") {
		t.Fatal("values must start masked")
	}

	// esc goes back to the list, not out of the database.
	m, _ = m.Update(testkeys.Press("esc"), true)
	if !m.listing() || m.fields != nil {
		t.Fatal("esc from an entry must return to the entry list and drop its fields")
	}
	// esc on the list closes the database and wipes the session password.
	pw := m.sessionPW
	m, _ = m.Update(testkeys.Press("esc"), true)
	if m.open || m.sessionPW != nil || string(pw) != "\x00\x00" {
		t.Fatalf("closing must clear the session password: %q", pw)
	}
}

func TestBrowseSearchFiltersEntries(t *testing.T) {
	m := unlockBrowse(t, newBrowse())
	m, _ = m.Update(testkeys.Press("/"), true)
	m = typeText(m, "kafka")
	v := ansi.Strip(m.View(100, 30, true))
	if strings.Contains(v, "CI/Jenkins") || !strings.Contains(v, "AMS/Kafka/E2E") {
		t.Fatalf("entry search failed:\n%s", v)
	}
	m, cmd := m.Update(testkeys.Press("enter"), true) // opens the one match
	m = deliver(m, cmd)
	if m.entry != "AMS/Kafka/E2E" {
		t.Fatalf("opened %q", m.entry)
	}
}

func TestBrowseWrongPasswordAndLock(t *testing.T) {
	b := newBrowse()
	m := New([]secrets.Item{b}, 100, 30)
	m, _ = m.Update(testkeys.Press("enter"), true)
	m = typeText(m, "nope")
	m, cmd := m.Update(testkeys.Press("enter"), true)
	m = deliver(m, cmd)
	if !m.prompting || m.promptErr != "wrong password — try again" {
		t.Fatalf("wrong password must ask again: prompting=%v err=%q", m.prompting, m.promptErr)
	}
	m = typeText(m, "pw")
	m, cmd = m.Update(testkeys.Press("enter"), true)
	m = deliver(m, cmd)
	if len(m.entries) != 3 {
		t.Fatal("unlock after a wrong password failed")
	}

	// l drops the session password; the next entry asks again, without
	// claiming the password was wrong.
	m, _ = m.Update(testkeys.Press("l"), true)
	m, cmd = m.Update(testkeys.Press("enter"), true)
	m = deliver(m, cmd)
	if !m.prompting || m.promptErr != "" {
		t.Fatalf("after l the entry must prompt quietly: prompting=%v err=%q", m.prompting, m.promptErr)
	}
	m = typeText(m, "pw")
	m, cmd = m.Update(testkeys.Press("enter"), true)
	m = deliver(m, cmd)
	if m.entry != "CI/Jenkins" || len(m.fields) != 1 {
		t.Fatalf("the pending entry must open after unlocking: entry=%q", m.entry)
	}
}

func TestHideDropsBrowseSession(t *testing.T) {
	m := unlockBrowse(t, newBrowse())
	pw := m.sessionPW
	m = m.Hide()
	if m.open || m.entries != nil || m.sessionPW != nil || string(pw) != "\x00\x00" {
		t.Fatal("Hide must close the database and wipe the session password")
	}
}

func TestCountLine(t *testing.T) {
	m := openItem(t, autoItem(false)) // db.password, api.token
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), "2 keys") {
		t.Fatal("open item must show its key count")
	}
	m, _ = m.Update(testkeys.Press("/"), true)
	m = typeText(m, "token")
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), "1 of 2 keys") {
		t.Fatal("a filter must show how many keys it leaves")
	}

	one := openItem(t, fakeItem{name: "x", status: secrets.Status{Ready: true},
		fields: []secrets.Field{{Key: "k", Value: "v"}}})
	if !strings.Contains(ansi.Strip(one.View(100, 30, true)), "1 key") || strings.Contains(ansi.Strip(one.View(100, 30, true)), "1 keys") {
		t.Fatal("singular: 1 key")
	}
}

func TestCountLineBrowsing(t *testing.T) {
	m := New([]secrets.Item{newBrowse()}, 100, 30)
	m, _ = m.Update(testkeys.Press("enter"), true)
	if strings.Contains(ansi.Strip(m.View(100, 30, true)), "entries") {
		t.Fatal("no count while asking for the password")
	}
	m = typeText(m, "pw")
	m, cmd := m.Update(testkeys.Press("enter"), true)
	m = deliver(m, cmd)
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), "3 entries") {
		t.Fatal("the entry list must show its entry count")
	}
}

// loginItem is a Vault-like item: reads fail until loggedIn is set, and it
// has a login command when login is true.
type loginItem struct {
	fakeItem
	login    bool
	loggedIn *bool
}

func (l loginItem) LoginCmd() *exec.Cmd {
	if !l.login {
		return nil
	}
	return exec.Command("true")
}
func (l loginItem) Probe() secrets.Status {
	if !*l.loggedIn {
		return secrets.Status{Reason: "vault login required — L to log in"}
	}
	return secrets.Status{Ready: true}
}
func (l loginItem) Read(context.Context) ([]secrets.Field, error) {
	if !*l.loggedIn {
		return nil, errors.New("vault: permission denied — token expired or no access (L to log in)")
	}
	return l.fields, nil
}

func newLoginItem(login bool) loginItem {
	in := true // the probe passes (a token file exists) but the token is expired
	return loginItem{name: "grafana", fields: []secrets.Field{{Key: "password", Value: "x"}},
		login: login, loggedIn: &in}
}

func TestLoginRetriesFailedRead(t *testing.T) {
	item := newLoginItem(true)
	*item.loggedIn = false
	m := New([]secrets.Item{item}, 100, 30)
	// The probe says login required, so enter does not even try.
	m, _ = m.Update(testkeys.Press("enter"), true)
	if m.open {
		t.Fatal("a not-ready item must not open")
	}
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), "L to log in") {
		t.Fatal("the status must say how to log in")
	}

	if _, cmd := m.Update(testkeys.Press("L"), true); cmd == nil {
		t.Fatal("L must run the login command")
	}
	*item.loggedIn = true
	m, _ = m.Update(loginDoneMsg{name: "grafana"}, true)
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), "ready") {
		t.Fatal("after login the item must be re-probed")
	}
}

func TestLoginRefetchesOpenItem(t *testing.T) {
	item := newLoginItem(true)
	m := New([]secrets.Item{item}, 100, 30)
	m, cmd := m.Update(testkeys.Press("enter"), true)
	*item.loggedIn = false // token expired between probe and read
	m = deliver(m, cmd)
	if m.readErr == "" || !m.readFailed[0] {
		t.Fatal("the read must fail and mark the item")
	}
	*item.loggedIn = true
	m, cmd = m.Update(loginDoneMsg{name: "grafana"}, true)
	if m.readFailed[0] {
		t.Fatal("login must clear the failure mark")
	}
	// The batch holds the flash and the re-read; run the re-read.
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if msg, isRead := c().(readMsg); isRead {
				m, _ = m.Update(msg, true)
			}
		}
	}
	if m.readErr != "" || len(m.fields) != 1 {
		t.Fatalf("the open item must be read again after login: err=%q fields=%v", m.readErr, m.fields)
	}
}

func TestLoginWithoutCommand(t *testing.T) {
	m := New([]secrets.Item{newLoginItem(false)}, 100, 30)
	_, cmd := m.Update(testkeys.Press("L"), true)
	if cmd == nil {
		t.Fatal("L without a login command must explain why")
	}
	if f, ok := cmd().(msgs.StatusFlashMsg); !ok || !strings.Contains(f.Text, "no login command") {
		t.Fatalf("flash = %#v", f)
	}
}

// treeItem is a fakeItem whose config asks for a view (secrets.Viewer).
type treeItem struct {
	fakeItem
	tree bool
}

func (f treeItem) TreeView() bool { return f.tree }

// editableTreeItem is a treeItem whose edits always save.
type editableTreeItem struct{ treeItem }

func (editableTreeItem) EditCmd() *exec.Cmd             { return exec.Command("true") }
func (editableTreeItem) EditResult(error) (bool, error) { return true, nil }

// nestedItem is shaped like a real SOPS file: keys with dots in them, which
// the dotted flat view cannot tell from nesting, and arrays.
func nestedItem(t *testing.T, tree bool) treeItem {
	t.Helper()
	fields, err := secrets.FlattenJSON([]byte(`{
		"cicd-database-secret": {"password": "pw-1", "username": "user-1"},
		"client.aacn": {"apiKey": "key-1", "basicauth.clientId": "cid-1"},
		"hosts": ["host-a", "host-b"],
		"users": [{"name": "name-1"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return treeItem{name: "e2e", status: secrets.Status{Ready: true}, fields: fields, tree: tree}
}

// lineWith is the first line of view containing s, without trailing padding.
func lineWith(t *testing.T, view, s string) string {
	t.Helper()
	for l := range strings.SplitSeq(view, "\n") {
		if strings.Contains(l, s) {
			return strings.TrimRight(l, " ")
		}
	}
	t.Fatalf("no line contains %q in:\n%s", s, view)
	return ""
}

func TestTreeViewNestsUnderParentKeys(t *testing.T) {
	m := openItem(t, nestedItem(t, true))
	v := ansi.Strip(m.View(100, 30, true))
	for _, secret := range []string{"pw-1", "user-1", "key-1", "cid-1", "host-a", "name-1"} {
		if strings.Contains(v, secret) {
			t.Fatalf("%q is visible before reveal", secret)
		}
	}
	if strings.Contains(v, "client.aacn.basicauth.clientId") {
		t.Error("the tree must not join keys with dots")
	}
	for _, want := range []string{
		"  cicd-database-secret:",
		"    password: " + mask,
		"  client.aacn:",
		"    apiKey:             " + mask,
		"    basicauth.clientId: " + mask,
		"  hosts:",
		"    - " + mask,
		"  users:",
		"    -",
		"      name: " + mask,
	} {
		found := false
		for l := range strings.SplitSeq(v, "\n") {
			if strings.TrimRight(l, " ") == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no line %q in:\n%s", want, v)
		}
	}
	if !strings.Contains(ansi.Strip(m.View(130, 30, true)), "t: flat") {
		t.Error("the hint should say t switches to the flat view")
	}
}

// The cursor moves over values only, so r and y keep acting on one value.
func TestTreeCursorSkipsHeadings(t *testing.T) {
	m := openItem(t, nestedItem(t, true))
	for i, want := range []string{
		"cicd-database-secret.password",
		"cicd-database-secret.username",
		"client.aacn.apiKey", // past the client.aacn heading
		"client.aacn.basicauth.clientId",
		"hosts[0]",
		"hosts[1]",
		"users[0].name",
	} {
		if i > 0 {
			m, _ = m.Update(testkeys.Press("down"), true)
		}
		f, ok := m.selectedField()
		if !ok || f.Key != want {
			t.Fatalf("step %d: selected %q, want %q", i, f.Key, want)
		}
	}
	m, _ = m.Update(testkeys.Press("r"), true)
	if l := lineWith(t, ansi.Strip(m.View(100, 30, true)), "  name:"); !strings.Contains(l, "name-1") {
		t.Errorf("r must reveal the selected value, got %q", l)
	}
}

func TestTTogglesViewAndKeepsSelection(t *testing.T) {
	m := openItem(t, nestedItem(t, true))
	m, _ = m.Update(testkeys.Press("down"), true)
	m, _ = m.Update(testkeys.Press("down"), true)
	m, _ = m.Update(testkeys.Press("t"), true)
	v := ansi.Strip(m.View(100, 30, true))
	if m.tree || !strings.Contains(v, "client.aacn.basicauth.clientId") || !strings.Contains(ansi.Strip(m.View(130, 30, true)), "t: tree") {
		t.Fatalf("t must switch to the flat view:\n%s", v)
	}
	if f, _ := m.selectedField(); f.Key != "client.aacn.apiKey" {
		t.Errorf("selected %q after t, want the same value as before", f.Key)
	}
	m, _ = m.Update(testkeys.Press("t"), true)
	if !m.tree || !strings.Contains(ansi.Strip(m.View(100, 30, true)), "  client.aacn:") {
		t.Fatal("t again must go back to the tree")
	}

	// Closing and reopening starts from the config again.
	m, _ = m.Update(testkeys.Press("t"), true)
	m, _ = m.Update(testkeys.Press("esc"), true)
	m, cmd := m.Update(testkeys.Press("enter"), true)
	m, _ = m.Update(cmd(), true)
	if !m.tree {
		t.Fatal("reopening must start in the configured view")
	}
}

func TestTreeFilterKeepsParentKeys(t *testing.T) {
	m := openItem(t, nestedItem(t, true))
	m, _ = m.Update(testkeys.Press("/"), true)
	m = typeText(m, "clientid")
	m, _ = m.Update(testkeys.Press("enter"), true)
	v := ansi.Strip(m.View(100, 30, true))
	if !strings.Contains(v, "  client.aacn:") || !strings.Contains(v, "basicauth.clientId:") {
		t.Fatalf("a match must show under its parent key:\n%s", v)
	}
	if strings.Contains(v, "cicd-database-secret") || strings.Contains(v, "apiKey") {
		t.Fatalf("the filter must hide the rest:\n%s", v)
	}
}

func TestItemsOpenFlatUnlessTheyAskForATree(t *testing.T) {
	if m := open(t); m.tree {
		t.Error("an item that is not a secrets.Viewer (KeePassXC) must open flat")
	}
	m := openItem(t, nestedItem(t, false))
	if m.tree || !strings.Contains(ansi.Strip(m.View(100, 30, true)), "client.aacn.basicauth.clientId") {
		t.Error("view: flat must open the dotted list")
	}
}

func TestTreeScrollsToTheCursor(t *testing.T) {
	var doc []string
	for i := range 30 {
		doc = append(doc, fmt.Sprintf(`"g%02d": {"k%02d": "v"}`, i, i))
	}
	fields, err := secrets.FlattenJSON([]byte("{" + strings.Join(doc, ",") + "}"))
	if err != nil {
		t.Fatal(err)
	}
	m := openItem(t, treeItem{name: "big", status: secrets.Status{Ready: true}, fields: fields, tree: true})
	for range 25 {
		m, _ = m.Update(testkeys.Press("down"), true)
	}
	v := ansi.Strip(m.View(100, 30, true))
	if !strings.Contains(v, "k25:") {
		t.Fatalf("the selected value must be on screen:\n%s", v)
	}
	if strings.Contains(v, "k00:") {
		t.Fatalf("the top of a 60-row tree cannot fit with the cursor at k25:\n%s", v)
	}
}

// A file re-read after an edit keeps the view the user switched to.
func TestViewSurvivesReadAfterEdit(t *testing.T) {
	m := openItem(t, editableTreeItem{nestedItem(t, true)})
	m, _ = m.Update(testkeys.Press("t"), true)
	m, _ = m.Update(editedMsg{name: "e2e"}, true)
	if !m.loading {
		t.Fatal("a saved edit must re-read the open file")
	}
	m, _ = m.Update(m.readCmd(0)(), true)
	if m.tree || len(m.fields) == 0 {
		t.Fatalf("after the re-read: tree = %v, %d fields; want the flat view kept", m.tree, len(m.fields))
	}
}

// clipboardSink replaces the system clipboard for a test: contents is what was
// written, and failing makes writes fail as on a machine with no clipboard.
func clipboardSink(t *testing.T, failing bool) *string {
	t.Helper()
	var contents string
	saved := components.WriteClipboard
	components.WriteClipboard = func(s string) error {
		if failing {
			return errors.New("no clipboard")
		}
		contents = s
		return nil
	}
	t.Cleanup(func() { components.WriteClipboard = saved })
	return &contents
}

// flatten runs cmd and every command it batches, and returns the messages.
func flatten(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, flatten(c)...)
		}
		return out
	case nil:
		return nil
	default:
		return []tea.Msg{msg}
	}
}

// terminalClipboard picks the OSC 52 request out of msgs: its text, and whether
// there was one.
func terminalClipboard(msgs []tea.Msg) (string, bool) {
	for _, msg := range msgs {
		if fmt.Sprintf("%T", msg) == "tea.setClipboardMsg" {
			return fmt.Sprint(msg), true
		}
	}
	return "", false
}

func TestCopyUsesTheSystemClipboardAndClearsIt(t *testing.T) {
	sink := clipboardSink(t, false)
	m := open(t)
	_, cmd := m.Update(testkeys.Press("y"), true)
	out := flatten(cmd)
	if *sink != "hunter2" {
		t.Fatalf("the clipboard holds %q, want the value", *sink)
	}
	if _, ok := terminalClipboard(out); ok {
		t.Fatal("the terminal should not be asked when the system clipboard worked")
	}
	var copied copiedMsg
	for _, msg := range out {
		if c, ok := msg.(copiedMsg); ok {
			copied = c
		}
	}
	if copied.key != "db.password" || copied.viaTerminal {
		t.Fatalf("copiedMsg = %+v, want the key, not through the terminal", copied)
	}
}

// Without a system clipboard the terminal sets it (OSC 52), and the 30 s clear
// is sent the same way: den cannot read that clipboard to check it first.
func TestCopyFallsBackToTheTerminalAndClearsItThere(t *testing.T) {
	clipboardSink(t, true)
	m := open(t)
	_, cmd := m.Update(testkeys.Press("y"), true)
	out := flatten(cmd)
	if text, ok := terminalClipboard(out); !ok || text != "hunter2" {
		t.Fatalf("the terminal was asked to set %q (asked: %v), want the value", text, ok)
	}
	var copied copiedMsg
	for _, msg := range out {
		if c, ok := msg.(copiedMsg); ok {
			copied = c
		}
	}
	if !copied.viaTerminal {
		t.Fatalf("copiedMsg = %+v, want it marked as sent through the terminal", copied)
	}

	_, clear := m.Update(clearClipMsg{sum: copied.sum, viaTerminal: true}, true)
	cleared := flatten(clear)
	if text, ok := terminalClipboard(cleared); !ok || text != "" {
		t.Fatalf("the clear should empty the terminal's clipboard (asked: %v, text %q)", ok, text)
	}
	var sawCleared bool
	for _, msg := range cleared {
		_, isCleared := msg.(clipClearedMsg)
		sawCleared = sawCleared || isCleared
	}
	if !sawCleared {
		t.Fatal("the pane should be told the clipboard was cleared")
	}
}
