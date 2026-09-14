package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/agent"
	"github.com/swanchain/computing-provider-v2/internal/computing"
	"github.com/swanchain/computing-provider-v2/internal/market"
	"github.com/swanchain/computing-provider-v2/internal/modelmem"
	"github.com/swanchain/computing-provider-v2/internal/runtime"
	"github.com/swanchain/computing-provider-v2/internal/vram"
)

// agentTools builds the agent's whole world.
//
// The list is deliberately short. Every tool here is a capability a confused or
// manipulated model gets to use, so each one is worth arguing for individually
// — which is a very different exercise from handing over a shell and hoping.
func agentTools(cpRepoPath string, cfg *conf.ComputeNode) *agent.Registry {
	return agent.NewRegistry(
		toolNodeStatus(cpRepoPath, cfg),
		toolRunningModels(cpRepoPath),
		toolLocalWeights(),
		toolModelMemory(cpRepoPath),
		toolMarket(cfg),
		toolEstimateVRAM(cfg),
		toolServeModel(cpRepoPath, cfg),
		toolStopModel(cpRepoPath, cfg),
	)
}

func toolNodeStatus(cpRepoPath string, cfg *conf.ComputeNode) *agent.Tool {
	return &agent.Tool{
		Name:        "node_status",
		Description: "This node's hardware, the models it currently declares, and what it earned in the last 24 hours.",
		Run: func(ctx context.Context, _ agent.Args) (string, error) {
			var b strings.Builder

			if hw := computing.DetectGPUHardware(); hw != nil {
				fmt.Fprintf(&b, "Hardware: %d x %s, %d GB each, %d GB total\n",
					hw.GPUCount, hw.GPUModel, hw.VRAMGB, hw.GPUCount*hw.VRAMGB)
			} else {
				b.WriteString("Hardware: not detected\n")
			}

			declared, err := readDeclaredModels(cpRepoPath)
			if err != nil {
				fmt.Fprintf(&b, "Declared models: could not read models.json (%v)\n", err)
			} else if len(declared) == 0 {
				b.WriteString("Declared models: none\n")
			} else {
				ids := make([]string, 0, len(declared))
				for id := range declared {
					ids = append(ids, id)
				}
				sort.Strings(ids)
				b.WriteString("Declared models:\n")
				for _, id := range ids {
					fmt.Fprintf(&b, "  %s at %s\n", id, declared[id])
				}
			}

			perModel, total, err := fetchLocalDailyEarnings(cfg)
			if err != nil {
				fmt.Fprintf(&b, "Earnings: unavailable (%v)\n", err)
			} else {
				fmt.Fprintf(&b, "Earnings, last 24h: $%.4f/day total\n", total)
				type row struct {
					id  string
					usd float64
				}
				rows := make([]row, 0, len(perModel))
				for id, usd := range perModel {
					rows = append(rows, row{id, usd})
				}
				sort.Slice(rows, func(i, j int) bool { return rows[i].usd > rows[j].usd })
				for _, r := range rows {
					fmt.Fprintf(&b, "  %-36s $%.4f\n", r.id, r.usd)
				}
			}
			return b.String(), nil
		},
	}
}

func toolRunningModels(cpRepoPath string) *agent.Tool {
	return &agent.Tool{
		Name:        "running_models",
		Description: "Model servers computing-provider started, which are the only ones it can stop. Backends started by the operator are not listed and cannot be touched.",
		Run: func(ctx context.Context, _ agent.Args) (string, error) {
			instances, err := runtime.NewManager(cpRepoPath).List(ctx)
			if err != nil {
				return "", err
			}
			if len(instances) == 0 {
				return "No model servers were started by computing-provider. Anything this node is serving was started by the operator and cannot be stopped here.", nil
			}
			var b strings.Builder
			for _, inst := range instances {
				fmt.Fprintf(&b, "%s  backend=%s  endpoint=%s  status=%s  container=%s\n",
					inst.ModelID, inst.Backend, inst.Endpoint, inst.Status, inst.Container)
			}
			return b.String(), nil
		},
	}
}

func toolLocalWeights() *agent.Tool {
	return &agent.Tool{
		Name:        "local_weights",
		Description: "Model weights already downloaded to this node. A model not listed here cannot be served, because weights are never downloaded automatically.",
		Run: func(ctx context.Context, _ agent.Args) (string, error) {
			dir := defaultModelsDir()
			orgs, err := os.ReadDir(dir)
			if err != nil {
				if os.IsNotExist(err) {
					return "No weights downloaded.", nil
				}
				return "", err
			}
			var b strings.Builder
			for _, org := range orgs {
				if !org.IsDir() {
					continue
				}
				models, err := os.ReadDir(dir + "/" + org.Name())
				if err != nil {
					continue
				}
				for _, m := range models {
					if m.IsDir() {
						fmt.Fprintf(&b, "%s/%s\n", org.Name(), m.Name())
					}
				}
			}
			if b.Len() == 0 {
				return "No weights downloaded.", nil
			}
			return b.String(), nil
		},
	}
}

func toolModelMemory(cpRepoPath string) *agent.Tool {
	return &agent.Tool{
		Name:        "model_memory",
		Description: "What this node has actually run and what it cost in VRAM. These are measurements, not estimates, and they override anything derived from published metadata.",
		Args: []agent.Arg{
			{Name: "model_id", Type: "string", Description: "One model to look up in full. Omit to list everything."},
		},
		Run: func(ctx context.Context, args agent.Args) (string, error) {
			store, err := modelmem.Load(cpRepoPath)
			if err != nil {
				return "", err
			}
			if id := args.String("model_id"); id != "" {
				record := store.Get(id)
				if record == nil {
					return fmt.Sprintf("Nothing recorded for %s. This node has never run it.", id), nil
				}
				out := fmt.Sprintf("%s: %s\n", record.ModelID, record.Describe())
				if record.Note != "" {
					out += "Note: " + record.Note + "\n"
				}
				if cmd := modelmem.ReproduceCommand(record); cmd != "" {
					out += "Known-good command:\n" + cmd + "\n"
				}
				return out, nil
			}

			records := store.All()
			if len(records) == 0 {
				return "This node has no record of running any model.", nil
			}
			var b strings.Builder
			for _, r := range records {
				fmt.Fprintf(&b, "%-36s %s\n", r.ModelID, r.Describe())
			}
			return b.String(), nil
		},
	}
}

func toolMarket(cfg *conf.ComputeNode) *agent.Tool {
	return &agent.Tool{
		Name:        "market",
		Description: "The live marketplace: what each model pays a provider, how much demand it has, and how many providers already serve it. Sorted by the per-provider earnings estimate.",
		Args: []agent.Arg{
			{Name: "limit", Type: "integer", Description: "How many models to return. Defaults to 12."},
		},
		Run: func(ctx context.Context, args agent.Args) (string, error) {
			models, err := market.FetchDemand(getServiceURL(cfg), "")
			if err != nil {
				return "", err
			}
			sort.Slice(models, func(i, j int) bool {
				return models[i].EstEntrantDailyEarnings > models[j].EstEntrantDailyEarnings
			})
			limit := args.Int("limit")
			if limit <= 0 || limit > len(models) {
				limit = 12
			}
			if limit > len(models) {
				limit = len(models)
			}

			var b strings.Builder
			fmt.Fprintf(&b, "%d models on the marketplace. Top %d by per-provider daily estimate:\n", len(models), limit)
			for _, m := range models[:limit] {
				vramNote := "VRAM requirement not published"
				if m.VRAMKnown && m.MinVRAMGB > 0 {
					vramNote = fmt.Sprintf("needs %d GB", m.MinVRAMGB)
				}
				fmt.Fprintf(&b, "%-34s $%.2f/day est, %d providers, %d req/24h, demand %s, %s\n",
					m.ModelID, m.EstEntrantDailyEarnings, m.OnlineProviders, m.Requests24h,
					orDashLocal(m.DemandTrend), vramNote)
			}
			b.WriteString("\nUse estimate_vram to find out whether any of these fit this node.\n")
			return b.String(), nil
		},
	}
}

func toolEstimateVRAM(cfg *conf.ComputeNode) *agent.Tool {
	return &agent.Tool{
		Name:        "estimate_vram",
		Description: "Work out whether a model fits this node, from its published parameters and architecture. Use this before proposing to serve anything the node has not run before.",
		Args: []agent.Arg{
			{Name: "model_id", Type: "string", Description: "The model to size, e.g. Qwen/Qwen2.5-7B-Instruct.", Required: true},
			{Name: "context_length", Type: "integer", Description: "Context window in tokens. Defaults to 32768."},
		},
		Run: func(ctx context.Context, args agent.Args) (string, error) {
			modelID := args.String("model_id")
			contextLength := args.Int("context_length")
			if contextLength <= 0 {
				contextLength = 32768
			}

			var budget float64
			if hw := computing.DetectGPUHardware(); hw != nil {
				budget = float64(hw.GPUCount * hw.VRAMGB)
			}

			hub := &vram.Hub{Token: os.Getenv("HF_TOKEN")}
			fit := vram.FitWithin(ctx, hub, modelID,
				vram.Plan{ContextLength: contextLength}, budget, cfg.Inference.AutoSwitch.MinPrecision)

			if !fit.Known() {
				return fmt.Sprintf("%s cannot be sized: %s. It must not be served — an unknown requirement is a refusal, not a zero.",
					modelID, fit.Reason), nil
			}

			var b strings.Builder
			fmt.Fprintf(&b, "%s needs about %.1f GiB at %d tokens of context (%s precision).\n",
				modelID, fit.TotalGiB, contextLength, fit.Precision)
			fmt.Fprintf(&b, "  weights %.1f + KV cache %.1f + overhead %.1f GiB\n",
				fit.WeightsGiB, fit.KVCacheGiB, fit.OverheadGiB)
			if fit.Fits {
				fmt.Fprintf(&b, "  FITS this node's %.0f GiB.\n", budget)
			} else {
				fmt.Fprintf(&b, "  DOES NOT FIT this node's %.0f GiB.\n", budget)
			}
			if fit.RequiresQuantisation {
				b.WriteString("  Only fits below its published precision, which assumes a quantised build exists. That has not been verified.\n")
			}
			for _, n := range fit.Notes {
				b.WriteString("  note: " + n + "\n")
			}
			return b.String(), nil
		},
	}
}

func toolServeModel(cpRepoPath string, cfg *conf.ComputeNode) *agent.Tool {
	return &agent.Tool{
		Name: "serve_model",
		Description: "Start serving a model and declare it. Weights must already be on this node. " +
			"Prefer the settings in model_memory when the node has run it before.",
		Acts: true,
		Args: []agent.Arg{
			{Name: "model_id", Type: "string", Description: "The model to serve.", Required: true},
			{Name: "reason", Type: "string", Description: "Why. This is recorded and mailed to the operator.", Required: true},
			{Name: "context_length", Type: "integer", Description: "Context window in tokens."},
			{Name: "gpus", Type: "string", Description: "Devices, e.g. \"0,1\", or \"all\"."},
		},
		Run: func(ctx context.Context, args agent.Args) (string, error) {
			modelID := args.String("model_id")

			weights, backend, err := resolveLocalWeights(modelID)
			if err != nil {
				return "", err
			}

			spec := &runtime.ServeSpec{
				ModelID:       modelID,
				Backend:       backend,
				Weights:       weights,
				ContextLength: args.Int("context_length"),
				GPUs:          args.String("gpus"),
			}

			manager := runtime.NewManager(cpRepoPath)
			manager.Announcer = switchNotifier(cpRepoPath)

			inst, err := manager.Serve(ctx, spec, runtime.ServeOptions{
				WaitReady: true,
				Reason:    args.String("reason"),
				Decider:   runtime.DeciderAgent,
			})
			if err != nil {
				rememberFailure(cpRepoPath, modelID, err.Error())
				return "", err
			}
			rememberServe(ctx, cpRepoPath, spec, inst, true)
			return fmt.Sprintf("%s is serving at %s (container %s).", inst.ModelID, inst.Endpoint, inst.Container), nil
		},
	}
}

func toolStopModel(cpRepoPath string, cfg *conf.ComputeNode) *agent.Tool {
	return &agent.Tool{
		Name:        "stop_model",
		Description: "Stop a model server this tool started and stop declaring the model. Only models listed by running_models can be stopped.",
		Acts:        true,
		Args: []agent.Arg{
			{Name: "model_id", Type: "string", Description: "The model to stop.", Required: true},
			{Name: "reason", Type: "string", Description: "Why. This is recorded and mailed to the operator.", Required: true},
		},
		Run: func(ctx context.Context, args agent.Args) (string, error) {
			modelID := args.String("model_id")

			// The pin list is enforced here as well as in the scheduler's
			// guardrails. cp.md promises the planner is never stopped, and a
			// promise enforced on one path but not the other is the kind an
			// agent finds. Without this, a node whose planner was started
			// with `models serve` could be talked into stopping its own
			// brain.
			if pinned := pinnedModels(cfg); pinned[strings.ToLower(modelID)] {
				return "", fmt.Errorf("%s is pinned and may never be stopped; it is the planner or on the operator's Pin list", modelID)
			}

			manager := runtime.NewManager(cpRepoPath)
			manager.Announcer = switchNotifier(cpRepoPath)

			inst, err := manager.Stop(ctx, modelID, runtime.StopOptions{
				Remove:  true,
				Reason:  args.String("reason"),
				Decider: runtime.DeciderAgent,
			})
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Stopped %s and removed it from models.json.", inst.ModelID), nil
		},
	}
}

// agentTimeout bounds one model call in an agent run.
const agentTimeout = 3 * time.Minute

// pinnedModels is the set of models the agent must never stop: the planner and
// whatever the operator listed under Pin, matched without regard to case.
func pinnedModels(cfg *conf.ComputeNode) map[string]bool {
	policy := cfg.Inference.AutoSwitch
	out := map[string]bool{}
	if p := strings.TrimSpace(policy.Planner); p != "" {
		out[strings.ToLower(p)] = true
	}
	for _, p := range policy.Pin {
		if p = strings.TrimSpace(p); p != "" {
			out[strings.ToLower(p)] = true
		}
	}
	return out
}
