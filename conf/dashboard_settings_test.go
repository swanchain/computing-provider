package conf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func dashboardSettingsRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config := `[API]
Port = 8085
NodeName = "test-provider"

[Inference]
Enable = true
ApiKey = "sk-prov-must-stay-secret"
Models = ["org/model"]

[Alerts]
CooldownMinutes = 15
DisconnectAfterMin = 5
ErrorRateThreshold = 0.5
ErrorRateMinRequests = 10

[Alerts.Email]
Host = "smtp.example.com"
Port = 587
Username = "operator@example.com"
Password = "smtp-must-stay-secret"
From = "operator@example.com"
To = ["alerts@example.com"]

[SelfCheck]
IntervalMinutes = 10
FailuresBeforeDisable = 2

[Log]
Dir = "logs"
Level = "info"
MaxSizeMB = 100
MaxBackups = 5
MaxAgeDays = 30

[RequestLimits]
RequestsPerSecond = 100
MaxConcurrent = 50
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), SecretFileMode); err != nil {
		t.Fatal(err)
	}
	models := map[string]ModelConfig{
		"org/model": {
			Endpoint:      "http://127.0.0.1:8000",
			Category:      "text-generation",
			APIKey:        "endpoint-must-stay-secret",
			ContextLength: 8192,
		},
	}
	data, err := json.Marshal(models)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.json"), data, SecretFileMode); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadDashboardSettingsRedactsSecrets(t *testing.T) {
	dir := dashboardSettingsRepo(t)
	settings, err := LoadDashboardSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Alerts.Email.PasswordSet || settings.Alerts.Email.Password != "" {
		t.Fatalf("SMTP password state = set:%v value:%q", settings.Alerts.Email.PasswordSet, settings.Alerts.Email.Password)
	}
	if len(settings.Models) != 1 || !settings.Models[0].APIKeySet || settings.Models[0].APIKey != "" {
		t.Fatalf("model key was not redacted: %+v", settings.Models)
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"smtp-must-stay-secret", "endpoint-must-stay-secret", "sk-prov-must-stay-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("settings response contains secret %q", secret)
		}
	}
}

func TestUpdateAlertSettingsPreservesWriteOnlyPassword(t *testing.T) {
	dir := dashboardSettingsRepo(t)
	settings, err := LoadDashboardSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	settings.Alerts.CooldownMinutes = 30
	settings.Alerts.Email.Password = ""
	if err := UpdateAlertSettings(dir, settings.Alerts); err != nil {
		t.Fatal(err)
	}
	node, err := readComputeNode(dir)
	if err != nil {
		t.Fatal(err)
	}
	if node.Alerts.Email.Password != "smtp-must-stay-secret" {
		t.Fatal("blank write-only password replaced the stored password")
	}
	if node.Alerts.CooldownMinutes != 30 {
		t.Fatalf("cooldown = %d", node.Alerts.CooldownMinutes)
	}
}

func TestUpdateDashboardModelsPreservesEndpointKeyAndModelAgreement(t *testing.T) {
	dir := dashboardSettingsRepo(t)
	settings, err := LoadDashboardSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	settings.Models[0].Endpoint = "http://127.0.0.1:9000"
	settings.Models = append(settings.Models, DashboardModel{
		ID:       "org/second",
		Endpoint: "http://127.0.0.1:9001",
		Category: "text-generation",
	})
	if err := UpdateDashboardModels(dir, settings.Models); err != nil {
		t.Fatal(err)
	}
	models, err := LoadModelsJson(dir)
	if err != nil {
		t.Fatal(err)
	}
	if models["org/model"].APIKey != "endpoint-must-stay-secret" {
		t.Fatal("blank write-only endpoint key replaced the stored key")
	}
	if models["org/model"].Endpoint != "http://127.0.0.1:9000" {
		t.Fatalf("endpoint = %q", models["org/model"].Endpoint)
	}
	var node ComputeNode
	if _, err := toml.DecodeFile(filepath.Join(dir, "config.toml"), &node); err != nil {
		t.Fatal(err)
	}
	if strings.Join(node.Inference.Models, ",") != "org/model,org/second" {
		t.Fatalf("inference models = %v", node.Inference.Models)
	}
	if node.Inference.ApiKey != "sk-prov-must-stay-secret" {
		t.Fatal("updating model endpoints changed the provider API key")
	}
}

func TestDashboardSettingsValidationRejectsUnsafeValues(t *testing.T) {
	dir := dashboardSettingsRepo(t)
	settings, err := LoadDashboardSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	settings.Alerts.Email.Host = "https://smtp.example.com"
	if err := UpdateAlertSettings(dir, settings.Alerts); err == nil {
		t.Fatal("SMTP URL was accepted as a hostname")
	}
	if err := UpdateDashboardModels(dir, []DashboardModel{{
		ID: "org/model", Endpoint: "file:///tmp/socket", Category: "text-generation",
	}}); err == nil {
		t.Fatal("non-HTTP model endpoint was accepted")
	}
	if err := UpdateRequestLimitSettings(dir, RequestLimitSettings{RequestsPerSecond: 0, MaxConcurrent: 10}); err == nil {
		t.Fatal("zero request rate was accepted")
	}
}
