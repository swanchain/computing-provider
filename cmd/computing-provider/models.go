package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/mitchellh/go-homedir"
	"github.com/olekukonko/tablewriter"
	"github.com/swanchain/computing-provider-v2/build"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/models"
	"github.com/urfave/cli/v2"
)

var modelsCmd = &cli.Command{
	Name:  "models",
	Usage: "Manage model weights from the Swan Model Repository",
	Subcommands: []*cli.Command{
		modelsCatalogCmd,
		modelsDownloadCmd,
		modelsVerifyCmd,
		modelsListCmd,
	},
}

// defaultModelsDir returns the default directory for storing model weights.
func defaultModelsDir() string {
	home, err := homedir.Dir()
	if err != nil {
		return filepath.Join(".", ".swan", "models")
	}
	return filepath.Join(home, ".swan", "models")
}

var modelsCatalogCmd = &cli.Command{
	Name:  "catalog",
	Usage: "List models available for download from the Swan Model Repository",
	Description: `Shows all models that have been ingested into the Swan Model Repository.
Indicates which models are already downloaded locally.

Examples:
  computing-provider models catalog
  computing-provider models catalog --json`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "service-url",
			Usage: "Swan Inference API URL",
		},
		&cli.BoolFlag{
			Name:  "json",
			Usage: "Output in JSON format",
		},
	},
	Action: func(cctx *cli.Context) error {
		serviceURL := cctx.String("service-url")
		if serviceURL == "" {
			serviceURL = getModelsServiceURL(cctx)
		}

		catalog, err := models.FetchCatalog(serviceURL)
		if err != nil {
			return fmt.Errorf("failed to fetch catalog: %v", err)
		}

		if len(catalog.Models) == 0 {
			fmt.Println("No models available in the Swan Model Repository yet.")
			return nil
		}

		// JSON output
		if cctx.Bool("json") {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(catalog)
		}

		// Check local download status
		modelsDir := defaultModelsDir()

		fmt.Printf("Available models in Swan Model Repository (%d):\n\n", catalog.Total)

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Model ID", "Category", "Files", "Size", "Status"})
		table.SetAutoWrapText(false)
		table.SetColumnAlignment([]int{
			tablewriter.ALIGN_LEFT,
			tablewriter.ALIGN_CENTER,
			tablewriter.ALIGN_RIGHT,
			tablewriter.ALIGN_RIGHT,
			tablewriter.ALIGN_CENTER,
		})

		for _, m := range catalog.Models {
			status := color.YellowString("not downloaded")
			localDir := filepath.Join(modelsDir, m.ModelID)
			if info, err := os.Stat(localDir); err == nil && info.IsDir() {
				localFiles := countFiles(localDir)
				if localFiles >= m.FileCount && m.FileCount > 0 {
					status = color.GreenString("downloaded")
				} else {
					status = color.CyanString(fmt.Sprintf("partial (%d/%d)", localFiles, m.FileCount))
				}
			}

			table.Append([]string{
				m.ModelID,
				m.Category,
				fmt.Sprintf("%d", m.FileCount),
				humanSizeCP(m.TotalSizeBytes),
				status,
			})
		}

		table.Render()
		fmt.Println()
		fmt.Println("Download a model:  computing-provider models download <model-id>")

		return nil
	},
}

var modelsDownloadCmd = &cli.Command{
	Name:      "download",
	Usage:     "Download verified model weights from Swan Model Repository",
	ArgsUsage: "<model-id>",
	Description: `Downloads model weights from the Swan Model Repository (NebulaBlock S3 storage).
Each file is verified against its SHA256 hash after download. Existing files
with matching hashes are skipped automatically.

Examples:
  computing-provider models download meta-llama/Llama-3.1-8B-Instruct
  computing-provider models download --dest /data/models meta-llama/Llama-3.3-70B-Instruct`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "dest",
			Usage: "Destination directory (default: ~/.swan/models/<model-id>)",
		},
		&cli.StringFlag{
			Name:  "service-url",
			Usage: "Swan Inference API URL (default: from config or " + build.DefaultInferenceURL + ")",
		},
	},
	Action: func(cctx *cli.Context) error {
		modelID := cctx.Args().First()
		if modelID == "" {
			return fmt.Errorf("model ID is required, e.g. meta-llama/Llama-3.1-8B-Instruct")
		}

		serviceURL := cctx.String("service-url")
		if serviceURL == "" {
			serviceURL = getModelsServiceURL(cctx)
		}

		fmt.Printf("Fetching file manifest for %s...\n", modelID)
		manifest, err := models.FetchModelFiles(serviceURL, modelID)
		if err != nil {
			return fmt.Errorf("failed to fetch model files: %v", err)
		}

		if manifest.FileCount == 0 {
			return fmt.Errorf("no files found for model %s. Has it been ingested?", modelID)
		}

		// Use canonical model ID from manifest for directory name (ensures correct casing)
		destDir := cctx.String("dest")
		if destDir == "" {
			destDir = filepath.Join(defaultModelsDir(), manifest.ModelID)
		}

		fmt.Printf("Model: %s\n", manifest.ModelID)
		fmt.Printf("Files: %d, Total size: %s\n", manifest.FileCount, humanSizeCP(manifest.TotalSizeBytes))
		fmt.Printf("Destination: %s\n\n", destDir)

		ctx := context.Background()
		if err := models.DownloadModelAndSaveManifest(ctx, modelID, manifest.Files, destDir); err != nil {
			return err
		}

		fmt.Println()
		color.Green("Download complete!")
		fmt.Println()
		fmt.Println("To use with SGLang:")
		fmt.Printf("  docker run -d --gpus all -p 30000:30000 --ipc=host \\\n")
		fmt.Printf("    -v %s:/models \\\n", destDir)
		fmt.Printf("    lmsysorg/sglang:latest \\\n")
		fmt.Printf("    python3 -m sglang.launch_server --model-path /models --host 0.0.0.0 --port 30000 \\\n")
		fmt.Printf("    --served-model-name %s\n", modelID)
		fmt.Println()

		return nil
	},
}

var modelsVerifyCmd = &cli.Command{
	Name:      "verify",
	Usage:     "Verify model weight integrity against expected hashes",
	ArgsUsage: "<model-id>",
	Description: `Checks each local model file against its expected SHA256 hash.
Reports pass/fail/missing for each file.

Examples:
  computing-provider models verify meta-llama/Llama-3.1-8B-Instruct
  computing-provider models verify --dir /data/models/meta-llama/Llama-3.1-8B-Instruct meta-llama/Llama-3.1-8B-Instruct`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "dir",
			Usage: "Override model directory",
		},
		&cli.StringFlag{
			Name:  "service-url",
			Usage: "Swan Inference API URL",
		},
	},
	Action: func(cctx *cli.Context) error {
		modelID := cctx.Args().First()
		if modelID == "" {
			return fmt.Errorf("model ID is required")
		}

		serviceURL := cctx.String("service-url")
		if serviceURL == "" {
			serviceURL = getModelsServiceURL(cctx)
		}

		modelDir := cctx.String("dir")
		if modelDir == "" {
			modelDir = filepath.Join(defaultModelsDir(), modelID)
		}

		fmt.Printf("Fetching expected hashes for %s...\n", modelID)
		manifest, err := models.FetchModelFiles(serviceURL, modelID)
		if err != nil {
			return fmt.Errorf("failed to fetch model files: %v", err)
		}

		fmt.Printf("Verifying %d files in %s...\n\n", manifest.FileCount, modelDir)

		results := models.VerifyModel(manifest.Files, modelDir)

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"File", "Status", "Hash"})
		table.SetAutoWrapText(false)

		var passed, failed, missing int
		for _, r := range results {
			hash := r.Expected
			if len(hash) > 16 {
				hash = hash[:16] + "..."
			}

			status := r.Status
			switch r.Status {
			case "pass":
				passed++
				status = color.GreenString("PASS")
			case "fail":
				failed++
				status = color.RedString("FAIL")
			case "missing":
				missing++
				status = color.YellowString("MISSING")
			}

			table.Append([]string{r.Filename, status, hash})
		}

		table.Render()
		fmt.Printf("\nTotal: %d files — %d passed, %d failed, %d missing\n",
			len(results), passed, failed, missing)

		if failed > 0 || missing > 0 {
			return fmt.Errorf("verification failed: %d issues found", failed+missing)
		}

		color.Green("All files verified successfully!")
		return nil
	},
}

var modelsListCmd = &cli.Command{
	Name:  "list",
	Usage: "List locally downloaded models",
	Action: func(cctx *cli.Context) error {
		modelsDir := defaultModelsDir()

		entries, err := os.ReadDir(modelsDir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Println("No models downloaded yet.")
				fmt.Println("Download a model with: computing-provider models download <model-id>")
				return nil
			}
			return fmt.Errorf("failed to read models directory: %v", err)
		}

		if len(entries) == 0 {
			fmt.Println("No models downloaded yet.")
			return nil
		}

		fmt.Printf("Models in %s:\n\n", modelsDir)

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			// Models use org/name structure (e.g. meta-llama/Llama-3.1-8B-Instruct)
			orgDir := filepath.Join(modelsDir, entry.Name())
			subEntries, err := os.ReadDir(orgDir)
			if err != nil {
				continue
			}
			for _, sub := range subEntries {
				if !sub.IsDir() {
					continue
				}
				modelPath := filepath.Join(orgDir, sub.Name())
				fileCount := countFiles(modelPath)
				fmt.Printf("  %s/%s (%d files)\n", entry.Name(), sub.Name(), fileCount)
			}
		}

		return nil
	},
}

// countFiles returns the number of regular files in a directory.
func countFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			count++
		}
	}
	return count
}

// getModelsServiceURL determines the Swan Inference API URL.
func getModelsServiceURL(cctx *cli.Context) string {
	// Try loading config
	cpRepoPath, err := homedir.Expand(cctx.String(FlagRepo.Name))
	if err == nil {
		if err := conf.InitConfig(cpRepoPath, true); err == nil {
			cfg := conf.GetConfig()
			url := getServiceURL(cfg)
			if url != "" {
				return url
			}
		}
	}

	return build.DefaultInferenceURL
}

func humanSizeCP(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

