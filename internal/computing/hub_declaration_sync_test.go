package computing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The hub reads model_declarations in place of model_hashes whenever both are
// sent, so anything the declarations leave out never reaches it.

func writeManifest(t *testing.T, root, modelID, hash string) {
	t.Helper()
	dir := filepath.Join(root, modelID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"model_id": modelID, "composite_hash": hash, "algorithm": "sha256"})
	if err := os.WriteFile(filepath.Join(dir, ".swan-hash-manifest.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func declarationsByID(c *InferenceClient) map[string]ModelDeclaration {
	byID := map[string]ModelDeclaration{}
	for _, d := range c.buildModelDeclarations() {
		byID[d.ModelID] = d
	}
	return byID
}

func TestDeclarationCarriesWeightHash(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "org/hashed", "abc123def4567890abc123def4567890")
	c := &InferenceClient{models: []string{"org/hashed", "org/unhashed"}, modelsRoot: root}

	// Before registration has loaded anything, and after.
	for _, stage := range []string{"before register", "after register"} {
		byID := declarationsByID(c)
		if got := byID["org/hashed"]; got.WeightHash != "abc123def4567890abc123def4567890" || got.HashAlgo != "sha256" {
			t.Errorf("%s: declaration hash = %q/%q, want the manifest's", stage, got.WeightHash, got.HashAlgo)
		}
		if got := byID["org/unhashed"]; got.WeightHash != "" || got.HashAlgo != "" {
			t.Errorf("%s: a model with no manifest declared a hash %q", stage, got.WeightHash)
		}
		c.loadModelHashes()
	}
}

func TestDeclarationHashIsCachedUntilReregister(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "org/m", "first-hash-0000000000000000")
	c := &InferenceClient{models: []string{"org/m"}, modelsRoot: root}
	c.loadModelHashes()

	// Heartbeats must not re-read the manifest from disk every interval.
	writeManifest(t, root, "org/m", "second-hash-000000000000000")
	if got := declarationsByID(c)["org/m"].WeightHash; got != "first-hash-0000000000000000" {
		t.Errorf("heartbeat declaration = %q, want the hash loaded at registration", got)
	}
	// A new registration picks the change up.
	c.loadModelHashes()
	if got := declarationsByID(c)["org/m"].WeightHash; got != "second-hash-000000000000000" {
		t.Errorf("after re-register = %q, want the new manifest's hash", got)
	}
}

func TestDeclarationCarriesConcurrencyCapacity(t *testing.T) {
	c := &InferenceClient{models: []string{"org/limited", "org/unknown"}, modelsRoot: t.TempDir()}

	if caps := declarationsByID(c)["org/limited"].Capacity; caps != nil {
		t.Errorf("no capacity provider: declared %+v, want nothing", caps)
	}

	c.SetModelCapacityProvider(func(id string) int {
		if id == "org/limited" {
			return 2
		}
		return 0
	})
	byID := declarationsByID(c)
	want := []DeclaredCapacity{{Type: "concurrency", Unit: "request", Value: 2}}
	if got := byID["org/limited"].Capacity; !reflect.DeepEqual(got, want) {
		t.Errorf("capacity = %+v, want %+v", got, want)
	}
	if got := byID["org/unknown"].Capacity; got != nil {
		t.Errorf("an unknown capacity must be omitted, not declared as 0: %+v", got)
	}
}

func TestModelCapacityFollowsLimiter(t *testing.T) {
	cfg := DefaultConcurrencyConfig()
	cfg.EnableGPUAwareness = false
	cl := NewConcurrencyLimiter(cfg, nil)

	if got := cl.ModelCapacity("m"); got != cfg.DefaultModelMax {
		t.Errorf("unconfigured model = %d, want default %d", got, cfg.DefaultModelMax)
	}
	cl.SetModelMax("m", 2)
	if got := cl.ModelCapacity("m"); got != 2 {
		t.Errorf("operator limit = %d, want 2", got)
	}
	cl.SetModelMax("m", 40)
	cl.SetGlobalMax(8)
	if got := cl.ModelCapacity("m"); got != 8 {
		t.Errorf("capacity above the global limit = %d, want the global 8", got)
	}
}

func TestRegisterAckRecordsHubView(t *testing.T) {
	c := &InferenceClient{models: []string{"a/one", "a/two"}}

	// A heartbeat ack, or one from an older hub, carries neither field.
	c.recordHubView(AckPayload{Success: true})
	if c.HubRegisteredModels() != nil || c.UpgradeAvailable() != nil {
		t.Fatal("an ack with no hub view must not record one")
	}

	c.recordHubView(AckPayload{
		Success:          true,
		RegisteredModels: []string{"a/one", "a/other"},
		UpgradeAvailable: &UpgradeAdvisory{Running: "0.5.5", Latest: "0.6.0"},
	})
	if got := c.HubRegisteredModels(); !reflect.DeepEqual(got, []string{"a/one", "a/other"}) {
		t.Errorf("hub models = %v", got)
	}
	if adv := c.UpgradeAvailable(); adv == nil || adv.Latest != "0.6.0" {
		t.Errorf("upgrade advisory = %+v", adv)
	}

	// The heartbeat acks that follow must not erase it.
	c.recordHubView(AckPayload{Success: true})
	if c.HubRegisteredModels() == nil || c.UpgradeAvailable() == nil {
		t.Error("a later ack without the fields erased the hub view")
	}
}

func TestDiffModelLists(t *testing.T) {
	missing, extra := diffModelLists([]string{"a", "b", "c"}, []string{"b", "c", "d"})
	if !reflect.DeepEqual(missing, []string{"a"}) || !reflect.DeepEqual(extra, []string{"d"}) {
		t.Errorf("missing=%v extra=%v", missing, extra)
	}
	missing, extra = diffModelLists([]string{"a"}, []string{"a"})
	if missing != nil || extra != nil {
		t.Errorf("identical lists: missing=%v extra=%v", missing, extra)
	}
}

func TestAckPayloadDecodesHubFields(t *testing.T) {
	raw := `{"request_id":"r","success":true,"message":"ok",
		"upgrade_available":{"running":"0.5.5","latest":"0.6.0","message":"upgrade"},
		"registered_models":["x/y"]}`
	var ack AckPayload
	if err := json.Unmarshal([]byte(raw), &ack); err != nil {
		t.Fatal(err)
	}
	if ack.UpgradeAvailable == nil || ack.UpgradeAvailable.Latest != "0.6.0" || len(ack.RegisteredModels) != 1 {
		t.Errorf("decoded %+v", ack)
	}
}
