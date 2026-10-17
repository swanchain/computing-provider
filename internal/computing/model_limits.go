package computing

import (
	"fmt"
	"math"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/swanchain/computing-provider-v2/internal/runtime"
)

// ModelLimits are one model's own admission limits, from its models.json
// entry. Zero means "not set" and leaves the global [RequestLimits] in charge.
//
// They live in models.json rather than as runtime calls because a restart
// used to silently drop them: an operator who had matched a 31B model to its
// two llama.cpp slots came back after a reboot to a model declaring ten.
type ModelLimits struct {
	MaxConcurrent int     `json:"max_concurrent"`
	RPM           float64 `json:"rpm"`
}

// Limits are the admission limits a models.json entry carries.
func (m ModelMapping) Limits() ModelLimits {
	return ModelLimits{MaxConcurrent: m.MaxConcurrent, RPM: m.RPM}
}

func limitsOf(model *RegisteredModel) ModelLimits {
	return ModelLimits{MaxConcurrent: model.MaxConcurrent, RPM: model.RPM}
}

// rpmBurst is the token-bucket capacity for a per-minute limit: the whole
// minute's worth. An upstream that says "20 requests a minute" counts them
// over the minute, so twenty arriving together are within it; a burst of one
// would refuse nineteen of them for no reason the upstream would recognise.
func rpmBurst(rpm float64) int {
	if b := int(math.Ceil(rpm)); b > 1 {
		return b
	}
	return 1
}

// applyModelLimits puts a model's limits into the limiters, or returns the
// model to the defaults when they are zero.
func (s *InferenceService) applyModelLimits(modelID string, l ModelLimits) {
	if s.concurrencyLimiter != nil {
		if l.MaxConcurrent > 0 {
			s.concurrencyLimiter.SetModelMax(modelID, l.MaxConcurrent)
		} else {
			s.concurrencyLimiter.ClearModelMax(modelID)
		}
	}
	if s.rateLimiter != nil {
		if l.RPM > 0 {
			s.rateLimiter.SetModelLimit(modelID, l.RPM/60, rpmBurst(l.RPM))
		} else {
			s.rateLimiter.ClearModelLimit(modelID)
		}
	}
}

// redeclareModels sends the registration again with the model set unchanged,
// so the hub picks up a new concurrency capacity. updateClientModels will not
// do this: it registers only when the set of models changes, which is why a
// limit change used to need a disable/enable cycle to reach the hub.
func (s *InferenceService) redeclareModels() {
	if s.client == nil {
		return
	}
	s.registerMu.Lock()
	defer s.registerMu.Unlock()
	if !s.client.IsConnected() {
		return
	}
	if err := s.client.register(); err != nil {
		logs.GetLogger().Errorf("Re-registration after a limit change failed: %v", err)
	}
}

// SetModelLimits applies a model's limits now, writes them into its
// models.json entry so they survive a restart, and re-declares the model to
// the hub when its concurrency changed. The registry copy is updated first so
// the reload the write triggers finds nothing to do.
func (s *InferenceService) SetModelLimits(modelID string, l ModelLimits) error {
	if l.MaxConcurrent < 0 || l.RPM < 0 {
		return fmt.Errorf("limits must be zero (default) or positive")
	}
	if s.registry == nil {
		return fmt.Errorf("registry not initialized")
	}
	before, ok := s.registry.GetModel(modelID)
	if !ok {
		return fmt.Errorf("model %s not found", modelID)
	}
	s.registry.SetModelLimits(modelID, l)
	if m, ok := s.mapping(modelID); ok {
		m.MaxConcurrent, m.RPM = l.MaxConcurrent, l.RPM
		s.setMapping(modelID, m)
	}
	s.applyModelLimits(modelID, l)
	if err := runtime.SetModelLimits(s.registry.configPath, modelID, l.MaxConcurrent, l.RPM); err != nil {
		return fmt.Errorf("limits applied for this run, but not saved to models.json: %w", err)
	}
	if l.MaxConcurrent != before.MaxConcurrent {
		s.redeclareModels()
	}
	return nil
}

// ModelLimitsFor is what the model is held to right now.
func (s *InferenceService) ModelLimitsFor(modelID string) ModelLimits {
	if s.registry == nil {
		return ModelLimits{}
	}
	model, ok := s.registry.GetModel(modelID)
	if !ok {
		return ModelLimits{}
	}
	return limitsOf(model)
}

// ModelCapacities is the concurrency each model is declared to the hub with.
func (s *InferenceService) ModelCapacities() map[string]int {
	out := map[string]int{}
	if s.registry == nil || s.concurrencyLimiter == nil {
		return out
	}
	for _, model := range s.registry.GetAllModels() {
		out[model.ID] = s.concurrencyLimiter.ModelCapacity(model.ID)
	}
	return out
}
