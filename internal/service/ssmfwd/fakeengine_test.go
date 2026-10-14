package ssmfwd

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// fakeEngine stands in for a service's engine. It is in a file of its own,
// with no build tag, because tests that build on Windows use it too.
type fakeEngine struct {
	ports   []int
	credErr error
	ttls    []time.Duration // lifetime of each mint in turn; 15 min past the end

	mu    sync.Mutex
	mints int
}

func (e *fakeEngine) Type() string           { return "fake" }
func (e *fakeEngine) UserPort() int          { return e.ports[0] }
func (e *fakeEngine) ConnectCommand() string { return fmt.Sprintf("client -p %d", e.ports[0]) }

func (e *fakeEngine) Forwards() ([]Forward, error) {
	var fwds []Forward
	for i, p := range e.ports {
		fwds = append(fwds, Forward{RemoteHost: fmt.Sprintf("node-%d.internal", i), RemotePort: 6379, LocalPort: p})
	}
	return fwds, nil
}

func (e *fakeEngine) Credential(ctx context.Context, log func(string)) (Credential, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.credErr != nil {
		return Credential{}, e.credErr
	}
	ttl := 15 * time.Minute
	if e.mints < len(e.ttls) {
		ttl = e.ttls[e.mints]
	}
	e.mints++
	return Credential{Secret: fmt.Sprintf("token-%d", e.mints), Expires: time.Now().Add(ttl)}, nil
}

func (e *fakeEngine) mintCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mints
}

func (e *fakeEngine) ConnectCommandWithSecret(secret string) string {
	return e.ConnectCommand() + " -a " + secret
}
