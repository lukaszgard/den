// Package kafka is a placeholder for Amazon MSK. den does not tunnel to Kafka
// yet: clients dial the brokers' advertised hostnames, so one SSM forward to a
// single broker is not enough, and the per-broker design is still open. A
// configured kafka service is listed, and connecting it says so plainly rather
// than appearing to work.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/process"
	"github.com/lukaszgard/den/internal/service"
)

// ErrPlanned is what connecting a Kafka service reports.
var ErrPlanned = errors.New("kafka: not implemented yet — planned: Amazon MSK with IAM auth")

// Service is a configured Kafka entry that cannot connect yet.
type Service struct {
	name string
	spec *config.KafkaSpec
	port int
	logs *process.RingBuffer

	mu      sync.RWMutex
	state   service.RuntimeState
	updates chan service.StatusUpdate
}

func New(name string, spec *config.KafkaSpec) *Service {
	port := spec.LocalPort
	if port == 0 {
		port = config.DefaultLocalPort(config.ServiceTypeKafka)
	}
	return &Service{
		name:    name,
		spec:    spec,
		port:    port,
		logs:    process.NewRingBuffer(50),
		updates: make(chan service.StatusUpdate, 32),
		state:   service.RuntimeState{Status: service.StatusIdle, LocalPort: port},
	}
}

func (s *Service) Info() service.ServiceInfo {
	return service.ServiceInfo{Name: s.name, Type: service.TypeKafka, Environment: s.spec.Environment}
}

func (s *Service) State() service.RuntimeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *Service) Logs() []string { return s.logs.Lines() }

func (s *Service) LogSeq() uint64 { return s.logs.Seq() }

func (s *Service) ConnectCommand() string {
	return fmt.Sprintf("kafka-console-consumer --bootstrap-server localhost:%d --topic <topic>", s.port)
}

// Start reports ErrPlanned: there is no Kafka tunnel yet.
func (s *Service) Start(context.Context) (<-chan service.StatusUpdate, error) {
	s.logs.Add(ErrPlanned.Error())
	s.setState(service.RuntimeState{Status: service.StatusError, Error: ErrPlanned, LocalPort: s.port})
	return s.updates, ErrPlanned
}

func (s *Service) Stop(context.Context) error {
	s.setState(service.RuntimeState{Status: service.StatusIdle, LocalPort: s.port})
	return nil
}

func (s *Service) RefreshToken(context.Context) error { return nil }

func (s *Service) setState(state service.RuntimeState) {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
	select {
	case s.updates <- service.StatusUpdate{Info: s.Info(), State: state}:
	default:
	}
}
