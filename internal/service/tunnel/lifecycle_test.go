//go:build !windows

package tunnel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
)

// A tunnel waiting to reconnect still owns its loop. Starting it again used to
// launch a second loop over the same supervisor; Stop used to return before the
// loop had exited.
func TestReconnectingTunnelRefusesSecondStartAndStopJoinsLoop(t *testing.T) {
	s := New(config.TunnelSpec{Name: "t", Command: "exit 1", Reconnect: true})
	if _, err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for s.State().Status != service.StatusReconnecting {
		if time.Now().After(deadline) {
			t.Fatalf("status = %v, want reconnecting", s.State().Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if _, err := s.Start(context.Background()); !errors.Is(err, service.ErrAlreadyRunning) {
		t.Fatalf("Start while reconnecting err = %v, want ErrAlreadyRunning", err)
	}

	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case <-s.done:
	default:
		t.Fatal("Stop returned while the loop was still running")
	}
	if st := s.State(); st.Status != service.StatusIdle {
		t.Fatalf("status after Stop = %v, want idle", st.Status)
	}
}
