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
	"time"

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
	API           API
	RPC           RPC           `toml:"RPC,omitempty"`
	Inference     Inference     `toml:"Inference,omitempty"`
	Log           Log           `toml:"Log,omitempty"`
	Alerts        Alerts        `toml:"Alerts,omitempty"`
	SelfCheck     SelfCheck     `toml:"SelfCheck,omitempty"`
	HealthCheck   HealthCheck   `toml:"HealthCheck,omitempty"`
	RequestLimits RequestLimits `toml:"RequestLimits,omitempty"`
	Dashboard     Dashboard     `toml:"Dashboard,omitempty"`
}

// Dashboard configures the web UI served by `computing-provider dashboard`.
// Setting it here means an operator configures the port once rather than
// remembering a flag on every start.
type Dashboard struct {
	// Host to listen on. Defaults to 127.0.0.1: the dashboard proxies to the
	// provider API, which has endpoints that can take models out of service, so
	// exposing it should be a deliberate act. Use 0.0.0.0 only on a network you
	// trust.
	Host string `toml:"Host"`
	Port int    `toml:"Port"` // Default: 3060
}

// RequestLimits are persistent global admission controls. They intentionally
// mirror the two controls already available at runtime in the dashboard.
type RequestLimits struct {
	RequestsPerSecond float64 `toml:"RequestsPerSecond"`
	MaxConcurrent     int     `toml:"MaxConcurrent"`
}

// HealthCheck tunes the periodic endpoint probe.
//
// The cheap probe (GET /v1/models) is served from a static registry on most
// backends, so it keeps returning 200 after the inference engine behind it has
// died. DeepCheckEvery adds a real one-token completion on every Nth check to
// catch that, at a cost of one token per model per DeepCheckEvery intervals.
//
// Providers fronting a metered upstream — a proxy billing per request rather
// than a local GPU — are the reason this is tunable: raise the number, or set
// it to -1 to switch the engine probe off entirely.
type HealthCheck struct {
	DeepCheckEvery   int `toml:"DeepCheckEvery"`   // Engine probe every Nth check. Default: 10. -1 disables.
	DeepCheckTimeout int `toml:"DeepCheckTimeout"` // Seconds to wait for that completion. Default: 30
}

// HealthCheckConfig overlays configured values on the built-in defaults. A zero
// field means "unset" and keeps the default, so an operator who names only one
// knob does not silently reset the others.
func (h HealthCheck) DeepEvery(def int) int {
	switch {
	case h.DeepCheckEvery < 0:
		return 0 // Explicitly disabled.
	case h.DeepCheckEvery == 0:
		return def
	default:
		return h.DeepCheckEvery
	}
}

// DeepTimeout returns the configured probe timeout, or def when unset.
func (h HealthCheck) DeepTimeout(def time.Duration) time.Duration {
	if h.DeepCheckTimeout <= 0 {
		return def
	}
	return time.Duration(h.DeepCheckTimeout) * time.Second
}

// SelfCheck controls the periodic audit and what it does about a model that
// cannot serve. A backend can pass health checks while failing every request —
// health checks probe /v1/models, which most backends answer without touching
// the inference engine — so without this the node keeps accepting traffic it
// cannot fulfil and its reliability score pays for it.
type SelfCheck struct {
	Enable          *bool `toml:"Enable"`          // Run the periodic audit. Default: true
	IntervalMinutes int   `toml:"IntervalMinutes"` // How often to audit. Default: 10
	AutoDisable     *bool `toml:"AutoDisable"`     // Deregister a model whose backend cannot serve. Default: true
	AutoRecover     *bool `toml:"AutoRecover"`     // Re-register it once the backend works again. Default: true
	// FailuresBeforeDisable is how many consecutive failed probes are required
	// before deregistering, so a single transient blip does not pull a model.
	FailuresBeforeDisable int `toml:"FailuresBeforeDisable"` // Default: 2
	// AlertAfterFailures is how many consecutive audits must report the same
	// problem before it is emailed. Without it a transient upstream error — a
	// proxy answering 502 once — pages the operator while being nowhere near
	// severe enough to deregister anything, so the alerting threshold and the
	// acting threshold disagree.
	AlertAfterFailures int `toml:"AlertAfterFailures"` // Default: 2; -1 alerts on the first audit
}

// Enabled reports whether the periodic audit runs (default true).
func (s SelfCheck) Enabled() bool { return s.Enable == nil || *s.Enable }

// AutoDisableEnabled reports whether failing models are deregistered (default true).
func (s SelfCheck) AutoDisableEnabled() bool { return s.AutoDisable == nil || *s.AutoDisable }

// AutoRecoverEnabled reports whether recovered models are re-registered (default true).
func (s SelfCheck) AutoRecoverEnabled() bool { return s.AutoRecover == nil || *s.AutoRecover }

// AlertThreshold returns how many consecutive audits must agree before a
// problem is emailed.
//
// Unset (0) takes the default. A negative value means "alert on the first
// audit" — an operator who wants the old immediate behaviour needs a way to ask
// for it, and silently converting their setting into a two-audit delay would
// leave them believing they had turned the debounce off.
func (s SelfCheck) AlertThreshold() int {
	switch {
	case s.AlertAfterFailures < 0:
		return 1
	case s.AlertAfterFailures == 0:
		return 2
	default:
		return s.AlertAfterFailures
	}
}

// Interval is the audit period.
func (s SelfCheck) Interval() time.Duration {
	return time.Duration(s.IntervalMinutes) * time.Minute
}

// Alerts configures operational notifications to a provider-run webhook. The
// failures worth waking someone for are the ones that leave the daemon running
// while it earns nothing, so none of them surface as a crash.
type Alerts struct {
	Email                Email   `toml:"Email,omitempty"`      // SMTP delivery; independent of the webhook
	WebhookURL           string  `toml:"WebhookURL"`           // POST target; empty disables alerting
	CooldownMinutes      int     `toml:"CooldownMinutes"`      // Suppress repeats of the same event. Default: 15
	DisconnectAfterMin   int     `toml:"DisconnectAfterMin"`   // Alert after this long disconnected from Swan Inference. Default: 5
	ErrorRateThreshold   float64 `toml:"ErrorRateThreshold"`   // Alert when a model's failure ratio exceeds this. Default: 0.5
	ErrorRateMinRequests int     `toml:"ErrorRateMinRequests"` // Ignore the ratio below this many requests. Default: 10
}

// Email delivers alerts over SMTP. Most providers are one operator with one
// machine and no monitoring stack, so requiring a webhook receiver would leave
// alerting switched off for the people who need it most.
type Email struct {
	Host     string `toml:"Host"`     // SMTP server; empty disables email
	Port     int    `toml:"Port"`     // 587 (STARTTLS) or 465 (implicit TLS). Default: 587
	Username string `toml:"Username"` // SMTP auth user; omit for an unauthenticated relay
	// Password is the SMTP credential. For Gmail, Outlook, Yahoo and most
	// consumer providers this must be a generated app password — a login
	// password is rejected once 2FA is on. Prefer the SMTP_PASSWORD env var
	// over storing it here.
	Password string   `toml:"Password"`
	From     string   `toml:"From"` // Envelope sender. Defaults to Username
	To       []string `toml:"To"`   // Recipients
}

// Enabled reports whether email alerting is configured.
func (e Email) Enabled() bool {
	return strings.TrimSpace(e.Host) != "" && len(e.To) > 0
}

// ImplicitTLS reports whether to open the connection wrapped in TLS (port 465)
// rather than upgrading with STARTTLS (587 and most other ports).
func (e Email) ImplicitTLS() bool { return e.Port == 465 }

// Sender returns the envelope sender, falling back to the auth username.
func (e Email) Sender() string {
	if strings.TrimSpace(e.From) != "" {
		return e.From
	}
	return e.Username
}

// WebhookEnabled reports whether a webhook is configured.
func (a Alerts) WebhookEnabled() bool { return strings.TrimSpace(a.WebhookURL) != "" }

// Enabled reports whether any delivery transport is configured.
func (a Alerts) Enabled() bool { return a.WebhookEnabled() || a.Email.Enabled() }

// Log controls where the provider writes its log files and how they are rotated.
// Without rotation a long-running provider can fill its disk: an unreachable
// Swan Inference endpoint produces a steady stream of reconnect lines.
type Log struct {
	Dir        string `toml:"Dir"`        // Log directory; relative paths resolve against $CP_PATH. Default: $CP_PATH/logs
	Level      string `toml:"Level"`      // trace|debug|info|warn|error. Default: info
	MaxSizeMB  int    `toml:"MaxSizeMB"`  // Rotate a file once it exceeds this size. Default: 100
	MaxBackups int    `toml:"MaxBackups"` // Rotated files to keep per level. Default: 5
	MaxAgeDays int    `toml:"MaxAgeDays"` // Delete rotated files older than this. -1 disables the age limit. Default: 30
	Compress   *bool  `toml:"Compress"`   // gzip rotated files. Default: true
	Stdout     *bool  `toml:"Stdout"`     // Also write to stdout. Default: true
}

// CompressEnabled reports whether rotated files should be gzipped (default true).
func (l Log) CompressEnabled() bool { return l.Compress == nil || *l.Compress }

// StdoutEnabled reports whether logs also go to stdout (default true).
func (l Log) StdoutEnabled() bool { return l.Stdout == nil || *l.Stdout }

// MaxAge returns the retention in days for lumberjack, where 0 means "no limit".
func (l Log) MaxAge() int {
	if l.MaxAgeDays < 0 {
		return 0
	}
	return l.MaxAgeDays
}

// Inference is the Swan Inference marketplace configuration (default mode)
type Inference struct {
	Enable       bool     `toml:"Enable"`
	ServiceURL   string   `toml:"ServiceURL"`   // HTTP API URL (e.g., http://localhost:8080)
	WebSocketURL string   `toml:"WebSocketURL"` // WebSocket URL (e.g., wss://inference-ws.swanchain.io)
	ApiKey       string   `toml:"ApiKey"`       // Provider API key for authentication (sk-prov-*)
	Models       []string `toml:"Models"`       // Models this provider serves
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

func InitConfig(cpRepoPath string, standalone bool) error {
	// Before anything reads the environment: $CP_PATH/.env supplies secrets
	// that should not live in config.toml.
	if err := LoadEnvFile(cpRepoPath); err != nil {
		log.Printf("Warning: %v\n", err)
	}

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

	// Early versions created these world-readable. Repair rather than warn:
	// an operator has no reason to know their node key is exposed.
	if fixed := SecureRepo(cpRepoPath); len(fixed) > 0 {
		log.Printf("Tightened permissions on provider secrets: %s\n", strings.Join(fixed, ", "))
	}
	if root, inRepo := GitRepoContaining(cpRepoPath); inRepo {
		log.Printf("Warning: %s is inside the git working tree at %s.\n"+
			"  `git clean -xfd` removes ignored files, which would delete private_key and this node's identity.\n"+
			"  Move the provider repo outside any checkout, e.g. CP_PATH=~/.swan/computing\n", cpRepoPath, root)
	}

	applyLogDefaults(&config.Log, cpRepoPath)
	applyAlertDefaults(&config.Alerts)
	applySelfCheckDefaults(&config.SelfCheck)
	applyDashboardDefaults(&config.Dashboard)
	applyRequestLimitDefaults(&config.RequestLimits)

	// Validate MultiAddress format if provided (optional for Inference mode)
	if config.API.MultiAddress != "" {
		multiAddressSplit := strings.Split(config.API.MultiAddress, "/")
		if len(multiAddressSplit) < 5 {
			log.Printf("Warning: MultiAddress %s may be invalid. Expected format: /ip4/<IP>/tcp/<PORT>\n", config.API.MultiAddress)
		}
	}

	return nil
}

// applyAlertDefaults fills unset [Alerts] fields. Defaults are chosen so that
// enabling alerting needs only a WebhookURL.
func applyAlertDefaults(a *Alerts) {
	a.WebhookURL = strings.TrimSpace(a.WebhookURL)
	a.Email.Host = strings.TrimSpace(a.Email.Host)
	if a.Email.Port <= 0 {
		a.Email.Port = 587
	}
	// Keeping the password out of config.toml is the point: that file is often
	// world-readable and gets pasted into support threads.
	if env := strings.TrimSpace(os.Getenv("SMTP_PASSWORD")); env != "" {
		a.Email.Password = env
	}
	if a.CooldownMinutes <= 0 {
		a.CooldownMinutes = 15
	}
	if a.DisconnectAfterMin <= 0 {
		a.DisconnectAfterMin = 5
	}
	if a.ErrorRateThreshold <= 0 || a.ErrorRateThreshold > 1 {
		a.ErrorRateThreshold = 0.5
	}
	if a.ErrorRateMinRequests <= 0 {
		a.ErrorRateMinRequests = 10
	}
}

// applySelfCheckDefaults fills unset [SelfCheck] fields.
func applySelfCheckDefaults(s *SelfCheck) {
	if s.IntervalMinutes <= 0 {
		s.IntervalMinutes = 10
	}
	if s.FailuresBeforeDisable <= 0 {
		s.FailuresBeforeDisable = 2
	}
}

func applyRequestLimitDefaults(l *RequestLimits) {
	if l.RequestsPerSecond <= 0 {
		l.RequestsPerSecond = 100
	}
	if l.MaxConcurrent <= 0 {
		l.MaxConcurrent = 50
	}
}

// DefaultDashboardPort is the port the web UI listens on unless configured.
const DefaultDashboardPort = 3060

// applyDashboardDefaults fills unset [Dashboard] fields.
func applyDashboardDefaults(d *Dashboard) {
	if strings.TrimSpace(d.Host) == "" {
		d.Host = "127.0.0.1"
	}
	if d.Port <= 0 {
		d.Port = DefaultDashboardPort
	}
}

// DefaultLog returns the [Log] defaults for a repo, for use before config.toml
// has been read — early startup logging still lands under the repo rather than
// in whatever directory the process happened to start in.
func DefaultLog(cpRepoPath string) Log {
	var l Log
	applyLogDefaults(&l, cpRepoPath)
	return l
}

// applyLogDefaults fills unset [Log] fields and resolves Dir to an absolute path
// under cpRepoPath, so the log location never depends on the process's cwd.
func applyLogDefaults(l *Log, cpRepoPath string) {
	if strings.TrimSpace(l.Dir) == "" {
		l.Dir = filepath.Join(cpRepoPath, "logs")
	} else if !filepath.IsAbs(l.Dir) {
		l.Dir = filepath.Join(cpRepoPath, l.Dir)
	}
	if strings.TrimSpace(l.Level) == "" {
		l.Level = "info"
	}
	if l.MaxSizeMB <= 0 {
		l.MaxSizeMB = 100
	}
	if l.MaxBackups <= 0 {
		l.MaxBackups = 5
	}
	if l.MaxAgeDays == 0 {
		l.MaxAgeDays = 30
	}
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
	}, SecretFileMode); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	file, err := os.Create(path.Join(cpRepoPath, "provider.db"))
	if err != nil {
		return err
	}
	defer file.Close()

	if err = os.MkdirAll(path.Join(cpRepoPath, "keystore"), SecretDirMode); err != nil {
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
			Enable:       true,
			ServiceURL:   build.DefaultInferenceURL,
			WebSocketURL: build.DefaultInferenceWSURL,
			Models:       []string{},
		},
		Log: Log{
			Level:      "info",
			MaxSizeMB:  100,
			MaxBackups: 5,
			MaxAgeDays: 30,
		},
		RequestLimits: RequestLimits{
			RequestsPerSecond: 100,
			MaxConcurrent:     50,
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
	Container     string `json:"container,omitempty"`
	Endpoint      string `json:"endpoint"`
	GPUMemory     int    `json:"gpu_memory"`
	Category      string `json:"category"`
	LocalModel    string `json:"local_model,omitempty"`
	Format        string `json:"format,omitempty"`
	Quantization  string `json:"quantization,omitempty"`
	APIKey        string `json:"api_key,omitempty"`
	ContextLength int    `json:"context_length,omitempty"` // Manual override for the backend's real context window (tokens)
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
	configTmpl.Inference.ServiceURL = build.DefaultInferenceURL
	configTmpl.Inference.WebSocketURL = build.DefaultInferenceWSURL
	if apiKey != "" {
		configTmpl.Inference.ApiKey = apiKey
	}
	if models != nil {
		configTmpl.Inference.Models = models
	}

	// Atomic write
	return atomicWriteFile(configFilePath, func(w io.Writer) error {
		return toml.NewEncoder(w).Encode(configTmpl)
	}, SecretFileMode)
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
	}, SecretFileMode)
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
