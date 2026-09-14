package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/mitchellh/go-homedir"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/autoswitch"
	"github.com/swanchain/computing-provider-v2/internal/computing"
	"github.com/swanchain/computing-provider-v2/internal/market"
	"github.com/swanchain/computing-provider-v2/internal/runtime"
	"github.com/swanchain/computing-provider-v2/internal/vram"
	"github.com/urfave/cli/v2"
)

var inferencePlanCmd = &cli.Command{
	Name:  "plan",
	Usage: "Run one auto-switch planning cycle and show the decision, without acting",
	Description: `Builds the snapshot the auto-switch planner sees — this node's hardware, what it
serves now, and the live market — asks the planner model to decide, then checks
that decision against the guardrails and prints both.

It changes nothing. No server is started or stopped and models.json is not
touched. This is the command for seeing what the scheduler would do, and why it
would be refused, before the scheduler exists to do it.

The planner is a model this node already serves, so asking it costs nothing
marginal. Configure it under [Inference.AutoSwitch]:

  [Inference.AutoSwitch]
  Planner      = "Qwen/Qwen3.8-27B"
  MinMarginUSD = 0.50
  Pin          = ["Qwen/Qwen3.8-27B"]
  Deny         = []

Examples:
  computing-provider inference plan
  computing-provider inference plan --json
  computing-provider inference plan --planner Qwen/Qwen3.8-27B --show-snapshot`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "planner",
			Usage: "Model to ask (default: [Inference.AutoSwitch] Planner)",
		},
		&cli.StringFlag{
			Name:  "planner-endpoint",
			Usage: "Where to reach the planner (default: its endpoint in models.json)",
		},
		&cli.Float64Flag{
			Name:  "margin",
			Usage: "Minimum gain in $/day a switch must show (default: config, else 0.50)",
		},
		&cli.BoolFlag{
			Name:  "show-snapshot",
			Usage: "Print the snapshot handed to the planner",
		},
		&cli.BoolFlag{
			Name:  "json",
			Usage: "Emit the snapshot, decision and verdict as JSON",
		},
		&cli.StringFlag{
			Name:  "decision",
			Usage: "Evaluate this decision JSON against the live snapshot instead of asking the planner (use @file to read from a file)",
		},
	},
	Action: func(cctx *cli.Context) error {
		cpRepoPath, err := homedir.Expand(cctx.String(FlagRepo.Name))
		if err != nil {
			return err
		}
		if err := conf.InitConfig(cpRepoPath, true); err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		cfg := conf.GetConfig()
		policy := cfg.Inference.AutoSwitch.WithDefaults()

		plannerModel := cctx.String("planner")
		if plannerModel == "" {
			plannerModel = policy.Planner
		}
		if plannerModel == "" {
			return fmt.Errorf("no planner configured: set Planner under [Inference.AutoSwitch] in config.toml, or pass --planner")
		}

		ctx := context.Background()

		snapshot, notes, err := buildPlanSnapshot(ctx, cpRepoPath, cfg, policy, plannerModel)
		if err != nil {
			return err
		}

		endpoint := cctx.String("planner-endpoint")
		apiKey := ""
		if endpoint == "" {
			endpoint, apiKey = plannerEndpoint(cpRepoPath, plannerModel)
		}
		if endpoint == "" {
			return fmt.Errorf("planner %s has no endpoint in models.json; pass --planner-endpoint", plannerModel)
		}

		margin := policy.MinMarginUSD
		if cctx.IsSet("margin") {
			margin = cctx.Float64("margin")
		}

		// The planner is always pinned, whatever the operator configured. A
		// switch that unloads the model making the decision leaves the node
		// with no planner for the next cycle.
		pins := dedupePins(append(append([]string{}, policy.Pin...), plannerModel))

		guard := autoswitch.Policy{
			Pin:            pins,
			Deny:           policy.Deny,
			MinMarginUSD:   margin,
			MinDwell:       time.Duration(policy.MinDwellMin) * time.Minute,
			ConfirmCycles:  policy.ConfirmCycles,
			MaxSwitchesDay: policy.MaxSwitchesDay,
		}

		if !cctx.Bool("json") {
			printPlanHeader(snapshot, plannerModel, endpoint, notes)
		}
		if cctx.Bool("show-snapshot") && !cctx.Bool("json") {
			out, _ := json.MarshalIndent(snapshot, "", "  ")
			fmt.Println(string(out))
			fmt.Println()
		}

		var (
			decision  *autoswitch.Decision
			raw       *autoswitch.Raw
			decideErr error
		)
		if proposed := cctx.String("decision"); proposed != "" {
			// Checking a policy against a decision the operator supplies,
			// rather than one the planner happened to make. The snapshot is
			// still the live one, so this answers "what would my guardrails
			// do about this plan, on this node, right now" — which is not a
			// question a unit test can answer.
			decision, raw, decideErr = loadProposedDecision(proposed)
		} else {
			decision, raw, decideErr = autoswitch.NewPlanner(endpoint, plannerModel, apiKey).Decide(ctx, snapshot)
		}

		// A planner that cannot be parsed is refused, not obeyed and not
		// silently treated as a keep by the caller: Evaluate is given a nil
		// decision and returns the same refusal it would for any other
		// invalid one.
		verdict := autoswitch.Evaluate(decision, snapshot, guard)

		if cctx.Bool("json") {
			out := map[string]interface{}{
				"snapshot": snapshot,
				"planner":  map[string]string{"model": plannerModel, "endpoint": endpoint},
				"decision": decision,
				"verdict":  verdict,
				"notes":    notes,
			}
			if decideErr != nil {
				out["planner_error"] = decideErr.Error()
			}
			if raw != nil {
				out["planner_raw"] = raw.Content
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		}

		return printPlanResult(decision, raw, decideErr, verdict, guard)
	},
}

// planNote records something the operator needs to know to read the result,
// such as an input that could not be collected.
type planNote = string

// buildPlanSnapshot assembles everything the planner is shown.
func buildPlanSnapshot(ctx context.Context, cpRepoPath string, cfg *conf.ComputeNode, policy conf.AutoSwitch, plannerModel string) (*autoswitch.Snapshot, []planNote, error) {
	var notes []planNote

	hardware := computing.DetectGPUHardware()
	node := autoswitch.NodeInfo{Name: cfg.API.NodeName}
	if hardware != nil {
		node.GPUModel = hardware.GPUModel
		node.GPUCount = hardware.GPUCount
		node.VRAMPerGPUGB = hardware.VRAMGB
		node.TotalVRAMGB = hardware.VRAMGB * hardware.GPUCount
		node.ServingEngines = hardware.ServingEngine
	} else {
		notes = append(notes, "GPU hardware could not be detected, so no model can be shown to fit")
	}

	serviceURL := getServiceURL(cfg)
	demand, err := market.FetchDemand(serviceURL, "")
	if err != nil {
		return nil, notes, fmt.Errorf("could not read the market: %w", err)
	}

	// Work out what the marketplace did not publish. Today it publishes no
	// VRAM requirement for any model, so without this every model is
	// "unknown" and every proposed switch is refused — a correct scheduler
	// that can never act.
	estimates := deriveMissingVRAM(ctx, demand, plannerContext(policy), float64(node.TotalVRAMGB), policy.MinPrecision, &notes)

	models := make([]autoswitch.MarketModel, 0, len(demand))
	for _, m := range demand {
		entry := autoswitch.MarketModel{
			ModelID:             m.ModelID,
			Category:            m.Category,
			ProviderInputPrice:  m.ProviderInputPrice,
			ProviderOutputPrice: m.ProviderOutputPrice,
			OnlineProviders:     m.OnlineProviders,
			Requests24h:         m.Requests24h,
			Tokens24h:           m.Tokens24h,
			DemandTrend:         m.DemandTrend,
			DemandChangePct:     m.DemandChangePct,
			MinVRAMGB:           m.MinVRAMGB,
			VRAMKnown:           m.VRAMKnown,
			VRAMFit:             m.Fit(node.TotalVRAMGB),
			EstEntrantDailyUSD:  m.EstEntrantDailyEarnings,
			EntrantBasis:        m.EntrantBasis,
			PlanCovered:         m.PlanCovered,
			SubscriptionShare:   m.SubscriptionShare,
			ContextLength:       m.ContextLength,
			PromptTokensP95:     m.PromptTokensP95,
		}

		switch {
		case m.VRAMKnown && m.MinVRAMGB > 0:
			// A published requirement outranks anything derived here.
			entry.VRAMSource = "published"

		case estimates[m.ModelID] != nil && estimates[m.ModelID].Known():
			fit := estimates[m.ModelID]
			entry.EstimatedVRAMGiB = fit.TotalGiB
			entry.VRAMSource = "derived"
			entry.VRAMPrecision = fit.Precision
			if fit.Fits {
				entry.VRAMFit = market.FitFits
			} else {
				entry.VRAMFit = market.FitTooLarge
			}
		}

		models = append(models, entry)
	}

	serving, dailyUSD, servingNotes := buildServingList(ctx, cpRepoPath, cfg, policy, plannerModel)
	notes = append(notes, servingNotes...)

	return &autoswitch.Snapshot{
		Node:            node,
		Serving:         serving,
		Market:          models,
		CurrentDailyUSD: dailyUSD,
	}, notes, nil
}

// buildServingList describes what this node serves right now.
func buildServingList(ctx context.Context, cpRepoPath string, cfg *conf.ComputeNode, policy conf.AutoSwitch, plannerModel string) ([]autoswitch.ServingModel, *float64, []planNote) {
	var notes []planNote
	var dailyUSD *float64

	declared, err := readDeclaredModels(cpRepoPath)
	if err != nil {
		notes = append(notes, fmt.Sprintf("models.json could not be read (%v), so the node looks like it serves nothing", err))
	}

	// Which of them computing-provider can actually stop.
	managed := map[string]bool{}
	if instances, err := runtime.NewManager(cpRepoPath).List(ctx); err == nil {
		for _, inst := range instances {
			managed[inst.ModelID] = true
		}
	} else {
		notes = append(notes, "docker is unavailable, so no served model is shown as manageable")
	}

	earnings, total, earnErr := fetchLocalDailyEarnings(cfg)
	if earnErr != nil {
		// Not substituted with zero: a zero baseline makes every proposal
		// look profitable, so the guardrails refuse a switch outright when
		// this is missing rather than judging it against a number nobody
		// measured.
		notes = append(notes, fmt.Sprintf("this node's daily earnings are unavailable (%v), so no switch can clear the margin rule", earnErr))
	} else {
		dailyUSD = &total
	}

	pinned := map[string]bool{strings.ToLower(plannerModel): true}
	for _, p := range policy.Pin {
		pinned[strings.ToLower(strings.TrimSpace(p))] = true
	}

	health := fetchLocalHealth(cfg)

	serving := make([]autoswitch.ServingModel, 0, len(declared))
	for modelID, endpoint := range declared {
		serving = append(serving, autoswitch.ServingModel{
			ModelID:      modelID,
			Managed:      managed[modelID],
			Pinned:       pinned[strings.ToLower(modelID)],
			Endpoint:     endpoint,
			Healthy:      health[modelID],
			EarnedUSD24h: earnings[modelID],
		})
	}
	sort.Slice(serving, func(i, j int) bool { return serving[i].ModelID < serving[j].ModelID })
	return serving, dailyUSD, notes
}

// readDeclaredModels returns model ID to endpoint from models.json.
func readDeclaredModels(cpRepoPath string) (map[string]string, error) {
	data, err := os.ReadFile(runtime.ModelsPath(cpRepoPath))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var entries map[string]struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(entries))
	for id, e := range entries {
		out[id] = e.Endpoint
	}
	return out, nil
}

// plannerEndpoint finds where a model is served, from models.json.
func plannerEndpoint(cpRepoPath, modelID string) (endpoint, apiKey string) {
	data, err := os.ReadFile(runtime.ModelsPath(cpRepoPath))
	if err != nil {
		return "", ""
	}
	var entries map[string]struct {
		Endpoint string `json:"endpoint"`
		APIKey   string `json:"api_key"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return "", ""
	}
	e, ok := entries[modelID]
	if !ok {
		return "", ""
	}
	return e.Endpoint, e.APIKey
}

// fetchLocalDailyEarnings reads what this node earned over the last 24 hours,
// per model and in total.
//
// From the daemon's own history rather than its lifetime counters: the
// aggregate counters reset when the process restarts and cover an unbounded
// window, so using them as a daily figure would compare a projection in $/day
// against a total accumulated over however long the process happened to have
// been up.
//
// The node's own arithmetic, not the platform's, because this is the number
// the operator sees on their own dashboard and the one a refusal has to be
// explainable against.
func fetchLocalDailyEarnings(cfg *conf.ComputeNode) (perModel map[string]float64, total float64, err error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/api/v1/computing/inference/earnings/history?duration=24h", cfg.API.Port)
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return nil, 0, fmt.Errorf("the daemon is not reachable on port %d", cfg.API.Port)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("the daemon returned HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Points []struct {
			USD    float64 `json:"usd"`
			Models map[string]struct {
				USD float64 `json:"usd"`
			} `json:"models"`
		} `json:"points"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, 0, fmt.Errorf("the daemon's earnings history could not be read: %w", err)
	}
	if len(payload.Points) == 0 {
		return nil, 0, fmt.Errorf("the daemon has no earnings history yet")
	}

	perModel = map[string]float64{}
	for _, point := range payload.Points {
		total += point.USD
		for modelID, m := range point.Models {
			perModel[modelID] += m.USD
		}
	}
	return perModel, total, nil
}

func printPlanHeader(s *autoswitch.Snapshot, plannerModel, endpoint string, notes []planNote) {
	fmt.Println()
	color.Cyan("Auto-switch plan (dry run — nothing will be started or stopped)")
	fmt.Println(strings.Repeat("=", 70))

	hw := "not detected"
	if s.Node.TotalVRAMGB > 0 {
		hw = fmt.Sprintf("%dx %s (%d GB each, %d GB total)",
			s.Node.GPUCount, s.Node.GPUModel, s.Node.VRAMPerGPUGB, s.Node.TotalVRAMGB)
	}
	fmt.Printf("Node:     %s\n", orDash(s.Node.Name))
	fmt.Printf("Hardware: %s\n", hw)
	fmt.Printf("Planner:  %s at %s\n", plannerModel, endpoint)

	fmt.Printf("Serving:  %d models", len(s.Serving))
	if len(s.Serving) > 0 {
		var names []string
		for _, m := range s.Serving {
			label := m.ModelID
			if m.Pinned {
				label += " (pinned)"
			}
			if !m.Managed {
				label += " (unmanaged)"
			}
			names = append(names, label)
		}
		fmt.Printf(" — %s", strings.Join(names, ", "))
	}
	fmt.Println()

	fits := 0
	for _, m := range s.Market {
		if m.VRAMFit == market.FitFits {
			fits++
		}
	}
	fmt.Printf("Market:   %d models, %d known to fit this node\n", len(s.Market), fits)

	for _, n := range notes {
		color.Yellow("Note:     %s", n)
	}
	fmt.Println()
}

func printPlanResult(decision *autoswitch.Decision, raw *autoswitch.Raw, decideErr error, verdict autoswitch.Verdict, policy autoswitch.Policy) error {
	if decideErr != nil {
		color.Red("Planner failed: %v", decideErr)
		if raw != nil && raw.Content != "" {
			fmt.Println("Planner said:")
			fmt.Println("  " + truncate(strings.TrimSpace(raw.Content), 500))
		}
		fmt.Println()
	} else if decision != nil {
		fmt.Println("Planner decision")
		fmt.Println(strings.Repeat("-", 70))
		fmt.Printf("  action:             %s\n", decision.Action)
		if len(decision.Serve) > 0 {
			fmt.Printf("  serve:              %s\n", strings.Join(decision.Serve, ", "))
		}
		if len(decision.Stop) > 0 {
			fmt.Printf("  stop:               %s\n", strings.Join(decision.Stop, ", "))
		}
		fmt.Printf("  expected $/day:     $%.2f\n", decision.ExpectedDailyUSD)
		fmt.Printf("  reason:             %s\n", truncate(strings.TrimSpace(decision.Reason), 400))
		fmt.Println()
	}

	fmt.Println("Guardrail verdict")
	fmt.Println(strings.Repeat("-", 70))
	if verdict.Allowed {
		if verdict.Effective.Action == autoswitch.ActionKeep {
			color.Green("  ALLOWED — keep. Nothing would change.")
		} else {
			color.Green("  ALLOWED — the scheduler would switch.")
			fmt.Printf("    serve: %s\n", strings.Join(verdict.Effective.Serve, ", "))
			if len(verdict.Effective.Stop) > 0 {
				fmt.Printf("    stop:  %s\n", strings.Join(verdict.Effective.Stop, ", "))
			}
		}
	} else {
		color.Red("  REFUSED — %d rule(s) broken. The node would keep its current models.", len(verdict.Violations))
		for _, v := range verdict.Violations {
			fmt.Printf("    %-24s %s\n", v.Rule, v.Detail)
		}
	}

	if len(verdict.Deferred) > 0 {
		fmt.Println()
		fmt.Println("  Not checked in a single cycle (the scheduler keeps this state):")
		for _, d := range verdict.Deferred {
			fmt.Printf("    - %s\n", d)
		}
	}

	fmt.Println()
	fmt.Printf("Policy: margin $%.2f/day, dwell %s, confirm %d cycles, max %d switches/day\n",
		policy.MinMarginUSD, policy.MinDwell, policy.ConfirmCycles, policy.MaxSwitchesDay)
	if len(policy.Pin) > 0 {
		fmt.Printf("Pinned: %s\n", strings.Join(policy.Pin, ", "))
	}
	fmt.Println()
	fmt.Println("This command changes nothing. The scheduler that would act on it is not built yet.")
	return nil
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// fetchLocalHealth asks the daemon which models are healthy.
//
// A model missing from the answer keeps a nil health rather than false. The
// planner is told what is known and nothing more: shown "healthy: false" for
// every model merely because the daemon is not running, it reasons about an
// outage that is not happening.
func fetchLocalHealth(cfg *conf.ComputeNode) map[string]*bool {
	out := map[string]*bool{}

	url := fmt.Sprintf("http://127.0.0.1:%d/api/v1/computing/inference/health", cfg.API.Port)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out
	}

	var payload struct {
		Models map[string]string `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return out
	}
	for modelID, status := range payload.Models {
		healthy := status == "healthy"
		out[modelID] = &healthy
	}
	return out
}

// dedupePins removes repeats so the printed policy does not list the planner
// twice when an operator has already pinned it.
func dedupePins(pins []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range pins {
		key := strings.ToLower(strings.TrimSpace(p))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// loadProposedDecision reads a decision supplied on the command line.
//
// It goes through the same parser the planner's reply does, so a decision that
// would be rejected as unparseable coming from the model is rejected the same
// way coming from an operator. Testing a policy against a decision the
// guardrails would never see is not a test of anything.
func loadProposedDecision(arg string) (*autoswitch.Decision, *autoswitch.Raw, error) {
	body := arg
	if strings.HasPrefix(arg, "@") {
		data, err := os.ReadFile(strings.TrimPrefix(arg, "@"))
		if err != nil {
			return nil, nil, fmt.Errorf("could not read the decision file: %w", err)
		}
		body = string(data)
	}
	raw := &autoswitch.Raw{Content: body}
	decision, err := autoswitch.ParseDecision(body)
	if err != nil {
		return nil, raw, err
	}
	return decision, raw, nil
}

// plannerContext is the context length a candidate model is sized against.
//
// Sizing every model at its own declared maximum would reject almost all of
// them: a 262k-token window costs more cache than most nodes have. A node
// chooses what to serve, so the estimate answers for what it would actually
// serve rather than for the model's theoretical ceiling.
func plannerContext(policy conf.AutoSwitch) int {
	if policy.EstimateContextLength > 0 {
		return policy.EstimateContextLength
	}
	return 32768
}

// vramEstimateConcurrency bounds how many candidates are sized at once. Each is
// one or two HTTP calls and the demand table runs to dozens, so serially this
// would make `plan` take a minute; unbounded it would open dozens of
// connections to the model hub at once.
const vramEstimateConcurrency = 6

// deriveMissingVRAM computes a requirement for every model the marketplace did
// not publish one for.
//
// Failures are counted rather than raised. A model whose requirement cannot be
// derived stays unknown and is refused by the guardrails, which is the correct
// outcome and not an error worth stopping the cycle for.
func deriveMissingVRAM(ctx context.Context, demand []market.DemandEntry, contextLength int, budgetGiB float64, minPrecision string, notes *[]planNote) map[string]*vram.FitResult {
	out := make(map[string]*vram.FitResult, len(demand))

	var pending []market.DemandEntry
	for _, m := range demand {
		if m.VRAMKnown && m.MinVRAMGB > 0 {
			continue
		}
		pending = append(pending, m)
	}
	if len(pending) == 0 {
		return out
	}

	hub := &vram.Hub{Token: os.Getenv("HF_TOKEN")}
	plan := vram.Plan{ContextLength: contextLength}

	var (
		mu        sync.Mutex
		wg        sync.WaitGroup
		sem       = make(chan struct{}, vramEstimateConcurrency)
		fail      int
		quantised int
	)
	for _, m := range pending {
		wg.Add(1)
		go func(modelID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// FitWithin rather than Derive: a model is sized at the
			// precision the node would actually serve it in, not at the
			// bf16 the repository publishes. This node runs a 27B that is
			// 60 GiB at bf16 in 17 GiB, because it serves a Q4 build.
			// Sizing only the published precision would refuse models the
			// node is demonstrably already running.
			fit := vram.FitWithin(ctx, hub, modelID, plan, budgetGiB, minPrecision)

			mu.Lock()
			defer mu.Unlock()
			out[modelID] = fit
			if !fit.Known() {
				fail++
			} else if fit.RequiresQuantisation {
				quantised++
			}
		}(m.ModelID)
	}
	wg.Wait()

	derived := len(pending) - fail
	*notes = append(*notes, fmt.Sprintf(
		"the marketplace published no VRAM requirement for %d of %d models; this node derived %d of them at %d tokens of context",
		len(pending), len(demand), derived, contextLength))
	if fail > 0 {
		*notes = append(*notes, fmt.Sprintf(
			"%d could not be derived and stay unknown, so they cannot be selected (set HF_TOKEN to read gated repositories)", fail))
	}
	if quantised > 0 {
		// Stated plainly because it is an assumption the node cannot check:
		// a model that only fits quantised needs a quantised build to exist,
		// and nothing here has verified that one does.
		*notes = append(*notes, fmt.Sprintf(
			"%d fit only below their published precision, which assumes a quantised build exists — unverified", quantised))
	}
	return out
}
