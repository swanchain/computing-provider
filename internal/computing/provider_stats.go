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
	// The platform's totals move slowly and the dashboard polls often, so the
	// figure is cached rather than fetched per request. A provider hammering
	// the API to redraw a number it already has is not a good citizen.
	providerStatsTTL      = 2 * time.Minute
	providerStatsRetryTTL = 30 * time.Second
)

// ProviderStats is the platform's own account of this provider. It is the
// authoritative record of what was served and what it earned; the node's local
// counters only ever cover the current process.
type ProviderStats struct {
	TotalInferences      int64 `json:"total_inferences"`
	SuccessfulInferences int64 `json:"successful_inferences"`
	FailedInferences     int64 `json:"failed_inferences"`
	TotalInputTokens     int64 `json:"total_input_tokens"`
	TotalOutputTokens    int64 `json:"total_output_tokens"`
	TotalTokens          int64 `json:"total_tokens"`
	// TotalEarningsUSDC is the field name the platform uses. USDC is a dollar
	// stablecoin, so it is presented as USD in the UI; the wire name is kept as
	// it is to stay honest about where the number came from.
	TotalEarningsUSDC float64 `json:"total_earnings_usdc"`
	UptimePercent7d   float64 `json:"uptime_7d_percent"`
	LastConnected     string  `json:"last_connected"`
}

// ProviderStatsClient fetches and caches the platform's figures.
type ProviderStatsClient struct {
	baseURL string
	apiKey  string
	client  *http.Client

	mu          sync.Mutex
	cached      *ProviderStats
	lastErr     error
	nextRefresh time.Time
}

func NewProviderStatsClient(baseURL, apiKey string) *ProviderStatsClient {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		base = "https://api.swanchain.io"
	}
	if !strings.Contains(base, "/api/") {
		base += "/api/v1"
	}
	return &ProviderStatsClient{
		baseURL: base,
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// Stats returns the platform's figures, or the last error if they cannot be
// reached. A stale cached value is preferred over nothing: an operator would
// rather see a number from two minutes ago than an empty panel.
func (c *ProviderStatsClient) Stats(ctx context.Context) (*ProviderStats, error) {
	if c == nil || c.apiKey == "" {
		return nil, fmt.Errorf("no provider API key configured")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Now().Before(c.nextRefresh) {
		if c.cached != nil {
			return c.cached, nil
		}
		return nil, c.lastErr
	}

	stats, err := c.fetch(ctx)
	if err != nil {
		c.lastErr = err
		c.nextRefresh = time.Now().Add(providerStatsRetryTTL)
		if c.cached != nil {
			return c.cached, nil // Stale beats blank.
		}
		return nil, err
	}
	c.cached = stats
	c.lastErr = nil
	c.nextRefresh = time.Now().Add(providerStatsTTL)
	return stats, nil
}

func (c *ProviderStatsClient) fetch(ctx context.Context) (*ProviderStats, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/provider/me/stats", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider stats returned %s", resp.Status)
	}

	var envelope struct {
		Data *ProviderStats `json:"data"`
		ProviderStats
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	if envelope.Data != nil {
		return envelope.Data, nil
	}
	s := envelope.ProviderStats
	return &s, nil
}
