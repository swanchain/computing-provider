package computing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	modelPriceCacheTTL = 5 * time.Minute
	modelPriceRetryTTL = 30 * time.Second
)

// ModelPrice is the marketplace price and provider payout for one million
// tokens. Provider prices are the operator-facing rates shown in the dashboard.
type ModelPrice struct {
	InputPrice          float64 `json:"input_price"`
	OutputPrice         float64 `json:"output_price"`
	ProviderInputPrice  float64 `json:"provider_input_price"`
	ProviderOutputPrice float64 `json:"provider_output_price"`
	Tier                string  `json:"tier,omitempty"`
	Unit                string  `json:"unit"`
}

type modelPriceAPIResponse struct {
	Code int `json:"code"`
	Data struct {
		Models []struct {
			ModelID             string   `json:"model_id"`
			InputPrice          float64  `json:"input_price"`
			OutputPrice         float64  `json:"output_price"`
			ProviderInputPrice  *float64 `json:"provider_input_price"`
			ProviderOutputPrice *float64 `json:"provider_output_price"`
			Tier                string   `json:"tier"`
		} `json:"models"`
	} `json:"data"`
}

// ModelPriceCatalog caches the public marketplace catalog so the dashboard's
// five-second polling does not produce five-second upstream polling.
type ModelPriceCatalog struct {
	endpoint string
	client   *http.Client

	mu          sync.Mutex
	prices      map[string]ModelPrice
	nextRefresh time.Time
}

func NewModelPriceCatalog(serviceURL string) *ModelPriceCatalog {
	return &ModelPriceCatalog{
		endpoint: strings.TrimRight(serviceURL, "/") + "/api/v1/stats/model-demand",
		client:   &http.Client{Timeout: 5 * time.Second},
		prices:   make(map[string]ModelPrice),
	}
}

// Prices returns prices for the requested model IDs. A catalog refresh failure
// returns the last successful snapshot (if any) together with the error.
func (c *ModelPriceCatalog) Prices(ctx context.Context, modelIDs []string) (map[string]ModelPrice, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var refreshErr error
	if time.Now().After(c.nextRefresh) {
		refreshErr = c.refresh(ctx)
	}

	result := make(map[string]ModelPrice, len(modelIDs))
	for _, modelID := range modelIDs {
		if price, ok := c.prices[modelID]; ok {
			result[modelID] = price
		}
	}
	return result, refreshErr
}

func (c *ModelPriceCatalog) Price(ctx context.Context, modelID string) (ModelPrice, bool, error) {
	prices, err := c.Prices(ctx, []string{modelID})
	price, ok := prices[modelID]
	return price, ok, err
}

// refresh is called with c.mu held.
func (c *ModelPriceCatalog) refresh(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		c.nextRefresh = time.Now().Add(modelPriceRetryTTL)
		return err
	}

	response, err := c.client.Do(request)
	if err != nil {
		c.nextRefresh = time.Now().Add(modelPriceRetryTTL)
		return fmt.Errorf("fetch model prices: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		c.nextRefresh = time.Now().Add(modelPriceRetryTTL)
		return fmt.Errorf("fetch model prices: HTTP %d", response.StatusCode)
	}

	var payload modelPriceAPIResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		c.nextRefresh = time.Now().Add(modelPriceRetryTTL)
		return fmt.Errorf("decode model prices: %w", err)
	}
	if payload.Code != 0 {
		c.nextRefresh = time.Now().Add(modelPriceRetryTTL)
		return fmt.Errorf("fetch model prices: API code %d", payload.Code)
	}

	prices := make(map[string]ModelPrice, len(payload.Data.Models))
	for _, entry := range payload.Data.Models {
		providerInput := entry.InputPrice
		if entry.ProviderInputPrice != nil {
			providerInput = *entry.ProviderInputPrice
		}
		providerOutput := entry.OutputPrice
		if entry.ProviderOutputPrice != nil {
			providerOutput = *entry.ProviderOutputPrice
		}
		prices[entry.ModelID] = ModelPrice{
			InputPrice:          entry.InputPrice,
			OutputPrice:         entry.OutputPrice,
			ProviderInputPrice:  providerInput,
			ProviderOutputPrice: providerOutput,
			Tier:                entry.Tier,
			Unit:                "USD per 1M tokens",
		}
	}

	c.prices = prices
	c.nextRefresh = time.Now().Add(modelPriceCacheTTL)
	return nil
}
