package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/secrets"
	"github.com/lukaszgard/den/internal/tui/msgs"
	"github.com/lukaszgard/den/internal/tui/testkeys"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const ssoExpired = "SSO login expired — log in from the AWS pane"

// awsItem stands for a secret read through an AWS profile, or (aws false) for
// one that could be but is not, like a SOPS file with an age key only. It
// counts its probes and answers them from st, which a test flips when the
// login arrives.
type awsItem struct {
	fakeItem
	aws    bool
	st     *secrets.Status
	probes *int
}

func (a awsItem) Probe() secrets.Status { *a.probes++; return *a.st }
func (a awsItem) UsesAWS() bool         { return a.aws }

func newAWSItem(name string, aws bool, st secrets.Status) awsItem {
	return awsItem{name: name, aws: aws, st: &st, probes: new(int)}
}

// awsLogins is an AWS config without a login, and a function that logs in:
// it writes the token `aws sso login` would.
func awsLogins(t *testing.T) (logins *secrets.AWSLogins, login func()) {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	if err := os.WriteFile(cfg, []byte("[profile p]\nsso_start_url = https://corp/start\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "sso", "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	n := 0
	return secrets.NewAWSLogins(cfg), func() {
		t.Helper()
		n++
		tok := fmt.Sprintf(`{"startUrl":"https://corp/start","accessToken":"x","expiresAt":%q}`,
			time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
		file := filepath.Join(cache, "tok.json")
		if err := os.WriteFile(file, []byte(tok), 0o600); err != nil {
			t.Fatal(err)
		}
		// Two logins within the file system's clock tick must still differ.
		mod := time.Now().Add(time.Duration(n) * time.Second)
		if err := os.Chtimes(file, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
}

// recheck runs one pass of RecheckAWS to its end.
func recheck(t *testing.T, m Model) Model {
	t.Helper()
	m, cmd := m.RecheckAWS()
	if cmd == nil {
		t.Fatal("RecheckAWS did not start a pass")
	}
	m, next := m.Update(cmd(), false)
	if next != nil {
		t.Fatal("a pass nothing interrupted must not start another")
	}
	return m
}

func TestRecheckAWSFollowsALogin(t *testing.T) {
	logins, login := awsLogins(t)
	kms := newAWSItem("kms", true, secrets.Status{Reason: ssoExpired})
	age := newAWSItem("age", false, secrets.Status{Ready: true})
	plain := fakeItem{name: "keepass", status: secrets.Status{Ready: true}}
	m := NewWithAWS([]secrets.Item{kms, age, plain}, logins, 100, 30)
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), ssoExpired) {
		t.Fatal("before the login the row must say why it cannot be read")
	}

	// Nothing changed: asking again must not probe a single item.
	m = recheck(t, m)
	if *kms.probes != 1 || *age.probes != 1 {
		t.Fatalf("probes without a change: kms %d, age %d, want 1 each (from New)", *kms.probes, *age.probes)
	}

	login()
	*kms.st = secrets.Status{Ready: true}
	m = recheck(t, m)
	if strings.Contains(ansi.Strip(m.View(100, 30, true)), ssoExpired) {
		t.Fatal("after the login the row must turn ready")
	}
	if *kms.probes != 2 {
		t.Errorf("kms probed %d times, want once more after the login", *kms.probes)
	}
	if *age.probes != 1 {
		t.Errorf("an item that does not use AWS was probed again (%d times)", *age.probes)
	}

	// The login is now what the statuses reflect: no further probes.
	recheck(t, m)
	if *kms.probes != 2 {
		t.Errorf("kms probed %d times, want none without a new change", *kms.probes)
	}
}

// A decrypt that failed under the old login must not keep the row red once
// the login changed and the probe passes.
func TestRecheckAWSClearsAStaleDecryptFailure(t *testing.T) {
	logins, login := awsLogins(t)
	readErr := errors.New("sops: AccessDenied")
	kms := newAWSItem("kms", true, secrets.Status{Ready: true})
	kms.err = &readErr
	m := NewWithAWS([]secrets.Item{kms}, logins, 100, 30)
	m, cmd := m.Update(testkeys.Press("enter"), true)
	m, _ = m.Update(cmd(), true)
	m = m.Hide()
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), "decrypt failed") {
		t.Fatal("the failed read must mark the row")
	}

	login()
	m = recheck(t, m)
	if v := ansi.Strip(m.View(100, 30, true)); strings.Contains(v, "decrypt failed") || !strings.Contains(v, "ready") {
		t.Fatalf("after the login the row must be ready again:\n%s", v)
	}
}

// Events can arrive faster than a pass takes. They must not pile up passes:
// one runs, and one more afterwards picks up whatever changed meanwhile.
func TestRecheckAWSRunsOnePassAtATime(t *testing.T) {
	logins, login := awsLogins(t)
	kms := newAWSItem("kms", true, secrets.Status{Reason: ssoExpired})
	m := NewWithAWS([]secrets.Item{kms}, logins, 100, 30)

	m, first := m.RecheckAWS()
	if first == nil {
		t.Fatal("RecheckAWS did not start a pass")
	}
	for range 3 {
		var cmd tea.Cmd
		if m, cmd = m.RecheckAWS(); cmd != nil {
			t.Fatal("a second pass started while one was running")
		}
	}
	firstResult := first() // saw no login
	login()
	*kms.st = secrets.Status{Ready: true}

	m, again := m.Update(firstResult, false)
	if again == nil {
		t.Fatal("the triggers that arrived during the pass must get one more pass")
	}
	m, last := m.Update(again(), false)
	if last != nil {
		t.Fatal("three triggers during a pass must cost one more pass, not three")
	}
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), "ready") {
		t.Fatal("the follow-up pass must have seen the login")
	}
}

// A config reload builds a new pane, possibly with other items. A pass the
// old pane started must not write its statuses into the new one.
func TestRecheckAWSIgnoresAnotherPanesPass(t *testing.T) {
	logins, login := awsLogins(t)
	old := newAWSItem("old", true, secrets.Status{Reason: ssoExpired})
	before := NewWithAWS([]secrets.Item{old}, logins, 100, 30)
	_, cmd := before.RecheckAWS()
	login()
	*old.st = secrets.Status{Ready: true}
	stale := cmd()

	other := fakeItem{name: "other", status: secrets.Status{Reason: "file not found"}}
	after := NewWithAWS([]secrets.Item{other}, logins, 100, 30)
	after, _ = after.Update(stale, false)
	if !strings.Contains(ansi.Strip(after.View(100, 30, true)), "file not found") {
		t.Fatal("a pass of the replaced pane changed the new pane's statuses")
	}
}

func TestTickPollsAWSLoginsEveryFewSeconds(t *testing.T) {
	logins, login := awsLogins(t)
	kms := newAWSItem("kms", true, secrets.Status{Reason: ssoExpired})
	m := NewWithAWS([]secrets.Item{kms}, logins, 100, 30)
	t0 := time.Now()

	m, cmd := m.Update(msgs.TickMsg{Time: t0}, false)
	if cmd == nil {
		t.Fatal("the first tick on screen must check the logins")
	}
	m, _ = m.Update(cmd(), false)

	// Logged in from another terminal: no event tells den.
	login()
	*kms.st = secrets.Status{Ready: true}
	if _, cmd := m.Update(msgs.TickMsg{Time: t0.Add(time.Second)}, false); cmd != nil {
		t.Fatal("the logins must not be checked on every tick")
	}
	m, cmd = m.Update(msgs.TickMsg{Time: t0.Add(awsPollEvery)}, false)
	if cmd == nil {
		t.Fatal("after the poll interval the logins must be checked again")
	}
	m, _ = m.Update(cmd(), false)
	if !strings.Contains(ansi.Strip(m.View(100, 30, true)), "ready") {
		t.Fatal("a login made outside den must show up")
	}
}

func TestPaneWithoutAWSLoginsNeverRechecks(t *testing.T) {
	m := New([]secrets.Item{fakeItem{name: "x", status: secrets.Status{Ready: true}}}, 100, 30)
	if _, cmd := m.RecheckAWS(); cmd != nil {
		t.Error("RecheckAWS without logins must do nothing")
	}
	if _, cmd := m.Update(msgs.TickMsg{Time: time.Now()}, false); cmd != nil {
		t.Error("a tick without logins must do nothing")
	}
}
