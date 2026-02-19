package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/setup"
)

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
