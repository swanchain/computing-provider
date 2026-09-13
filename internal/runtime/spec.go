// Package runtime starts and stops the model servers a node serves from.
//
// Until this existed, computing-provider could fetch weights and read a
// models.json that someone else had filled in, but it could not bring a model
// up or take one down: `setup` printed a docker command for the operator to
// run by hand. Anything that re-plans which models to serve needs to execute
// that plan, and this is the piece that executes it.
//
// Servers run as Docker containers because that is how they already run on
// provider hardware — llama.cpp and vLLM both ship server images, and the
// container gives the manager an identity that survives a restart of
// computing-provider itself. A pid does not: pids are recycled, so a state file
// naming one can come back after a reboot pointing at an unrelated process.
// Labels on a container cannot.
package runtime

import (
	"fmt"
	"regexp"
	"strings"
)

// Label keys stamped on every container this package creates. Ownership is
// decided by these and nothing else, so a container the operator started by
// hand is invisible to List and untouchable by Stop.
const (
	LabelManaged = "io.swanchain.cp.managed"
	LabelModelID = "io.swanchain.cp.model-id"
	LabelBackend = "io.swanchain.cp.backend"

	// ManagedValue is the value of LabelManaged. It is matched exactly.
	ManagedValue = "true"

	// NamePrefix prefixes every container name, so `docker ps` is readable
	// without consulting the labels.
	NamePrefix = "swan-cp-"
)

// ServeSpec describes one model server to bring up.
type ServeSpec struct {
	// ModelID is the Swan Inference model ID, e.g. Qwen/Qwen3.8-27B. It is
	// what the model reports as its own name, so it must match the ID the
	// node declares or the hub routes requests the backend then rejects.
	ModelID string

	// Backend names the server to run: llamacpp, vllm or sglang.
	Backend string

	// Weights is a host path: a .gguf file for llamacpp, a model directory
	// for vllm and sglang. Mounted into the container read-only.
	Weights string

	// Port is the host port to publish on. Zero means the manager picks one.
	Port int

	// GPUs selects devices, in the form accepted by `docker --gpus`:
	// "all", or a device list such as "0,1". Empty means all.
	GPUs string

	// ContextLength caps the context window. Zero leaves the backend default.
	ContextLength int

	// Category is recorded in models.json. Defaults to text-generation.
	Category string

	// Image overrides the backend's default container image.
	Image string

	// ExtraArgs are appended to the server's own argument list, after
	// everything the backend generates, so an operator can always override
	// a generated flag by repeating it.
	ExtraArgs []string

	// GPUMemory is recorded in models.json in MB. Purely declarative.
	GPUMemory int
}

// Instance is a model server this package is running.
type Instance struct {
	ModelID     string `json:"model_id"`
	Backend     string `json:"backend"`
	Container   string `json:"container"`
	ContainerID string `json:"container_id"`
	Port        int    `json:"port"`
	Endpoint    string `json:"endpoint"`
	Image       string `json:"image"`
	Status      string `json:"status"`
	Ready       bool   `json:"ready"`
}

// modelIDPattern matches the characters a container name may carry. Docker
// accepts [a-zA-Z0-9][a-zA-Z0-9_.-]*; model IDs carry a slash and may carry
// anything else a HuggingFace repo ID allows.
var unsafeNameChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

// ContainerName derives a container name from a model ID.
//
// The mapping is deliberately not reversible: two model IDs could in principle
// collapse to one name, and the name is therefore never parsed to recover an
// ID. The model ID is read back from the container's label, which holds it
// verbatim.
func ContainerName(modelID string) string {
	slug := unsafeNameChars.ReplaceAllString(modelID, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "model"
	}
	if len(slug) > 60 {
		slug = strings.Trim(slug[:60], "-")
	}
	return NamePrefix + slug
}

// Validate checks a spec for the mistakes that would otherwise surface as an
// opaque failure minutes later, once the image had been pulled and the weights
// had started loading.
func (s *ServeSpec) Validate() error {
	if strings.TrimSpace(s.ModelID) == "" {
		return fmt.Errorf("model ID is required")
	}
	if strings.TrimSpace(s.Weights) == "" {
		return fmt.Errorf("weights path is required")
	}
	if _, err := LookupBackend(s.Backend); err != nil {
		return err
	}
	if s.Port < 0 || s.Port > 65535 {
		return fmt.Errorf("port %d is out of range", s.Port)
	}
	if s.ContextLength < 0 {
		return fmt.Errorf("context length cannot be negative")
	}
	return nil
}

// category returns the models.json category for the spec.
func (s *ServeSpec) category() string {
	if c := strings.TrimSpace(s.Category); c != "" {
		return c
	}
	return "text-generation"
}
