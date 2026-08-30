package conf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

type DashboardSettings struct {
	Alerts    AlertSettings        `json:"alerts"`
	SelfCheck SelfCheckSettings    `json:"self_check"`
	Log       LogSettings          `json:"log"`
	Limits    RequestLimitSettings `json:"limits"`
	Models    []DashboardModel     `json:"models"`
}

type AlertSettings struct {
	WebhookURL           string        `json:"webhook_url"`
	CooldownMinutes      int           `json:"cooldown_minutes"`
	DisconnectAfterMin   int           `json:"disconnect_after_min"`
	ErrorRateThreshold   float64       `json:"error_rate_threshold"`
	ErrorRateMinRequests int           `json:"error_rate_min_requests"`
	Email                EmailSettings `json:"email"`
}

type EmailSettings struct {
	Host          string   `json:"host"`
	Port          int      `json:"port"`
	Username      string   `json:"username"`
	Password      string   `json:"password,omitempty"`
	PasswordSet   bool     `json:"password_set"`
	ClearPassword bool     `json:"clear_password,omitempty"`
	From          string   `json:"from"`
	To            []string `json:"to"`
}

type SelfCheckSettings struct {
	Enable                bool `json:"enable"`
	IntervalMinutes       int  `json:"interval_minutes"`
	AutoDisable           bool `json:"auto_disable"`
	AutoRecover           bool `json:"auto_recover"`
	FailuresBeforeDisable int  `json:"failures_before_disable"`
}

type LogSettings struct {
	Dir        string `json:"dir"`
	Level      string `json:"level"`
	MaxSizeMB  int    `json:"max_size_mb"`
	MaxBackups int    `json:"max_backups"`
	MaxAgeDays int    `json:"max_age_days"`
	Compress   bool   `json:"compress"`
	Stdout     bool   `json:"stdout"`
}

type RequestLimitSettings struct {
	RequestsPerSecond float64 `json:"requests_per_second"`
	MaxConcurrent     int     `json:"max_concurrent"`
}

type DashboardModel struct {
	ID            string `json:"id"`
	Container     string `json:"container,omitempty"`
	Endpoint      string `json:"endpoint"`
	GPUMemory     int    `json:"gpu_memory"`
	Category      string `json:"category"`
	LocalModel    string `json:"local_model,omitempty"`
	Format        string `json:"format,omitempty"`
	Quantization  string `json:"quantization,omitempty"`
	APIKey        string `json:"api_key,omitempty"`
	APIKeySet     bool   `json:"api_key_set"`
	ClearAPIKey   bool   `json:"clear_api_key,omitempty"`
	ContextLength int    `json:"context_length,omitempty"`
}

func boolPointer(value bool) *bool { return &value }

func readComputeNode(cpRepoPath string) (ComputeNode, error) {
	var node ComputeNode
	if _, err := toml.DecodeFile(filepath.Join(cpRepoPath, "config.toml"), &node); err != nil {
		return node, fmt.Errorf("read config: %w", err)
	}
	return node, nil
}

func writeComputeNode(cpRepoPath string, node ComputeNode) error {
	return atomicWriteFile(filepath.Join(cpRepoPath, "config.toml"), func(w io.Writer) error {
		return toml.NewEncoder(w).Encode(node)
	}, SecretFileMode)
}

// LoadDashboardSettings returns editable operational settings without ever
// returning a stored SMTP or model endpoint credential.
func LoadDashboardSettings(cpRepoPath string) (DashboardSettings, error) {
	node, err := readComputeNode(cpRepoPath)
	if err != nil {
		return DashboardSettings{}, err
	}
	applyAlertDefaults(&node.Alerts)
	applySelfCheckDefaults(&node.SelfCheck)
	applyLogDefaults(&node.Log, cpRepoPath)
	applyRequestLimitDefaults(&node.RequestLimits)

	models, err := LoadModelsJson(cpRepoPath)
	if err != nil {
		return DashboardSettings{}, err
	}
	modelSettings := make([]DashboardModel, 0, len(models))
	for id, model := range models {
		modelSettings = append(modelSettings, DashboardModel{
			ID:            id,
			Container:     model.Container,
			Endpoint:      model.Endpoint,
			GPUMemory:     model.GPUMemory,
			Category:      model.Category,
			LocalModel:    model.LocalModel,
			Format:        model.Format,
			Quantization:  model.Quantization,
			APIKeySet:     strings.TrimSpace(model.APIKey) != "",
			ContextLength: model.ContextLength,
		})
	}
	sort.Slice(modelSettings, func(i, j int) bool { return modelSettings[i].ID < modelSettings[j].ID })

	return DashboardSettings{
		Alerts: AlertSettings{
			WebhookURL:           node.Alerts.WebhookURL,
			CooldownMinutes:      node.Alerts.CooldownMinutes,
			DisconnectAfterMin:   node.Alerts.DisconnectAfterMin,
			ErrorRateThreshold:   node.Alerts.ErrorRateThreshold,
			ErrorRateMinRequests: node.Alerts.ErrorRateMinRequests,
			Email: EmailSettings{
				Host:        node.Alerts.Email.Host,
				Port:        node.Alerts.Email.Port,
				Username:    node.Alerts.Email.Username,
				PasswordSet: strings.TrimSpace(node.Alerts.Email.Password) != "" || strings.TrimSpace(os.Getenv("SMTP_PASSWORD")) != "",
				From:        node.Alerts.Email.From,
				To:          append([]string(nil), node.Alerts.Email.To...),
			},
		},
		SelfCheck: SelfCheckSettings{
			Enable:                node.SelfCheck.Enabled(),
			IntervalMinutes:       node.SelfCheck.IntervalMinutes,
			AutoDisable:           node.SelfCheck.AutoDisableEnabled(),
			AutoRecover:           node.SelfCheck.AutoRecoverEnabled(),
			FailuresBeforeDisable: node.SelfCheck.FailuresBeforeDisable,
		},
		Log: LogSettings{
			Dir:        node.Log.Dir,
			Level:      node.Log.Level,
			MaxSizeMB:  node.Log.MaxSizeMB,
			MaxBackups: node.Log.MaxBackups,
			MaxAgeDays: node.Log.MaxAgeDays,
			Compress:   node.Log.CompressEnabled(),
			Stdout:     node.Log.StdoutEnabled(),
		},
		Limits: RequestLimitSettings{
			RequestsPerSecond: node.RequestLimits.RequestsPerSecond,
			MaxConcurrent:     node.RequestLimits.MaxConcurrent,
		},
		Models: modelSettings,
	}, nil
}

func updateDashboardConfig(cpRepoPath string, update func(*ComputeNode) error) error {
	node, err := readComputeNode(cpRepoPath)
	if err != nil {
		return err
	}
	if err := update(&node); err != nil {
		return err
	}
	if err := writeComputeNode(cpRepoPath, node); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func validateHTTPURL(label, raw string, allowEmpty bool) error {
	raw = strings.TrimSpace(raw)
	if raw == "" && allowEmpty {
		return nil
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s must be a valid http or https URL", label)
	}
	return nil
}

func UpdateAlertSettings(cpRepoPath string, settings AlertSettings) error {
	settings.WebhookURL = strings.TrimSpace(settings.WebhookURL)
	settings.Email.Host = strings.TrimSpace(settings.Email.Host)
	if err := validateHTTPURL("webhook URL", settings.WebhookURL, true); err != nil {
		return err
	}
	if settings.CooldownMinutes < 1 || settings.CooldownMinutes > 10080 {
		return fmt.Errorf("cooldown must be between 1 and 10080 minutes")
	}
	if settings.DisconnectAfterMin < 1 || settings.DisconnectAfterMin > 10080 {
		return fmt.Errorf("disconnect delay must be between 1 and 10080 minutes")
	}
	if settings.ErrorRateThreshold <= 0 || settings.ErrorRateThreshold > 1 {
		return fmt.Errorf("error rate threshold must be greater than 0 and at most 1")
	}
	if settings.ErrorRateMinRequests < 1 {
		return fmt.Errorf("minimum request count must be positive")
	}
	if settings.Email.Host != "" {
		if strings.Contains(settings.Email.Host, "://") || strings.ContainsAny(settings.Email.Host, " /\\") {
			return fmt.Errorf("SMTP host must be a hostname without a URL scheme")
		}
		if settings.Email.Port < 1 || settings.Email.Port > 65535 {
			return fmt.Errorf("SMTP port must be between 1 and 65535")
		}
		if len(settings.Email.To) == 0 {
			return fmt.Errorf("at least one email recipient is required when SMTP is configured")
		}
	}
	for _, address := range append(append([]string(nil), settings.Email.To...), settings.Email.From) {
		if strings.TrimSpace(address) == "" {
			continue
		}
		if _, err := mail.ParseAddress(address); err != nil {
			return fmt.Errorf("invalid email address %q", address)
		}
	}

	return updateDashboardConfig(cpRepoPath, func(node *ComputeNode) error {
		password := node.Alerts.Email.Password
		if settings.Email.ClearPassword {
			password = ""
		} else if settings.Email.Password != "" {
			password = settings.Email.Password
		}
		node.Alerts = Alerts{
			WebhookURL:           settings.WebhookURL,
			CooldownMinutes:      settings.CooldownMinutes,
			DisconnectAfterMin:   settings.DisconnectAfterMin,
			ErrorRateThreshold:   settings.ErrorRateThreshold,
			ErrorRateMinRequests: settings.ErrorRateMinRequests,
			Email: Email{
				Host:     settings.Email.Host,
				Port:     settings.Email.Port,
				Username: strings.TrimSpace(settings.Email.Username),
				Password: password,
				From:     strings.TrimSpace(settings.Email.From),
				To:       cleanStrings(settings.Email.To),
			},
		}
		return nil
	})
}

func cleanStrings(values []string) []string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return cleaned
}

func UpdateSelfCheckSettings(cpRepoPath string, settings SelfCheckSettings) error {
	if settings.IntervalMinutes < 1 || settings.IntervalMinutes > 10080 {
		return fmt.Errorf("self-check interval must be between 1 and 10080 minutes")
	}
	if settings.FailuresBeforeDisable < 1 || settings.FailuresBeforeDisable > 100 {
		return fmt.Errorf("failures before disable must be between 1 and 100")
	}
	return updateDashboardConfig(cpRepoPath, func(node *ComputeNode) error {
		node.SelfCheck = SelfCheck{
			Enable:                boolPointer(settings.Enable),
			IntervalMinutes:       settings.IntervalMinutes,
			AutoDisable:           boolPointer(settings.AutoDisable),
			AutoRecover:           boolPointer(settings.AutoRecover),
			FailuresBeforeDisable: settings.FailuresBeforeDisable,
		}
		return nil
	})
}

func UpdateLogSettings(cpRepoPath string, settings LogSettings) error {
	settings.Level = strings.ToLower(strings.TrimSpace(settings.Level))
	allowedLevels := map[string]bool{"trace": true, "debug": true, "info": true, "warn": true, "error": true}
	if !allowedLevels[settings.Level] {
		return fmt.Errorf("log level must be trace, debug, info, warn, or error")
	}
	if strings.TrimSpace(settings.Dir) == "" {
		return fmt.Errorf("log directory is required")
	}
	if settings.MaxSizeMB < 1 || settings.MaxSizeMB > 102400 {
		return fmt.Errorf("maximum log size must be between 1 and 102400 MB")
	}
	if settings.MaxBackups < 1 || settings.MaxBackups > 1000 {
		return fmt.Errorf("maximum backups must be between 1 and 1000")
	}
	if settings.MaxAgeDays == 0 || settings.MaxAgeDays < -1 || settings.MaxAgeDays > 36500 {
		return fmt.Errorf("maximum age must be -1 or between 1 and 36500 days")
	}
	return updateDashboardConfig(cpRepoPath, func(node *ComputeNode) error {
		node.Log = Log{
			Dir:        strings.TrimSpace(settings.Dir),
			Level:      settings.Level,
			MaxSizeMB:  settings.MaxSizeMB,
			MaxBackups: settings.MaxBackups,
			MaxAgeDays: settings.MaxAgeDays,
			Compress:   boolPointer(settings.Compress),
			Stdout:     boolPointer(settings.Stdout),
		}
		return nil
	})
}

func UpdateRequestLimitSettings(cpRepoPath string, settings RequestLimitSettings) error {
	if settings.RequestsPerSecond < 0.1 || settings.RequestsPerSecond > 100000 {
		return fmt.Errorf("request rate must be between 0.1 and 100000 requests per second")
	}
	if settings.MaxConcurrent < 1 || settings.MaxConcurrent > 100000 {
		return fmt.Errorf("maximum concurrency must be between 1 and 100000")
	}
	return updateDashboardConfig(cpRepoPath, func(node *ComputeNode) error {
		node.RequestLimits = RequestLimits{
			RequestsPerSecond: settings.RequestsPerSecond,
			MaxConcurrent:     settings.MaxConcurrent,
		}
		return nil
	})
}

func UpdateDashboardModels(cpRepoPath string, settings []DashboardModel) error {
	existing, err := LoadModelsJson(cpRepoPath)
	if err != nil {
		return err
	}
	models := make(map[string]ModelConfig, len(settings))
	ids := make([]string, 0, len(settings))
	for _, setting := range settings {
		setting.ID = strings.TrimSpace(setting.ID)
		setting.Endpoint = strings.TrimSpace(setting.Endpoint)
		if setting.ID == "" {
			return fmt.Errorf("model ID is required")
		}
		if _, duplicate := models[setting.ID]; duplicate {
			return fmt.Errorf("model ID %q is duplicated", setting.ID)
		}
		if err := validateHTTPURL("endpoint for "+setting.ID, setting.Endpoint, false); err != nil {
			return err
		}
		if strings.TrimSpace(setting.Category) == "" {
			return fmt.Errorf("category is required for %s", setting.ID)
		}
		if setting.GPUMemory < 0 || setting.ContextLength < 0 {
			return fmt.Errorf("GPU memory and context length cannot be negative for %s", setting.ID)
		}
		apiKey := existing[setting.ID].APIKey
		if setting.ClearAPIKey {
			apiKey = ""
		} else if setting.APIKey != "" {
			apiKey = setting.APIKey
		}
		models[setting.ID] = ModelConfig{
			Container:     strings.TrimSpace(setting.Container),
			Endpoint:      setting.Endpoint,
			GPUMemory:     setting.GPUMemory,
			Category:      strings.TrimSpace(setting.Category),
			LocalModel:    strings.TrimSpace(setting.LocalModel),
			Format:        strings.TrimSpace(setting.Format),
			Quantization:  strings.TrimSpace(setting.Quantization),
			APIKey:        apiKey,
			ContextLength: setting.ContextLength,
		}
		ids = append(ids, setting.ID)
	}
	sort.Strings(ids)

	node, err := readComputeNode(cpRepoPath)
	if err != nil {
		return err
	}
	node.Inference.Models = ids
	var configBuffer bytes.Buffer
	if err := toml.NewEncoder(&configBuffer).Encode(node); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	modelsData, err := json.MarshalIndent(models, "", "  ")
	if err != nil {
		return fmt.Errorf("encode models: %w", err)
	}

	configPath := filepath.Join(cpRepoPath, "config.toml")
	previousConfig, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read existing config: %w", err)
	}
	if err := atomicWriteFile(configPath, func(w io.Writer) error {
		_, err := w.Write(configBuffer.Bytes())
		return err
	}, SecretFileMode); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	modelsPath := filepath.Join(cpRepoPath, "models.json")
	if err := atomicWriteFile(modelsPath, func(w io.Writer) error {
		_, err := w.Write(modelsData)
		return err
	}, SecretFileMode); err != nil {
		rollbackErr := atomicWriteFile(configPath, func(w io.Writer) error {
			_, writeErr := w.Write(previousConfig)
			return writeErr
		}, SecretFileMode)
		if rollbackErr != nil {
			return fmt.Errorf("write models: %v (config rollback also failed: %v)", err, rollbackErr)
		}
		return fmt.Errorf("write models: %w", err)
	}
	return nil
}
