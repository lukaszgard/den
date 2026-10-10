package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeService is a Service whose status the test sets directly. Like the real
// services it hands out the same updates channel from every Start.
type fakeService struct {
	name    string
	updates chan StatusUpdate

	mu     sync.Mutex
	status Status
	stops  int
}

func newFake(name string) *fakeService {
	return &fakeService{name: name, updates: make(chan StatusUpdate, 32)}
}

// newManager is a Manager closed when the test ends, so no copier outlives it.
func newManager(t *testing.T) *Manager {
	m := NewManager()
	t.Cleanup(m.Close)
	return m
}

func (f *fakeService) Info() ServiceInfo { return ServiceInfo{Name: f.name, Type: "fake"} }
func (f *fakeService) State() RuntimeState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return RuntimeState{Status: f.status}
}
func (f *fakeService) Start(context.Context) (<-chan StatusUpdate, error) {
	f.set(StatusConnected)
	return f.updates, nil
}
func (f *fakeService) Stop(context.Context) error {
	f.mu.Lock()
	f.stops++
	f.mu.Unlock()
	f.set(StatusIdle)
	return nil
}
func (f *fakeService) RefreshToken(context.Context) error { return nil }
func (f *fakeService) ConnectCommand() string             { return "" }
func (f *fakeService) Logs() []string                     { return nil }
func (f *fakeService) LogSeq() uint64                     { return 0 }

func (f *fakeService) set(s Status) {
	f.mu.Lock()
	f.status = s
	f.mu.Unlock()
	f.updates <- StatusUpdate{Info: f.Info(), State: RuntimeState{Status: s}}
}

func (f *fakeService) stopCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stops
}

type spec struct{ Port int }

func entry(key string, s spec, built map[string]*fakeService) Entry {
	return Entry{Key: key, Spec: s, Build: func() (Service, error) {
		f := newFake(key)
		built[key] = f
		return f, nil
	}}
}

func TestReconcileKeepsUnchangedAndReplacesChanged(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	built := map[string]*fakeService{}
	m.Reconcile(ctx, []Entry{entry("a", spec{1}, built), entry("b", spec{2}, built), entry("c", spec{3}, built)})
	a, b, c := built["a"], built["b"], built["c"]
	if err := m.StartOne(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := m.StartOne(ctx, b); err != nil {
		t.Fatal(err)
	}

	// a unchanged and running, b changed while running, c removed, d new.
	res := m.Reconcile(ctx, []Entry{entry("d", spec{4}, built), entry("a", spec{1}, built), entry("b", spec{20}, built)})

	if res.Kept != 1 || res.Added != 2 || res.Removed != 2 || res.Stopped != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	got := m.Services()
	if len(got) != 3 || got[0] != built["d"] || got[1] != a || got[2] == b || got[2] != built["b"] {
		t.Fatalf("services not in entry order with a kept: %v", got)
	}
	if a.State().Status != StatusConnected || a.stopCount() != 0 {
		t.Fatal("an unchanged running service must keep running")
	}
	if b.stopCount() != 1 {
		t.Fatal("a changed running service must be stopped before its replacement")
	}
	if c.stopCount() != 0 {
		t.Fatal("an idle removed service needs no Stop")
	}
}

// WouldStop is the question asked before a reload: it must name exactly the
// services the Reconcile after it stops, and change nothing itself.
func TestWouldStopNamesWhatReconcileStops(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	built := map[string]*fakeService{}
	m.Reconcile(ctx, []Entry{entry("a", spec{1}, built), entry("b", spec{2}, built), entry("c", spec{3}, built), entry("d", spec{4}, built)})
	for _, k := range []string{"a", "b", "c"} {
		if err := m.StartOne(ctx, built[k]); err != nil {
			t.Fatal(err)
		}
	}

	// a unchanged, b changed, c removed — all three running; d changed but idle.
	next := []Entry{entry("a", spec{1}, built), entry("b", spec{20}, built), entry("d", spec{40}, built)}
	got := m.WouldStop(next)
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("WouldStop = %v, want [b c]", got)
	}
	if built["b"].stopCount() != 0 || len(m.Services()) != 4 {
		t.Fatal("WouldStop must not change anything")
	}
	if res := m.Reconcile(ctx, next); res.Stopped != len(got) {
		t.Fatalf("Reconcile stopped %d, WouldStop named %d", res.Stopped, len(got))
	}
	if m.WouldStop(next) != nil {
		t.Error("nothing is left to stop once the same entries are applied")
	}
}

func TestReconcileReportsBuildErrors(t *testing.T) {
	m := newManager(t)
	res := m.Reconcile(context.Background(), []Entry{
		{Key: "bad", Spec: spec{1}, Build: func() (Service, error) { return nil, errors.New("boom") }},
		{Key: "dup", Spec: spec{1}, Build: func() (Service, error) { return newFake("dup"), nil }},
		{Key: "dup", Spec: spec{2}, Build: func() (Service, error) { return newFake("dup"), nil }},
	})
	if len(res.Errors) != 2 || res.Added != 1 || len(m.Services()) != 1 {
		t.Fatalf("result = %+v, services = %d", res, len(m.Services()))
	}
}

// Services hand out one channel for their whole life. Starting a service a
// second time used to add a second copier on that channel, and the two could
// deliver its events out of order — "connecting" after "connected".
func TestRestartedServiceKeepsEventOrder(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	f := newFake("svc")
	m.Register(f)
	for range 3 {
		if err := m.StartOne(ctx, f); err != nil {
			t.Fatal(err)
		}
		if err := f.Stop(ctx); err != nil {
			t.Fatal(err)
		}
	}
	want := []Status{StatusConnected, StatusIdle, StatusConnected, StatusIdle, StatusConnected, StatusIdle}
	for i, w := range want {
		select {
		case u := <-m.Updates():
			if u.State.Status != w {
				t.Fatalf("event %d = %v, want %v", i, u.State.Status, w)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("event %d never arrived", i)
		}
	}
}

func TestRemovedServiceStopsForwarding(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	built := map[string]*fakeService{}
	m.Reconcile(ctx, []Entry{entry("a", spec{1}, built)})
	a := built["a"]
	if err := m.StartOne(ctx, a); err != nil {
		t.Fatal(err)
	}
	<-m.Updates() // connected

	m.Reconcile(ctx, []Entry{}) // stops a: its Idle may or may not be forwarded
	time.Sleep(50 * time.Millisecond)
	for len(m.Updates()) > 0 {
		<-m.Updates()
	}
	a.updates <- StatusUpdate{Info: a.Info(), State: RuntimeState{Status: StatusError}}
	select {
	case u := <-m.Updates():
		t.Fatalf("event from a removed service was forwarded: %+v", u)
	case <-time.After(100 * time.Millisecond):
	}
}

// Close ends the copiers of the services still registered, which would
// otherwise wait for their events for as long as the process lives.
func TestCloseStopsForwarding(t *testing.T) {
	ctx := context.Background()
	m := NewManager()
	f := newFake("svc")
	m.Register(f)
	if err := m.StartOne(ctx, f); err != nil {
		t.Fatal(err)
	}
	<-m.Updates() // connected

	m.Close()
	m.Close() // a second Close is harmless
	select {
	case <-m.Done():
	default:
		t.Fatal("Done is still open after Close")
	}
	time.Sleep(50 * time.Millisecond)
	f.updates <- StatusUpdate{Info: f.Info(), State: RuntimeState{Status: StatusError}}
	select {
	case u := <-m.Updates():
		t.Fatalf("event forwarded after Close: %+v", u)
	case <-time.After(100 * time.Millisecond):
	}
}
