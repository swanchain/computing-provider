package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Docker is the subset of the docker CLI this package needs.
//
// It is an interface so the manager can be tested without a docker daemon:
// every decision the manager makes is visible in the argument lists it builds,
// and those are what the tests assert on.
type Docker interface {
	// Run executes a docker subcommand and returns its stdout.
	Run(ctx context.Context, args ...string) (string, error)
}

// CLIDocker drives the docker binary on PATH.
//
// Shelling out rather than linking the Docker SDK: the SDK pulls a large
// dependency tree in exchange for API access this package does not need, and
// an operator debugging a failed start can paste the argument list from the
// error straight into their own shell.
type CLIDocker struct {
	// Binary is the docker executable, "docker" when empty.
	Binary string
}

func (c CLIDocker) binary() string {
	if c.Binary != "" {
		return c.Binary
	}
	return "docker"
}

func (c CLIDocker) Run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.binary(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			return "", fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
		}
		// The failing command is included because the useful ones are long
		// and an operator cannot otherwise reproduce what was attempted.
		return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return stdout.String(), nil
}

// Available reports whether the docker CLI can reach a daemon.
func (c CLIDocker) Available(ctx context.Context) error {
	if _, err := exec.LookPath(c.binary()); err != nil {
		return fmt.Errorf("docker not found on PATH: %w", err)
	}
	if _, err := c.Run(ctx, "info", "--format", "{{.ServerVersion}}"); err != nil {
		return fmt.Errorf("docker is installed but not reachable: %w", err)
	}
	return nil
}

// runArgs builds the `docker run` argument list for a spec.
//
// Split out from Serve so the whole of the container's configuration can be
// asserted in a test without a daemon, and so `serve --dry-run` can print
// exactly what would be executed.
func runArgs(spec *ServeSpec, backend Backend, image string, hostPort int) []string {
	args := []string{
		"run", "--detach",
		"--name", ContainerName(spec.ModelID),
		// Restart unless an operator or this manager stopped it: a node is
		// meant to keep serving across a host reboot or a backend crash,
		// and `stop` sets the stopped flag that suppresses this.
		"--restart", "unless-stopped",
		"--label", LabelManaged + "=" + ManagedValue,
		"--label", LabelModelID + "=" + spec.ModelID,
		"--label", LabelBackend + "=" + backend.Name(),
		"--publish", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, backend.ContainerPort()),
	}

	// "none" omits --gpus entirely, which is what a CPU-only backend needs:
	// passing --gpus at all fails on a host with no NVIDIA runtime, and some
	// models (small ones, embeddings) have no reason to hold a GPU.
	if gpus := normaliseGPUs(spec.GPUs); gpus != "" {
		args = append(args, "--gpus", gpus)
	}

	// Both llama.cpp and vLLM want more than the default 64 MB of shared
	// memory once more than one GPU is involved; the failure without it is
	// an opaque NCCL error partway through loading.
	args = append(args, "--shm-size", "4g", "--ipc", "host")

	mount := spec.Weights + ":" + backend.WeightsTarget() + ":ro"
	args = append(args, "--volume", mount)

	if ep := backend.Entrypoint(); len(ep) > 0 {
		args = append(args, "--entrypoint", ep[0])
	}

	args = append(args, image)

	if ep := backend.Entrypoint(); len(ep) > 1 {
		args = append(args, ep[1:]...)
	}

	return append(args, backend.Args(spec)...)
}

// DryRun returns the docker arguments a spec would be started with.
//
// It does not check that the weights exist or that the port is free, so it can
// be used to review a spec on a machine that is not the one that will run it.
func DryRun(spec *ServeSpec) ([]string, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	backend, err := LookupBackend(spec.Backend)
	if err != nil {
		return nil, err
	}
	image := spec.Image
	if image == "" {
		image = backend.DefaultImage()
	}
	port := spec.Port
	if port == 0 {
		port = portSearchBase
	}
	return runArgs(spec, backend, image, port), nil
}

// normaliseGPUs converts a spec's GPU selector into a --gpus value, or "" when
// the container should be given no GPU at all.
func normaliseGPUs(selector string) string {
	gpus := strings.TrimSpace(selector)
	switch {
	case gpus == "":
		return "all"
	case strings.EqualFold(gpus, "none"):
		return ""
	case strings.EqualFold(gpus, "all"):
		return "all"
	case strings.HasPrefix(gpus, "device="):
		return quoteDeviceList(gpus)
	default:
		return quoteDeviceList("device=" + gpus)
	}
}

// quoteDeviceList wraps a multi-device selector in literal double quotes.
//
// Docker parses the --gpus value as CSV, so an unquoted "device=2,3" splits
// into "device=2" and a bare "3" — and a bare number is read as a device
// *count*, which fails as "cannot set both Count and DeviceIDs". The quotes
// have to survive into the argv element itself, since we exec docker directly
// rather than through a shell that would strip them.
func quoteDeviceList(gpus string) string {
	if !strings.Contains(gpus, ",") {
		return gpus
	}
	return `"` + gpus + `"`
}
