package conf

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/swanchain/computing-provider-v2/build"
)

// atomicWriteFile writes data to a temp file then renames to target path.
// This prevents config corruption if the process is interrupted during write.
func atomicWriteFile(targetPath string, writeFunc func(w io.Writer) error, perm os.FileMode) error {
	dir := filepath.Dir(targetPath)

	// Create temp file in same directory (for same-filesystem rename)
	tmpFile, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Clean up temp file on any error
	defer func() {
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
	}()

	// Write to temp file
	if err := writeFunc(tmpFile); err != nil {
		tmpFile.Close()
		return err
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Set permissions
	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	tmpPath = "" // Prevent cleanup of successfully renamed file
	return nil
}

var config *ComputeNode

type Pricing bool

func (p *Pricing) UnmarshalTOML(data interface{}) error {
	switch v := data.(type) {
	case bool:
		*p = Pricing(v)
	case string:
		*p = strings.ToLower(v) == "true" || v == ""
	default:
		*p = true
	}
	return nil
}

// ComputeNode is a compute node config
type ComputeNode struct {
	API       API
	RPC       RPC       `toml:"RPC,omitempty"`
	Inference Inference  `toml:"Inference,omitempty"`
}

// Inference is the Swan Inference marketplace configuration (default mode)
type Inference struct {
	Enable             bool     `toml:"Enable"`
	ServiceURL         string   `toml:"ServiceURL"`         // HTTP API URL (e.g., http://localhost:8080)
	WebSocketURL       string   `toml:"WebSocketURL"`       // WebSocket URL (e.g., wss://inference-ws.swanchain.io)
	ApiKey             string   `toml:"ApiKey"`             // Provider API key for authentication (sk-prov-*)
	Models             []string `toml:"Models"`             // Models this provider serves
}

type API struct {
	Port                          int
	MultiAddress                  string
	Domain                        string
	NodeName                      string
	Pricing                       Pricing  `toml:"pricing"`
	AutoDeleteImage               bool     `toml:"AutoDeleteImage"`
	ClearLogDuration              int      `toml:"ClearLogDuration"`
	PortRange                     []string `toml:"PortRange"`
	GpuUtilizationRejectThreshold float64  `toml:"GpuUtilizationRejectThreshold"`
}

type RPC struct {
	SwanChainRpc string `toml:"SWAN_CHAIN_RPC"`
}

func GetRpcByNetWorkName() (string, error) {
	if len(strings.TrimSpace(GetConfig().RPC.SwanChainRpc)) == 0 {
		return "", fmt.Errorf("You need to set SWAN_CHAIN_RPC in the configuration file")
	}
	return GetConfig().RPC.SwanChainRpc, nil
}

func InitConfig(cpRepoPath string, standalone bool) error {
	configFile := filepath.Join(cpRepoPath, "config.toml")

	if _, err := os.Stat(configFile); err != nil {
		return fmt.Errorf("not found %s repo, "+
			"please use `computing-provider init` to initialize the repo ", cpRepoPath)
	}

	_, err := toml.DecodeFile(configFile, &config)
	if err != nil {
		return fmt.Errorf("failed load config file, path: %s, error: %w", configFile, err)
	}

	if config.API.GpuUtilizationRejectThreshold == 0 {
		config.API.GpuUtilizationRejectThreshold = 1.0
	}

	// Validate MultiAddress format if provided (optional for Inference mode)
	if config.API.MultiAddress != "" {
		multiAddressSplit := strings.Split(config.API.MultiAddress, "/")
		if len(multiAddressSplit) < 5 {
			log.Printf("Warning: MultiAddress %s may be invalid. Expected format: /ip4/<IP>/tcp/<PORT>\n", config.API.MultiAddress)
		}
	}

	return nil
}

func GetConfig() *ComputeNode {
	return config
}

func GenerateAndUpdateConfigFile(cpRepoPath string, multiAddress, nodeName string, port int) error {
	fmt.Println("Checking if repo exists")

	if Exists(cpRepoPath) {
		return fmt.Errorf("repo at '%s' is already initialized", cpRepoPath)
	}

	var configTmpl ComputeNode

	configFilePath := path.Join(cpRepoPath, "config.toml")
	if _, err := os.Stat(configFilePath); os.IsNotExist(err) {
		configTmpl = generateDefaultConfig()
	} else {
		if _, err = toml.DecodeFile(configFilePath, &configTmpl); err != nil {
			return err
		}
	}

	if len(multiAddress) != 0 && !strings.EqualFold(multiAddress, strings.TrimSpace(configTmpl.API.MultiAddress)) {
		configTmpl.API.MultiAddress = multiAddress
	}

	if len(strings.TrimSpace(nodeName)) != 0 {
		configTmpl.API.NodeName = nodeName
	} else {
		hostname, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("get hostname failed, error: %v", err)
		}
		configTmpl.API.NodeName = hostname
	}

	if port != 0 {
		configTmpl.API.Port = port
	}

	// Atomic write of config file
	if err := atomicWriteFile(configFilePath, func(w io.Writer) error {
		return toml.NewEncoder(w).Encode(configTmpl)
	}, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	file, err := os.Create(path.Join(cpRepoPath, "provider.db"))
	if err != nil {
		return err
	}
	defer file.Close()

	if err = os.MkdirAll(path.Join(cpRepoPath, "keystore"), 0755); err != nil {
		return fmt.Errorf("failed to create keystore, error: %v", err)
	}

	fmt.Printf("Initialized CP repo at '%s'. \n", cpRepoPath)
	return nil
}

func generateDefaultConfig() ComputeNode {
	return ComputeNode{
		API: API{
			Port:         8085,
			MultiAddress: "/ip4/<PUBLIC_IP>/tcp/<PORT>",
			NodeName:     "<YOUR_CP_Node_Name>",
			Pricing:      true,
		},
		Inference: Inference{
			Enable:             true,
			ServiceURL:         build.DefaultInferenceURL,
			WebSocketURL:       build.DefaultInferenceWSURL,
			Models:             []string{},
		},
	}
}

func Exists(cpPath string) bool {
	_, err := os.Stat(filepath.Join(cpPath, "keystore"))
	KeyStoreNoExist := os.IsNotExist(err)
	err = nil
	_, err = os.Stat(filepath.Join(cpPath, "provider.db"))
	providerNotExist := os.IsNotExist(err)

	if KeyStoreNoExist && providerNotExist {
		return false
	}
	return true
}

// ModelConfig represents a model configuration for models.json
type ModelConfig struct {
	Container  string `json:"container,omitempty"`
	Endpoint   string `json:"endpoint"`
	GPUMemory  int    `json:"gpu_memory"`
	Category   string `json:"category"`
	LocalModel string `json:"local_model,omitempty"`
}

// UpdateInferenceConfig updates the Inference section in config.toml
func UpdateInferenceConfig(cpRepoPath, apiKey string, models []string) error {
	configFilePath := path.Join(cpRepoPath, "config.toml")

	var configTmpl ComputeNode
	if _, err := toml.DecodeFile(configFilePath, &configTmpl); err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// Update Inference config
	configTmpl.Inference.Enable = true
	if apiKey != "" {
		configTmpl.Inference.ApiKey = apiKey
	}
	if models != nil {
		configTmpl.Inference.Models = models
	}

	// Atomic write
	return atomicWriteFile(configFilePath, func(w io.Writer) error {
		return toml.NewEncoder(w).Encode(configTmpl)
	}, 0644)
}

// WriteModelsJson writes the models.json file from model configurations
func WriteModelsJson(cpRepoPath string, models map[string]ModelConfig) error {
	modelsPath := path.Join(cpRepoPath, "models.json")

	data, err := json.MarshalIndent(models, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal models: %w", err)
	}

	// Atomic write
	return atomicWriteFile(modelsPath, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	}, 0644)
}

// LoadModelsJson loads the models.json file
func LoadModelsJson(cpRepoPath string) (map[string]ModelConfig, error) {
	modelsPath := path.Join(cpRepoPath, "models.json")

	data, err := os.ReadFile(modelsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]ModelConfig), nil
		}
		return nil, fmt.Errorf("failed to read models.json: %w", err)
	}

	var models map[string]ModelConfig
	if err := json.Unmarshal(data, &models); err != nil {
		return nil, fmt.Errorf("failed to parse models.json: %w", err)
	}

	return models, nil
}

// GetInferenceApiKey returns the configured Inference API key
func GetInferenceApiKey(cpRepoPath string) string {
	if key := os.Getenv("INFERENCE_API_KEY"); key != "" {
		return key
	}

	configFilePath := path.Join(cpRepoPath, "config.toml")
	var configTmpl ComputeNode
	if _, err := toml.DecodeFile(configFilePath, &configTmpl); err == nil {
		return configTmpl.Inference.ApiKey
	}

	return ""
}
