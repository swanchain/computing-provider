package computing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestPlatformSeriesIsAuthoritativeAndSplitByModel(t *testing.T) {
	t0 := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	h := &PlatformEarningsHistory{
		Currency: "USD", BucketSeconds: 86400, TotalUSD: 1.5,
		TotalPayAsYouGoUSD: 1.0, TotalSubscriptionUSD: 0.5,
		SubscriptionProRate: map[string]SubscriptionProRate{"2026-09": {Ratio: 0.5, Determined: true}},
		Points: []PlatformEarningsPoint{
			{Timestamp: t0.Add(24 * time.Hour), USD: 1.0, PayAsYouGoUSD: 1.0, Requests: 3,
				Models: map[string]*PlatformEarningsModel{"a": {USD: 0.75}, "b": {USD: 0.25}}},
			// A bucket the platform could split only partly.
			{Timestamp: t0, USD: 0.5, SubscriptionUSD: 0.5,
				Models: map[string]*PlatformEarningsModel{"a": {USD: 0.2}}},
		},
	}
	s := PlatformSeries(h, "7d")

	if s.Source != EarningsSourcePlatform || !s.ModelSplitAuthoritative {
		t.Errorf("source=%q split authoritative=%v", s.Source, s.ModelSplitAuthoritative)
	}
	if s.AuthoritativePoints != 2 || s.BucketSeconds != 86400 || s.TotalUSD != 1.5 {
		t.Errorf("series = %+v", s)
	}
	if !s.Points[0].Timestamp.Equal(t0) {
		t.Error("points must be oldest first, as the chart draws them")
	}
	for _, p := range s.Points {
		if !p.Authoritative {
			t.Errorf("point %v not marked authoritative", p.Timestamp)
		}
	}
	if got := s.Points[0].Unattributed; got < 0.2999 || got > 0.3001 {
		t.Errorf("unattributed = %v, want the 0.3 the split does not cover", got)
	}
	if s.Points[1].Unattributed != 0 {
		t.Errorf("a fully split bucket has unattributed %v", s.Points[1].Unattributed)
	}
	if s.PayAsYouGoUSD != 1.0 || s.SubscriptionUSD != 0.5 || s.SubscriptionProRate["2026-09"].Ratio != 0.5 {
		t.Errorf("billing split lost: %+v", s)
	}
}

func TestPlatformSpans(t *testing.T) {
	for window, want := range map[string][2]string{"24h": {"24h", "1h"}, "7d": {"7d", "1d"}, "30d": {"30d", "1d"}} {
		d, b, ok := PlatformSpans(window)
		if !ok || d != want[0] || b != want[1] {
			t.Errorf("%s -> %s/%s/%v", window, d, b, ok)
		}
	}
	if _, _, ok := PlatformSpans("90m"); ok {
		t.Error("a window the platform is not asked for must fall back to the local series")
	}
}

func TestBuildHubModelView(t *testing.T) {
	view := BuildHubModelView([]PlatformModel{
		{ModelID: "old/model", Offered: false},
		{ModelID: "b/served", Offered: true},
		{ModelID: "a/served", Offered: true},
	}, []string{"a/served", "b/served", "c/missing", "old/model"})

	var order []string
	for _, r := range view.Models {
		order = append(order, r.ModelID)
	}
	if !reflect.DeepEqual(order, []string{"a/served", "b/served", "old/model"}) {
		t.Errorf("order = %v, want offered first then history", order)
	}
	// A model the node registers but the platform holds only as history, or
	// not at all, gets no traffic either way.
	if !reflect.DeepEqual(view.NotListed, []string{"c/missing", "old/model"}) {
		t.Errorf("not listed = %v", view.NotListed)
	}
	if !view.Models[0].RegisteredLocally {
		t.Error("registered_locally not set")
	}
}

func TestPlatformClientResolvesIDAndCaches(t *testing.T) {
	var meCalls, detailCalls, fail atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-prov-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/provider/me":
			meCalls.Add(1)
			w.Write([]byte(`{"data":{"id":"prov-1","name":"n"}}`))
		case "/api/v1/providers/prov-1/models/details":
			detailCalls.Add(1)
			if fail.Load() == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.Write([]byte(`{"provider_id":"prov-1","models":[{"model_id":"m","offered":true,"capacity":1}]}`))
		case "/api/v1/provider/me/earnings/history":
			if r.URL.Query().Get("duration") != "7d" || r.URL.Query().Get("bucket") != "1d" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Write([]byte(`{"points":[{"timestamp":"2026-09-27T00:00:00Z","usd":0.5}],"total_usd":0.5,"bucket_seconds":86400}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewProviderStatsClient(srv.URL, "sk-prov-test")
	ctx := context.Background()

	models, err := c.Models(ctx)
	if err != nil || len(models) != 1 || models[0].ModelID != "m" || !models[0].Offered {
		t.Fatalf("models = %+v, err = %v", models, err)
	}
	if _, err := c.Models(ctx); err != nil {
		t.Fatal(err)
	}
	if detailCalls.Load() != 1 {
		t.Errorf("fetched details %d times within the TTL, want 1", detailCalls.Load())
	}

	// Once the TTL passes and the platform fails, the last good answer stands.
	c.platform.entries["models"].nextRefresh = time.Now().Add(-time.Second)
	fail.Store(1)
	if models, err := c.Models(ctx); err != nil || len(models) != 1 {
		t.Errorf("stale value not preferred over an error: %+v, %v", models, err)
	}
	if meCalls.Load() != 1 {
		t.Errorf("resolved the provider ID %d times, want once", meCalls.Load())
	}

	h, err := c.EarningsHistory(ctx, "7d", "1d")
	if err != nil || h.TotalUSD != 0.5 || len(h.Points) != 1 {
		t.Errorf("history = %+v, err = %v", h, err)
	}
}

func TestPlatformClientWithoutKey(t *testing.T) {
	c := NewProviderStatsClient("http://127.0.0.1:1", "")
	if _, err := c.EarningsHistory(context.Background(), "7d", "1d"); err == nil {
		t.Error("no key must be an error, not an empty history")
	}
}
