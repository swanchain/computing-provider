package computing

import (
	"encoding/json"
	"strings"
	"testing"
)

// An absent field must mean "unknown". Receivers that predate the field, and
// agents that cannot determine a window, have to keep reading identically —
// which is what lets this ship without a coordinated rollout.
func TestContextSourceOmittedWhenNotDetermined(t *testing.T) {
	for _, tc := range []struct {
		name string
		info ModelContextInfo
		want string
	}{
		{"detected", ModelContextInfo{Length: 32768, Source: ContextSourceDetected}, ContextSourceDetected},
		{"override", ModelContextInfo{Length: 128000, Source: ContextSourceOverride}, ContextSourceOverride},
		{"unknown is absent", ModelContextInfo{Length: 0, Source: ContextSourceUnknown}, ""},
		{"pending is absent", ModelContextInfo{Length: 0, Source: ContextSourcePending}, ""},
		// A source without a length has nothing whose provenance could matter.
		{"source without length is absent", ModelContextInfo{Length: 0, Source: ContextSourceDetected}, ""},
	} {
		if got := declaredContextSource(tc.info); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The JSON must actually omit the key, not send an empty string — "" is a
// present field and a receiver would have to special-case it.
func TestContextSourceAbsentFromJSONWhenUnknown(t *testing.T) {
	unknown, err := json.Marshal(ModelInfo{ModelID: "m", ContextLength: 0})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unknown), "context_source") {
		t.Errorf("unknown window emitted the key: %s", unknown)
	}

	known, err := json.Marshal(ModelInfo{ModelID: "m", ContextLength: 32768, ContextSource: ContextSourceDetected})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(known), `"context_source":"detected"`) {
		t.Errorf("detected window did not carry its provenance: %s", known)
	}
}

func TestHeartbeatMetadataCarriesProvenance(t *testing.T) {
	c := &InferenceClient{models: []string{"detected-model", "override-model", "unknown-model"}}
	c.SetModelContextsProvider(func() map[string]ModelContextInfo {
		return map[string]ModelContextInfo{
			"detected-model": {Length: 32768, Source: ContextSourceDetected},
			"override-model": {Length: 128000, Source: ContextSourceOverride},
			// unknown-model deliberately absent
		}
	})

	byID := map[string]ModelInfo{}
	for _, info := range c.buildModelMetadata() {
		byID[info.ModelID] = info
	}

	if got := byID["detected-model"]; got.ContextLength != 32768 || got.ContextSource != ContextSourceDetected {
		t.Errorf("detected model = %+v", got)
	}
	if got := byID["override-model"]; got.ContextLength != 128000 || got.ContextSource != ContextSourceOverride {
		t.Errorf("override model = %+v", got)
	}
	if _, ok := byID["unknown-model"]; ok {
		t.Error("a model with no determined window should not be declared at all")
	}
}

func TestDeclarationCarriesProvenance(t *testing.T) {
	c := &InferenceClient{models: []string{"detected-model", "unknown-model"}}
	c.SetModelContextsProvider(func() map[string]ModelContextInfo {
		return map[string]ModelContextInfo{
			"detected-model": {Length: 32768, Source: ContextSourceDetected},
		}
	})
	c.SetModelMappingsProvider(func() map[string]ModelMapping {
		return map[string]ModelMapping{
			"detected-model": {Category: "text-generation"},
			"unknown-model":  {Category: "text-generation"},
		}
	})

	byID := map[string]ModelDeclaration{}
	for _, d := range c.buildModelDeclarations() {
		byID[d.ModelID] = d
	}

	if got := byID["detected-model"].ContextSource; got != ContextSourceDetected {
		t.Errorf("declaration source = %q, want %q", got, ContextSourceDetected)
	}
	if got := byID["unknown-model"].ContextSource; got != "" {
		t.Errorf("undetermined window declared a source %q, want none", got)
	}

	// And the same must hold once serialised.
	raw, err := json.Marshal(byID["unknown-model"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "context_source") {
		t.Errorf("undetermined declaration emitted the key: %s", raw)
	}
}
