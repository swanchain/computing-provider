package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/agent"
	"github.com/swanchain/computing-provider-v2/internal/modelmem"
	"github.com/urfave/cli/v2"
)

var agentCmd = &cli.Command{
	Name:      "agent",
	Usage:     "Ask a local model to investigate or operate this node",
	ArgsUsage: "<goal>",
	Description: `Gives a goal in plain language to a model this node already serves, together
with cp.md and a fixed set of tools, and shows what it does.

Read-only unless you say otherwise. The agent cannot run shell commands and
cannot call anything outside its tool list, so what it can do is bounded by
those tools rather than by its own judgement. Tools that change the node are
refused without --allow-actions, and even then they go through the same
guardrails a person running the same command would hit.

The planner model does the reasoning. It is one this node is already serving, so
asking it costs nothing marginal.

Examples:
  computing-provider agent "what is this node earning, and from which models?"
  computing-provider agent "is there a model on the marketplace that would fit and pay better?"
  computing-provider agent "why is nothing being served?"
  computing-provider agent --allow-actions "serve the best-paying model we have weights for"`,
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "allow-actions",
			Usage: "Permit tools that start or stop models. Off by default.",
		},
		&cli.StringFlag{
			Name:  "model",
			Usage: "Model to reason with (default: [Inference.AutoSwitch] Planner)",
		},
		&cli.StringFlag{
			Name:  "endpoint",
			Usage: "Where to reach it (default: its endpoint in models.json)",
		},
		&cli.IntFlag{
			Name:  "max-steps",
			Usage: "Stop after this many tool calls",
			Value: agent.DefaultMaxSteps,
		},
		&cli.BoolFlag{
			Name:  "quiet",
			Usage: "Print only the final answer",
		},
	},
	Action: func(cctx *cli.Context) error {
		goal := strings.TrimSpace(strings.Join(cctx.Args().Slice(), " "))
		if goal == "" {
			return fmt.Errorf("give the agent a goal, e.g. computing-provider agent \"what is this node earning?\"")
		}

		cpRepoPath, err := repoPath(cctx)
		if err != nil {
			return err
		}
		if err := conf.InitConfig(cpRepoPath, true); err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		cfg := conf.GetConfig()

		model := cctx.String("model")
		if model == "" {
			model = cfg.Inference.AutoSwitch.Planner
		}
		if model == "" {
			return fmt.Errorf("no model to reason with: set Planner under [Inference.AutoSwitch], or pass --model")
		}

		endpoint, apiKey := cctx.String("endpoint"), ""
		if endpoint == "" {
			endpoint, apiKey = plannerEndpoint(cpRepoPath, model)
		}
		if endpoint == "" {
			return fmt.Errorf("%s has no endpoint in models.json; pass --endpoint", model)
		}

		// cp.md is the node's own context. Regenerated first so the agent
		// never reasons from a stale copy — it is cheap, and a wrong fact
		// about the node is worse than a missing one.
		nodeCtx := loadNodeContext(cpRepoPath)

		runner := &agent.Agent{
			Endpoint:     endpoint,
			Model:        model,
			APIKey:       apiKey,
			Context:      nodeCtx,
			Tools:        agentTools(cpRepoPath, cfg),
			AllowActions: cctx.Bool("allow-actions"),
			MaxSteps:     cctx.Int("max-steps"),
			Timeout:      agentTimeout,
		}
		if !cctx.Bool("quiet") {
			runner.Observer = &consoleObserver{}
		}

		if !cctx.Bool("quiet") {
			fmt.Println()
			color.Cyan("Goal: %s", goal)
			if runner.AllowActions {
				color.Yellow("This run MAY start and stop models on this node.")
			} else {
				fmt.Println("Read-only: the agent can look but not change anything.")
			}
			fmt.Printf("Reasoning with %s at %s\n", model, endpoint)
			fmt.Println(strings.Repeat("=", 70))
		}

		result, err := runner.Run(context.Background(), goal)
		if err != nil {
			return err
		}

		if cctx.Bool("quiet") {
			fmt.Println(result.Answer)
			return nil
		}

		fmt.Println()
		fmt.Println(strings.Repeat("=", 70))
		color.Green("Answer")
		fmt.Println(result.Answer)
		fmt.Println()
		fmt.Printf("%d steps", result.Steps)
		if result.Acted {
			color.Yellow("  — this run changed what the node serves")
		} else {
			fmt.Print("  — nothing on the node was changed")
		}
		fmt.Println()
		return nil
	},
}

// loadNodeContext returns cp.md, regenerating it first so the agent is never
// given a stale picture of the node.
func loadNodeContext(cpRepoPath string) string {
	store, err := modelmem.Load(cpRepoPath)
	if err == nil {
		store.SetNodeContext(nodeContext(cpRepoPath, store))
		_ = store.Save()
	}
	data, err := os.ReadFile(filepath.Join(cpRepoPath, modelmem.MarkdownFile))
	if err != nil {
		return ""
	}
	return string(data)
}

// consoleObserver narrates a run.
//
// Every step is shown, including the arguments. An agent that changes a node
// while printing only its conclusions gives the operator no way to stop it part
// way, and no way to understand afterwards what it actually did.
type consoleObserver struct{}

func (c *consoleObserver) Thinking(step int, thought string) {
	fmt.Println()
	color.Cyan("[%d] %s", step, thought)
}

func (c *consoleObserver) Using(step int, tool string, args agent.Args, permitted bool) {
	var pairs []string
	for k, v := range args {
		pairs = append(pairs, fmt.Sprintf("%s=%v", k, v))
	}
	call := tool
	if len(pairs) > 0 {
		call += "(" + strings.Join(pairs, ", ") + ")"
	}
	if permitted {
		fmt.Printf("    → %s\n", call)
	} else {
		color.Red("    ✗ %s — refused", call)
	}
}

func (c *consoleObserver) Observed(step int, output string, err error) {
	if err != nil {
		color.Red("      %v", err)
		return
	}
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		fmt.Printf("      %s\n", line)
	}
}

func (c *consoleObserver) Ungrounded(step int, answer string) {
	color.Red("    ✗ tried to answer without checking anything — pushed back")
	if answer != "" {
		fmt.Printf("      discarded: %s\n", truncate(strings.TrimSpace(answer), 160))
	}
}

func (c *consoleObserver) Finished(string) {}
