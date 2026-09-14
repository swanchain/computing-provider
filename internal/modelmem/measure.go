package modelmem

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// MiB is what nvidia-smi reports in.
const MiB = 1024 * 1024

// Measure reads how much VRAM a container is actually using.
//
// Attribution is by process, not by device total. A GPU shared between two
// models reports one total for both, and charging the whole of it to whichever
// model was measured last would make the memory actively misleading. nvidia-smi
// reports per-process usage, and `docker top` says which processes belong to
// the container, so the two together give a figure that belongs to one model.
func Measure(ctx context.Context, containerName string) (*Measurement, error) {
	pids, err := containerPIDs(ctx, containerName)
	if err != nil {
		return nil, err
	}
	if len(pids) == 0 {
		return nil, fmt.Errorf("container %s has no processes", containerName)
	}

	usage, err := computeAppMemory(ctx)
	if err != nil {
		return nil, err
	}

	var perGPU []float64
	var total float64
	for _, entry := range usage {
		if !pids[entry.pid] {
			continue
		}
		gib := float64(entry.bytes) / (1024 * 1024 * 1024)
		perGPU = append(perGPU, gib)
		total += gib
	}

	if total == 0 {
		// A CPU-only model, or one that has not allocated yet. Reported as an
		// error rather than as a measurement of zero, which would be recorded
		// as "this model needs no VRAM".
		return nil, fmt.Errorf("no GPU memory attributed to %s", containerName)
	}

	return &Measurement{VRAMGiB: total, PerGPUGiB: perGPU, At: time.Now().UTC()}, nil
}

// containerPIDs returns the host PIDs running inside a container.
func containerPIDs(ctx context.Context, name string) (map[int]bool, error) {
	out, err := exec.CommandContext(ctx, "docker", "top", name, "-eo", "pid").Output()
	if err != nil {
		return nil, fmt.Errorf("could not list processes in %s: %w", name, err)
	}

	pids := map[int]bool{}
	for i, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if i == 0 {
			continue // header
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
			pids[pid] = true
		}
	}
	return pids, nil
}

type computeApp struct {
	pid   int
	bytes int64
}

// computeAppMemory reads per-process GPU memory.
//
// One process appears once per device it occupies, so a model sharded across
// two cards yields two entries. They are kept separate rather than summed here
// because the split is what decides whether a model fits a particular
// arrangement of cards, which a single total cannot say.
func computeAppMemory(ctx context.Context) ([]computeApp, error) {
	out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-compute-apps=pid,used_gpu_memory", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, fmt.Errorf("could not read GPU memory usage: %w", err)
	}

	var apps []computeApp
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil {
			continue
		}
		mib, err := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64)
		if err != nil {
			continue
		}
		apps = append(apps, computeApp{pid: pid, bytes: mib * MiB})
	}
	return apps, nil
}

// MeasureWhenSettled measures after giving the backend time to finish
// allocating.
//
// A model that has answered one request has not necessarily reached its steady
// state: llama.cpp allocates its KV cache lazily, and vLLM's profiling run
// settles a moment after the server starts answering. Measuring too early
// records a figure below what the model will hold, which is the direction that
// leads to over-committing a GPU.
func MeasureWhenSettled(ctx context.Context, containerName string, settle time.Duration) (*Measurement, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(settle):
	}
	return Measure(ctx, containerName)
}
