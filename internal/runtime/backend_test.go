package runtime

import (
	"strconv"
	"strings"
	"testing"
)

// argValue returns the value following a flag, and whether the flag is present.
func argValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func hasArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func TestContainerName(t *testing.T) {
	cases := []struct {
		modelID string
		want    string
	}{
		{"Qwen/Qwen3.8-27B", "swan-cp-Qwen-Qwen3.8-27B"},
		{"meta-llama/Llama-3.2-3B-Instruct", "swan-cp-meta-llama-Llama-3.2-3B-Instruct"},
		{"/leading-and-trailing/", "swan-cp-leading-and-trailing"},
		{"///", "swan-cp-model"},
	}
	for _, tc := range cases {
		if got := ContainerName(tc.modelID); got != tc.want {
			t.Errorf("ContainerName(%q) = %q, want %q", tc.modelID, got, tc.want)
		}
	}
}

// A model ID long enough to overflow docker's name limits must still produce a
// name docker accepts, and must not end in the separator.
func TestContainerNameTruncates(t *testing.T) {
	long := "some-org/" + strings.Repeat("a", 200)
	got := ContainerName(long)
	if len(got) > len(NamePrefix)+60 {
		t.Fatalf("name %q is %d chars, want at most %d", got, len(got), len(NamePrefix)+60)
	}
	if strings.HasSuffix(got, "-") {
		t.Errorf("name %q ends in a separator", got)
	}
}

func TestLookupBackendAliases(t *testing.T) {
	for _, alias := range []string{"llamacpp", "llama.cpp", "llama-server", "GGUF", " llama "} {
		b, err := LookupBackend(alias)
		if err != nil {
			t.Fatalf("LookupBackend(%q): %v", alias, err)
		}
		if b.Name() != "llamacpp" {
			t.Errorf("LookupBackend(%q) = %q, want llamacpp", alias, b.Name())
		}
	}

	if _, err := LookupBackend("tensorrt"); err == nil {
		t.Error("expected an error for an unknown backend")
	}
	if _, err := LookupBackend(""); err == nil {
		t.Error("expected an error for an empty backend")
	}
}

// Every backend must serve the model under the Swan Inference model ID. If it
// serves it under a filename or a mount path instead, the hub routes requests
// the backend then rejects as an unknown model.
func TestBackendsServeUnderTheModelID(t *testing.T) {
	spec := &ServeSpec{ModelID: "Qwen/Qwen3.8-27B", Weights: "/w"}

	cases := map[string]string{
		"llamacpp": "--alias",
		"vllm":     "--served-model-name",
		"sglang":   "--served-model-name",
	}
	for name, flag := range cases {
		b, err := LookupBackend(name)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := argValue(b.Args(spec), flag)
		if !ok {
			t.Fatalf("%s: %s missing from %v", name, flag, b.Args(spec))
		}
		if got != spec.ModelID {
			t.Errorf("%s: %s = %q, want %q", name, flag, got, spec.ModelID)
		}
	}
}

// The server must listen on every interface inside the container, because the
// published port reaches it over the container's own network.
func TestBackendsBindAllInterfacesInContainer(t *testing.T) {
	spec := &ServeSpec{ModelID: "m", Weights: "/w"}
	for _, name := range BackendNames() {
		b, _ := LookupBackend(name)
		args := b.Args(spec)
		if host, ok := argValue(args, "--host"); !ok || host != "0.0.0.0" {
			t.Errorf("%s: --host = %q, want 0.0.0.0", name, host)
		}
		port, ok := argValue(args, "--port")
		if !ok {
			t.Fatalf("%s: --port missing", name)
		}
		if want := b.ContainerPort(); port != strconv.Itoa(want) {
			t.Errorf("%s: --port = %q, want %d", name, port, want)
		}
	}
}

func TestContextLengthFlagPerBackend(t *testing.T) {
	cases := map[string]string{
		"llamacpp": "-c",
		"vllm":     "--max-model-len",
		"sglang":   "--context-length",
	}
	for name, flag := range cases {
		b, _ := LookupBackend(name)

		withCtx := &ServeSpec{ModelID: "m", Weights: "/w", ContextLength: 65536}
		if got, ok := argValue(b.Args(withCtx), flag); !ok || got != "65536" {
			t.Errorf("%s: %s = %q, want 65536", name, flag, got)
		}

		// Zero must leave the backend's own default rather than passing 0,
		// which every one of these reads as an explicit zero-length window.
		withoutCtx := &ServeSpec{ModelID: "m", Weights: "/w"}
		if hasArg(b.Args(withoutCtx), flag) {
			t.Errorf("%s: %s passed when no context length was requested", name, flag)
		}
	}
}

func TestGPUCount(t *testing.T) {
	cases := map[string]int{
		"":           0,
		"all":        0,
		"ALL":        0,
		"0":          1,
		"0,1":        2,
		"device=2,3": 2,
		"0, 1, 2":    3,
	}
	for in, want := range cases {
		if got := gpuCount(in); got != want {
			t.Errorf("gpuCount(%q) = %d, want %d", in, got, want)
		}
	}
}

// Tensor parallelism must be set from an explicit device list, and left alone
// otherwise: "all" is not a number the backend can be told.
func TestTensorParallelFromDeviceList(t *testing.T) {
	cases := map[string]string{"vllm": "--tensor-parallel-size", "sglang": "--tp"}
	for name, flag := range cases {
		b, _ := LookupBackend(name)

		two := &ServeSpec{ModelID: "m", Weights: "/w", GPUs: "2,3"}
		if got, ok := argValue(b.Args(two), flag); !ok || got != "2" {
			t.Errorf("%s: %s = %q, want 2", name, flag, got)
		}

		for _, gpus := range []string{"", "all", "1"} {
			one := &ServeSpec{ModelID: "m", Weights: "/w", GPUs: gpus}
			if hasArg(b.Args(one), flag) {
				t.Errorf("%s: %s passed for --gpus %q", name, flag, gpus)
			}
		}
	}
}

// Extra arguments go last so that repeating a generated flag overrides it,
// which is the only way an operator can correct a default this tool gets wrong
// for their hardware.
func TestExtraArgsComeLast(t *testing.T) {
	for _, name := range BackendNames() {
		b, _ := LookupBackend(name)
		spec := &ServeSpec{
			ModelID:   "m",
			Weights:   "/w",
			ExtraArgs: []string{"--sentinel", "1"},
		}
		args := b.Args(spec)
		if len(args) < 2 {
			t.Fatalf("%s: too few args", name)
		}
		if args[len(args)-2] != "--sentinel" || args[len(args)-1] != "1" {
			t.Errorf("%s: extra args are not last: %v", name, args)
		}
	}
}
