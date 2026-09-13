package market

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// DemandEntry is one model as /api/v1/stats/model-demand describes it.
//
// Every field the endpoint publishes is carried, not just the ones the table
// prints, because the planner reads fields no human column shows —
// subscription share, plan coverage, prompt-size percentiles. One struct and
// one fetch, so the display and the auto-switch guardrails cannot end up
// reading different numbers for the same model.
type DemandEntry struct {
	ModelID   string `json:"model_id"`
	ModelName string `json:"model_name"`
	Category  string `json:"category"`
	Tier      string `json:"tier"`

	// Customer-facing rates.
	InputPrice  float64 `json:"input_price"`
	OutputPrice float64 `json:"output_price"`

	// What a provider is actually paid. These are the ones a node's own
	// earnings follow, and they are not the customer-facing rates.
	ProviderInputPrice  float64 `json:"provider_input_price"`
	ProviderOutputPrice float64 `json:"provider_output_price"`

	OnlineProviders int     `json:"online_providers"`
	Requests24h     int     `json:"requests_24h"`
	Tokens24h       int64   `json:"tokens_24h"`
	Revenue24h      float64 `json:"revenue_24h"`
	AvgLatencyMs    float64 `json:"avg_latency_ms"`

	DemandTrend     string  `json:"demand_trend"`
	DemandChangePct float64 `json:"demand_change_pct"`

	MinVRAMGB int `json:"min_vram_gb"`
	// VRAMKnown says whether MinVRAMGB means anything. False means the
	// requirement has never been established, and MinVRAMGB arrives as 0.
	VRAMKnown bool `json:"vram_known"`

	EstDailyEarnings        float64 `json:"est_daily_earnings"`
	EstEntrantDailyEarnings float64 `json:"est_entrant_daily_earnings"`
	EntrantBasis            string  `json:"entrant_basis"`

	PlanCovered       bool    `json:"plan_covered"`
	SubscriptionShare float64 `json:"subscription_share"`
	LongContextShare  float64 `json:"long_context_share"`

	ContextLength   int `json:"context_length"`
	PromptTokensP50 int `json:"prompt_tokens_p50"`
	PromptTokensP95 int `json:"prompt_tokens_p95"`
	PromptTokensMax int `json:"prompt_tokens_max"`
	TotalTokensP95  int `json:"total_tokens_p95"`
}

type demandResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Models []DemandEntry `json:"models"`
	} `json:"data"`
}

// FetchDemand reads the live model-demand table.
func FetchDemand(serviceURL, category string) ([]DemandEntry, error) {
	endpoint := serviceURL + "/api/v1/stats/model-demand"
	if category != "" {
		endpoint += "?category=" + url.QueryEscape(category)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch model demand data: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model-demand API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var parsed demandResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse model demand response: %w", err)
	}
	return parsed.Data.Models, nil
}

// Fit classifies an entry against a node's VRAM.
func (e DemandEntry) Fit(totalVRAMGB int) string {
	return VRAMFit(e.MinVRAMGB, e.VRAMKnown, totalVRAMGB)
}
