package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchellh/go-homedir"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/setup"
	"github.com/urfave/cli/v2"
)

const totalSteps = 5

var setupCmd = &cli.Command{
	Name:  "setup",
	Usage: "Interactive setup wizard for new providers",
	Description: `Run the setup wizard to configure your computing provider.

This wizard will:
  1. Check system prerequisites (Docker, GPU, etc.)
  2. Handle authentication with Swan Inference
  3. Discover running model servers
  4. Configure models
  5. Finalize setup

Examples:
  # Run full setup wizard
  computing-provider setup

  # Skip model discovery
  computing-provider setup --skip-discovery

  # Use existing API key
  computing-provider setup --api-key=sk-prov-xxx`,
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "skip-discovery",
			Usage: "Skip automatic model server discovery",
		},
		&cli.StringFlag{
			Name:  "api-key",
			Usage: "Provide API key directly (skip authentication)",
		},
	},
	Subcommands: []*cli.Command{
		setupDiscoverCmd,
		setupLoginCmd,
		setupSignupCmd,
	},
	Action: runSetupWizard,
}

var setupDiscoverCmd = &cli.Command{
	Name:  "discover",
	Usage: "Discover running model servers",
	Action: func(c *cli.Context) error {
		setup.PrintHeader("Model Server Discovery")

		discovery := setup.NewModelDiscovery()
		servers := discovery.DiscoverAll()

		if len(servers) == 0 {
			setup.PrintWarning("No model servers found")
			fmt.Println("\nTo start a model server, try:")
			fmt.Println("  SGLang:  docker run -d --gpus all -p 30000:30000 lmsysorg/sglang:latest ...")
			fmt.Println("  vLLM:    docker run -d --gpus all -p 8000:8000 vllm/vllm-openai:latest ...")
			fmt.Println("  Ollama:  ollama serve")
			return nil
		}

		fmt.Printf("\nFound %d server(s):\n\n", len(servers))
		for _, server := range servers {
			fmt.Printf("  %s @ %s:%d\n", server.Type, server.Host, server.Port)
			if len(server.Models) > 0 {
				fmt.Printf("    Models: %s\n", strings.Join(server.Models, ", "))
			}
		}

		return nil
	},
}

var setupLoginCmd = &cli.Command{
	Name:  "login",
	Usage: "Login to Swan Inference with email and password",
	Action: func(c *cli.Context) error {
		cpRepoPath, err := getCpRepoPath(c)
		if err != nil {
			return err
		}

		setup.PrintHeader("Swan Inference - Login")

		prompter := setup.NewPrompter()
		authClient := setup.NewAuthClient("")

		// Get node name from config or ask for it
		nodeName := getNodeNameFromConfig(cpRepoPath)
		if nodeName == "" {
			hostname, _ := os.Hostname()
			nodeName, err = prompter.AskString("Provider Name", hostname)
			if err != nil {
				return err
			}
		}

		_, err = handleLogin(cpRepoPath, prompter, authClient, nodeName)
		return err
	},
}

var setupSignupCmd = &cli.Command{
	Name:  "signup",
	Usage: "Create a new Swan Inference account and provider",
	Action: func(c *cli.Context) error {
		cpRepoPath, err := getCpRepoPath(c)
		if err != nil {
			return err
		}

		setup.PrintHeader("Swan Inference - Create Account")

		prompter := setup.NewPrompter()
		authClient := setup.NewAuthClient("")

		// Get node name from config or ask for it
		nodeName := getNodeNameFromConfig(cpRepoPath)
		if nodeName == "" {
			hostname, _ := os.Hostname()
			nodeName, err = prompter.AskString("Provider Name", hostname)
			if err != nil {
				return err
			}
		}

		_, err = handleSignup(cpRepoPath, prompter, authClient, nodeName)
		return err
	},
}

// runSetupWizard runs the main setup wizard
func runSetupWizard(c *cli.Context) error {
	setup.PrintHeader("Computing Provider Setup Wizard")

	cpRepoPath, err := getCpRepoPath(c)
	if err != nil {
		return err
	}

	prompter := setup.NewPrompter()
	skipDiscovery := c.Bool("skip-discovery")
	providedApiKey := c.String("api-key")

	// Step 1: Prerequisites
	setup.PrintStep(1, totalSteps, "Checking Prerequisites")
	if err := checkPrerequisites(); err != nil {
		return err
	}

	// Step 2: Initialize repo if needed
	setup.PrintStep(2, totalSteps, "Initializing Configuration")
	nodeName, err := initializeRepo(cpRepoPath, prompter)
	if err != nil {
		return err
	}

	// Step 3: Authentication
	setup.PrintStep(3, totalSteps, "Authentication")
	apiKey, err := handleAuthentication(cpRepoPath, prompter, providedApiKey, nodeName)
	if err != nil {
		return err
	}

	// Step 4: Model Discovery
	var discoveredModels []discoveredModel
	if !skipDiscovery {
		setup.PrintStep(4, totalSteps, "Discovering Model Servers")
		discoveredModels, err = discoverModels()
		if err != nil {
			setup.PrintWarning(fmt.Sprintf("Discovery error: %v", err))
		}
	} else {
		setup.PrintStep(4, totalSteps, "Model Discovery (Skipped)")
		setup.PrintInfo("Skipping model discovery")
	}

	// Step 5: Finalize
	setup.PrintStep(5, totalSteps, "Finalizing Setup")
	if err := finalizeSetup(cpRepoPath, prompter, apiKey, discoveredModels); err != nil {
		return err
	}

	// Done!
	fmt.Println()
	setup.PrintHeader("Setup Complete!")
	fmt.Println("Your computing provider is now configured.")
	fmt.Println()
	fmt.Println("Next steps:")
	setup.PrintBullet("Start the provider: computing-provider run")
	setup.PrintBullet("View dashboard: computing-provider dashboard")
	setup.PrintBullet("Check status: computing-provider inference status")
	fmt.Println()

	fmt.Println("To start your provider, run:")
	fmt.Println()
	fmt.Println("  computing-provider run")
	fmt.Println()

	return nil
}

// checkPrerequisites checks system prerequisites
func checkPrerequisites() error {
	checker := setup.NewPrerequisiteChecker()
	results := checker.CheckAll()

	// Check if we have at least one working backend
	hasOllama := false
	hasDocker := false
	for _, r := range results {
		if r.Name == "Ollama" && r.Status {
			hasOllama = true
		}
		if r.Name == "Docker" && r.Status {
			hasDocker = true
		}
	}

	// Print results with appropriate styling
	for _, r := range results {
		if r.Status {
			setup.PrintSuccess(fmt.Sprintf("%s: %s", r.Name, r.Message))
		} else {
			// Show Docker as warning (optional) if Ollama is available
			if r.Name == "Docker" && hasOllama {
				setup.PrintWarning(fmt.Sprintf("%s: %s (optional - Ollama available)", r.Name, r.Message))
			} else if r.Name == "Ollama" && hasDocker {
				setup.PrintWarning(fmt.Sprintf("%s: %s (optional - Docker available)", r.Name, r.Message))
			} else {
				setup.PrintError(fmt.Sprintf("%s: %s", r.Name, r.Message))
			}
		}
	}

	if checker.HasCriticalFailures() {
		fmt.Println()
		setup.PrintError("No inference backend available!")
		fmt.Println()
		fmt.Println("You need at least one of:")
		setup.PrintBullet("Docker (running) - for SGLang, vLLM, or other containerized servers")
		setup.PrintBullet("Ollama (running) - for native inference on macOS/Linux")
		fmt.Println()
		fmt.Println("To start Ollama:")
		fmt.Println("  ollama serve")
		fmt.Println()
		fmt.Println("To start Docker:")
		fmt.Println("  # macOS: Open Docker Desktop")
		fmt.Println("  # Linux: sudo systemctl start docker")
		fmt.Println()
		return fmt.Errorf("no inference backend available (need Docker or Ollama running)")
	}

	return nil
}

// initializeRepo initializes the CP repo if needed and returns the node name
func initializeRepo(cpRepoPath string, prompter *setup.Prompter) (string, error) {
	// Check if already initialized
	if conf.Exists(cpRepoPath) {
		setup.PrintSuccess("Configuration already initialized")
		// Read existing node name from config
		nodeName := getNodeNameFromConfig(cpRepoPath)
		if nodeName != "" {
			setup.PrintInfo(fmt.Sprintf("Node Name: %s", nodeName))
		}
		return nodeName, nil
	}

	// Create directory
	if err := os.MkdirAll(cpRepoPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create config directory: %w", err)
	}

	// Get node name
	hostname, _ := os.Hostname()
	nodeName, err := prompter.AskString("Node Name", hostname)
	if err != nil {
		return "", err
	}
	if err := setup.ValidateName(nodeName); err != nil {
		setup.PrintError(err.Error())
		return "", err
	}

	// Generate config (using localhost for inference mode)
	multiAddr := "/ip4/127.0.0.1/tcp/9085"
	if err := conf.GenerateAndUpdateConfigFile(cpRepoPath, multiAddr, nodeName, 9085); err != nil {
		return "", fmt.Errorf("failed to initialize config: %w", err)
	}

	setup.PrintSuccess("Configuration initialized")
	return nodeName, nil
}

// getNodeNameFromConfig reads the node name from existing config
func getNodeNameFromConfig(cpRepoPath string) string {
	configFile := filepath.Join(cpRepoPath, "config.toml")
	data, err := os.ReadFile(configFile)
	if err != nil {
		return ""
	}
	// Simple parsing - look for NodeName
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "NodeName") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				name := strings.TrimSpace(parts[1])
				name = strings.Trim(name, "\"")
				return name
			}
		}
	}
	return ""
}

// handleAuthentication handles the authentication step
func handleAuthentication(cpRepoPath string, prompter *setup.Prompter, providedApiKey, nodeName string) (string, error) {
	credMgr := setup.NewCredentialsManager(cpRepoPath)
	authClient := setup.NewAuthClient("")

	// Check for provided API key
	if providedApiKey != "" {
		fmt.Println("Validating provided API key...")
		valid, err := authClient.ValidateApiKey(providedApiKey)
		if err != nil || !valid {
			setup.PrintError(fmt.Sprintf("Invalid API key: %v", err))
			return "", fmt.Errorf("invalid API key")
		}
		setup.PrintSuccess(fmt.Sprintf("API Key validated: %s", setup.MaskApiKey(providedApiKey)))

		// Save it
		if err := credMgr.Save(&setup.Credentials{ApiKey: providedApiKey}); err != nil {
			setup.PrintWarning(fmt.Sprintf("Failed to save credentials: %v", err))
		}

		return providedApiKey, nil
	}

	// Check for existing API key
	existingKey := credMgr.GetApiKey()
	if existingKey == "" {
		existingKey = conf.GetInferenceApiKey(cpRepoPath)
	}

	if existingKey != "" {
		setup.PrintInfo(fmt.Sprintf("Found existing API key: %s", setup.MaskApiKey(existingKey)))
		useExisting, err := prompter.AskYesNo("Use existing API key", true)
		if err != nil {
			return "", err
		}
		if useExisting {
			// Validate it
			fmt.Println("Validating API key...")
			valid, err := authClient.ValidateApiKey(existingKey)
			if err != nil || !valid {
				setup.PrintWarning(fmt.Sprintf("Existing API key is invalid: %v", err))
				setup.PrintInfo("You'll need to login or create a new account")
			} else {
				setup.PrintSuccess("API key is valid")
				return existingKey, nil
			}
		}
	}

	// Ask if user has an account
	hasAccount, err := prompter.AskYesNo("Do you have a Swan Inference account", false)
	if err != nil {
		return "", err
	}

	if hasAccount {
		return handleLogin(cpRepoPath, prompter, authClient, nodeName)
	}
	return handleSignup(cpRepoPath, prompter, authClient, nodeName)
}

// handleLogin handles the email/password login flow
func handleLogin(cpRepoPath string, prompter *setup.Prompter, authClient *setup.AuthClient, nodeName string) (string, error) {
	fmt.Println("\nLogin to Swan Inference")

	email, err := prompter.AskString("Email", "")
	if err != nil {
		return "", err
	}
	if err := setup.ValidateEmail(email); err != nil {
		setup.PrintError(err.Error())
		return "", err
	}

	password, err := prompter.AskPassword("Password")
	if err != nil {
		return "", err
	}
	if err := setup.ValidatePassword(password); err != nil {
		setup.PrintError(err.Error())
		return "", err
	}

	fmt.Println("\nLogging in...")
	loginResp, err := authClient.Login(email, password)
	if err != nil {
		setup.PrintError(fmt.Sprintf("Login failed: %v", err))
		return "", err
	}

	setup.PrintSuccess(fmt.Sprintf("Logged in as %s", loginResp.User.Email))

	// Check if user is already a provider
	if loginResp.User.AccountType == "provider" {
		setup.PrintInfo("You are already a provider")
		return handleExistingProviderLogin(cpRepoPath, prompter, authClient, loginResp.Token)
	}

	// Try to upgrade to provider
	apiKey, isAlreadyProvider, err := handleUpgradeToProvider(cpRepoPath, prompter, authClient, loginResp.Token, email, nodeName)
	if err != nil {
		if isAlreadyProvider {
			// User is already a provider - silently redirect to key creation
			return handleExistingProviderLogin(cpRepoPath, prompter, authClient, loginResp.Token)
		}
		return "", err
	}

	return apiKey, nil
}

// handleExistingProviderLogin handles login for users who are already providers
// It automatically creates a new provider API key using the user's JWT token
func handleExistingProviderLogin(cpRepoPath string, prompter *setup.Prompter, authClient *setup.AuthClient, token string) (string, error) {
	fmt.Println()
	fmt.Println("Creating a new provider API key for this device...")

	// Create a new provider key using the user's JWT token
	keyResp, err := authClient.CreateProviderKey(token, "CLI Key")
	if err != nil {
		setup.PrintError(fmt.Sprintf("Failed to create provider key: %v", err))
		fmt.Println()
		fmt.Println("If you have an existing API key, you can enter it manually.")
		fmt.Println("You can also manage your keys at https://inference-dev.swanchain.io")
		fmt.Println()

		// Fall back to manual entry
		apiKey, err := prompter.AskString("API Key (or press Enter to cancel)", "")
		if err != nil {
			return "", err
		}

		if apiKey == "" {
			return "", fmt.Errorf("API key is required")
		}

		fmt.Println("\nValidating API key...")
		valid, err := authClient.ValidateApiKey(apiKey)
		if err != nil || !valid {
			setup.PrintError(fmt.Sprintf("Invalid API key: %v", err))
			return "", fmt.Errorf("invalid API key")
		}

		setup.PrintSuccess(fmt.Sprintf("API Key validated: %s", setup.MaskApiKey(apiKey)))

		// Save credentials
		credMgr := setup.NewCredentialsManager(cpRepoPath)
		if err := credMgr.Save(&setup.Credentials{ApiKey: apiKey}); err != nil {
			setup.PrintWarning(fmt.Sprintf("Failed to save credentials: %v", err))
		}

		return apiKey, nil
	}

	apiKey := keyResp.ApiKey
	setup.PrintSuccess("Provider API key created!")
	fmt.Println()
	setup.PrintWarning("IMPORTANT: Save this API key! It is only shown once.")
	fmt.Printf("\n  API Key: %s\n\n", apiKey)

	// Save credentials
	credMgr := setup.NewCredentialsManager(cpRepoPath)
	if err := credMgr.Save(&setup.Credentials{
		ApiKey:     apiKey,
		ProviderID: keyResp.ProviderID,
	}); err != nil {
		setup.PrintWarning(fmt.Sprintf("Failed to save credentials: %v", err))
	}

	return apiKey, nil
}

// handleUpgradeToProvider upgrades a user account to provider
// Returns (apiKey, isAlreadyProvider, error)
func handleUpgradeToProvider(cpRepoPath string, prompter *setup.Prompter, authClient *setup.AuthClient, token, email, nodeName string) (string, bool, error) {
	fmt.Println("\nSet up your provider account")

	providerName, err := prompter.AskString("Provider Name", nodeName)
	if err != nil {
		return "", false, err
	}

	if providerName == "" {
		providerName = nodeName
	}
	if err := setup.ValidateName(providerName); err != nil {
		setup.PrintError(err.Error())
		return "", false, err
	}

	fmt.Println("\nWallet address is optional but required for receiving rewards.")
	fmt.Println("You can use any EVM-compatible address, or press Enter to skip.")
	fmt.Println()

	walletAddr, err := prompter.AskString("Wallet Address (optional, press Enter to skip)", "")
	if err != nil {
		return "", false, err
	}

	// Validate format only if provided
	if walletAddr != "" {
		if err := setup.ValidateEVMAddress(walletAddr); err != nil {
			setup.PrintError(err.Error())
			return "", false, err
		}
	} else {
		setup.PrintInfo("No wallet address provided - you can add one later to receive rewards")
	}

	fmt.Println("\nUpgrading to provider...")
	resp, err := authClient.UpgradeToProvider(token, providerName, walletAddr)
	if err != nil {
		// Check if user is already a provider - don't show error, just return flag
		if strings.Contains(err.Error(), "already a provider") {
			return "", true, err
		}
		setup.PrintError(fmt.Sprintf("Upgrade failed: %v", err))
		return "", false, err
	}

	apiKey := resp.ProviderApiKey
	setup.PrintSuccess("Provider account created!")
	setup.PrintInfo(fmt.Sprintf("Provider ID: %s", resp.ProviderID))
	setup.PrintInfo(fmt.Sprintf("Status: %s", resp.Status))
	fmt.Println()
	setup.PrintWarning("IMPORTANT: Save this API key! It is only shown once.")
	fmt.Printf("\n  API Key: %s\n\n", apiKey)

	// Save credentials
	credMgr := setup.NewCredentialsManager(cpRepoPath)
	if err := credMgr.Save(&setup.Credentials{
		ApiKey:     apiKey,
		Email:      email,
		ProviderID: resp.ProviderID,
		Name:       providerName,
	}); err != nil {
		setup.PrintWarning(fmt.Sprintf("Failed to save credentials: %v", err))
	}

	return apiKey, false, nil
}

// handleSignup handles the signup flow for new users
func handleSignup(cpRepoPath string, prompter *setup.Prompter, authClient *setup.AuthClient, nodeName string) (string, error) {
	fmt.Println("\nCreate a new Swan Inference account")

	email, err := prompter.AskString("Email", "")
	if err != nil {
		return "", err
	}
	if err := setup.ValidateEmail(email); err != nil {
		setup.PrintError(err.Error())
		return "", err
	}

	password, err := prompter.AskPassword("Password")
	if err != nil {
		return "", err
	}
	if err := setup.ValidatePassword(password); err != nil {
		setup.PrintError(err.Error())
		return "", err
	}

	// Use node name as display name
	displayName := nodeName

	fmt.Println("\nCreating account...")
	signupResp, err := authClient.Signup(email, password, displayName)
	if err != nil {
		setup.PrintError(fmt.Sprintf("Signup failed: %v", err))
		return "", err
	}

	setup.PrintSuccess("Account created!")

	// Now upgrade to provider
	apiKey, _, err := handleUpgradeToProvider(cpRepoPath, prompter, authClient, signupResp.Token, email, nodeName)
	return apiKey, err
}

// discoveredModel represents a discovered model with its configuration
type discoveredModel struct {
	LocalModel  string           // Original local model name (e.g., "llama3.2:3b")
	SwanModelID string           // Swan Inference model ID (e.g., "llama-3.2-3b")
	SwanModelName string         // Human-readable name from Swan
	Endpoint    string
	ServerType  setup.ServerType
	GPUMemory   int
	Category    string
	Matched     bool             // Whether this model matched a Swan model
	Confidence  float64          // Match confidence
}

// discoverModels discovers running model servers and matches them to Swan Inference models
func discoverModels() ([]discoveredModel, error) {
	discovery := setup.NewModelDiscovery()
	servers := discovery.DiscoverAll()

	if len(servers) == 0 {
		setup.PrintWarning("No model servers found")
		fmt.Println("\nTo start a model server, try:")
		setup.PrintBullet("SGLang:  docker run -d --gpus all -p 30000:30000 lmsysorg/sglang:latest ...")
		setup.PrintBullet("vLLM:    docker run -d --gpus all -p 8000:8000 vllm/vllm-openai:latest ...")
		setup.PrintBullet("Ollama:  ollama serve")
		return nil, nil
	}

	// Collect all local models
	var localModels []string
	serverMap := make(map[string]setup.DiscoveredServer) // local model -> server

	for _, server := range servers {
		setup.PrintSuccess(fmt.Sprintf("Found %s at %s:%d", server.Type, server.Host, server.Port))
		for _, model := range server.Models {
			setup.PrintBullet(model)
			localModels = append(localModels, model)
			serverMap[model] = server
		}
	}

	// Fetch Swan Inference supported models
	fmt.Println("\nMatching with Swan Inference models...")
	swanModels, err := setup.FetchSwanModels("")
	if err != nil {
		setup.PrintWarning(fmt.Sprintf("Could not fetch Swan models: %v", err))
		setup.PrintInfo("Models will be registered with local names (may not be recognized by Swan Inference)")

		// Fall back to using local names directly
		var discovered []discoveredModel
		for _, localModel := range localModels {
			server := serverMap[localModel]
			discovered = append(discovered, discoveredModel{
				LocalModel:  localModel,
				SwanModelID: localModel, // Use local name as Swan ID
				Endpoint:    server.Endpoint,
				ServerType:  server.Type,
				GPUMemory:   setup.EstimateGPUMemory(localModel),
				Category:    setup.DetectModelCategory(localModel),
				Matched:     false,
			})
		}
		return discovered, nil
	}

	// Match local models to Swan models
	matches := setup.MatchModels(localModels, swanModels)

	// Detect collisions: multiple local models matching the same Swan model ID
	swanToLocal := make(map[string][]string) // SwanModelID -> []LocalModel
	for _, match := range matches {
		swanToLocal[match.SwanModelID] = append(swanToLocal[match.SwanModelID], match.LocalModel)
	}

	// Warn about collisions
	for swanID, locals := range swanToLocal {
		if len(locals) > 1 {
			setup.PrintWarning(fmt.Sprintf("Multiple models match %s:", swanID))
			for _, local := range locals {
				setup.PrintBullet(local)
			}
			setup.PrintInfo("Only the first will be used. Run setup again to choose differently.")
		}
	}

	// Use only first match per Swan ID to avoid duplicates
	seenSwanIDs := make(map[string]bool)
	var uniqueMatches []setup.ModelMatch
	for _, match := range matches {
		if !seenSwanIDs[match.SwanModelID] {
			seenSwanIDs[match.SwanModelID] = true
			uniqueMatches = append(uniqueMatches, match)
		}
	}
	matches = uniqueMatches

	// Create discovered models from matches
	var discovered []discoveredModel
	matchedLocals := make(map[string]bool)

	for _, match := range matches {
		server := serverMap[match.LocalModel]
		matchedLocals[match.LocalModel] = true

		confidenceStr := fmt.Sprintf("%.0f%%", match.Confidence*100)
		setup.PrintSuccess(fmt.Sprintf("  %s -> %s (%s)", match.LocalModel, match.SwanModelID, confidenceStr))

		discovered = append(discovered, discoveredModel{
			LocalModel:    match.LocalModel,
			SwanModelID:   match.SwanModelID,
			SwanModelName: match.SwanModelName,
			Endpoint:      server.Endpoint,
			ServerType:    server.Type,
			GPUMemory:     setup.EstimateGPUMemory(match.LocalModel),
			Category:      setup.DetectModelCategory(match.LocalModel),
			Matched:       true,
			Confidence:    match.Confidence,
		})
	}

	// Report unmatched models
	for _, localModel := range localModels {
		if !matchedLocals[localModel] {
			setup.PrintWarning(fmt.Sprintf("  %s -> (no match found)", localModel))
		}
	}

	if len(discovered) == 0 {
		setup.PrintWarning("No local models matched Swan Inference models")
		setup.PrintInfo("Your models may not be supported by Swan Inference yet")
	}

	return discovered, nil
}

// finalizeSetup finalizes the setup by writing config files
func finalizeSetup(cpRepoPath string, prompter *setup.Prompter, apiKey string, discoveredModels []discoveredModel) error {
	// Configure models
	var selectedModels []string
	modelConfigs := make(map[string]conf.ModelConfig)

	if len(discoveredModels) > 0 {
		fmt.Println("\nSelect models to enable:")

		// Build options - show Swan model ID with local model mapping
		var options []setup.SelectionOption
		for _, m := range discoveredModels {
			memStr := fmt.Sprintf("~%dGB", m.GPUMemory/1000)
			var label, desc string
			if m.Matched {
				label = m.SwanModelID
				desc = fmt.Sprintf("%s @ %s  %s  (local: %s)", m.ServerType, m.Endpoint, memStr, m.LocalModel)
			} else {
				label = m.LocalModel
				desc = fmt.Sprintf("%s @ %s  %s  (unmatched)", m.ServerType, m.Endpoint, memStr)
			}
			options = append(options, setup.SelectionOption{
				Label:       label,
				Description: desc,
			})
		}

		selected, err := prompter.AskMultiSelect("", options)
		if err != nil {
			return err
		}

		for _, idx := range selected {
			m := discoveredModels[idx]
			// Use Swan model ID as the key (or local model if unmatched)
			modelID := m.SwanModelID
			if modelID == "" {
				modelID = m.LocalModel
			}

			selectedModels = append(selectedModels, modelID)

			// Set LocalModel only if it differs from the model ID
			localModel := ""
			if m.Matched && m.LocalModel != modelID {
				localModel = m.LocalModel
			}

			modelConfigs[modelID] = conf.ModelConfig{
				Endpoint:   m.Endpoint,
				GPUMemory:  m.GPUMemory,
				Category:   m.Category,
				LocalModel: localModel,
			}
		}
	}

	// Update config.toml with API key and models
	if err := conf.UpdateInferenceConfig(cpRepoPath, apiKey, selectedModels); err != nil {
		setup.PrintError(fmt.Sprintf("Failed to update config.toml: %v", err))
		return err
	}
	setup.PrintSuccess("Updated config.toml")

	// Write models.json if we have models
	if len(modelConfigs) > 0 {
		if err := conf.WriteModelsJson(cpRepoPath, modelConfigs); err != nil {
			setup.PrintError(fmt.Sprintf("Failed to write models.json: %v", err))
			return err
		}
		setup.PrintSuccess(fmt.Sprintf("Created models.json with %d model(s)", len(modelConfigs)))

		// Show the mapping
		fmt.Println("\nModel mappings:")
		for modelID, config := range modelConfigs {
			if config.LocalModel != "" {
				setup.PrintBullet(fmt.Sprintf("%s -> %s (local)", modelID, config.LocalModel))
			} else {
				setup.PrintBullet(fmt.Sprintf("%s", modelID))
			}
		}
	}

	return nil
}

// getCpRepoPath gets the CP repo path from context or default
func getCpRepoPath(c *cli.Context) (string, error) {
	cpRepoPath := c.String(FlagRepo.Name)
	if cpRepoPath == "" {
		cpRepoPath = "~/.swan/computing"
	}

	expanded, err := homedir.Expand(cpRepoPath)
	if err != nil {
		return "", fmt.Errorf("failed to expand path: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(expanded), 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// Set environment variable for other components
	os.Setenv("CP_PATH", expanded)

	return expanded, nil
}
