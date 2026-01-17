package computing

import (
	"runtime/debug"
	"sync"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
)

// RestartableService defines the interface for services that can be monitored and restarted
type RestartableService interface {
	Start() error
	Stop()
	IsHealthy() bool
	Name() string
}

// SupervisorConfig configures the supervisor behavior
type SupervisorConfig struct {
	HealthCheckInterval time.Duration // How often to check service health
	MaxRestartAttempts  int           // Maximum restart attempts before giving up (0 = unlimited)
	RestartBackoff      time.Duration // Initial backoff between restart attempts
	MaxRestartBackoff   time.Duration // Maximum backoff between restart attempts
}

// DefaultSupervisorConfig returns sensible defaults
func DefaultSupervisorConfig() SupervisorConfig {
	return SupervisorConfig{
		HealthCheckInterval: 30 * time.Second,
		MaxRestartAttempts:  0, // Unlimited
		RestartBackoff:      5 * time.Second,
		MaxRestartBackoff:   5 * time.Minute,
	}
}

// ServiceState tracks the state of a supervised service
type ServiceState struct {
	Name           string
	Healthy        bool
	RestartCount   int
	LastRestartAt  time.Time
	LastHealthyAt  time.Time
	CurrentBackoff time.Duration
}

// Supervisor monitors and restarts services when they become unhealthy
type Supervisor struct {
	mu       sync.RWMutex
	config   SupervisorConfig
	services map[string]RestartableService
	states   map[string]*ServiceState
	stopCh   chan struct{}
	running  bool
	wg       sync.WaitGroup
}

// NewSupervisor creates a new service supervisor
func NewSupervisor(config SupervisorConfig) *Supervisor {
	return &Supervisor{
		config:   config,
		services: make(map[string]RestartableService),
		states:   make(map[string]*ServiceState),
		stopCh:   make(chan struct{}),
	}
}

// Register adds a service to be supervised
func (s *Supervisor) Register(service RestartableService) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name := service.Name()
	s.services[name] = service
	s.states[name] = &ServiceState{
		Name:           name,
		Healthy:        true,
		CurrentBackoff: s.config.RestartBackoff,
		LastHealthyAt:  time.Now(),
	}

	logs.GetLogger().Infof("[supervisor] Registered service: %s", name)
}

// Unregister removes a service from supervision
func (s *Supervisor) Unregister(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.services, name)
	delete(s.states, name)

	logs.GetLogger().Infof("[supervisor] Unregistered service: %s", name)
}

// Start begins the supervisor monitoring loop
func (s *Supervisor) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	s.wg.Add(1)
	go s.monitorLoop()

	logs.GetLogger().Info("[supervisor] Started service supervisor")
}

// Stop stops the supervisor
func (s *Supervisor) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stopCh)
	s.mu.Unlock()

	s.wg.Wait()
	logs.GetLogger().Info("[supervisor] Stopped service supervisor")
}

// monitorLoop periodically checks service health and restarts unhealthy services
func (s *Supervisor) monitorLoop() {
	defer s.wg.Done()
	defer func() {
		if err := recover(); err != nil {
			logs.GetLogger().Errorf("[supervisor] panic recovered in monitor loop: %v\n%s", err, debug.Stack())
		}
	}()

	ticker := time.NewTicker(s.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.checkServices()
		}
	}
}

// checkServices checks all registered services and restarts unhealthy ones
func (s *Supervisor) checkServices() {
	s.mu.RLock()
	services := make(map[string]RestartableService, len(s.services))
	for name, svc := range s.services {
		services[name] = svc
	}
	s.mu.RUnlock()

	for name, service := range services {
		s.checkService(name, service)
	}
}

// checkService checks a single service and restarts if unhealthy
func (s *Supervisor) checkService(name string, service RestartableService) {
	defer func() {
		if err := recover(); err != nil {
			logs.GetLogger().Errorf("[supervisor] panic checking service %s: %v", name, err)
		}
	}()

	healthy := service.IsHealthy()

	s.mu.Lock()
	state := s.states[name]
	if state == nil {
		s.mu.Unlock()
		return
	}

	wasHealthy := state.Healthy
	state.Healthy = healthy

	if healthy {
		state.LastHealthyAt = time.Now()
		// Reset backoff on healthy check
		state.CurrentBackoff = s.config.RestartBackoff
		s.mu.Unlock()
		return
	}

	// Service is unhealthy
	if wasHealthy {
		logs.GetLogger().Warnf("[supervisor] Service %s became unhealthy", name)
	}

	// Check if we should attempt restart
	if s.config.MaxRestartAttempts > 0 && state.RestartCount >= s.config.MaxRestartAttempts {
		logs.GetLogger().Errorf("[supervisor] Service %s exceeded max restart attempts (%d), giving up",
			name, s.config.MaxRestartAttempts)
		s.mu.Unlock()
		return
	}

	// Check backoff
	if time.Since(state.LastRestartAt) < state.CurrentBackoff {
		s.mu.Unlock()
		return
	}

	// Attempt restart
	state.RestartCount++
	state.LastRestartAt = time.Now()

	// Increase backoff for next attempt (exponential backoff)
	state.CurrentBackoff *= 2
	if state.CurrentBackoff > s.config.MaxRestartBackoff {
		state.CurrentBackoff = s.config.MaxRestartBackoff
	}

	restartCount := state.RestartCount
	s.mu.Unlock()

	logs.GetLogger().Infof("[supervisor] Restarting service %s (attempt %d)", name, restartCount)

	// Stop the service first
	func() {
		defer func() {
			if err := recover(); err != nil {
				logs.GetLogger().Errorf("[supervisor] panic stopping service %s: %v", name, err)
			}
		}()
		service.Stop()
	}()

	// Small delay before restart
	time.Sleep(time.Second)

	// Start the service
	err := func() error {
		defer func() {
			if err := recover(); err != nil {
				logs.GetLogger().Errorf("[supervisor] panic starting service %s: %v", name, err)
			}
		}()
		return service.Start()
	}()

	if err != nil {
		logs.GetLogger().Errorf("[supervisor] Failed to restart service %s: %v", name, err)
	} else {
		logs.GetLogger().Infof("[supervisor] Successfully restarted service %s", name)
	}
}

// GetServiceStates returns the current state of all supervised services
func (s *Supervisor) GetServiceStates() map[string]ServiceState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	states := make(map[string]ServiceState, len(s.states))
	for name, state := range s.states {
		states[name] = *state
	}
	return states
}

// GetServiceState returns the state of a specific service
func (s *Supervisor) GetServiceState(name string) (ServiceState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	state, exists := s.states[name]
	if !exists {
		return ServiceState{}, false
	}
	return *state, true
}

// ForceRestart forces a restart of a specific service
func (s *Supervisor) ForceRestart(name string) error {
	s.mu.RLock()
	service, exists := s.services[name]
	s.mu.RUnlock()

	if !exists {
		return ErrServiceNotFound
	}

	logs.GetLogger().Infof("[supervisor] Force restarting service %s", name)

	service.Stop()
	time.Sleep(time.Second)
	return service.Start()
}

// Errors
var (
	ErrServiceNotFound = &SupervisorError{Message: "service not found"}
)

type SupervisorError struct {
	Message string
}

func (e *SupervisorError) Error() string {
	return e.Message
}
