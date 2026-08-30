package computing

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRegisteredModelJSONDoesNotExposeAPIKey(t *testing.T) {
	payload, err := json.Marshal(RegisteredModel{ID: "private/model", APIKey: "super-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "super-secret") || strings.Contains(string(payload), "api_key") {
		t.Fatalf("registered model JSON exposed API key: %s", payload)
	}
}
