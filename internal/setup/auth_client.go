package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// DefaultInferenceAPIURL is the default Swan Inference API URL (dev environment)
	DefaultInferenceAPIURL = "https://inference-dev.swanchain.io/api/v1"
)

// AuthClient handles authentication with Swan Inference API
type AuthClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewAuthClient creates a new auth client
func NewAuthClient(baseURL string) *AuthClient {
	if baseURL == "" {
		baseURL = DefaultInferenceAPIURL
	}
	return &AuthClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// UserLoginRequest represents the user login request
type UserLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// UserLoginResponse represents the user login response
type UserLoginResponse struct {
	User  *UserPublicInfo `json:"user"`
	Token string          `json:"token"`
	Error string          `json:"error,omitempty"`
}

// UserPublicInfo represents public user information
type UserPublicInfo struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	AccountType string `json:"account_type"` // "consumer" or "provider"
	ProviderID  string `json:"provider_id,omitempty"`
}

// UserSignupRequest represents the user signup request
type UserSignupRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

// UserSignupResponse represents the user signup response
type UserSignupResponse struct {
	Token  string          `json:"token"`
	User   *UserPublicInfo `json:"user"`
	ApiKey string          `json:"api_key,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// UpgradeToProviderRequest represents the upgrade to provider request
type UpgradeToProviderRequest struct {
	Name               string `json:"name"`
	WalletAddress      string `json:"wallet_address"`
	WorkerAddress      string `json:"worker_address,omitempty"`
	BeneficiaryAddress string `json:"beneficiary_address,omitempty"`
	Description        string `json:"description,omitempty"`
}

// UpgradeToProviderResponse represents the upgrade to provider response
type UpgradeToProviderResponse struct {
	ProviderID        string   `json:"provider_id"`
	ProviderApiKey    string   `json:"provider_api_key"`
	ProviderKeyPrefix string   `json:"provider_key_prefix"`
	Status            string   `json:"status"`
	CanConnect        bool     `json:"can_connect"`
	Message           string   `json:"message"`
	NextSteps         []string `json:"next_steps"`
	Error             string   `json:"error,omitempty"`
}

// Login authenticates a user with email and password
func (c *AuthClient) Login(email, password string) (*UserLoginResponse, error) {
	req := UserLoginRequest{
		Email:    email,
		Password: password,
	}

	resp := &UserLoginResponse{}
	if err := c.doRequest("POST", "/user/login", nil, req, resp); err != nil {
		return nil, err
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("login failed: %s", resp.Error)
	}

	if resp.Token == "" {
		return nil, fmt.Errorf("login failed: no token returned")
	}

	return resp, nil
}

// Signup creates a new user account
func (c *AuthClient) Signup(email, password, displayName string) (*UserSignupResponse, error) {
	req := UserSignupRequest{
		Email:       email,
		Password:    password,
		DisplayName: displayName,
	}

	resp := &UserSignupResponse{}
	if err := c.doRequest("POST", "/user/signup", nil, req, resp); err != nil {
		return nil, err
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("signup failed: %s", resp.Error)
	}

	if resp.Token == "" {
		return nil, fmt.Errorf("signup failed: no token returned")
	}

	return resp, nil
}

// UpgradeToProvider upgrades a user account to a provider
func (c *AuthClient) UpgradeToProvider(token, name, walletAddress string) (*UpgradeToProviderResponse, error) {
	req := UpgradeToProviderRequest{
		Name:          name,
		WalletAddress: walletAddress,
	}

	resp := &UpgradeToProviderResponse{}
	if err := c.doRequestWithBearerToken("POST", "/consumer/upgrade-to-provider", token, req, resp); err != nil {
		return nil, err
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("upgrade failed: %s", resp.Error)
	}

	if resp.ProviderApiKey == "" {
		return nil, fmt.Errorf("upgrade failed: no API key returned")
	}

	return resp, nil
}

// ProviderSignupRequest represents the provider signup request payload (direct signup without user account)
type ProviderSignupRequest struct {
	Name               string `json:"name"`
	OwnerAddress       string `json:"owner_address"`
	WorkerAddress      string `json:"worker_address,omitempty"`
	BeneficiaryAddress string `json:"beneficiary_address,omitempty"`
	Description        string `json:"description,omitempty"`
}

// ProviderSignupResponse represents the provider signup response
type ProviderSignupResponse struct {
	ProviderID string   `json:"provider_id"`
	ApiKey     string   `json:"api_key"`
	KeyPrefix  string   `json:"key_prefix"`
	Status     string   `json:"status"`
	CanConnect bool     `json:"can_connect"`
	Message    string   `json:"message"`
	NextSteps  []string `json:"next_steps"`
	Error      string   `json:"error,omitempty"`
}

// ProviderSignup creates a new provider account directly
// This is the main signup flow - creates provider and returns API key
func (c *AuthClient) ProviderSignup(name, ownerAddress string) (*ProviderSignupResponse, error) {
	// Validate owner address format
	if ownerAddress != "" && (len(ownerAddress) != 42 || ownerAddress[:2] != "0x") {
		return nil, fmt.Errorf("invalid wallet address format: must be 42 characters starting with 0x")
	}

	req := ProviderSignupRequest{
		Name:         name,
		OwnerAddress: ownerAddress,
	}

	resp := &ProviderSignupResponse{}
	if err := c.doRequest("POST", "/provider/signup", nil, req, resp); err != nil {
		return nil, err
	}

	// Check for error in response
	if resp.Error != "" {
		return nil, fmt.Errorf("signup failed: %s", resp.Error)
	}

	// Check if we got an API key
	if resp.ApiKey == "" {
		return nil, fmt.Errorf("signup failed: no API key returned")
	}

	return resp, nil
}

// ProviderStatusResponse represents the provider status response
type ProviderStatusResponse struct {
	ProviderID  string   `json:"provider_id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	CanConnect  bool     `json:"can_connect"`
	ApiKeyValid bool     `json:"api_key_valid"`
	Message     string   `json:"message"`
	NextSteps   []string `json:"next_steps"`
	Error       string   `json:"error,omitempty"`
}

// GetProviderStatus retrieves provider status using an API key
func (c *AuthClient) GetProviderStatus(apiKey string) (*ProviderStatusResponse, error) {
	resp := &ProviderStatusResponse{}
	if err := c.doRequestWithBearerToken("GET", "/provider/status", apiKey, nil, resp); err != nil {
		return nil, err
	}

	// Check for error response
	if resp.Error != "" {
		return nil, fmt.Errorf("get provider status failed: %s", resp.Error)
	}

	return resp, nil
}

// ValidateApiKey validates an existing API key by checking provider status
func (c *AuthClient) ValidateApiKey(apiKey string) (bool, error) {
	resp, err := c.GetProviderStatus(apiKey)
	if err != nil {
		return false, err
	}
	return resp.ApiKeyValid, nil
}

// CreateProviderKeyRequest represents a request to create a new provider key
type CreateProviderKeyRequest struct {
	Name string `json:"name"`
}

// CreateProviderKeyResponse represents the response from creating a provider key
type CreateProviderKeyResponse struct {
	ApiKey     string `json:"api_key"`
	ProviderID string `json:"provider_id"`
	Message    string `json:"message"`
	Error      string `json:"error,omitempty"`
}

// CreateProviderKey creates a new provider API key using user JWT token
// This is used for existing providers who need a new API key
func (c *AuthClient) CreateProviderKey(userToken, keyName string) (*CreateProviderKeyResponse, error) {
	req := CreateProviderKeyRequest{
		Name: keyName,
	}

	resp := &CreateProviderKeyResponse{}
	if err := c.doRequestWithBearerToken("POST", "/user/me/provider-keys", userToken, req, resp); err != nil {
		return nil, err
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("create provider key failed: %s", resp.Error)
	}

	if resp.ApiKey == "" {
		return nil, fmt.Errorf("create provider key failed: no API key returned")
	}

	return resp, nil
}

// doRequest performs an HTTP request
func (c *AuthClient) doRequest(method, path string, token *string, body interface{}, result interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if token != nil {
		req.Header.Set("Authorization", "Bearer "+*token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check for HTTP error status codes
	if resp.StatusCode >= 400 {
		// Try to parse error from response body
		var errResp struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && (errResp.Error != "" || errResp.Message != "") {
			errMsg := errResp.Error
			if errMsg == "" {
				errMsg = errResp.Message
			}
			return fmt.Errorf("%s", errMsg)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("failed to parse response: %w (body: %s)", err, string(respBody))
	}

	return nil
}

// doRequestWithBearerToken performs an HTTP request with Bearer token auth
func (c *AuthClient) doRequestWithBearerToken(method, path, token string, body interface{}, result interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check for HTTP error status codes
	if resp.StatusCode >= 400 {
		// Try to parse error from response body
		var errResp struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && (errResp.Error != "" || errResp.Message != "") {
			errMsg := errResp.Error
			if errMsg == "" {
				errMsg = errResp.Message
			}
			return fmt.Errorf("%s", errMsg)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("failed to parse response: %w (body: %s)", err, string(respBody))
	}

	return nil
}
