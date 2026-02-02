package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/mitchellh/go-homedir"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/urfave/cli/v2"
)

var inferenceCmd = &cli.Command{
	Name:  "inference",
	Usage: "Swan Inference marketplace commands",
	Subcommands: []*cli.Command{
		inferenceStatusCmd,
		inferenceConfigCmd,
	},
}

// ProviderStatusResponse mirrors the backend response
type ProviderStatusResponse struct {
	ProviderID  string   `json:"provider_id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	CanConnect  bool     `json:"can_connect"`
	APIKeyValid bool     `json:"api_key_valid"`
	Message     string   `json:"message"`
	NextSteps   []string `json:"next_steps,omitempty"`
}

var inferenceStatusCmd = &cli.Command{
	Name:  "status",
	Usage: "Check provider status on Swan Inference",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:    "json",
			Usage:   "Output in JSON format",
			Aliases: []string{"j"},
		},
	},
	Action: func(cctx *cli.Context) error {
		cpRepoPath, err := homedir.Expand(cctx.String(FlagRepo.Name))
		if err != nil {
			return fmt.Errorf("failed to expand repo path: %v", err)
		}
		if err := conf.InitConfig(cpRepoPath, true); err != nil {
			return fmt.Errorf("failed to load config: %v", err)
		}

		cfg := conf.GetConfig()
		if !cfg.Inference.Enable {
			return fmt.Errorf("inference mode is not enabled in config.toml")
		}

		apiKey := cfg.Inference.ApiKey
		if apiKey == "" {
			// Check environment variable
			apiKey = os.Getenv("INFERENCE_API_KEY")
		}

		if apiKey == "" {
			fmt.Println()
			color.Red("No API key configured!")
			fmt.Println()
			fmt.Println("To use Swan Inference, you need a provider API key.")
			fmt.Println()
			fmt.Println("Next steps:")
			fmt.Println("  1. Sign up at https://inference.swanchain.io or via API:")
			fmt.Println("     curl -X POST https://inference.swanchain.io/api/v1/provider/signup \\")
			fmt.Println("       -H \"Content-Type: application/json\" \\")
			fmt.Println("       -d '{\"name\":\"My Provider\",\"owner_address\":\"0x...\"}'")
			fmt.Println()
			fmt.Println("  2. Add your API key to config.toml:")
			fmt.Println("     [Inference]")
			fmt.Println("     ApiKey = \"sk-prov-xxxxxxxxxxxxxxxxxxxx\"")
			fmt.Println()
			fmt.Println("  3. Or set the environment variable:")
			fmt.Println("     export INFERENCE_API_KEY=sk-prov-xxxxxxxxxxxxxxxxxxxx")
			fmt.Println()
			return nil
		}

		// Determine the service URL for status check
		serviceURL := cfg.Inference.ServiceURL
		if serviceURL == "" {
			// Derive from WebSocket URL
			wsURL := cfg.Inference.WebSocketURL
			if wsURL == "" {
				wsURL = "wss://inference.swanchain.io/ws"
			}
			// Convert ws(s)://host/ws to http(s)://host
			serviceURL = strings.Replace(wsURL, "wss://", "https://", 1)
			serviceURL = strings.Replace(serviceURL, "ws://", "http://", 1)
			serviceURL = strings.TrimSuffix(serviceURL, "/ws")
		}

		statusURL := serviceURL + "/api/v1/provider/status"

		// Make HTTP request
		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequest("GET", statusURL, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to connect to Swan Inference: %v\nURL: %s", err, statusURL)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response: %v", err)
		}

		var status ProviderStatusResponse
		if err := json.Unmarshal(body, &status); err != nil {
			return fmt.Errorf("failed to parse response: %v\nBody: %s", err, string(body))
		}

		if cctx.Bool("json") {
			output, _ := json.MarshalIndent(status, "", "  ")
			fmt.Println(string(output))
			return nil
		}

		// Pretty print status
		fmt.Println()
		fmt.Println("Swan Inference Provider Status")
		fmt.Println(strings.Repeat("=", 40))

		if status.APIKeyValid {
			color.Green("API Key: Valid")
		} else {
			color.Red("API Key: Invalid or Revoked")
		}

		if status.ProviderID != "" {
			fmt.Printf("Provider ID: %s\n", status.ProviderID)
		}
		if status.Name != "" {
			fmt.Printf("Provider Name: %s\n", status.Name)
		}

		fmt.Printf("Status: ")
		switch status.Status {
		case "active":
			color.Green(status.Status)
		case "pending":
			color.Yellow(status.Status)
		case "suspended":
			color.Red(status.Status)
		default:
			fmt.Println(status.Status)
		}

		fmt.Printf("Can Connect: ")
		if status.CanConnect {
			color.Green("Yes")
		} else {
			color.Red("No")
		}

		fmt.Println()
		fmt.Println(status.Message)

		if len(status.NextSteps) > 0 {
			fmt.Println()
			fmt.Println("Next Steps:")
			for _, step := range status.NextSteps {
				fmt.Printf("  %s\n", step)
			}
		}

		fmt.Println()
		return nil
	},
}

var inferenceConfigCmd = &cli.Command{
	Name:  "config",
	Usage: "Show current inference configuration",
	Action: func(cctx *cli.Context) error {
		cpRepoPath, err := homedir.Expand(cctx.String(FlagRepo.Name))
		if err != nil {
			return fmt.Errorf("failed to expand repo path: %v", err)
		}
		if err := conf.InitConfig(cpRepoPath, true); err != nil {
			return fmt.Errorf("failed to load config: %v", err)
		}

		cfg := conf.GetConfig()

		fmt.Println()
		fmt.Println("Swan Inference Configuration")
		fmt.Println(strings.Repeat("=", 40))

		fmt.Printf("Enabled: %v\n", cfg.Inference.Enable)
		fmt.Printf("Service URL: %s\n", cfg.Inference.ServiceURL)
		fmt.Printf("WebSocket URL: %s\n", cfg.Inference.WebSocketURL)

		// Mask API key
		apiKey := cfg.Inference.ApiKey
		if apiKey == "" {
			apiKey = os.Getenv("INFERENCE_API_KEY")
		}
		if apiKey != "" {
			if len(apiKey) > 16 {
				fmt.Printf("API Key: %s...\n", apiKey[:16])
			} else {
				fmt.Printf("API Key: %s\n", apiKey)
			}
		} else {
			color.Yellow("API Key: Not configured")
		}

		if len(cfg.Inference.Models) > 0 {
			fmt.Printf("Models: %s\n", strings.Join(cfg.Inference.Models, ", "))
		} else {
			fmt.Println("Models: None configured")
		}

		fmt.Println()
		return nil
	},
}
