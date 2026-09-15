package runtime

import (
	"strconv"
	"strings"
	"testing"
)

// labels collects the --label values from a docker run argument list.
func labels(args []string) map[string]string {
	out := map[string]string{}
	for i, a := range args {
		if a == "--label" && i+1 < len(args) {
			if k, v, ok := strings.Cut(args[i+1], "="); ok {
				out[k] = v
			}
		}
	}
	return out
}

// Ownership is decided entirely by these labels, so a container created
// without them would be invisible to List and unstoppable by Stop.
func TestRunArgsStampsOwnershipLabels(t *testing.T) {
	spec := &ServeSpec{ModelID: "Qwen/Qwen3.8-27B", Backend: "llamacpp", Weights: "/w/m.gguf"}
	backend, _ := LookupBackend("llamacpp")

	got := labels(runArgs(spec, backend, "img", 30001))

	want := map[string]string{
		LabelManaged: ManagedValue,
		LabelModelID: "Qwen/Qwen3.8-27B",
		LabelBackend: "llamacpp",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("label %s = %q, want %q", k, got[k], v)
		}
	}
}

// The model ID is stored verbatim in the label because the container name is a
// lossy slug: reading the ID back from the name would corrupt any ID that
// contains a character the name cannot carry.
func TestModelIDLabelIsVerbatimNotTheSlug(t *testing.T) {
	spec := &ServeSpec{ModelID: "org/Model.v1+extra", Backend: "vllm", Weights: "/w"}
	backend, _ := LookupBackend("vllm")

	args := runArgs(spec, backend, "img", 30000)
	if got := labels(args)[LabelModelID]; got != spec.ModelID {
		t.Errorf("model-id label = %q, want %q", got, spec.ModelID)
	}
	name, _ := argValue(args, "--name")
	if name == spec.ModelID {
		t.Error("expected the container name to be a slug, not the raw model ID")
	}
}

// Model servers have no authentication of their own. Publishing them on
// 0.0.0.0 would put an unauthenticated GPU on every interface of the host,
// including whatever the provider's network exposes.
func TestRunArgsPublishesOnLoopbackOnly(t *testing.T) {
	spec := &ServeSpec{ModelID: "m", Backend: "vllm", Weights: "/w"}
	backend, _ := LookupBackend("vllm")

	publish, ok := argValue(runArgs(spec, backend, "img", 31234), "--publish")
	if !ok {
		t.Fatal("--publish missing")
	}
	if want := "127.0.0.1:31234:8000"; publish != want {
		t.Errorf("--publish = %q, want %q", publish, want)
	}
}

// The host port is chosen freely and mapped onto whichever port the backend
// listens on inside its container, so the two must not be assumed equal.
func TestHostPortMapsOntoBackendPort(t *testing.T) {
	cases := map[string]int{"llamacpp": 8080, "vllm": 8000, "sglang": 30000}
	for name, containerPort := range cases {
		backend, _ := LookupBackend(name)
		spec := &ServeSpec{ModelID: "m", Backend: name, Weights: "/w"}

		publish, _ := argValue(runArgs(spec, backend, "img", 40000), "--publish")
		parts := strings.Split(publish, ":")
		if len(parts) != 3 {
			t.Fatalf("%s: unexpected --publish %q", name, publish)
		}
		if parts[1] != "40000" {
			t.Errorf("%s: host port = %q, want 40000", name, parts[1])
		}
		if parts[2] != strconv.Itoa(containerPort) {
			t.Errorf("%s: container port = %q, want %d", name, parts[2], containerPort)
		}
	}
}

func TestRunArgsNormalisesGPUSelector(t *testing.T) {
	backend, _ := LookupBackend("vllm")

	cases := map[string]string{
		"":           "all",
		"all":        "all",
		"0,1":        `"device=0,1"`,
		"device=2,3": `"device=2,3"`,
		"0,1,2,3":    `"device=0,1,2,3"`,
	}
	for in, want := range cases {
		spec := &ServeSpec{ModelID: "m", Backend: "vllm", Weights: "/w", GPUs: in}
		got, ok := argValue(runArgs(spec, backend, "img", 30000), "--gpus")
		if !ok {
			t.Fatalf("--gpus missing for %q", in)
		}
		if got != want {
			t.Errorf("--gpus for %q = %q, want %q", in, got, want)
		}
	}
}

// Weights are mounted read-only: a backend has no reason to write to them, and
// a bug that truncated a 30 GB download would cost hours to undo.
func TestWeightsAreMountedReadOnly(t *testing.T) {
	cases := map[string]string{
		"llamacpp": "/host/model.gguf:/models/model.gguf:ro",
		"vllm":     "/host/model.gguf:/models:ro",
	}
	for name, want := range cases {
		backend, _ := LookupBackend(name)
		spec := &ServeSpec{ModelID: "m", Backend: name, Weights: "/host/model.gguf"}
		got, ok := argValue(runArgs(spec, backend, "img", 30000), "--volume")
		if !ok {
			t.Fatalf("%s: --volume missing", name)
		}
		if got != want {
			t.Errorf("%s: --volume = %q, want %q", name, got, want)
		}
	}
}

// SGLang's image has no server entrypoint, so the manager supplies one. The
// first element becomes --entrypoint and the rest must land after the image
// name, where docker reads them as the command.
func TestSGLangEntrypointSplitsAroundTheImage(t *testing.T) {
	backend, _ := LookupBackend("sglang")
	spec := &ServeSpec{ModelID: "m", Backend: "sglang", Weights: "/w"}

	args := runArgs(spec, backend, "sglang-image", 30000)

	ep, ok := argValue(args, "--entrypoint")
	if !ok || ep != "python3" {
		t.Fatalf("--entrypoint = %q, want python3", ep)
	}

	imageIdx := -1
	for i, a := range args {
		if a == "sglang-image" {
			imageIdx = i
			break
		}
	}
	if imageIdx < 0 {
		t.Fatal("image name missing from args")
	}
	rest := args[imageIdx+1:]
	if len(rest) < 2 || rest[0] != "-m" || rest[1] != "sglang.launch_server" {
		t.Errorf("command after image = %v, want it to start with -m sglang.launch_server", rest)
	}
}

// A backend whose image already has a server entrypoint must not have one
// forced on it.
func TestNoEntrypointOverrideWhenTheImageHasOne(t *testing.T) {
	for _, name := range []string{"llamacpp", "vllm"} {
		backend, _ := LookupBackend(name)
		spec := &ServeSpec{ModelID: "m", Backend: name, Weights: "/w"}
		if hasArg(runArgs(spec, backend, "img", 30000), "--entrypoint") {
			t.Errorf("%s: --entrypoint passed, overriding the image's own", name)
		}
	}
}

// A container that only restarts on failure would stay down after a host
// reboot, silently removing the node from routing until someone noticed.
func TestRunArgsRestartsUnlessStopped(t *testing.T) {
	backend, _ := LookupBackend("vllm")
	spec := &ServeSpec{ModelID: "m", Backend: "vllm", Weights: "/w"}

	got, ok := argValue(runArgs(spec, backend, "img", 30000), "--restart")
	if !ok || got != "unless-stopped" {
		t.Errorf("--restart = %q, want unless-stopped", got)
	}
}

func TestDryRunNeedsNoWeightsOnDisk(t *testing.T) {
	spec := &ServeSpec{ModelID: "m", Backend: "vllm", Weights: "/does/not/exist"}
	args, err := DryRun(spec)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if len(args) == 0 || args[0] != "run" {
		t.Fatalf("DryRun args = %v, want them to start with run", args)
	}
}

func TestDryRunRejectsAnInvalidSpec(t *testing.T) {
	if _, err := DryRun(&ServeSpec{Backend: "vllm", Weights: "/w"}); err == nil {
		t.Error("expected an error for a spec with no model ID")
	}
	if _, err := DryRun(&ServeSpec{ModelID: "m", Backend: "nope", Weights: "/w"}); err == nil {
		t.Error("expected an error for an unknown backend")
	}
}

// A CPU-only container must be given no --gpus at all: passing the flag fails
// outright on a host with no NVIDIA runtime, so "none" cannot be spelled as an
// empty device list.
func TestGPUsNoneOmitsTheFlag(t *testing.T) {
	backend, _ := LookupBackend("vllm")
	spec := &ServeSpec{ModelID: "m", Backend: "vllm", Weights: "/w", GPUs: "none"}

	if hasArg(runArgs(spec, backend, "img", 30000), "--gpus") {
		t.Error("--gpus was passed for a CPU-only container")
	}
}

func TestNormaliseGPUs(t *testing.T) {
	cases := map[string]string{
		"":           "all",
		"  ":         "all",
		"all":        "all",
		"ALL":        "all",
		"none":       "",
		"None":       "",
		"0":          "device=0",
		"0,1":        `"device=0,1"`,
		"device=2,3": `"device=2,3"`,
		"0,1,2,3":    `"device=0,1,2,3"`,
	}
	for in, want := range cases {
		if got := normaliseGPUs(in); got != want {
			t.Errorf("normaliseGPUs(%q) = %q, want %q", in, got, want)
		}
	}
}

// "none" is not a device list, so it must not be counted as one device and
// turned into a tensor-parallel size of 1.
func TestGPUsNoneSetsNoTensorParallelism(t *testing.T) {
	for _, name := range []string{"vllm", "sglang"} {
		backend, _ := LookupBackend(name)
		spec := &ServeSpec{ModelID: "m", Backend: name, Weights: "/w", GPUs: "none"}
		for _, flag := range []string{"--tensor-parallel-size", "--tp"} {
			if hasArg(backend.Args(spec), flag) {
				t.Errorf("%s: %s passed for --gpus none", name, flag)
			}
		}
	}
}
