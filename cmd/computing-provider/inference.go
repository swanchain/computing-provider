package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/mitchellh/go-homedir"
	"github.com/olekukonko/tablewriter"
	"github.com/swanchain/computing-provider-v2/build"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/computing"
	"github.com/swanchain/computing-provider-v2/internal/setup"
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
		inferenceDepositCmd,
		inferenceSetBeneficiaryCmd,
		inferenceRecommendModelsCmd,
		inferenceSelectModelCmd,
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

// CollateralChainInfo mirrors the backend chain info response
type CollateralChainInfo struct {
	ChainID         int64   `json:"chain_id"`
	ChainName       string  `json:"chain_name"`
	ContractAddress string  `json:"contract_address"`
	TokenAddress    string  `json:"token_address"`
	TokenSymbol     string  `json:"token_symbol"`
	TokenDecimals   int     `json:"token_decimals"`
	MinCollateral   float64 `json:"min_collateral"`
	RPCURL          string  `json:"rpc_url"`
	ExplorerURL     string  `json:"explorer_url"`
	FaucetURL       string  `json:"faucet_url"`
}

// CollateralCheckResponse mirrors the backend collateral check response
type CollateralCheckResponse struct {
	HasCollateral bool            `json:"has_collateral"`
	Required      bool            `json:"required"`
	AmountRequired float64        `json:"amount_required"`
	Collateral    json.RawMessage `json:"collateral,omitempty"`
}

// getServiceURL determines the HTTP API URL from config
func getServiceURL(cfg *conf.ComputeNode) string {
	serviceURL := cfg.Inference.ServiceURL
	if serviceURL == "" {
		wsURL := cfg.Inference.WebSocketURL
		if wsURL == "" {
			wsURL = build.DefaultInferenceWSURL
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
	Usage: "Generate a provider API key (login/signup with Swan Inference account)",
	Description: `Generate or retrieve a provider API key by logging in or creating a Swan Inference account.

This command links your provider to a user account, allowing you to:
  - Log into the Swan Inference dashboard
  - Manage API keys and provider settings
  - Track earnings and performance

Examples:
  # Interactive login/signup flow (recommended)
  computing-provider inference keygen

  # Provide an existing API key directly
  computing-provider inference keygen --api-key=sk-prov-xxx

  # Specify provider name
  computing-provider inference keygen --name "My GPU Provider"

  # Legacy standalone signup (deprecated)
  computing-provider inference keygen --standalone --name=test --owner-address=0x...`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "name",
			Usage: "Provider name (defaults to node name from config)",
		},
		&cli.StringFlag{
			Name:  "owner-address",
			Usage: "Owner wallet address (0x...), required for --standalone",
		},
		&cli.StringFlag{
			Name:  "api-key",
			Usage: "Provide an existing API key directly (skip authentication)",
		},
		&cli.BoolFlag{
			Name:  "standalone",
			Usage: "Use legacy standalone signup without user account (deprecated)",
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

		// Legacy standalone mode
		if cctx.Bool("standalone") {
			return keygenStandalone(cctx, cpRepoPath)
		}

		// Determine node name from --name flag or config
		nodeName := cctx.String("name")
		if nodeName == "" {
			nodeName = getNodeNameFromConfig(cpRepoPath)
		}

		providedApiKey := cctx.String("api-key")

		// Use the account-based authentication flow
		setup.PrintHeader("Swan Inference - Provider Key Generation")

		prompter := setup.NewPrompter()
		apiKey, err := handleAuthentication(cpRepoPath, prompter, providedApiKey, nodeName)
		if err != nil {
			return err
		}

		// Save API key to config.toml
		if err := conf.UpdateInferenceConfig(cpRepoPath, apiKey, nil); err != nil {
			color.Yellow("Warning: Could not save API key to config: %v", err)
			fmt.Println("Please manually add to config.toml:")
			fmt.Printf("  [Inference]\n  ApiKey = \"%s\"\n", apiKey)
		} else {
			setup.PrintSuccess("API key saved to config.toml")
		}

		// Display success and next steps
		fmt.Println()
		setup.PrintHeader("Provider Key Generated!")
		fmt.Println("Your provider is now linked to your Swan Inference account.")
		fmt.Println()
		fmt.Println("Next steps:")
		setup.PrintBullet("Start the provider: computing-provider run")
		setup.PrintBullet("View dashboard: computing-provider dashboard")
		setup.PrintBullet("Check status: computing-provider inference status")
		fmt.Println()

		return nil
	},
}

// keygenStandalone runs the legacy standalone signup (POST /api/v1/provider/signup)
// without linking to a user account. This is deprecated.
func keygenStandalone(cctx *cli.Context, cpRepoPath string) error {
	color.Yellow("WARNING: --standalone is deprecated. Providers created without an account")
	color.Yellow("cannot log into the dashboard. Run 'inference keygen' without --standalone instead.")
	fmt.Println()

	name := cctx.String("name")
	if name == "" {
		return fmt.Errorf("--name is required for standalone signup")
	}

	ownerAddress := cctx.String("owner-address")
	if ownerAddress == "" {
		return fmt.Errorf("--owner-address is required for standalone signup")
	}

	// Validate owner address format
	if !strings.HasPrefix(ownerAddress, "0x") || len(ownerAddress) != 42 {
		return fmt.Errorf("invalid owner-address: must be a 42-character hex address starting with 0x")
	}

	cfg := conf.GetConfig()
	serviceURL := getServiceURL(cfg)

	signupURL := serviceURL + "/api/v1/provider/signup"
	signupData := map[string]string{
		"name":          name,
		"owner_address": ownerAddress,
	}
	reqBody, _ := json.Marshal(signupData)

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
	fmt.Println("Swan Inference Provider Registration (Standalone)")
	fmt.Println(strings.Repeat("=", 50))
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
	color.Yellow("TIP: Run 'computing-provider inference keygen' (without --standalone) to link")
	color.Yellow("your provider to a user account for dashboard access.")
	fmt.Println()
	return nil
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
			fmt.Println("  computing-provider inference keygen")
			fmt.Println()
			fmt.Println("Or manually:")
			fmt.Println("  1. Sign up at " + build.DefaultInferenceURL + " or via API")
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

var inferenceDepositCmd = &cli.Command{
	Name:  "deposit",
	Usage: "Check deposit status or get instructions to deposit collateral",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "check",
			Usage: "Check current collateral status",
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

		// Step 1: Check provider status
		statusURL := serviceURL + "/api/v1/provider/status"
		client := &http.Client{Timeout: 15 * time.Second}
		req, err := http.NewRequest("GET", statusURL, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to connect to Swan Inference: %v", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response: %v", err)
		}

		var status ProviderStatusResponse
		if err := json.Unmarshal(body, &status); err != nil {
			return fmt.Errorf("failed to parse status: %v", err)
		}

		// Gate by status
		switch status.Status {
		case "pending":
			color.Yellow("Your provider is still pending.")
			fmt.Println("Run 'computing-provider inference request-approval' to request admin approval first.")
			return nil
		case "under_review":
			color.Yellow("Your provider is under review.")
			fmt.Println("Please wait for admin approval (typically 1-3 business days).")
			return nil
		case "active":
			color.Green("Your provider is already active and earning rewards!")
			fmt.Println("Collateral deposit has been confirmed. No further action needed.")
			return nil
		case "approved", "activating":
			// Proceed to show deposit info
		default:
			return fmt.Errorf("unexpected provider status: %s", status.Status)
		}

		if status.Status == "activating" {
			color.Cyan("Your collateral deposit is confirmed. Provider is activating.")
			fmt.Println("Your provider will be fully active within approximately 2 hours.")
			fmt.Println("No further action needed.")
			return nil
		}

		// Step 2: Get chain/contract info (public endpoint, no auth)
		contractURL := serviceURL + "/api/v1/provider/collateral/contract"
		contractResp, err := client.Get(contractURL)
		if err != nil {
			return fmt.Errorf("failed to get contract info: %v", err)
		}
		defer contractResp.Body.Close()

		contractBody, err := io.ReadAll(contractResp.Body)
		if err != nil {
			return fmt.Errorf("failed to read contract info: %v", err)
		}

		var contractData struct {
			Chains []CollateralChainInfo `json:"chains"`
		}
		if err := json.Unmarshal(contractBody, &contractData); err != nil {
			return fmt.Errorf("failed to parse contract info: %v", err)
		}

		// Step 3: Display deposit instructions
		fmt.Println()
		fmt.Println("Swan Inference Collateral Deposit")
		fmt.Println(strings.Repeat("=", 45))
		color.Green("Your provider has been approved!")
		fmt.Println()
		fmt.Println("To start earning rewards, deposit collateral on one of the supported chains:")
		fmt.Println()

		for i, chain := range contractData.Chains {
			if i > 0 {
				fmt.Println(strings.Repeat("-", 40))
			}
			fmt.Printf("  Chain:            %s (ID: %d)\n", chain.ChainName, chain.ChainID)
			fmt.Printf("  Token:            %s\n", chain.TokenSymbol)
			fmt.Printf("  Min Collateral:   %.2f %s\n", chain.MinCollateral, chain.TokenSymbol)
			fmt.Printf("  Contract:         %s\n", chain.ContractAddress)
			if chain.ExplorerURL != "" {
				fmt.Printf("  Explorer:         %s\n", chain.ExplorerURL)
			}
			if chain.FaucetURL != "" {
				fmt.Printf("  Faucet (testnet): %s\n", chain.FaucetURL)
			}
		}

		fmt.Println()
		color.Yellow("How to deposit:")
		fmt.Println("  1. Visit your Provider Dashboard to complete the deposit via MetaMask")
		fmt.Printf("     %s/dashboard\n", serviceURL)
		fmt.Println("  2. Or deposit directly to the contract from your owner wallet")
		fmt.Println("  3. After deposit, your provider will activate within ~2 hours")
		fmt.Println()

		// Step 4: If --check, show current collateral status
		if cctx.Bool("check") {
			fmt.Println("Checking collateral status...")
			fmt.Println()

			collateralURL := serviceURL + "/api/v1/provider/collateral"
			collReq, err := http.NewRequest("GET", collateralURL, nil)
			if err != nil {
				return fmt.Errorf("failed to create request: %v", err)
			}
			collReq.Header.Set("Authorization", "Bearer "+apiKey)

			collResp, err := client.Do(collReq)
			if err != nil {
				return fmt.Errorf("failed to check collateral: %v", err)
			}
			defer collResp.Body.Close()

			collBody, err := io.ReadAll(collResp.Body)
			if err != nil {
				return fmt.Errorf("failed to read collateral response: %v", err)
			}

			var collStatus CollateralCheckResponse
			if err := json.Unmarshal(collBody, &collStatus); err != nil {
				return fmt.Errorf("failed to parse collateral status: %v", err)
			}

			if collStatus.HasCollateral {
				color.Green("Collateral record found!")
				fmt.Printf("  Raw data: %s\n", string(collStatus.Collateral))
			} else {
				color.Yellow("No collateral deposit found yet.")
				if collStatus.Required {
					fmt.Printf("  Required amount: %.2f\n", collStatus.AmountRequired)
				}
			}
			fmt.Println()
		}

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

// --- recommend-models / select-model types ---

// modelDemandResponse is the response from GET /api/v1/stats/model-demand
type modelDemandResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Models []modelDemandAPIEntry `json:"models"`
	} `json:"data"`
}

type modelDemandAPIEntry struct {
	ModelID          string  `json:"model_id"`
	ModelName        string  `json:"model_name"`
	Category         string  `json:"category"`
	InputPrice       float64 `json:"input_price"`
	OutputPrice      float64 `json:"output_price"`
	Tier             string  `json:"tier"`
	OnlineProviders  int     `json:"online_providers"`
	Requests24h      int     `json:"requests_24h"`
	Tokens24h        int64   `json:"tokens_24h"`
	Revenue24h       float64 `json:"revenue_24h"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
	DemandTrend      string  `json:"demand_trend"`       // "up", "down", "stable"
	DemandChangePct  float64 `json:"demand_change_pct"`
	EstDailyEarnings float64 `json:"est_daily_earnings"`
	MinVRAMGB        int     `json:"min_vram_gb"`
}

// modelDemandEntry is the display/output struct for recommend-models and select-model
type modelDemandEntry struct {
	ModelID          string  `json:"model_id"`
	Name             string  `json:"name"`
	Category         string  `json:"category"`
	InputPrice       float64 `json:"input_price"`
	OutputPrice      float64 `json:"output_price"`
	OnlineProviders  int     `json:"online_providers"`
	Requests24h      int     `json:"requests_24h"`
	Tokens24h        int64   `json:"tokens_24h"`
	Revenue24h       float64 `json:"revenue_24h"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
	DemandTrend      string  `json:"demand_trend"`
	DemandChangePct  float64 `json:"demand_change_pct"`
	MinVRAMGB        int     `json:"min_vram_gb"`
	Compatible       bool    `json:"compatible"`
	EstDailyEarnings float64 `json:"est_daily_earnings"`
}

var inferenceSetBeneficiaryCmd = &cli.Command{
	Name:      "set-beneficiary",
	Usage:     "Set the wallet address for receiving rewards",
	ArgsUsage: "<0x-address>",
	Action: func(cctx *cli.Context) error {
		address := cctx.Args().First()
		if address == "" {
			return fmt.Errorf("wallet address is required, e.g. computing-provider inference set-beneficiary 0x...")
		}

		if !strings.HasPrefix(address, "0x") || len(address) != 42 {
			return fmt.Errorf("invalid Ethereum address: must be 42 characters starting with 0x")
		}

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
			"beneficiary_address": address,
		})

		url := serviceURL + "/api/v1/provider/me"
		client := &http.Client{Timeout: 15 * time.Second}
		req, err := http.NewRequest("PUT", url, bytes.NewReader(reqBody))
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

		if resp.StatusCode != http.StatusOK {
			var errResp struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
				return fmt.Errorf("failed to update: %s", errResp.Error.Message)
			}
			return fmt.Errorf("failed to update (HTTP %d): %s", resp.StatusCode, string(body))
		}

		fmt.Println()
		color.Green("Beneficiary address updated!")
		fmt.Printf("Rewards will be sent to: %s\n", address)
		fmt.Println()

		return nil
	},
}

var inferenceRecommendModelsCmd = &cli.Command{
	Name:    "recommend-models",
	Usage:   "Show model recommendations based on demand and your hardware",
	Aliases: []string{"recommend"},
	Description: `Query the Swan Inference model marketplace to find the most profitable models
for your GPU hardware. Combines model pricing, demand data, and provider supply
to estimate daily earnings.

Examples:
  # Show top 10 recommended models
  computing-provider inference recommend-models

  # Show only models compatible with your GPU
  computing-provider inference recommend-models --compatible-only

  # Override detected VRAM (e.g., for planning)
  computing-provider inference recommend-models --vram 24

  # Filter by category
  computing-provider inference recommend-models --category text-generation

  # Output as JSON
  computing-provider inference recommend-models --json`,
	Flags: []cli.Flag{
		&cli.IntFlag{
			Name:  "vram",
			Usage: "Override auto-detected per-GPU VRAM in GB",
		},
		&cli.IntFlag{
			Name:  "top",
			Usage: "Number of models to show",
			Value: 10,
		},
		&cli.StringFlag{
			Name:  "category",
			Usage: "Filter by category (e.g., text-generation, image-generation, embedding)",
		},
		&cli.BoolFlag{
			Name:    "json",
			Usage:   "Output in JSON format",
			Aliases: []string{"j"},
		},
		&cli.BoolFlag{
			Name:  "compatible-only",
			Usage: "Show only models that fit your GPU VRAM",
		},
		&cli.StringFlag{
			Name:  "sort",
			Usage: "Sort by: earnings (default), requests, providers, vram",
			Value: "earnings",
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

		// Detect GPU hardware
		hardware := computing.DetectGPUHardware()
		vramPerGPU := 0
		totalVRAM := 0
		if hardware != nil {
			vramPerGPU = hardware.VRAMGB
			totalVRAM = hardware.VRAMGB * hardware.GPUCount
		}
		if cctx.IsSet("vram") {
			vramPerGPU = cctx.Int("vram")
			if hardware != nil {
				totalVRAM = vramPerGPU * hardware.GPUCount
			} else {
				totalVRAM = vramPerGPU
			}
		}

		// Fetch model demand data
		entries, err := fetchModelDemand(serviceURL, cctx.String("category"), vramPerGPU, totalVRAM, cctx.Bool("compatible-only"))
		if err != nil {
			return err
		}

		// Sort
		sortField := cctx.String("sort")
		sort.Slice(entries, func(i, j int) bool {
			switch sortField {
			case "requests":
				return entries[i].Requests24h > entries[j].Requests24h
			case "providers":
				return entries[i].OnlineProviders < entries[j].OnlineProviders
			case "vram":
				return entries[i].MinVRAMGB < entries[j].MinVRAMGB
			default: // "earnings"
				return entries[i].EstDailyEarnings > entries[j].EstDailyEarnings
			}
		})

		// Limit results
		totalModels := len(entries)
		top := cctx.Int("top")
		if top > 0 && top < len(entries) {
			entries = entries[:top]
		}

		// JSON output
		if cctx.Bool("json") {
			output, _ := json.MarshalIndent(entries, "", "  ")
			fmt.Println(string(output))
			return nil
		}

		// Print header
		nodeName := cfg.API.NodeName
		if nodeName == "" {
			nodeName = "Unknown"
		}

		fmt.Println()
		fmt.Printf("Swan Inference \u2014 Model Recommendations for %s\n", nodeName)
		if hardware != nil {
			gpuLabel := hardware.GPUModel
			if hardware.GPUCount > 1 {
				fmt.Printf("Hardware: %dx %s (%d GB each, %d GB total)\n", hardware.GPUCount, gpuLabel, vramPerGPU, totalVRAM)
			} else {
				fmt.Printf("Hardware: %s (%d GB)\n", gpuLabel, vramPerGPU)
			}
		} else if vramPerGPU > 0 {
			fmt.Printf("Hardware: VRAM override %d GB\n", vramPerGPU)
		} else {
			color.Yellow("Hardware: Not detected (use --vram to specify)")
		}
		fmt.Println(strings.Repeat("\u2550", 70))
		fmt.Println()

		if len(entries) == 0 {
			color.Yellow("No models found matching your criteria.")
			if cctx.Bool("compatible-only") {
				fmt.Println("Try removing --compatible-only to see all models.")
			}
			return nil
		}

		// Table output
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"MODEL", "VRAM", "PRICE (IN/OUT)", "24H REQ", "PROVIDERS", "TREND", "EST $/DAY", "FIT"})
		table.SetAutoFormatHeaders(false)
		table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
		table.SetAlignment(tablewriter.ALIGN_LEFT)
		table.SetHeaderLine(false)
		table.SetBorder(false)
		table.SetCenterSeparator("")
		table.SetColumnSeparator("")
		table.SetRowSeparator("")
		table.SetTablePadding("  ")
		table.SetNoWhiteSpace(true)

		for _, e := range entries {
			fit := "\u2713"
			if !e.Compatible {
				fit = "\u2717"
			}

			vramStr := "-"
			if e.MinVRAMGB > 0 {
				vramStr = fmt.Sprintf("%d GB", e.MinVRAMGB)
			}

			priceStr := fmt.Sprintf("$%.2f/$%.2f", e.InputPrice, e.OutputPrice)
			reqStr := fmt.Sprintf("%d", e.Requests24h)
			provStr := fmt.Sprintf("%d", e.OnlineProviders)

			trendStr := demandTrendSymbol(e.DemandTrend)

			earningStr := "-"
			if e.EstDailyEarnings > 0 {
				earningStr = fmt.Sprintf("$%.2f", e.EstDailyEarnings)
			}

			// Truncate model ID for display
			modelDisplay := e.ModelID
			if len(modelDisplay) > 40 {
				modelDisplay = modelDisplay[:37] + "..."
			}

			colors := []tablewriter.Colors{}
			if e.Compatible {
				colors = []tablewriter.Colors{{}, {}, {}, {}, {}, {}, {}, {tablewriter.FgGreenColor}}
			} else {
				colors = []tablewriter.Colors{{}, {}, {}, {}, {}, {}, {}, {tablewriter.FgRedColor}}
			}

			table.Rich([]string{modelDisplay, vramStr, priceStr, reqStr, provStr, trendStr, earningStr, fit}, colors)
		}

		table.Render()

		fmt.Println()
		sortLabel := sortField
		if sortLabel == "" {
			sortLabel = "earnings"
		}
		fmt.Printf("Showing %d of %d models (sorted by %s)\n", len(entries), totalModels, sortLabel)

		if !cctx.Bool("compatible-only") && vramPerGPU > 0 {
			fmt.Println("Tip: Use --compatible-only to show only models that fit your hardware")
		}
		fmt.Println("Tip: Use 'inference select-model' to interactively pick and configure a model")
		fmt.Println()

		return nil
	},
}

// fetchModelDemand fetches model demand data from the API and returns filtered entries.
func fetchModelDemand(serviceURL, categoryFilter string, vramPerGPU, totalVRAM int, compatibleOnly bool) ([]modelDemandEntry, error) {
	reqURL := serviceURL + "/api/v1/stats/model-demand"
	if categoryFilter != "" {
		reqURL += "?category=" + categoryFilter
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch model demand data: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model-demand API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result modelDemandResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse model demand response: %v", err)
	}

	var entries []modelDemandEntry
	for _, m := range result.Data.Models {
		compatible := vramPerGPU == 0 || m.MinVRAMGB == 0 || m.MinVRAMGB <= totalVRAM
		if compatibleOnly && !compatible {
			continue
		}

		entries = append(entries, modelDemandEntry{
			ModelID:          m.ModelID,
			Name:             m.ModelName,
			Category:         m.Category,
			InputPrice:       m.InputPrice,
			OutputPrice:      m.OutputPrice,
			OnlineProviders:  m.OnlineProviders,
			Requests24h:      m.Requests24h,
			Tokens24h:        m.Tokens24h,
			Revenue24h:       m.Revenue24h,
			AvgLatencyMs:     m.AvgLatencyMs,
			DemandTrend:      m.DemandTrend,
			DemandChangePct:  m.DemandChangePct,
			MinVRAMGB:        m.MinVRAMGB,
			Compatible:       compatible,
			EstDailyEarnings: m.EstDailyEarnings,
		})
	}

	return entries, nil
}

// demandTrendSymbol returns an arrow symbol for the demand trend.
func demandTrendSymbol(trend string) string {
	switch trend {
	case "up":
		return "\u2191"
	case "down":
		return "\u2193"
	default:
		return "\u2192"
	}
}

var inferenceSelectModelCmd = &cli.Command{
	Name:  "select-model",
	Usage: "Interactively pick a model from the marketplace and add it to your config",
	Aliases: []string{"select"},
	Description: `Browse the Swan Inference model marketplace, pick a model that fits your
hardware, and automatically configure it in models.json and config.toml.
The running provider will hot-reload the new model via fsnotify.

Examples:
  # Interactive model selection
  computing-provider inference select-model

  # Pre-filter by category
  computing-provider inference select-model --category text-generation

  # Skip prompts with flags
  computing-provider inference select-model --endpoint http://localhost:30000

  # Override detected VRAM
  computing-provider inference select-model --vram 24`,
	Flags: []cli.Flag{
		&cli.IntFlag{
			Name:  "vram",
			Usage: "Override auto-detected per-GPU VRAM in GB",
		},
		&cli.StringFlag{
			Name:  "endpoint",
			Usage: "Model server endpoint URL (skip prompt)",
		},
		&cli.StringFlag{
			Name:  "category",
			Usage: "Filter by category (e.g., text-generation, image-generation, embedding)",
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

		// Detect GPU hardware
		hardware := computing.DetectGPUHardware()
		vramPerGPU := 0
		totalVRAM := 0
		if hardware != nil {
			vramPerGPU = hardware.VRAMGB
			totalVRAM = hardware.VRAMGB * hardware.GPUCount
		}
		if cctx.IsSet("vram") {
			vramPerGPU = cctx.Int("vram")
			if hardware != nil {
				totalVRAM = vramPerGPU * hardware.GPUCount
			} else {
				totalVRAM = vramPerGPU
			}
		}

		setup.PrintHeader("Swan Inference - Model Selection")

		if hardware != nil {
			gpuLabel := hardware.GPUModel
			if hardware.GPUCount > 1 {
				fmt.Printf("Hardware: %dx %s (%d GB each, %d GB total)\n", hardware.GPUCount, gpuLabel, vramPerGPU, totalVRAM)
			} else {
				fmt.Printf("Hardware: %s (%d GB)\n", gpuLabel, vramPerGPU)
			}
		} else if vramPerGPU > 0 {
			fmt.Printf("Hardware: VRAM override %d GB\n", vramPerGPU)
		} else {
			color.Yellow("Hardware: Not detected (use --vram to specify)")
		}
		fmt.Println()

		// Fetch model demand data (compatible only when VRAM is known)
		compatibleOnly := vramPerGPU > 0
		entries, err := fetchModelDemand(serviceURL, cctx.String("category"), vramPerGPU, totalVRAM, compatibleOnly)
		if err != nil {
			return err
		}

		if len(entries) == 0 {
			color.Yellow("No compatible models found.")
			if compatibleOnly {
				fmt.Println("Try using --vram with a higher value, or remove it to see all models.")
			}
			return nil
		}

		// Sort by estimated earnings (best first)
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].EstDailyEarnings > entries[j].EstDailyEarnings
		})

		// Build selection options
		options := make([]setup.SelectionOption, len(entries))
		for i, e := range entries {
			vramStr := "?"
			if e.MinVRAMGB > 0 {
				vramStr = fmt.Sprintf("%dGB", e.MinVRAMGB)
			}
			earningStr := "-"
			if e.EstDailyEarnings > 0 {
				earningStr = fmt.Sprintf("$%.2f/day", e.EstDailyEarnings)
			}
			trendStr := demandTrendSymbol(e.DemandTrend)

			options[i] = setup.SelectionOption{
				Label:       e.ModelID,
				Description: fmt.Sprintf("VRAM: %s | %s (in/out): $%.2f/$%.2f | %d req/24h | %d providers | %s est | %s", vramStr, e.Category, e.InputPrice, e.OutputPrice, e.Requests24h, e.OnlineProviders, earningStr, trendStr),
			}
		}

		prompter := setup.NewPrompter()

		fmt.Printf("Found %d compatible models:\n\n", len(entries))
		idx, err := prompter.AskSelection("Select a model to serve:", options)
		if err != nil {
			return fmt.Errorf("selection failed: %v", err)
		}

		selected := entries[idx]
		fmt.Println()
		setup.PrintSuccess(fmt.Sprintf("Selected: %s", selected.ModelID))

		// Ask for endpoint
		endpoint := cctx.String("endpoint")
		if endpoint == "" {
			endpoint, err = prompter.AskString("Model server endpoint URL", "http://localhost:30000")
			if err != nil {
				return fmt.Errorf("failed to read endpoint: %v", err)
			}
		}

		// Determine GPU memory in MB
		gpuMemoryMB := 0
		if selected.MinVRAMGB > 0 {
			gpuMemoryMB = selected.MinVRAMGB * 1000
		} else if vramPerGPU > 0 {
			gpuMemoryMB = vramPerGPU * 1000
		}

		gpuMemStr, err := prompter.AskString("GPU memory for this model (MB)", strconv.Itoa(gpuMemoryMB))
		if err != nil {
			return fmt.Errorf("failed to read GPU memory: %v", err)
		}
		gpuMemoryMB, err = strconv.Atoi(gpuMemStr)
		if err != nil {
			return fmt.Errorf("invalid GPU memory value: %v", err)
		}

		// Load existing models.json
		models, err := conf.LoadModelsJson(cpRepoPath)
		if err != nil {
			return fmt.Errorf("failed to load models.json: %v", err)
		}

		// Check for duplicate
		if _, exists := models[selected.ModelID]; exists {
			overwrite, err := prompter.AskYesNo(fmt.Sprintf("Model %s already exists in models.json. Overwrite?", selected.ModelID), false)
			if err != nil {
				return fmt.Errorf("failed to read confirmation: %v", err)
			}
			if !overwrite {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		// Add model entry
		category := selected.Category
		if category == "" {
			category = "text-generation"
		}
		models[selected.ModelID] = conf.ModelConfig{
			Endpoint:  endpoint,
			GPUMemory: gpuMemoryMB,
			Category:  category,
		}

		// Write models.json (atomic write triggers fsnotify hot-reload)
		if err := conf.WriteModelsJson(cpRepoPath, models); err != nil {
			return fmt.Errorf("failed to write models.json: %v", err)
		}
		setup.PrintSuccess("Updated models.json")

		// Update config.toml Models list
		existingModels := cfg.Inference.Models
		found := false
		for _, m := range existingModels {
			if m == selected.ModelID {
				found = true
				break
			}
		}
		if !found {
			updatedModels := append(existingModels, selected.ModelID)
			if err := conf.UpdateInferenceConfig(cpRepoPath, "", updatedModels); err != nil {
				color.Yellow("Warning: Could not update config.toml Models list: %v", err)
				fmt.Printf("Please manually add \"%s\" to the Models list in config.toml\n", selected.ModelID)
			} else {
				setup.PrintSuccess("Updated config.toml")
			}
		}

		// Success summary
		fmt.Println()
		setup.PrintHeader("Model Added Successfully")
		setup.PrintKeyValue("Model", selected.ModelID)
		setup.PrintKeyValue("Category", category)
		setup.PrintKeyValue("Endpoint", endpoint)
		setup.PrintKeyValue("GPU Memory", fmt.Sprintf("%d MB", gpuMemoryMB))
		if selected.EstDailyEarnings > 0 {
			setup.PrintKeyValue("Est. Earnings", fmt.Sprintf("$%.2f/day", selected.EstDailyEarnings))
		}
		fmt.Println()

		fmt.Println("Next steps:")
		setup.PrintBullet(fmt.Sprintf("Ensure your model server is running at %s", endpoint))
		setup.PrintBullet("If the provider is running, the model will be hot-reloaded automatically")
		setup.PrintBullet("Otherwise start it: computing-provider run")
		fmt.Println()

		return nil
	},
}
