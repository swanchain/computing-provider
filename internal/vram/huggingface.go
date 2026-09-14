package vram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultHubURL is the public HuggingFace host.
const DefaultHubURL = "https://huggingface.co"

// Hub reads model metadata from a HuggingFace-compatible host.
//
// Two endpoints, and the difference between them matters: the API summary
// answers for gated repositories without a token, while fetching config.json
// from a gated repository returns 401. So a gated model yields a weight size
// and no cache size unless the operator supplies a token.
type Hub struct {
	// BaseURL is the host. Empty means DefaultHubURL.
	BaseURL string

	// Token is an optional HuggingFace access token, needed only to read
	// config.json from gated repositories.
	Token string

	// Client is the HTTP client. Nil builds one with a sane timeout.
	Client *http.Client
}

func (h *Hub) baseURL() string {
	if h.BaseURL != "" {
		return strings.TrimRight(h.BaseURL, "/")
	}
	return DefaultHubURL
}

func (h *Hub) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (h *Hub) get(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}

	resp, err := h.client().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	// Capped so a misrouted request cannot pull a weights file into memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// SafetensorsSummary is the parameter breakdown the hub publishes.
type SafetensorsSummary struct {
	Parameters map[string]int64 `json:"parameters"`
	Total      int64            `json:"total"`
}

// TotalParams returns the parameter count, summing the per-dtype breakdown
// rather than trusting the published total.
//
// The published total is not reliable. TheDrummer/Cydonia-24B-v4.3 reports
// parameters {BF16: 23,572,403,200} alongside a total of 414,720 — five orders
// of magnitude out. Taking that total sized a 24B model at 0.0 GiB and reported
// it as comfortably fitting, which is the under-estimate this whole package
// exists to avoid. The breakdown is what the hub computes from the actual
// tensor headers; the total is a separate field that can be stale or wrong.
func (s *SafetensorsSummary) TotalParams() int64 {
	var sum int64
	for _, count := range s.Parameters {
		if count > 0 {
			sum += count
		}
	}
	if sum > 0 {
		return sum
	}
	return s.Total
}

// Parameters fetches a model's parameter counts by dtype.
//
// This works for gated repositories without a token, which is the reason it is
// preferred over reading the safetensors index directly.
func (h *Hub) Parameters(ctx context.Context, modelID string) (*SafetensorsSummary, error) {
	url := fmt.Sprintf("%s/api/models/%s?expand=safetensors", h.baseURL(), modelID)
	body, status, err := h.get(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("could not reach the model hub: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("model hub returned HTTP %d for %s", status, modelID)
	}

	var payload struct {
		Safetensors *SafetensorsSummary `json:"safetensors"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("model hub returned an unreadable summary: %w", err)
	}
	if payload.Safetensors == nil || payload.Safetensors.TotalParams() == 0 {
		// A repository with no safetensors weights — a GGUF-only repo, most
		// often. Reported plainly so the caller can try another route rather
		// than treating it as a transport failure.
		return nil, fmt.Errorf("%s publishes no safetensors parameter counts", modelID)
	}
	return payload.Safetensors, nil
}

// Config fetches and parses a model's config.json.
func (h *Hub) Config(ctx context.Context, modelID string) (*ModelConfig, error) {
	url := fmt.Sprintf("%s/%s/resolve/main/config.json", h.baseURL(), modelID)
	body, status, err := h.get(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("could not reach the model hub: %w", err)
	}
	switch status {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		// Named precisely, because the fix is specific and the operator
		// cannot guess it: the API summary answered for this same model, so
		// "not found" or "unreachable" would send them looking elsewhere.
		return nil, fmt.Errorf("%s is gated; set a HuggingFace token to read its config", modelID)
	case http.StatusNotFound:
		return nil, fmt.Errorf("%s publishes no config.json", modelID)
	default:
		return nil, fmt.Errorf("model hub returned HTTP %d for %s config.json", status, modelID)
	}
	return ParseModelConfig(body)
}

// Derive works out a model's VRAM requirement from what the hub publishes.
//
// Weights and cache are fetched independently on purpose. A gated repository
// gives up its parameter counts but not its config, and a weight size alone is
// still worth having: it rules out the models that cannot fit on weights
// alone, which on a small node is most of the ones worth ruling out.
func Derive(ctx context.Context, hub *Hub, modelID string, plan Plan) *Estimate {
	if hub == nil {
		hub = &Hub{}
	}

	params, err := hub.Parameters(ctx, modelID)
	if err != nil {
		return unknown(modelID, err.Error())
	}

	// Weights and cache are fetched independently on purpose. A gated
	// repository gives up its parameter counts but not its config, and a
	// weight size alone is still worth having: it rules out the models that
	// cannot fit on weights alone.
	cfg, cfgErr := hub.Config(ctx, modelID)

	est := estimateFrom(modelID, params, cfg, cfgErr, plan)
	if est.Known() && est.SafetyFactor != 1 {
		est.Notes = append(est.Notes, fmt.Sprintf(
			"padded by %.0f%% because this is derived from published metadata, not measured here",
			(est.SafetyFactor-1)*100))
	}
	return est
}
