package computing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"
)

// The platform's own records of this provider, read with the provider key.
// These are the figures the platform settles and routes on; the node's local
// counters only ever describe the current process and what it priced itself.

// PlatformEarningsModel is one model's share of a platform earnings bucket.
type PlatformEarningsModel struct {
	USD             float64 `json:"usd"`
	PayAsYouGoUSD   float64 `json:"pay_as_you_go_usd"`
	SubscriptionUSD float64 `json:"subscription_usd"`
	TokensIn        int64   `json:"tokens_in"`
	TokensOut       int64   `json:"tokens_out"`
	Requests        int64   `json:"requests"`
}

// PlatformEarningsPoint is one bucket of the platform's earnings history.
type PlatformEarningsPoint struct {
	Timestamp       time.Time                         `json:"timestamp"`
	USD             float64                           `json:"usd"`
	PayAsYouGoUSD   float64                           `json:"pay_as_you_go_usd"`
	SubscriptionUSD float64                           `json:"subscription_usd"`
	TokensIn        int64                             `json:"tokens_in"`
	TokensOut       int64                             `json:"tokens_out"`
	Requests        int64                             `json:"requests"`
	Models          map[string]*PlatformEarningsModel `json:"models"`
}

// SubscriptionProRate is a billing period's subscription settlement ratio, as
// the platform reports it for this provider.
type SubscriptionProRate struct {
	Ratio      float64 `json:"ratio"`
	Settled    bool    `json:"settled"`
	Determined bool    `json:"determined"`
}

// PlatformEarningsHistory is GET /provider/me/earnings/history.
type PlatformEarningsHistory struct {
	Points               []PlatformEarningsPoint        `json:"points"`
	Currency             string                         `json:"currency"`
	BucketSeconds        int64                          `json:"bucket_seconds"`
	DurationSeconds      int64                          `json:"duration_seconds"`
	From                 time.Time                      `json:"from"`
	To                   time.Time                      `json:"to"`
	TotalUSD             float64                        `json:"total_usd"`
	TotalPayAsYouGoUSD   float64                        `json:"total_pay_as_you_go_usd"`
	TotalSubscriptionUSD float64                        `json:"total_subscription_usd"`
	SubscriptionProRate  map[string]SubscriptionProRate `json:"subscription_pro_rate"`
}

// PlatformModel is the platform's view of one model this provider offers or
// has offered, from GET /providers/:id/models/details.
type PlatformModel struct {
	ModelID            string     `json:"model_id"`
	ModelName          string     `json:"model_name"`
	ContextLength      int        `json:"context_length"`
	ContextSource      string     `json:"context_source"`
	InputPrice         float64    `json:"input_price"`
	OutputPrice        float64    `json:"output_price"`
	Capacity           int        `json:"capacity"`
	Available          int        `json:"available"`
	TotalRequests      int64      `json:"total_requests"`
	TotalTokens        int64      `json:"total_tokens"`
	Throughput         float64    `json:"throughput_tok_per_s"`
	OfferingSource     string     `json:"offering_source,omitempty"`
	PlanCovered        bool       `json:"plan_covered,omitempty"`
	RecentSuccessCount int64      `json:"recent_success_count"`
	RecentFailureCount int64      `json:"recent_failure_count"`
	DispatchFailures   int64      `json:"dispatch_failures,omitempty"`
	SuccessCount       int64      `json:"success_count"`
	FailureCount       int64      `json:"failure_count"`
	LastRequestAt      *time.Time `json:"last_request_at,omitempty"`
	// Offered is whether the node is advertising this model now. False rows
	// are history: served before, not routable today.
	Offered bool `json:"offered"`
}

// timedEntry is a cached platform response.
type timedEntry struct {
	value       any
	err         error
	nextRefresh time.Time
}

// platformCache holds platform responses by key with the same policy as
// Stats: refresh on the TTL, and prefer a stale value over an error.
type platformCache struct {
	mu      sync.Mutex
	entries map[string]*timedEntry
}

func (pc *platformCache) get(key string, fetch func() (any, error)) (any, error) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.entries == nil {
		pc.entries = make(map[string]*timedEntry)
	}
	e := pc.entries[key]
	if e != nil && time.Now().Before(e.nextRefresh) {
		if e.value != nil {
			return e.value, nil
		}
		return nil, e.err
	}
	v, err := fetch()
	if err != nil {
		if e == nil {
			e = &timedEntry{}
			pc.entries[key] = e
		}
		e.err = err
		e.nextRefresh = time.Now().Add(providerStatsRetryTTL)
		if e.value != nil {
			return e.value, nil // Stale beats blank.
		}
		return nil, err
	}
	pc.entries[key] = &timedEntry{value: v, nextRefresh: time.Now().Add(providerStatsTTL)}
	return v, nil
}

// getJSON fetches a platform path with the provider key and decodes it,
// unwrapping a {"data": ...} envelope when there is one.
func (c *ProviderStatsClient) getJSON(ctx context.Context, path string, out any) error {
	if c == nil || c.apiKey == "" {
		return fmt.Errorf("no provider API key configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", path, resp.Status)
	}

	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return err
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) == nil && len(envelope.Data) > 0 && envelope.Data[0] == '{' {
		raw = envelope.Data
	}
	return json.Unmarshal(raw, out)
}

// EarningsHistory returns the platform's bucketed earnings for this provider.
// duration and bucket use the platform's own spans: "24h", "7d", "30d", "1h",
// "1d".
func (c *ProviderStatsClient) EarningsHistory(ctx context.Context, duration, bucket string) (*PlatformEarningsHistory, error) {
	if c == nil {
		return nil, fmt.Errorf("no provider API key configured")
	}
	v, err := c.platform.get("earnings:"+duration+":"+bucket, func() (any, error) {
		q := url.Values{"duration": {duration}, "bucket": {bucket}}
		var h PlatformEarningsHistory
		if err := c.getJSON(ctx, "/provider/me/earnings/history?"+q.Encode(), &h); err != nil {
			return nil, err
		}
		return &h, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*PlatformEarningsHistory), nil
}

// providerID is this key's provider ID on the platform. It never changes for a
// key, so once known it is kept for the life of the process.
func (c *ProviderStatsClient) providerID(ctx context.Context) (string, error) {
	c.idMu.Lock()
	defer c.idMu.Unlock()
	if c.id != "" {
		return c.id, nil
	}
	var me struct {
		ID string `json:"id"`
	}
	if err := c.getJSON(ctx, "/provider/me", &me); err != nil {
		return "", err
	}
	if me.ID == "" {
		return "", fmt.Errorf("/provider/me returned no provider ID")
	}
	c.id = me.ID
	return c.id, nil
}

// Models returns the platform's per-model view of this provider: what it
// holds as offered, and how each offering has served.
func (c *ProviderStatsClient) Models(ctx context.Context) ([]PlatformModel, error) {
	if c == nil {
		return nil, fmt.Errorf("no provider API key configured")
	}
	v, err := c.platform.get("models", func() (any, error) {
		id, err := c.providerID(ctx)
		if err != nil {
			return nil, err
		}
		var body struct {
			Models []PlatformModel `json:"models"`
		}
		if err := c.getJSON(ctx, "/providers/"+url.PathEscape(id)+"/models/details", &body); err != nil {
			return nil, err
		}
		return body.Models, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]PlatformModel), nil
}

// PlatformSeries converts the platform's earnings history into the series the
// dashboard's chart draws. Every point is authoritative, and so is its split
// by model: both come from the platform's settlement records, not from local
// token counts priced at published rates.
func PlatformSeries(h *PlatformEarningsHistory, duration string) EarningsSeries {
	s := EarningsSeries{
		Points:                  make([]EarningsPoint, 0, len(h.Points)),
		TotalUSD:                h.TotalUSD,
		Currency:                h.Currency,
		Duration:                duration,
		AuthoritativePoints:     len(h.Points),
		BucketSeconds:           int(h.BucketSeconds),
		Source:                  EarningsSourcePlatform,
		PayAsYouGoUSD:           h.TotalPayAsYouGoUSD,
		SubscriptionUSD:         h.TotalSubscriptionUSD,
		SubscriptionProRate:     h.SubscriptionProRate,
		ModelSplitAuthoritative: true,
	}
	if s.Currency == "" {
		s.Currency = "USD"
	}
	for _, p := range h.Points {
		ep := EarningsPoint{
			Timestamp:       p.Timestamp,
			TokensIn:        p.TokensIn,
			TokensOut:       p.TokensOut,
			USD:             p.USD,
			Authoritative:   true,
			PayAsYouGoUSD:   p.PayAsYouGoUSD,
			SubscriptionUSD: p.SubscriptionUSD,
			Requests:        p.Requests,
		}
		if len(p.Models) > 0 {
			ep.Models = make(map[string]ModelEarningsPoint, len(p.Models))
			var attributed float64
			for id, m := range p.Models {
				if m == nil {
					continue
				}
				ep.Models[id] = ModelEarningsPoint{TokensIn: m.TokensIn, TokensOut: m.TokensOut, USD: m.USD}
				attributed += m.USD
			}
			if rest := p.USD - attributed; rest > 0.000001 {
				ep.Unattributed = rest
			}
		}
		s.Points = append(s.Points, ep)
	}
	sort.Slice(s.Points, func(i, j int) bool { return s.Points[i].Timestamp.Before(s.Points[j].Timestamp) })
	return s
}

// PlatformSpans maps a dashboard window onto the platform's duration and
// bucket, using the same bucketing the local series uses: hourly up to a day,
// daily beyond it.
func PlatformSpans(window string) (duration, bucket string, ok bool) {
	switch window {
	case "24h":
		return "24h", "1h", true
	case "7d":
		return "7d", "1d", true
	case "30d":
		return "30d", "1d", true
	}
	return "", "", false
}

// HubModelView is the platform's model list set beside this node's, so a
// model that one side holds and the other does not is visible.
type HubModelView struct {
	Models []HubModelRow `json:"models"`
	// NotListed are models this node registers that the platform does not
	// hold as offered. Requests for them can still arrive over the
	// connection, but with no offering the work may not be credited.
	NotListed []string `json:"not_listed"`
	// Error is set when the platform could not be reached; Models is then
	// empty rather than a guess.
	Error string `json:"error,omitempty"`
}

// HubModelRow is one platform row plus whether this node registers it.
type HubModelRow struct {
	PlatformModel
	RegisteredLocally bool `json:"registered_locally"`
}

// BuildHubModelView merges the platform's rows with the node's registered
// models. Offered rows come first, then history, each by model ID.
func BuildHubModelView(platform []PlatformModel, registered []string) HubModelView {
	local := make(map[string]bool, len(registered))
	for _, m := range registered {
		local[m] = true
	}
	offered := make(map[string]bool, len(platform))
	view := HubModelView{Models: make([]HubModelRow, 0, len(platform)), NotListed: []string{}}
	for _, p := range platform {
		if p.Offered {
			offered[p.ModelID] = true
		}
		view.Models = append(view.Models, HubModelRow{PlatformModel: p, RegisteredLocally: local[p.ModelID]})
	}
	for _, m := range registered {
		if !offered[m] {
			view.NotListed = append(view.NotListed, m)
		}
	}
	sort.Strings(view.NotListed)
	sort.SliceStable(view.Models, func(i, j int) bool {
		a, b := view.Models[i], view.Models[j]
		if a.Offered != b.Offered {
			return a.Offered
		}
		return a.ModelID < b.ModelID
	})
	return view
}
