package service

import (
	"context"
	"fmt"
	"reflect"
	"sync"
)

// Manager owns the registered services for the life of the app, and fans
// their status events into one channel for the TUI.
//
// A config reload does not replace the Manager: Reconcile swaps in the new set
// of services and keeps the instances — and so the live connections — of every
// service whose configuration did not change.
type Manager struct {
	mu       sync.RWMutex
	services []*managed // in display order
	updates  chan StatusUpdate

	// done is closed by Close: it ends the copiers and releases whoever waits
	// on Updates through Done. updates itself is never closed, so a copier
	// that is mid-send can never panic.
	done      chan struct{}
	closeOnce sync.Once
}

// managed is one registered service and what the Manager tracks about it.
type managed struct {
	key  string
	svc  Service
	spec any // the configuration it was built from; nil for ad-hoc services

	// forwarding is set once a goroutine is copying the service's updates into
	// the shared channel. Services hand out the same channel on every Start,
	// so exactly one copier may read it: two would race and could deliver a
	// service's events out of order.
	forwarding bool
	// removed is closed when the service leaves the Manager, ending its copier.
	removed chan struct{}
}

// Entry describes one service the configuration asks for.
type Entry struct {
	// Key identifies the service across reloads; Reconcile keeps an instance
	// only when an entry with the same Key and an equal Spec is still present.
	Key string
	// Spec is the configuration the service is built from, compared with
	// reflect.DeepEqual.
	Spec any
	// Build creates the service. It is only called for new or changed entries.
	Build func() (Service, error)
}

// ReconcileResult reports what Reconcile did.
type ReconcileResult struct {
	Kept    int     // unchanged services whose instances were kept
	Added   int     // services built from new or changed entries
	Removed int     // services dropped because their entry is gone or changed
	Stopped int     // of those, the ones that were running and had to be stopped
	Errors  []error // entries whose Build failed; they are left out
}

// NewManager creates an empty Manager.
func NewManager() *Manager {
	return &Manager{updates: make(chan StatusUpdate, 128), done: make(chan struct{})}
}

// Close ends the Manager's goroutines: the copier of every service's events,
// and the start reports of StartAll. It does not stop the services — StopAll
// does that — and a Manager is not used after Close.
func (m *Manager) Close() {
	m.closeOnce.Do(func() { close(m.done) })
}

// Done is closed by Close. Whoever blocks on Updates selects on it too, so it
// is not left waiting for an event that will never come.
func (m *Manager) Done() <-chan struct{} {
	return m.done
}

// Key is the default identity of a service: its type and name.
func Key(info ServiceInfo) string { return info.Type + ":" + info.Name }

// Register adds a service that is not tracked against configuration, such as
// the one-off services `den connect` and `den create-env` build.
func (m *Manager) Register(svc Service) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.services = append(m.services, &managed{
		key:     uniqueKey(m.keys(), Key(svc.Info())),
		svc:     svc,
		removed: make(chan struct{}),
	})
}

// Reconcile makes the registered services match entries, in entries' order.
// Services whose Key and Spec are unchanged keep their instance, running or
// not. Every other registered service is stopped if it is running and removed;
// new and changed entries are built.
func (m *Manager) Reconcile(ctx context.Context, entries []Entry) ReconcileResult {
	var res ReconcileResult

	m.mu.Lock()
	old := make(map[string]*managed, len(m.services))
	for _, mg := range m.services {
		old[mg.key] = mg
	}
	m.mu.Unlock()

	wanted := byKey(entries)

	// Stop and drop what is going away before building its replacement, so a
	// changed service never holds its old local port while the new one starts.
	var dropped []*managed
	for _, mg := range old {
		if !unchanged(mg, wanted) {
			dropped = append(dropped, mg)
		}
	}
	for _, mg := range dropped {
		if running(mg.svc) {
			_ = mg.svc.Stop(ctx)
			res.Stopped++
		}
		close(mg.removed)
		res.Removed++
	}

	next := make([]*managed, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		if seen[e.Key] {
			res.Errors = append(res.Errors, fmt.Errorf("duplicate service %q", e.Key))
			continue
		}
		seen[e.Key] = true
		if mg, ok := old[e.Key]; ok && unchanged(mg, wanted) {
			next = append(next, mg)
			res.Kept++
			continue
		}
		svc, err := e.Build()
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("%s: %w", e.Key, err))
			continue
		}
		next = append(next, &managed{key: e.Key, svc: svc, spec: e.Spec, removed: make(chan struct{})})
		res.Added++
	}

	m.mu.Lock()
	m.services = next
	m.mu.Unlock()
	return res
}

// WouldStop names the running services Reconcile(entries) would stop, in
// display order, without changing anything: those whose entry is gone or
// changed. It lets the caller ask before a reload cuts a live connection.
func (m *Manager) WouldStop(entries []Entry) []string {
	wanted := byKey(entries)
	m.mu.RLock()
	var going []Service
	for _, mg := range m.services {
		if !unchanged(mg, wanted) {
			going = append(going, mg.svc)
		}
	}
	m.mu.RUnlock()
	var names []string
	for _, svc := range going {
		if running(svc) {
			names = append(names, svc.Info().Name)
		}
	}
	return names
}

func byKey(entries []Entry) map[string]Entry {
	wanted := make(map[string]Entry, len(entries))
	for _, e := range entries {
		wanted[e.Key] = e
	}
	return wanted
}

// unchanged reports whether wanted still has mg's entry as it was, which is
// the only case in which Reconcile keeps mg's instance. Reconcile and
// WouldStop both decide through it, so a reload never stops a service the
// question before it did not name.
func unchanged(mg *managed, wanted map[string]Entry) bool {
	e, ok := wanted[mg.key]
	return ok && mg.spec != nil && reflect.DeepEqual(mg.spec, e.Spec)
}

// Services returns a snapshot of the registered services, in display order.
func (m *Manager) Services() []Service {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Service, len(m.services))
	for i, mg := range m.services {
		out[i] = mg.svc
	}
	return out
}

// Updates returns the fan-in channel of every registered service's events.
func (m *Manager) Updates() <-chan StatusUpdate {
	return m.updates
}

// StartAll starts every registered service.
func (m *Manager) StartAll(ctx context.Context) {
	for _, svc := range m.Services() {
		go func(s Service) {
			if err := m.StartOne(ctx, s); err != nil {
				select {
				case m.updates <- StatusUpdate{Info: s.Info(), State: RuntimeState{Status: StatusError, Error: err}}:
				case <-ctx.Done():
				case <-m.done:
				}
			}
		}(svc)
	}
}

// StartOne starts svc and makes sure its events reach Updates.
func (m *Manager) StartOne(ctx context.Context, svc Service) error {
	ch, err := svc.Start(ctx)
	if err != nil {
		return err
	}
	m.forward(svc, ch)
	return nil
}

// forward starts the one goroutine that copies svc's events into the shared
// channel, unless it is already running. It stops when svc leaves the Manager
// or the Manager is closed.
func (m *Manager) forward(svc Service, ch <-chan StatusUpdate) {
	m.mu.Lock()
	mg := m.lookup(svc)
	if mg == nil || mg.forwarding {
		m.mu.Unlock()
		return
	}
	mg.forwarding = true
	removed := mg.removed
	m.mu.Unlock()

	go func() {
		for {
			select {
			case u := <-ch:
				select {
				case m.updates <- u:
				case <-removed:
					return
				case <-m.done:
					return
				}
			case <-removed:
				return
			case <-m.done:
				return
			}
		}
	}()
}

// StopAll stops every registered service and waits for them.
func (m *Manager) StopAll(ctx context.Context) {
	var wg sync.WaitGroup
	for _, svc := range m.Services() {
		wg.Add(1)
		go func(s Service) {
			defer wg.Done()
			_ = s.Stop(ctx)
		}(svc)
	}
	wg.Wait()
}

func (m *Manager) lookup(svc Service) *managed {
	for _, mg := range m.services {
		if mg.svc == svc {
			return mg
		}
	}
	return nil
}

func (m *Manager) keys() map[string]bool {
	keys := make(map[string]bool, len(m.services))
	for _, mg := range m.services {
		keys[mg.key] = true
	}
	return keys
}

// uniqueKey returns key, or key with a "#n" suffix when it is already taken.
func uniqueKey(taken map[string]bool, key string) string {
	if !taken[key] {
		return key
	}
	for n := 2; ; n++ {
		k := fmt.Sprintf("%s#%d", key, n)
		if !taken[k] {
			return k
		}
	}
}

// running reports whether svc is up or on its way up.
func running(svc Service) bool {
	switch svc.State().Status {
	case StatusConnecting, StatusConnected, StatusReconnecting:
		return true
	}
	return false
}
