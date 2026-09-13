package runtime

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Backend knows how to run one inference server as a container.
//
// Each backend contributes only the two things that differ between servers:
// the image and entrypoint to run, and the arguments that server takes. The
// container plumbing — labels, GPU access, the published port, the weights
// mount — is identical for all of them and lives in the manager, so a fourth
// backend is a table entry rather than a second copy of the run command.
type Backend interface {
	// Name is the value stored in the backend label and accepted on the
	// command line.
	Name() string

	// DefaultImage is the image used when the spec does not override it.
	DefaultImage() string

	// ContainerPort is the port the server listens on inside the container.
	// The manager publishes the host port onto this one, so the host port
	// can be chosen freely without the server knowing about it.
	ContainerPort() int

	// Entrypoint overrides the image's entrypoint, or nil to keep it.
	Entrypoint() []string

	// WeightsTarget is the path the weights are mounted at inside the
	// container.
	WeightsTarget() string

	// WeightsAreFile reports whether Weights names a single file rather
	// than a directory. It decides both how the mount is made and what is
	// checked before the container is created.
	WeightsAreFile() bool

	// Args builds the server's argument list for a spec.
	Args(spec *ServeSpec) []string
}

var backends = map[string]Backend{
	"llamacpp": llamacppBackend{},
	"vllm":     vllmBackend{},
	"sglang":   sglangBackend{},
}

// backendAliases map the names operators actually type onto backend names.
var backendAliases = map[string]string{
	"llama":        "llamacpp",
	"llama.cpp":    "llamacpp",
	"llama-cpp":    "llamacpp",
	"llamaserver":  "llamacpp",
	"llama-server": "llamacpp",
	"gguf":         "llamacpp",
	"sgl":          "sglang",
}

// LookupBackend resolves a backend name, accepting the common aliases.
func LookupBackend(name string) (Backend, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return nil, fmt.Errorf("backend is required (one of %s)", strings.Join(BackendNames(), ", "))
	}
	if canonical, ok := backendAliases[key]; ok {
		key = canonical
	}
	b, ok := backends[key]
	if !ok {
		return nil, fmt.Errorf("unknown backend %q (expected one of %s)", name, strings.Join(BackendNames(), ", "))
	}
	return b, nil
}

// BackendNames lists the supported backends in a stable order.
func BackendNames() []string {
	names := make([]string, 0, len(backends))
	for name := range backends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// llamacppBackend runs llama.cpp's llama-server against a single GGUF file.
type llamacppBackend struct{}

func (llamacppBackend) Name() string          { return "llamacpp" }
func (llamacppBackend) DefaultImage() string  { return "ghcr.io/ggml-org/llama.cpp:server-cuda" }
func (llamacppBackend) ContainerPort() int    { return 8080 }
func (llamacppBackend) Entrypoint() []string  { return nil }
func (llamacppBackend) WeightsTarget() string { return "/models/model.gguf" }
func (llamacppBackend) WeightsAreFile() bool  { return true }

func (b llamacppBackend) Args(spec *ServeSpec) []string {
	args := []string{
		"-m", b.WeightsTarget(),
		// --alias is what the server reports as the model's name. Without
		// it llama-server answers /v1/models with the GGUF filename, which
		// is not the ID the hub routes on, so every routed request would
		// be rejected as an unknown model.
		"--alias", spec.ModelID,
		"--host", "0.0.0.0",
		"--port", strconv.Itoa(b.ContainerPort()),
		// Offload every layer it can. A GPU box that wanted partial
		// offload can say so in ExtraArgs, which are appended after these.
		"-ngl", "99",
	}
	if spec.ContextLength > 0 {
		args = append(args, "-c", strconv.Itoa(spec.ContextLength))
	}
	return append(args, spec.ExtraArgs...)
}

// vllmBackend runs vLLM's OpenAI-compatible server against a model directory.
type vllmBackend struct{}

func (vllmBackend) Name() string          { return "vllm" }
func (vllmBackend) DefaultImage() string  { return "vllm/vllm-openai:latest" }
func (vllmBackend) ContainerPort() int    { return 8000 }
func (vllmBackend) Entrypoint() []string  { return nil }
func (vllmBackend) WeightsTarget() string { return "/models" }
func (vllmBackend) WeightsAreFile() bool  { return false }

func (b vllmBackend) Args(spec *ServeSpec) []string {
	args := []string{
		"--model", b.WeightsTarget(),
		// Same reasoning as llama.cpp's --alias: without this vLLM serves
		// the model under its mount path, "/models".
		"--served-model-name", spec.ModelID,
		"--host", "0.0.0.0",
		"--port", strconv.Itoa(b.ContainerPort()),
	}
	if spec.ContextLength > 0 {
		args = append(args, "--max-model-len", strconv.Itoa(spec.ContextLength))
	}
	if n := gpuCount(spec.GPUs); n > 1 {
		args = append(args, "--tensor-parallel-size", strconv.Itoa(n))
	}
	return append(args, spec.ExtraArgs...)
}

// sglangBackend runs SGLang's launch_server against a model directory.
type sglangBackend struct{}

func (sglangBackend) Name() string         { return "sglang" }
func (sglangBackend) DefaultImage() string { return "lmsysorg/sglang:latest" }
func (sglangBackend) ContainerPort() int   { return 30000 }
func (sglangBackend) Entrypoint() []string {
	return []string{"python3", "-m", "sglang.launch_server"}
}
func (sglangBackend) WeightsTarget() string { return "/models" }
func (sglangBackend) WeightsAreFile() bool  { return false }

func (b sglangBackend) Args(spec *ServeSpec) []string {
	args := []string{
		"--model-path", b.WeightsTarget(),
		"--served-model-name", spec.ModelID,
		"--host", "0.0.0.0",
		"--port", strconv.Itoa(b.ContainerPort()),
	}
	if spec.ContextLength > 0 {
		args = append(args, "--context-length", strconv.Itoa(spec.ContextLength))
	}
	if n := gpuCount(spec.GPUs); n > 1 {
		args = append(args, "--tp", strconv.Itoa(n))
	}
	return append(args, spec.ExtraArgs...)
}

// gpuCount counts the devices named in a --gpus selector.
//
// It reports 0 for "all" and for the empty selector rather than guessing at
// the host's device count. Both mean "however many there are", and a tensor
// parallel size is a number the backend must be told exactly: passing a wrong
// one fails at load time, while omitting it leaves the backend's own default,
// which is 1. An operator wanting parallelism across all devices names them.
func gpuCount(gpus string) int {
	spec := strings.TrimSpace(gpus)
	if spec == "" || strings.EqualFold(spec, "all") || strings.EqualFold(spec, "none") {
		return 0
	}
	spec = strings.TrimPrefix(spec, "device=")
	n := 0
	for _, part := range strings.Split(spec, ",") {
		if strings.TrimSpace(part) != "" {
			n++
		}
	}
	return n
}
