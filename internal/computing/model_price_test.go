package computing

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestModelPriceCatalogReturnsProviderPayoutAndCaches(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/api/v1/stats/model-demand" {
			t.Errorf("unexpected catalog path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"code":0,"data":{"models":[{"model_id":"openai/gpt-5.4","input_price":2,"output_price":12,"provider_input_price":1.8,"provider_output_price":10.8,"tier":"standard"}]}}`)
	}))
	defer server.Close()

	catalog := NewModelPriceCatalog(server.URL)
	for range 2 {
		price, ok, err := catalog.Price(context.Background(), "openai/gpt-5.4")
		if err != nil {
			t.Fatalf("get price: %v", err)
		}
		if !ok {
			t.Fatal("expected model price")
		}
		if price.ProviderInputPrice != 1.8 || price.ProviderOutputPrice != 10.8 {
			t.Fatalf("unexpected provider prices: %#v", price)
		}
		if price.Unit != "USD per 1M tokens" {
			t.Fatalf("unexpected unit %q", price.Unit)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("expected one upstream request, got %d", requests.Load())
	}
}

func TestModelPriceCatalogFallsBackToMarketplacePrices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"code":0,"data":{"models":[{"model_id":"legacy/model","input_price":0.5,"output_price":1.5}]}}`)
	}))
	defer server.Close()

	price, ok, err := NewModelPriceCatalog(server.URL).Price(context.Background(), "legacy/model")
	if err != nil || !ok {
		t.Fatalf("get fallback price: ok=%v err=%v", ok, err)
	}
	if price.ProviderInputPrice != 0.5 || price.ProviderOutputPrice != 1.5 {
		t.Fatalf("expected marketplace fallback, got %#v", price)
	}
}
