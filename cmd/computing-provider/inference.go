package main

import (
	"bytes"
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
		inferenceKeygenCmd,
		inferenceRequestApprovalCmd,
	},
}

// ProviderStatusResponse mirrors the backend response
type ProviderStatusResponse struct {
	ProviderID      string   `json:"provider_id"`
	Name            string   `json:"name"`
	Status          string   `json:"status"`
	CanConnect      bool     `json:"can_connect"`
	APIKeyValid     bool     `json:"api_key_valid"`
	Message         string   `json:"message"`
	Warning         string   `json:"warning,omitempty"`
	NextSteps       []string `json:"next_steps,omitempty"`
	Step            int      `json:"step"`
	TotalSteps      int      `json:"total_steps"`
	StepLabel       string   `json:"step_label"`
	EarningsEnabled bool     `json:"earnings_enabled"`
}

// ProviderSignupResponse mirrors the backend signup response
type ProviderSignupResponse struct {
	ProviderID string   `json:"provider_id"`
	APIKey     string   `json:"api_key"`
	KeyPrefix  string   `json:"key_prefix"`
	Status     string   `json:"status"`
	CanConnect bool     `json:"can_connect"`
	Message    string   `json:"message"`
	Warning    string   `json:"warning,omitempty"`
	NextSteps  []string `json:"next_steps"`
}

// ApprovalRequestResponse mirrors the backend approval response
type ApprovalRequestResponse struct {
	ProviderID  string `json:"provider_id"`
	Status      string `json:"status"`
	Message     string `json:"message"`
	RequestedAt string `json:"requested_at"`
}

// getServiceURL determines the HTTP API URL from config
func getServiceURL(cfg *conf.ComputeNode) string {
	serviceURL := cfg.Inference.ServiceURL
	if serviceURL == "" {
		wsURL := cfg.Inference.WebSocketURL
		if wsURL == "" {
			wsURL = "wss://inference.swanchain.io/ws"
		}
		serviceURL = strings.Replace(wsURL, "wss://", "https://", 1)
		serviceURL = strings.Replace(serviceURL, "ws://", "http://", 1)
		serviceURL = strings.TrimSuffix(serviceURL, "/ws")
	}
	return serviceURL
}

// getAPIKey retrieves the API key from config or environment
func getAPIKey(cfg *conf.ComputeNode) string {
	apiKey := cfg.Inference.ApiKey
	if apiKey == "" {
		apiKey = os.Getenv("INFERENCE_API_KEY")
	}
	return apiKey
}

var inferenceKeygenCmd = &cli.Command{
	Name:  "keygen",
	Usage: "Generate a provider API key (register with Swan Inference)",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "name",
			Usage:    "Provider name",
			Required: true,
		},
		&cli.StringFlag{
			Name:     "owner-address",
			Usage:    "Owner wallet address (0x...)",
			Required: true,
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
		serviceURL := getServiceURL(cfg)

		name := cctx.String("name")
		ownerAddress := cctx.String("owner-address")

		// Validate owner address format
		if !strings.HasPrefix(ownerAddress, "0x") || len(ownerAddress) != 42 {
			return fmt.Errorf("invalid owner-address: must be a 42-character hex address starting with 0x")
		}

		signupURL := serviceURL + "/api/v1/provider/signup"
		reqBody, _ := json.Marshal(map[string]string{
			"name":          name,
			"owner_address": ownerAddress,
		})

		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Post(signupURL, "application/json", bytes.NewReader(reqBody))
		if err != nil {
			return fmt.Errorf("failed to connect to Swan Inference: %v", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response: %v", err)
		}

		if resp.StatusCode == http.StatusConflict {
			return fmt.Errorf("a provider with this owner address already exists. Use 'inference status' to check your provider")
		}

		if resp.StatusCode != http.StatusCreated {
			var errResp struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
				return fmt.Errorf("signup failed: %s", errResp.Error.Message)
			}
			return fmt.Errorf("signup failed (HTTP %d): %s", resp.StatusCode, string(body))
		}

		var signup ProviderSignupResponse
		if err := json.Unmarshal(body, &signup); err != nil {
			return fmt.Errorf("failed to parse response: %v", err)
		}

		// Save API key to config
		if err := conf.UpdateInferenceConfig(cpRepoPath, signup.APIKey, nil); err != nil {
			color.Yellow("Warning: Could not save API key to config: %v", err)
			fmt.Println("Please manually add to config.toml:")
			fmt.Printf("  [Inference]\n  ApiKey = \"%s\"\n", signup.APIKey)
		} else {
			color.Green("API key saved to config.toml")
		}

		// Display results
		fmt.Println()
		fmt.Println("Swan Inference Provider Registration")
		fmt.Println(strings.Repeat("=", 45))
		color.Green("Registration successful!")
		fmt.Println()
		fmt.Printf("Provider ID: %s\n", signup.ProviderID)
		fmt.Printf("Status:      %s\n", signup.Status)
		fmt.Printf("Can Connect: %v\n", signup.CanConnect)
		fmt.Println()

		color.Yellow("YOUR API KEY (save this — it won't be shown again!):")
		fmt.Println(signup.APIKey)
		fmt.Println()

		if signup.Warning != "" {
			color.Yellow("Warning: %s", signup.Warning)
			fmt.Println()
		}

		fmt.Println(signup.Message)

		if len(signup.NextSteps) > 0 {
			fmt.Println()
			fmt.Println("Next Steps:")
			for _, step := range signup.NextSteps {
				fmt.Printf("  %s\n", step)
			}
		}

		fmt.Println()
		return nil
	},
}

var inferenceRequestApprovalCmd = &cli.Command{
	Name:  "request-approval",
	Usage: "Request admin approval to start earning rewards",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "name",
			Usage: "Update provider name (optional)",
		},
		&cli.StringFlag{
			Name:     "hardware",
			Usage:    "Hardware description (e.g., '2x RTX 4090, 128GB RAM')",
			Required: true,
		},
		&cli.StringFlag{
			Name:     "email",
			Usage:    "Contact email for approval notifications",
			Required: true,
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
		serviceURL := getServiceURL(cfg)
		apiKey := getAPIKey(cfg)

		if apiKey == "" {
			return fmt.Errorf("no API key configured. Run 'computing-provider inference keygen' first")
		}

		reqBody, _ := json.Marshal(map[string]string{
			"provider_name":    cctx.String("name"),
			"hardware_summary": cctx.String("hardware"),
			"contact_email":    cctx.String("email"),
		})

		approvalURL := serviceURL + "/api/v1/provider/request-approval"
		client := &http.Client{Timeout: 15 * time.Second}
		req, err := http.NewRequest("POST", approvalURL, bytes.NewReader(reqBody))
		if err != nil {
			return fmt.Errorf("failed to create request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to connect to Swan Inference: %v", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response: %v", err)
		}

		if resp.StatusCode == http.StatusConflict {
			var errResp struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
				return fmt.Errorf("%s", errResp.Error.Message)
			}
			return fmt.Errorf("approval request conflict: %s", string(body))
		}

		if resp.StatusCode != http.StatusOK {
			var errResp struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
				return fmt.Errorf("request failed: %s", errResp.Error.Message)
			}
			return fmt.Errorf("request failed (HTTP %d): %s", resp.StatusCode, string(body))
		}

		var approval ApprovalRequestResponse
		if err := json.Unmarshal(body, &approval); err != nil {
			return fmt.Errorf("failed to parse response: %v", err)
		}

		fmt.Println()
		fmt.Println("Swan Inference Approval Request")
		fmt.Println(strings.Repeat("=", 45))
		color.Green("Approval request submitted!")
		fmt.Println()
		fmt.Printf("Provider ID:  %s\n", approval.ProviderID)
		fmt.Printf("Status:       %s\n", approval.Status)
		fmt.Printf("Requested At: %s\n", approval.RequestedAt)
		fmt.Println()
		fmt.Println(approval.Message)
		fmt.Println()
		fmt.Println("Check your status anytime with:")
		fmt.Println("  computing-provider inference status")
		fmt.Println()
		return nil
	},
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

		apiKey := getAPIKey(cfg)

		if apiKey == "" {
			fmt.Println()
			color.Red("No API key configured!")
			fmt.Println()
			fmt.Println("To use Swan Inference, you need a provider API key.")
			fmt.Println()
			fmt.Println("Quick start:")
			fmt.Println("  computing-provider inference keygen --name \"My Provider\" --owner-address 0x...")
			fmt.Println()
			fmt.Println("Or manually:")
			fmt.Println("  1. Sign up at https://inference.swanchain.io or via API")
			fmt.Println("  2. Add your API key to config.toml [Inference] section")
			fmt.Println("  3. Or set: export INFERENCE_API_KEY=sk-prov-xxxxxxxxxxxxxxxxxxxx")
			fmt.Println()
			return nil
		}

		serviceURL := getServiceURL(cfg)
		statusURL := serviceURL + "/api/v1/provider/status"

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
		case "under_review":
			color.Cyan(status.Status)
		case "approved":
			color.Green(status.Status)
		case "activating":
			color.Cyan(status.Status)
		case "suspended":
			color.Red(status.Status)
		default:
			fmt.Println(status.Status)
		}

		// Display step progress
		if status.TotalSteps > 0 {
			fmt.Printf("Progress: Step %d/%d — %s\n", status.Step, status.TotalSteps, status.StepLabel)
		}

		fmt.Printf("Can Connect: ")
		if status.CanConnect {
			color.Green("Yes")
		} else {
			color.Red("No")
		}

		fmt.Printf("Earnings: ")
		if status.EarningsEnabled {
			color.Green("Enabled")
		} else {
			color.Yellow("Disabled (requires admin approval)")
		}

		fmt.Println()
		fmt.Println(status.Message)

		if status.Warning != "" {
			fmt.Println()
			color.Yellow("Warning: %s", status.Warning)
		}

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
