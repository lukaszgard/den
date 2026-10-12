package runbook

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newTestDrafts(t *testing.T) *Drafts {
	t.Helper()
	return NewDrafts(filepath.Join(t.TempDir(), "runbook-drafts"))
}

func TestDraftsSaveListAndDelete(t *testing.T) {
	d := newTestDrafts(t)
	if got := d.List(30); len(got) != 0 {
		t.Fatalf("a store never written to lists %v", got)
	}
	first, err := d.Save(Draft{Kind: DraftNew, Name: "RDS status", Agent: "claude", Request: "show my clusters"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.Version != 1 || first.State != StateDraft || first.Created.IsZero() || !first.Updated.Equal(first.Created) {
		t.Fatalf("saved = %+v; want an ID, version 1, state draft and both times", first)
	}
	d.now = func() time.Time { return first.Updated.Add(time.Minute) }
	second, err := d.Save(Draft{Kind: DraftEdit, Runbook: "Greet", Request: "add a parameter"})
	if err != nil {
		t.Fatal(err)
	}

	got := d.List(30)
	if len(got) != 2 || got[0].ID != second.ID || got[1].ID != first.ID {
		t.Fatalf("List = %+v, want the two drafts, newest first", got)
	}
	if got[1].Request != "show my clusters" || got[1].Agent != "claude" || got[1].Name != "RDS status" {
		t.Errorf("read back %+v", got[1])
	}
	if dr, ok := d.Get(first.ID); !ok || dr.Request != "show my clusters" {
		t.Errorf("Get(%s) = %+v, %v", first.ID, dr, ok)
	}

	// Saving it again keeps its ID and when it was made.
	d.now = func() time.Time { return first.Updated.Add(time.Hour) }
	first.Request = "show my clusters in prod"
	again, err := d.Save(first)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || !again.Created.Equal(first.Created) || !again.Updated.After(first.Updated) {
		t.Errorf("saved again = %+v, want the same ID and creation, a later update", again)
	}
	if got := d.List(30); len(got) != 2 || got[0].ID != first.ID {
		t.Errorf("the draft saved last should come first: %+v", got)
	}

	if err := d.Delete(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(first.ID); err != nil {
		t.Errorf("deleting a draft that is gone: %v", err)
	}
	if got := d.List(30); len(got) != 1 || got[0].ID != second.ID {
		t.Errorf("after the delete List = %+v", got)
	}
}

// A draft is written whole or not at all, readable only by its owner: a
// request may hold a password.
func TestDraftsAreWrittenAtomicallyAndPrivately(t *testing.T) {
	d := newTestDrafts(t)
	for _, req := range []string{"one", "two", "three"} {
		if _, err := d.Save(Draft{Kind: DraftNew, Request: req}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(d.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("the folder holds %d entries, want the 3 drafts", len(entries))
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			t.Errorf("%s is left in the folder", e.Name())
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(d.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("the folder has mode %o, want 700", perm)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s has mode %o, want 600", e.Name(), perm)
		}
	}
}

// The ID is no function of the name: a draft may have none yet, two may
// share one, and the name changes while it is typed.
func TestDraftIDDoesNotDependOnTheName(t *testing.T) {
	d := newTestDrafts(t)
	a, err := d.Save(Draft{Kind: DraftNew, Name: "deploy", Request: "x"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.Save(Draft{Kind: DraftNew, Name: "deploy", Request: "y"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.Save(Draft{Kind: DraftNew, Request: "z"})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID || c.ID == "" || strings.Contains(a.ID, "deploy") {
		t.Fatalf("IDs %q, %q, %q: want distinct ones that do not hold the name", a.ID, b.ID, c.ID)
	}
	a.Name = "Deploy the API"
	renamed, err := d.Save(a)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.ID != a.ID || len(d.List(30)) != 3 {
		t.Errorf("a rename made a new draft: %q, %d listed", renamed.ID, len(d.List(30)))
	}
}

func TestDraftsUntouchedForDaysAreRemoved(t *testing.T) {
	d := newTestDrafts(t)
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now.AddDate(0, 0, -40) }
	old, err := d.Save(Draft{Kind: DraftNew, Request: "old"})
	if err != nil {
		t.Fatal(err)
	}
	d.now = func() time.Time { return now.AddDate(0, 0, -2) }
	recent, err := d.Save(Draft{Kind: DraftNew, Request: "recent"})
	if err != nil {
		t.Fatal(err)
	}
	// A saved request follows keep_request, not days.
	d.now = func() time.Time { return now.AddDate(0, 0, -90) }
	if _, err := d.Save(Draft{Kind: DraftNew, State: StateCreated, Runbook: "deploy", Request: "kept"}); err != nil {
		t.Fatal(err)
	}
	d.now = func() time.Time { return now }

	if got := d.List(0); len(got) != 2 {
		t.Fatalf("days 0 keeps every draft, listed %d", len(got))
	}
	got := d.List(30)
	if len(got) != 1 || got[0].ID != recent.ID {
		t.Fatalf("List(30) = %+v, want only the recent draft", got)
	}
	if _, ok := d.Get(old.ID); ok {
		t.Error("the draft older than 30 days is still on disk")
	}
	if _, ok := d.Request("deploy", ""); !ok {
		t.Error("the saved request was removed with the old drafts")
	}
}

func TestDraftsSkipACorruptFile(t *testing.T) {
	d := newTestDrafts(t)
	good, err := d.Save(Draft{Kind: DraftNew, Request: "good"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.Dir(), "20260101T000000-deadbeef.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.Dir(), "notes.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := d.List(30)
	if len(got) != 1 || got[0].ID != good.ID {
		t.Fatalf("List = %+v, want only the good draft", got)
	}
}

func TestDraftsRejectAnIDThatIsAPath(t *testing.T) {
	d := newTestDrafts(t)
	outside := filepath.Join(filepath.Dir(d.Dir()), "precious.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete("../precious"); err == nil {
		t.Error("Delete took an ID with a path in it")
	}
	if _, err := d.Save(Draft{ID: "../precious", Request: "x"}); err == nil {
		t.Error("Save took an ID with a path in it")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("the file outside the folder is gone: %v", err)
	}
}

// A saved request is the request a runbook was created or changed with: not
// a draft, one per runbook and kind, the newest kept.
func TestSavedRequests(t *testing.T) {
	d := newTestDrafts(t)
	start := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return start }
	if _, err := d.Save(Draft{Kind: DraftNew, State: StateCreated, Runbook: "deploy", Request: "write it"}); err != nil {
		t.Fatal(err)
	}
	d.now = func() time.Time { return start.Add(time.Minute) }
	if _, err := d.Save(Draft{Kind: DraftEdit, State: StateCreated, Runbook: "deploy", Request: "change it"}); err != nil {
		t.Fatal(err)
	}
	if got := d.List(30); len(got) != 0 {
		t.Fatalf("saved requests are listed as drafts: %+v", got)
	}
	if r, ok := d.Request("deploy", DraftNew); !ok || r.Request != "write it" {
		t.Errorf("Request(deploy, new) = %+v, %v", r, ok)
	}
	if r, ok := d.Request("deploy", ""); !ok || r.Request != "change it" {
		t.Errorf("Request(deploy, any) = %+v, %v; want the newest", r, ok)
	}
	if _, ok := d.Request("other", ""); ok {
		t.Error("a runbook without a request has one")
	}

	d.now = func() time.Time { return start.Add(time.Hour) }
	if _, err := d.Save(Draft{Kind: DraftEdit, State: StateCreated, Runbook: "deploy", Request: "change it again"}); err != nil {
		t.Fatal(err)
	}
	if r, _ := d.Request("deploy", DraftEdit); r.Request != "change it again" {
		t.Errorf("the newer request should replace the older one: %+v", r)
	}
	if n := countFiles(t, d.Dir()); n != 2 {
		t.Errorf("%d files on disk, want one per runbook and kind", n)
	}

	if err := d.DropRequest("deploy", DraftEdit); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Request("deploy", DraftEdit); ok {
		t.Error("DropRequest left the request of the change")
	}
	if _, ok := d.Request("deploy", DraftNew); !ok {
		t.Error("DropRequest of the change took the request of the new runbook too")
	}

	if err := d.Forget("deploy"); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Request("deploy", ""); ok {
		t.Error("Forget left a request of deploy")
	}
	if err := d.Delete(""); err != nil {
		t.Errorf("deleting no draft: %v", err)
	}
}

func TestNoDraftsWithoutAFolder(t *testing.T) {
	var d *Drafts
	if NewDrafts("") != nil {
		t.Error("NewDrafts(\"\") should keep nothing")
	}
	if got := d.List(30); got != nil {
		t.Errorf("List = %v", got)
	}
	if _, err := d.Save(Draft{Request: "x"}); err == nil {
		t.Error("saving into no folder should say it was not kept")
	}
	if _, ok := d.Request("deploy", ""); ok {
		t.Error("Request found something in no folder")
	}
	if err := d.Delete("x"); err != nil {
		t.Error(err)
	}
	if err := d.Forget("deploy"); err != nil {
		t.Error(err)
	}
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// Deleting a runbook deletes what was asked of it, so a runbook given the
// name later is offered nothing it was not asked.
func TestRegistryDeleteForgetsTheSavedRequest(t *testing.T) {
	d := newDeleteDen(t, "")
	drafts := newTestDrafts(t)
	d.reg.SetDrafts(drafts)
	folder, err := d.reg.NewRunbook("deploy", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.reg.SaveDraft(Draft{Kind: DraftNew, State: StateCreated, Runbook: "deploy", Folder: folder, Request: "write it"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.reg.SaveDraft(Draft{Kind: DraftEdit, Runbook: "deploy", Request: "half a change"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.reg.SavedRequest("deploy", DraftNew); !ok {
		t.Fatal("the saved request is not found")
	}
	if _, err := d.reg.Delete("deploy"); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.reg.SavedRequest("deploy", ""); ok {
		t.Error("the deleted runbook's request is still kept")
	}
	if got := d.reg.Drafts(); len(got) != 0 {
		t.Errorf("a draft of a change to the deleted runbook is left: %+v", got)
	}
}

// A saved request whose runbook's folder went while den was not looking goes
// too; one whose runbook only fails to load stays, since recovering from an
// agent that left a broken runbook.yaml is what it is kept for.
func TestSetDraftsDropsRequestsWhoseFolderIsGone(t *testing.T) {
	d := newDeleteDen(t, "")
	drafts := newTestDrafts(t)
	broken := writeFolder(t, d.mine, "broken", "command: ./run.sh\nunknown_key: 1\n", nil)
	for _, dr := range []Draft{
		{Kind: DraftNew, State: StateCreated, Runbook: "gone", Folder: filepath.Join(d.mine, "gone"), Request: "a"},
		{Kind: DraftNew, State: StateCreated, Runbook: "broken", Folder: broken, Request: "b"},
		{Kind: DraftNew, Name: "unfinished", Request: "c"},
	} {
		if _, err := drafts.Save(dr); err != nil {
			t.Fatal(err)
		}
	}
	d.reg.Rescan()
	d.reg.SetDrafts(drafts)
	if _, ok := d.reg.SavedRequest("gone", ""); ok {
		t.Error("the request of a runbook whose folder is gone is kept")
	}
	if _, ok := d.reg.SavedRequest("broken", ""); !ok {
		t.Error("the request of a runbook that does not load was dropped")
	}
	if got := d.reg.Drafts(); len(got) != 1 {
		t.Errorf("drafts = %+v, want the unfinished one", got)
	}
}

func TestRegistryDraftSettingsFollowTheConfig(t *testing.T) {
	d := newDeleteDen(t, "  drafts:\n    keep_request: never\n    days: 1\n")
	drafts := newTestDrafts(t)
	drafts.now = func() time.Time { return time.Now().AddDate(0, 0, -3) }
	if _, err := drafts.Save(Draft{Kind: DraftNew, Request: "old"}); err != nil {
		t.Fatal(err)
	}
	drafts.now = time.Now
	d.reg.SetDrafts(drafts)
	if got := d.reg.KeepRequest(); got != "never" {
		t.Errorf("KeepRequest = %q, want never", got)
	}
	if !d.reg.KeepsDrafts() {
		t.Error("a registry with a store should keep drafts")
	}
	if got := d.reg.Drafts(); len(got) != 0 {
		t.Errorf("days: 1 should drop a draft of three days ago: %+v", got)
	}
	none := newDeleteDen(t, "")
	if none.reg.KeepsDrafts() || none.reg.KeepRequest() != "until-deleted" {
		t.Error("without SetDrafts nothing is kept; keep_request defaults to until-deleted")
	}
}
