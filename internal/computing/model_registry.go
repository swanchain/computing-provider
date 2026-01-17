package computing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/fsnotify/fsnotify"
)

// ModelState represents the current state of a model
type ModelState int

const (
	ModelStateUnknown ModelState = iota
	ModelStateLoading
	ModelStateReady
	ModelStateUnhealthy
	ModelStateDisabled
)

func (s ModelState) String() string {
	switch s {
	case ModelStateLoading:
		return "loading"
	case ModelStateReady:
		return "ready"
	case ModelStateUnhealthy:
		return "unhealthy"
	case ModelStateDisabled:
		return "disabled"
	default:
		return "unknown"
	}
}

// RegisteredModel represents a fully configured model in the registry
type RegisteredModel struct {
	ID          string       `json:"id"`
	Container   string       `json:"container"`
	Endpoint    string       `json:"endpoint"`
	GPUMemory   int          `json:"gpu_memory"`
	Category    string       `json:"category"`
	State       ModelState   `json:"state"`
	StateString string       `json:"state_string"`
	Health      ModelHealth  `json:"health"`
	HealthString string      `json:"health_string"`
	LoadedAt    time.Time    `json:"loaded_at,omitempty"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Enabled     bool         `json:"enabled"`
}

// ModelRegistry manages the lifecycle of model configurations
type ModelRegistry struct {
	mu            sync.RWMutex
	models        map[string]*RegisteredModel
	configPath    string
	healthChecker *ModelHealthChecker
	watcher       *fsnotify.Watcher
	stopCh        chan struct{}
	running       bool

	// Callbacks
	onModelAdded   func(model *RegisteredModel)
	onModelRemoved func(modelID string)
	onModelUpdated func(model *RegisteredModel)
}

// NewModelRegistry creates a new model registry
func NewModelRegistry(configPath string, healthChecker *ModelHealthChecker) *ModelRegistry {
	r := &ModelRegistry{
		models:        make(map[string]*RegisteredModel),
		configPath:    configPath,
		healthChecker: healthChecker,
		stopCh:        make(chan struct{}),
	}

	// Set up health status change callback
	if healthChecker != nil {
		healthChecker.SetStatusChangeCallback(r.onHealthStatusChange)
	}

	return r
}

// SetCallbacks sets callbacks for model lifecycle events
func (r *ModelRegistry) SetCallbacks(
	onAdded func(model *RegisteredModel),
	onRemoved func(modelID string),
	onUpdated func(model *RegisteredModel),
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onModelAdded = onAdded
	r.onModelRemoved = onRemoved
	r.onModelUpdated = onUpdated
}

// Start loads initial configuration and begins watching for changes
func (r *ModelRegistry) Start() error {
	// Load initial configuration
	if err := r.loadConfig(); err != nil {
		logs.GetLogger().Warnf("Failed to load initial model config: %v", err)
	}

	// Start file watcher for hot-reload
	if err := r.startWatcher(); err != nil {
		logs.GetLogger().Warnf("Failed to start config watcher: %v", err)
	}

	r.mu.Lock()
	r.running = true
	r.mu.Unlock()

	logs.GetLogger().Info("Model registry started")
	return nil
}

// Stop stops the registry and file watcher
func (r *ModelRegistry) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	r.running = false
	close(r.stopCh)
	r.mu.Unlock()

	if r.watcher != nil {
		r.watcher.Close()
	}

	logs.GetLogger().Info("Model registry stopped")
}

// loadConfig loads model configuration from models.json
func (r *ModelRegistry) loadConfig() error {
	modelsPath := filepath.Join(r.configPath, "models.json")
	data, err := os.ReadFile(modelsPath)
	if err != nil {
		if os.IsNotExist(err) {
			logs.GetLogger().Infof("No models.json found at %s", modelsPath)
			return nil
		}
		return err
	}

	var mappings map[string]ModelMapping
	if err := json.Unmarshal(data, &mappings); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Track existing models for removal detection
	existingModels := make(map[string]bool)
	for id := range r.models {
		existingModels[id] = true
	}

	now := time.Now()

	// Process loaded models
	for modelID, mapping := range mappings {
		existingModel, exists := r.models[modelID]

		if exists {
			// Update existing model
			delete(existingModels, modelID)

			// Check if configuration changed
			if existingModel.Endpoint != mapping.Endpoint ||
				existingModel.Container != mapping.Container ||
				existingModel.GPUMemory != mapping.GPUMemory ||
				existingModel.Category != mapping.Category {

				existingModel.Endpoint = mapping.Endpoint
				existingModel.Container = mapping.Container
				existingModel.GPUMemory = mapping.GPUMemory
				existingModel.Category = mapping.Category
				existingModel.UpdatedAt = now

				// Update health checker with new endpoint
				if r.healthChecker != nil {
					r.healthChecker.RegisterModel(modelID, mapping.Endpoint)
				}

				logs.GetLogger().Infof("Updated model configuration: %s", modelID)
				if r.onModelUpdated != nil {
					go func(m *RegisteredModel) {
						defer func() {
							if err := recover(); err != nil {
								logs.GetLogger().Errorf("[model_registry:onModelUpdated] panic recovered: %v", err)
							}
						}()
						r.onModelUpdated(m)
					}(existingModel)
				}
			}
		} else {
			// Add new model
			model := &RegisteredModel{
				ID:          modelID,
				Container:   mapping.Container,
				Endpoint:    mapping.Endpoint,
				GPUMemory:   mapping.GPUMemory,
				Category:    mapping.Category,
				State:       ModelStateLoading,
				StateString: ModelStateLoading.String(),
				Health:      ModelHealthUnknown,
				HealthString: ModelHealthUnknown.String(),
				LoadedAt:    now,
				UpdatedAt:   now,
				Enabled:     true,
			}
			r.models[modelID] = model

			// Register with health checker
			if r.healthChecker != nil {
				r.healthChecker.RegisterModel(modelID, mapping.Endpoint)
			}

			logs.GetLogger().Infof("Registered new model: %s -> %s", modelID, mapping.Endpoint)
			if r.onModelAdded != nil {
				go func(m *RegisteredModel) {
					defer func() {
						if err := recover(); err != nil {
							logs.GetLogger().Errorf("[model_registry:onModelAdded] panic recovered: %v", err)
						}
					}()
					r.onModelAdded(m)
				}(model)
			}
		}
	}

	// Remove models that are no longer in config
	for modelID := range existingModels {
		if r.healthChecker != nil {
			r.healthChecker.UnregisterModel(modelID)
		}
		delete(r.models, modelID)
		logs.GetLogger().Infof("Removed model: %s", modelID)
		if r.onModelRemoved != nil {
			go func(id string) {
				defer func() {
					if err := recover(); err != nil {
						logs.GetLogger().Errorf("[model_registry:onModelRemoved] panic recovered: %v", err)
					}
				}()
				r.onModelRemoved(id)
			}(modelID)
		}
	}

	logs.GetLogger().Infof("Loaded %d models from configuration", len(r.models))
	return nil
}

// startWatcher starts the file system watcher for hot-reload
func (r *ModelRegistry) startWatcher() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	r.watcher = watcher

	modelsPath := filepath.Join(r.configPath, "models.json")

	// Watch the config directory for changes
	if err := watcher.Add(r.configPath); err != nil {
		return err
	}

	go func() {
		defer func() {
			if err := recover(); err != nil {
				logs.GetLogger().Errorf("[model_registry:file_watcher] panic recovered: %v\n%s", err, debug.Stack())
			}
		}()

		debounceTimer := time.NewTimer(0)
		if !debounceTimer.Stop() {
			<-debounceTimer.C
		}

		for {
			select {
			case <-r.stopCh:
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Check if models.json was modified
				if filepath.Base(event.Name) == "models.json" {
					if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
						// Debounce rapid changes
						debounceTimer.Reset(500 * time.Millisecond)
					}
				}
			case <-debounceTimer.C:
				logs.GetLogger().Info("Detected models.json change, reloading configuration...")
				if err := r.loadConfig(); err != nil {
					logs.GetLogger().Errorf("Failed to reload model config: %v", err)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logs.GetLogger().Errorf("File watcher error: %v", err)
			}
		}
	}()

	logs.GetLogger().Infof("Watching for config changes at %s", modelsPath)
	return nil
}

// onHealthStatusChange handles health status changes from the health checker
func (r *ModelRegistry) onHealthStatusChange(modelID string, oldHealth, newHealth ModelHealth) {
	r.mu.Lock()
	defer r.mu.Unlock()

	model, exists := r.models[modelID]
	if !exists {
		return
	}

	model.Health = newHealth
	model.HealthString = newHealth.String()
	model.UpdatedAt = time.Now()

	// Update model state based on health
	switch newHealth {
	case ModelHealthHealthy:
		if model.State == ModelStateLoading || model.State == ModelStateUnhealthy {
			model.State = ModelStateReady
			model.StateString = ModelStateReady.String()
		}
	case ModelHealthUnhealthy:
		model.State = ModelStateUnhealthy
		model.StateString = ModelStateUnhealthy.String()
	}

	logs.GetLogger().Infof("Model %s health changed: %s -> %s", modelID, oldHealth.String(), newHealth.String())

	if r.onModelUpdated != nil {
		modelCopy := *model
		go func(m *RegisteredModel) {
			defer func() {
				if err := recover(); err != nil {
					logs.GetLogger().Errorf("[model_registry:onModelUpdated] panic recovered: %v", err)
				}
			}()
			r.onModelUpdated(m)
		}(&modelCopy)
	}
}

// GetModel returns a registered model by ID
func (r *ModelRegistry) GetModel(modelID string) (*RegisteredModel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	model, exists := r.models[modelID]
	if !exists {
		return nil, false
	}

	// Return a copy
	modelCopy := *model
	return &modelCopy, true
}

// GetAllModels returns all registered models
func (r *ModelRegistry) GetAllModels() []*RegisteredModel {
	r.mu.RLock()
	defer r.mu.RUnlock()

	models := make([]*RegisteredModel, 0, len(r.models))
	for _, model := range r.models {
		modelCopy := *model
		models = append(models, &modelCopy)
	}
	return models
}

// GetReadyModels returns models that are ready to serve requests
func (r *ModelRegistry) GetReadyModels() []*RegisteredModel {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var ready []*RegisteredModel
	for _, model := range r.models {
		if model.Enabled && (model.State == ModelStateReady || model.Health == ModelHealthHealthy || model.Health == ModelHealthDegraded) {
			modelCopy := *model
			ready = append(ready, &modelCopy)
		}
	}
	return ready
}

// GetReadyModelIDs returns IDs of models ready to serve requests
func (r *ModelRegistry) GetReadyModelIDs() []string {
	models := r.GetReadyModels()
	ids := make([]string, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}
	return ids
}

// GetModelEndpoint returns the endpoint for a model if it's ready
func (r *ModelRegistry) GetModelEndpoint(modelID string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	model, exists := r.models[modelID]
	if !exists {
		return "", false
	}

	// Check if model is ready to serve
	if !model.Enabled {
		return "", false
	}

	// Allow degraded models to still serve (circuit breaker handles failures)
	if model.Health == ModelHealthUnhealthy && model.State == ModelStateUnhealthy {
		return "", false
	}

	return model.Endpoint, true
}

// EnableModel enables a model for serving
func (r *ModelRegistry) EnableModel(modelID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	model, exists := r.models[modelID]
	if !exists {
		return ErrModelNotFound
	}

	model.Enabled = true
	model.UpdatedAt = time.Now()

	logs.GetLogger().Infof("Enabled model: %s", modelID)
	return nil
}

// DisableModel disables a model from serving
func (r *ModelRegistry) DisableModel(modelID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	model, exists := r.models[modelID]
	if !exists {
		return ErrModelNotFound
	}

	model.Enabled = false
	model.State = ModelStateDisabled
	model.StateString = ModelStateDisabled.String()
	model.UpdatedAt = time.Now()

	logs.GetLogger().Infof("Disabled model: %s", modelID)
	return nil
}

// ReloadConfig manually triggers a configuration reload
func (r *ModelRegistry) ReloadConfig() error {
	logs.GetLogger().Info("Manual config reload requested")
	return r.loadConfig()
}

// GetModelMappings returns model mappings in the original format (for compatibility)
func (r *ModelRegistry) GetModelMappings() map[string]ModelMapping {
	r.mu.RLock()
	defer r.mu.RUnlock()

	mappings := make(map[string]ModelMapping, len(r.models))
	for id, model := range r.models {
		mappings[id] = ModelMapping{
			Container: model.Container,
			Endpoint:  model.Endpoint,
			GPUMemory: model.GPUMemory,
			Category:  model.Category,
		}
	}
	return mappings
}

// GetStatusSummary returns a summary of model statuses
func (r *ModelRegistry) GetStatusSummary() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var total, ready, unhealthy, disabled int
	for _, model := range r.models {
		total++
		switch {
		case !model.Enabled:
			disabled++
		case model.State == ModelStateReady || model.Health == ModelHealthHealthy:
			ready++
		case model.State == ModelStateUnhealthy || model.Health == ModelHealthUnhealthy:
			unhealthy++
		}
	}

	return map[string]interface{}{
		"total":     total,
		"ready":     ready,
		"unhealthy": unhealthy,
		"disabled":  disabled,
	}
}

// Custom errors
var (
	ErrModelNotFound = &ModelError{Message: "model not found"}
)

type ModelError struct {
	Message string
}

func (e *ModelError) Error() string {
	return e.Message
}
