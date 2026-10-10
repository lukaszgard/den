package kafka

import (
	"context"
	"errors"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
)

// A kafka entry used to show "connecting", log one line and fall back to idle,
// which looked like a flaky tunnel rather than a missing feature.
func TestConnectingSaysKafkaIsPlanned(t *testing.T) {
	s := New("msk", &config.KafkaSpec{LocalPort: 19092})
	if _, err := s.Start(context.Background()); !errors.Is(err, ErrPlanned) {
		t.Fatalf("Start err = %v, want ErrPlanned", err)
	}
	if st := s.State(); st.Status != service.StatusError || !errors.Is(st.Error, ErrPlanned) {
		t.Fatalf("state = %+v, want an error naming the plan", st)
	}
	if err := s.Stop(context.Background()); err != nil || s.State().Status != service.StatusIdle {
		t.Fatalf("Stop: %v, status %v", err, s.State().Status)
	}
}
