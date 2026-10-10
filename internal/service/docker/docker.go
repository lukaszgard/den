package docker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/process"
	"github.com/lukaszgard/den/internal/service"
)

// Service implements service.Service for docker-compose environments.
type Service struct {
	mu      sync.RWMutex
	spec    *config.DockerSpec
	name    string
	sup     *process.Supervisor
	state   service.RuntimeState
	updates chan service.StatusUpdate
	cancel  context.CancelFunc
}

func New(name string, spec *config.DockerSpec) *Service {
	return &Service{
		spec:    spec,
		name:    name,
		sup:     process.NewSupervisor(200),
		updates: make(chan service.StatusUpdate, 32),
		state:   service.RuntimeState{Status: service.StatusIdle},
	}
}

func (s *Service) Info() service.ServiceInfo {
	return service.ServiceInfo{Name: s.name, Type: service.TypeDocker}
}

func (s *Service) State() service.RuntimeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.state
	return st
}

func (s *Service) LogWriter() io.Writer { return s.sup.LogWriter() }

func (s *Service) Logs() []string { return s.sup.Logs() }

func (s *Service) LogSeq() uint64 { return s.sup.LogSeq() }

func (s *Service) ConnectCommand() string { return "docker compose ps" }

func (s *Service) Start(ctx context.Context) (<-chan service.StatusUpdate, error) {
	tunnelCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	go s.run(tunnelCtx)
	return s.updates, nil
}

func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	_ = s.sup.Stop()
	s.mu.Lock()
	s.state = service.RuntimeState{Status: service.StatusIdle}
	s.mu.Unlock()
	return nil
}

func (s *Service) RefreshToken(_ context.Context) error { return nil }

func (s *Service) run(ctx context.Context) {
	s.setState(service.RuntimeState{Status: service.StatusConnecting})
	s.log("Starting docker compose...")

	args := []string{"compose", "-f", s.spec.ComposeFile}
	if s.spec.EnvFile != "" {
		args = append(args, "--env-file", s.spec.EnvFile)
	}
	args = append(args, "up", "-d")
	if len(s.spec.Services) > 0 {
		args = append(args, s.spec.Services...)
	}

	err := s.sup.Start(ctx, "docker", args...)
	if err != nil {
		s.setState(service.RuntimeState{Status: service.StatusError, Error: err})
		return
	}

	// Wait for compose to finish (it exits after bringing up services)
	for ev := range s.sup.Events() {
		if ev.Kind == process.EventExited {
			if ev.Err != nil {
				s.setState(service.RuntimeState{Status: service.StatusError, Error: ev.Err})
				return
			}
			break
		}
	}

	now := time.Now()
	svcList := strings.Join(s.spec.Services, ", ")
	if svcList == "" {
		svcList = "all services"
	}
	s.log(fmt.Sprintf("docker compose up: %s", svcList))
	s.setState(service.RuntimeState{Status: service.StatusConnected, StartedAt: &now})
}

func (s *Service) log(msg string) {
	fmt.Fprintf(s.sup.LogWriter(), "[docker] %s\n", msg)
}

func (s *Service) setState(state service.RuntimeState) {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
	st := state
	select {
	case s.updates <- service.StatusUpdate{Info: s.Info(), State: st}:
	default:
	}
}
