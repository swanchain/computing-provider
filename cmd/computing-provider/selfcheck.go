package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/selfcheck"
	"github.com/urfave/cli/v2"
)

var selfcheckCmd = &cli.Command{
	Name:  "selfcheck",
	Usage: "Audit this provider for problems that produce no error: unregistered models, context mismatches, backends that pass health checks but cannot serve",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "json",
			Usage: "Print the report as JSON",
		},
		&cli.BoolFlag{
			Name:  "no-inference",
			Usage: "Skip the real-completion probe (which costs one token per model)",
		},
		&cli.Float64Flag{
			Name:  "min-free-gb",
			Usage: "Warn when a volume has less than this much free space",
			Value: 10,
		},
	},
	Action: func(cctx *cli.Context) error {
		cpRepoPath, ok := os.LookupEnv("CP_PATH")
		if !ok {
			return fmt.Errorf("missing CP_PATH env, please set export CP_PATH=<YOUR CP_PATH>")
		}
		if err := conf.InitConfig(cpRepoPath, true); err != nil {
			return fmt.Errorf("load config file failed, error: %+v", err)
		}
		cfg := conf.GetConfig()

		report := selfcheck.Run(selfcheck.Options{
			RepoPath:       cpRepoPath,
			APIBase:        fmt.Sprintf("http://localhost:%d", cfg.API.Port),
			ConfigModels:   cfg.Inference.Models,
			LogDir:         cfg.Log.Dir,
			MinFreeGB:      cctx.Float64("min-free-gb"),
			SkipCompletion: cctx.Bool("no-inference"),
		})

		if cctx.Bool("json") {
			out, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(out))
		} else {
			printReport(report)
		}

		// Non-zero exit so this is usable as a cron or monitoring check.
		if report.Failed() {
			return cli.Exit("", 1)
		}
		return nil
	},
}

// truncate keeps the table readable; the full text is printed below it for
// anything that needs attention.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func printReport(r selfcheck.Report) {
	var rows [][]string
	for _, res := range r.Results {
		marker := "  OK  "
		switch res.Status {
		case selfcheck.StatusWarn:
			marker = " WARN "
		case selfcheck.StatusFail:
			marker = " FAIL "
		}
		rows = append(rows, []string{marker, res.Name, truncate(res.Message, 90)})
	}
	NewVisualTable([]string{"", "Check", "Result"}, rows, []RowColor{}).SetAutoWrapText(false).Generate(false)

	// Hints only for what needs attention, so a clean run stays quiet.
	for _, res := range r.Problems() {
		fmt.Printf("\n%s: %s\n", res.Name, res.Message)
		if res.Hint != "" {
			fmt.Printf("  → %s\n", res.Hint)
		}
	}
	fmt.Printf("\n%s in %s\n", r.Summary(), r.Duration)
}
