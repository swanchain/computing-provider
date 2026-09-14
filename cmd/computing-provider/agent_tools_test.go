package main

import (
	"testing"

	"github.com/swanchain/computing-provider-v2/conf"
)

// cp.md promises the planner is never stopped. That promise was enforced only
// in the scheduler's guardrails, so an agent asked to stop the planner would
// have done it. The pin list has to bind on this path too.
func TestPinnedModelsIncludeThePlannerAndThePinList(t *testing.T) {
	cfg := &conf.ComputeNode{}
	cfg.Inference.AutoSwitch.Planner = "Qwen/Qwen3.8-27B"
	cfg.Inference.AutoSwitch.Pin = []string{"Keep/This", " Also/This "}

	pinned := pinnedModels(cfg)

	for _, id := range []string{"qwen/qwen3.8-27b", "keep/this", "also/this"} {
		if !pinned[id] {
			t.Errorf("%s is not pinned", id)
		}
	}
	if pinned["some/Other"] || pinned[""] {
		t.Error("an unpinned model or the empty string was pinned")
	}
}

func TestPinnedModelsWithNothingConfigured(t *testing.T) {
	if got := pinnedModels(&conf.ComputeNode{}); len(got) != 0 {
		t.Errorf("pinned = %v, want none", got)
	}
}
