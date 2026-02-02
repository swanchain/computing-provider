package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Credentials represents stored credentials
type Credentials struct {
	ApiKey     string `json:"api_key"`
	Email      string `json:"email,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
	Name       string `json:"name,omitempty"`
}

// CredentialsManager handles credential storage
type CredentialsManager struct {
	cpPath string
}

// NewCredentialsManager creates a new credentials manager
func NewCredentialsManager(cpPath string) *CredentialsManager {
	return &CredentialsManager{
		cpPath: cpPath,
	}
}

// credentialsPath returns the path to credentials.json
func (m *CredentialsManager) credentialsPath() string {
	return filepath.Join(m.cpPath, "credentials.json")
}

// Save saves credentials to file with restricted permissions
func (m *CredentialsManager) Save(creds *Credentials) error {
	// Ensure directory exists
	if err := os.MkdirAll(m.cpPath, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	// Write with restricted permissions (owner read/write only)
	if err := os.WriteFile(m.credentialsPath(), data, 0600); err != nil {
		return fmt.Errorf("failed to write credentials: %w", err)
	}

	return nil
}

// Load loads credentials from file
func (m *CredentialsManager) Load() (*Credentials, error) {
	data, err := os.ReadFile(m.credentialsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No credentials file is not an error
		}
		return nil, fmt.Errorf("failed to read credentials: %w", err)
	}

	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %w", err)
	}

	return &creds, nil
}

// HasApiKey checks if an API key is stored
func (m *CredentialsManager) HasApiKey() bool {
	creds, err := m.Load()
	if err != nil || creds == nil {
		return false
	}
	return creds.ApiKey != ""
}

// GetApiKey returns the stored API key
func (m *CredentialsManager) GetApiKey() string {
	creds, err := m.Load()
	if err != nil || creds == nil {
		return ""
	}
	return creds.ApiKey
}

// Delete removes the credentials file
func (m *CredentialsManager) Delete() error {
	path := m.credentialsPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // Already doesn't exist
	}
	return os.Remove(path)
}

// MaskApiKey returns a masked version of the API key for display
func MaskApiKey(apiKey string) string {
	if len(apiKey) <= 12 {
		return "***"
	}
	return apiKey[:8] + "..." + apiKey[len(apiKey)-4:]
}
