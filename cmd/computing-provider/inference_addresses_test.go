package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchProviderAddresses(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		_, _ = w.Write([]byte(`{"owner_address":"0xOwner","worker_address":"0xWorker","beneficiary_address":"0xBene"}`))
	}))
	defer srv.Close()

	addr, err := fetchProviderAddresses(srv.URL, "sk-prov-test")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotPath != "/api/v1/provider/me" {
		t.Errorf("path = %q, want /api/v1/provider/me", gotPath)
	}
	if gotAuth != "Bearer sk-prov-test" {
		t.Errorf("auth = %q, want the provider key", gotAuth)
	}
	if addr.OwnerAddress != "0xOwner" || addr.BeneficiaryAddress != "0xBene" || addr.WorkerAddress != "0xWorker" {
		t.Errorf("got %+v", addr)
	}
}

func TestFetchProviderAddressesSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	if _, err := fetchProviderAddresses(srv.URL, "bad"); err == nil {
		t.Fatal("a non-200 must be an error, not an empty address set")
	} else if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, want it to name the status", err)
	}
}

// The addresses must be additional top-level fields, never a nested object:
// anything already parsing `inference status --json` has to keep working.
func TestStatusJSONKeepsExistingKeysFlat(t *testing.T) {
	status := ProviderStatusResponse{ProviderID: "p1", Name: "n", Status: "active", APIKeyValid: true}
	addresses := &ProviderAddresses{OwnerAddress: "0xOwner", BeneficiaryAddress: "0xBene"}

	raw, err := json.Marshal(struct {
		ProviderStatusResponse
		*ProviderAddresses
	}{status, addresses})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"provider_id", "name", "status", "api_key_valid"} {
		if _, ok := got[key]; !ok {
			t.Errorf("existing key %q disappeared from the output", key)
		}
	}
	for _, key := range []string{"owner_address", "beneficiary_address"} {
		if _, ok := got[key]; !ok {
			t.Errorf("address key %q is not at the top level", key)
		}
	}
	if _, nested := got["status"].(map[string]any); nested {
		t.Error("status was nested under a key instead of staying flat")
	}
}
