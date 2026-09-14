package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/alerts"
	"github.com/swanchain/computing-provider-v2/internal/autoswitch"
	"github.com/swanchain/computing-provider-v2/internal/runtime"
)

// startAutoSwitch wires the scheduler into a running daemon, returning nil when
// the operator has not enabled it.
//
// Off unless asked for. This is the one subsystem that changes what the node
// serves without anyone watching, so it does not start by default and it does
// not start silently.
func startAutoSwitch(ctx context.Context, cpRepoPath string, cfg *conf.ComputeNode, notifier *alerts.Notifier) *autoswitch.Scheduler {
	policy := cfg.Inference.AutoSwitch.WithDefaults()
	if !policy.Enable {
		return nil
	}

	if policy.Planner == "" {
		logs.GetLogger().Errorf("auto-switch is enabled but no Planner is set under [Inference.AutoSwitch]; not starting")
		return nil
	}

	// The history is what makes dwell time and the daily cap survive a
	// restart. Starting without it would mean a crash loop became a switch
	// loop, so a history that cannot be read stops the scheduler rather than
	// being replaced with an empty one.
	history, err := autoswitch.LoadHistory(cpRepoPath)
	if err != nil {
		logs.GetLogger().Errorf("auto-switch: %v; not starting, because dwell time and the daily switch cap cannot be honoured without it", err)
		return nil
	}

	endpoint, apiKey := plannerEndpoint(cpRepoPath, policy.Planner)
	if endpoint == "" {
		logs.GetLogger().Errorf("auto-switch: planner %s has no endpoint in models.json; not starting", policy.Planner)
		return nil
	}

	manager := runtime.NewManager(cpRepoPath)
	manager.Announcer = runtime.NewAlertAnnouncer(notifier)

	guard := autoswitch.Policy{
		// The planner is pinned whatever the operator configured: a switch
		// that unloads the model making the decision leaves no planner for
		// the next cycle.
		Pin:            dedupePins(append(append([]string{}, policy.Pin...), policy.Planner)),
		Deny:           policy.Deny,
		MinMarginUSD:   policy.MinMarginUSD,
		MinDwell:       time.Duration(policy.MinDwellMin) * time.Minute,
		ConfirmCycles:  policy.ConfirmCycles,
		MaxSwitchesDay: policy.MaxSwitchesDay,
	}

	scheduler := &autoswitch.Scheduler{
		Policy:   guard,
		Interval: time.Duration(policy.IntervalMin) * time.Minute,
		History:  history,

		Snapshot: func(ctx context.Context) (*autoswitch.Snapshot, error) {
			snapshot, _, err := buildPlanSnapshot(ctx, cpRepoPath, cfg, policy, policy.Planner)
			return snapshot, err
		},
		Decide: func(ctx context.Context, snapshot *autoswitch.Snapshot) (*autoswitch.Decision, error) {
			decision, _, err := autoswitch.NewPlanner(endpoint, policy.Planner, apiKey).Decide(ctx, snapshot)
			return decision, err
		},
		Execute: func(ctx context.Context, decision *autoswitch.Decision) error {
			return executeSwitch(ctx, manager, cpRepoPath, decision)
		},
	}

	scheduler.Start(ctx)
	logs.GetLogger().Infof(
		"auto-switch enabled: planner %s, every %d min, dwell %d min, margin $%.2f, %d confirming cycles, max %d switches/day",
		policy.Planner, policy.IntervalMin, policy.MinDwellMin,
		policy.MinMarginUSD, policy.ConfirmCycles, policy.MaxSwitchesDay)

	return scheduler
}

// executeSwitch carries out an approved plan.
//
// Stops before starts, because the models being stopped are usually the ones
// whose VRAM the new models need. Starting first would try to fit both at once
// and fail on a node that is anywhere near full.
//
// A failure part-way is returned rather than swallowed, and the scheduler does
// not record a switch that failed — so the node retries next cycle rather than
// entering a dwell period it did not earn.
func executeSwitch(ctx context.Context, manager *runtime.Manager, cpRepoPath string, decision *autoswitch.Decision) error {
	for _, modelID := range decision.Stop {
		if _, err := manager.Stop(ctx, modelID, runtime.StopOptions{
			Remove:  true,
			Reason:  decision.Reason,
			Decider: runtime.DeciderAutoSwitch,
		}); err != nil {
			return fmt.Errorf("could not stop %s: %w", modelID, err)
		}
	}

	for _, modelID := range decision.Serve {
		// The scheduler cannot yet obtain weights it does not have, so a
		// model with none is reported rather than silently skipped: a plan
		// that half executed is worse than one that refused, and the operator
		// needs to know which half.
		weights, backend, err := resolveLocalWeights(modelID)
		if err != nil {
			return fmt.Errorf("cannot serve %s: %w", modelID, err)
		}

		if _, err := manager.Serve(ctx, &runtime.ServeSpec{
			ModelID: modelID,
			Backend: backend,
			Weights: weights,
		}, runtime.ServeOptions{
			WaitReady: true,
			Reason:    decision.Reason,
			Decider:   runtime.DeciderAutoSwitch,
		}); err != nil {
			return fmt.Errorf("could not serve %s: %w", modelID, err)
		}
	}
	return nil
}

// resolveLocalWeights finds weights already on disk for a model, and the
// backend that can serve them.
//
// Deliberately does not download. A scheduler that fetched 600 GB because a
// planner named a model would saturate the operator's connection and fill their
// disk, on a decision that took thirty seconds to make. Obtaining weights is a
// separate, slower motion than switching between models the node already has,
// and it belongs behind its own explicit opt-in.
func resolveLocalWeights(modelID string) (weights, backend string, err error) {
	dir := filepath.Join(defaultModelsDir(), modelID)
	info, statErr := os.Stat(dir)
	if statErr != nil || !info.IsDir() {
		return "", "", fmt.Errorf("no weights at %s; download them first with: computing-provider models download %s", dir, modelID)
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		return "", "", fmt.Errorf("cannot read %s: %w", dir, readErr)
	}

	// A single GGUF is llama.cpp's; a directory of safetensors is vLLM's.
	var gguf string
	var safetensors bool
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		switch {
		case strings.HasSuffix(name, ".gguf") && !strings.Contains(name, "mmproj"):
			if gguf == "" {
				gguf = filepath.Join(dir, e.Name())
			}
		case strings.HasSuffix(name, ".safetensors"):
			safetensors = true
		}
	}

	switch {
	case safetensors:
		return dir, "vllm", nil
	case gguf != "":
		return gguf, "llamacpp", nil
	default:
		return "", "", fmt.Errorf("%s holds neither safetensors nor a .gguf", dir)
	}
}
