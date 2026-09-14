package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/computing"
	"github.com/swanchain/computing-provider-v2/internal/modelmem"
	"github.com/swanchain/computing-provider-v2/internal/runtime"
	"github.com/urfave/cli/v2"
)

// settleBeforeMeasuring is how long to wait after a model answers before
// reading its memory use.
//
// A model that has answered once has not finished allocating: llama.cpp fills
// its KV cache lazily and vLLM's profiling run settles a moment after the
// server starts responding. Measuring immediately records a figure below what
// the model will hold, which is the direction that over-commits a GPU.
const settleBeforeMeasuring = 15 * time.Second

// rememberServe records a successful start, measuring what it cost.
//
// Failures here are reported and otherwise ignored: the model is serving, and
// losing a bookkeeping entry is not a reason to tell the operator their start
// failed.
func rememberServe(ctx context.Context, cpRepoPath string, spec *runtime.ServeSpec, inst *runtime.Instance, measured bool) {
	store, err := modelmem.Load(cpRepoPath)
	if err != nil {
		fmt.Printf("Note: model memory could not be read (%v); this run was not recorded\n", err)
		return
	}

	record := modelmem.Record{
		ModelID: spec.ModelID,
		Weights: modelmem.Weights{
			LocalPath:    spec.Weights,
			Quantisation: quantisationOf(spec.Weights),
			SizeBytes:    fileSize(spec.Weights),
		},
		Serving: modelmem.Serving{
			Backend:       inst.Backend,
			Image:         inst.Image,
			GPUs:          spec.GPUs,
			ContextLength: spec.ContextLength,
			ExtraArgs:     spec.ExtraArgs,
		},
	}

	if hw := computing.DetectGPUHardware(); hw != nil {
		record.Hardware = modelmem.Hardware{
			GPUModel:     hw.GPUModel,
			GPUCount:     hw.GPUCount,
			VRAMPerGPUGB: hw.VRAMGB,
		}
	}

	if measured {
		fmt.Printf("Measuring what %s actually uses...\n", spec.ModelID)
		m, err := modelmem.MeasureWhenSettled(ctx, inst.Container, settleBeforeMeasuring)
		if err != nil {
			fmt.Printf("Note: could not measure GPU use (%v)\n", err)
		} else {
			record.Measured = m
			color.Green("  %s uses %.2f GiB across %d GPU(s)", spec.ModelID, m.VRAMGiB, len(m.PerGPUGiB))
		}
	}

	store.SetNodeContext(nodeContext(cpRepoPath, store))
	store.RecordSuccess(time.Now().UTC(), record)
	if err := store.Save(); err != nil {
		fmt.Printf("Note: model memory could not be saved (%v)\n", err)
		return
	}
	fmt.Printf("Recorded in %s and %s\n", modelmem.StoreFile, modelmem.MarkdownFile)
}

// rememberFailure records a start that did not work, so it is not retried
// blindly.
func rememberFailure(cpRepoPath, modelID, note string) {
	store, err := modelmem.Load(cpRepoPath)
	if err != nil {
		return
	}
	store.SetNodeContext(nodeContext(cpRepoPath, store))
	store.RecordFailure(time.Now().UTC(), modelID, note)
	_ = store.Save()
}

// nodeContext gathers what cp.md should say about this machine.
func nodeContext(cpRepoPath string, store *modelmem.Store) modelmem.NodeContext {
	node := modelmem.NodeContext{NodeID: computing.GetNodeId(cpRepoPath)}
	if err := conf.InitConfig(cpRepoPath, true); err == nil {
		node.NodeName = conf.GetConfig().API.NodeName
	}
	if hw := computing.DetectGPUHardware(); hw != nil {
		node.GPUModel = hw.GPUModel
		node.GPUCount = hw.GPUCount
		node.VRAMPerGPUGB = hw.VRAMGB
		node.TotalVRAMGB = hw.VRAMGB * hw.GPUCount
	}
	if declared, err := readDeclaredModels(cpRepoPath); err == nil {
		for id := range declared {
			node.Serving = append(node.Serving, id)
		}
	}
	return node
}

// quantisationOf guesses a weight format from the path, which is where it is
// usually written: a GGUF filename carries its quantisation, and an AWQ
// directory is normally named for it.
func quantisationOf(path string) string {
	for _, tag := range []string{
		"Q4_K_M", "Q4_K_S", "Q5_K_M", "Q3_K_M", "Q8_0", "Q6_K", "Q5_0", "Q4_0", "Q2_K",
		"AWQ", "GPTQ", "FP8", "BF16", "FP16",
	} {
		if strings.Contains(strings.ToUpper(path), tag) {
			return tag
		}
	}
	return ""
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if !info.IsDir() {
		return info.Size()
	}
	var total int64
	_ = filepath.Walk(path, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			total += fi.Size()
		}
		return nil
	})
	return total
}

var modelsMemoryCmd = &cli.Command{
	Name:  "memory",
	Usage: "What this node remembers about models it has run",
	Description: `Shows the models this node has actually run, what they cost in VRAM, and the
settings that worked.

This is the node's own ground truth. Where it disagrees with an estimate derived
from published metadata, this is right: published metadata describes the original
weights, and this is what the node actually loaded. A model an estimate calls too
large may be one you have already made work.

The records live in ` + modelmem.StoreFile + `, and ` + modelmem.MarkdownFile + ` is a
readable rendering of them for an agent or a person picking the node up later.

Examples:
  computing-provider models memory
  computing-provider models memory --json
  computing-provider models memory --show Qwen/Qwen3.8-27B
  computing-provider models memory --pin Qwen/Qwen3.8-27B
  computing-provider models memory --forget some/Model`,
	Flags: []cli.Flag{
		&cli.BoolFlag{Name: "json", Usage: "Output the raw records"},
		&cli.StringFlag{Name: "show", Usage: "Show one model in full, with the command that reproduces it"},
		&cli.StringFlag{Name: "forget", Usage: "Remove a model's record"},
		&cli.StringFlag{Name: "pin", Usage: "Protect a record from being overwritten automatically"},
		&cli.StringFlag{Name: "unpin", Usage: "Allow a record to be updated automatically again"},
		&cli.StringFlag{Name: "note", Usage: "Attach a note to a model (use with --show's model ID)"},
		&cli.BoolFlag{Name: "regenerate", Usage: "Rewrite " + modelmem.MarkdownFile + " from the records"},
	},
	Action: func(cctx *cli.Context) error {
		cpRepoPath, err := repoPath(cctx)
		if err != nil {
			return err
		}
		store, err := modelmem.Load(cpRepoPath)
		if err != nil {
			return err
		}
		store.SetNodeContext(nodeContext(cpRepoPath, store))

		switch {
		case cctx.String("forget") != "":
			id := cctx.String("forget")
			if !store.Forget(id) {
				return fmt.Errorf("nothing recorded for %s", id)
			}
			if err := store.Save(); err != nil {
				return err
			}
			color.Green("Forgot %s", id)
			return nil

		case cctx.String("pin") != "" || cctx.String("unpin") != "":
			id, pinned := cctx.String("pin"), true
			if id == "" {
				id, pinned = cctx.String("unpin"), false
			}
			if !store.Pin(id, pinned) {
				return fmt.Errorf("nothing recorded for %s", id)
			}
			if err := store.Save(); err != nil {
				return err
			}
			if pinned {
				color.Green("Pinned %s; automatic runs will not overwrite it", id)
			} else {
				color.Green("Unpinned %s", id)
			}
			return nil

		case cctx.Bool("regenerate"):
			if err := store.Save(); err != nil {
				return err
			}
			color.Green("Rewrote %s", filepath.Join(cpRepoPath, modelmem.MarkdownFile))
			return nil
		}

		if id := cctx.String("show"); id != "" {
			record := store.Get(id)
			if record == nil {
				return fmt.Errorf("nothing recorded for %s", id)
			}
			if note := cctx.String("note"); note != "" {
				record.Note = note
				if err := store.Save(); err != nil {
					return err
				}
			}
			if cctx.Bool("json") {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(record)
			}
			return showRecord(record)
		}

		records := store.All()
		if cctx.Bool("json") {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(records)
		}
		if len(records) == 0 {
			fmt.Println("Nothing recorded yet.")
			fmt.Println("This fills in as models are served with: computing-provider models serve <model-id> ...")
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Model", "Status", "Measured", "Weights", "Backend", "Context", "Runs"})
		table.SetAutoWrapText(false)

		for _, r := range records {
			status := color.GreenString("works")
			if !r.Works() {
				status = color.RedString("failed")
			}
			if r.Pinned {
				status += " (pinned)"
			}
			vram := "-"
			if v := r.VRAMGiB(); v > 0 {
				vram = fmt.Sprintf("%.1f GiB", v)
			}
			quant := r.Weights.Quantisation
			if quant == "" {
				quant = r.Weights.Format
			}
			ctxLen := "-"
			if r.Serving.ContextLength > 0 {
				ctxLen = fmt.Sprintf("%d", r.Serving.ContextLength)
			}
			runs := fmt.Sprintf("%d ok", r.Successes)
			if r.Failures > 0 {
				runs += fmt.Sprintf(", %d failed", r.Failures)
			}
			table.Append([]string{r.ModelID, status, vram, orDashLocal(quant), orDashLocal(r.Serving.Backend), ctxLen, runs})
		}
		table.Render()

		fmt.Println()
		fmt.Printf("Records: %s\n", filepath.Join(cpRepoPath, modelmem.StoreFile))
		fmt.Printf("For an agent: %s\n", filepath.Join(cpRepoPath, modelmem.MarkdownFile))
		return nil
	},
}

func showRecord(r *modelmem.Record) error {
	fmt.Println()
	color.Cyan("%s", r.ModelID)
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("Status:    %s\n", r.Status)
	if r.Pinned {
		fmt.Println("Pinned:    yes — automatic runs will not overwrite this")
	}
	if r.Note != "" {
		fmt.Printf("Note:      %s\n", r.Note)
	}
	if r.Measured != nil {
		fmt.Printf("Measured:  %.2f GiB across %d GPU(s) on %s\n",
			r.Measured.VRAMGiB, len(r.Measured.PerGPUGiB), r.Measured.At.Format("2006-01-02"))
	}
	if r.Hardware.GPUModel != "" {
		fmt.Printf("Hardware:  %d x %s (%d GB each)\n", r.Hardware.GPUCount, r.Hardware.GPUModel, r.Hardware.VRAMPerGPUGB)
	}
	if r.Weights.LocalPath != "" {
		fmt.Printf("Weights:   %s\n", r.Weights.LocalPath)
	}
	if r.Weights.Repo != "" {
		fmt.Printf("Source:    %s\n", r.Weights.Repo)
	}
	fmt.Printf("Runs:      %d successful, %d failed\n", r.Successes, r.Failures)

	if cmd := modelmem.ReproduceCommand(r); cmd != "" {
		fmt.Println()
		fmt.Println("Reproduce with:")
		fmt.Println("  " + strings.ReplaceAll(cmd, "\n", "\n  "))
	}
	fmt.Println()
	return nil
}

func orDashLocal(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
