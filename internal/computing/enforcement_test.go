package computing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRateLimiterAllowModel(t *testing.T) {
	rl := NewRateLimiter(DefaultRateLimiterConfig(), nil)
	rl.SetModelLimit("m1", 1, 1) // 1 req/s, burst 1

	if !rl.AllowModel("m1") {
		t.Fatal("first request should be allowed")
	}
	if rl.AllowModel("m1") {
		t.Fatal("second request should be rate limited")
	}
	// Model without a specific limit only hits the global bucket
	if !rl.AllowModel("other-model") {
		t.Fatal("unlimited model should be allowed")
	}
}

func TestConcurrencyLimiterAcquireRelease(t *testing.T) {
	config := DefaultConcurrencyConfig()
	config.GlobalMaxConcurrent = 1
	config.AcquireTimeout = 50 * time.Millisecond
	cl := NewConcurrencyLimiter(config, nil)

	token, err := cl.Acquire(context.Background(), "m1")
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	if _, err := cl.Acquire(context.Background(), "m1"); !errors.Is(err, ErrGlobalConcurrencyLimit) {
		t.Fatalf("expected ErrGlobalConcurrencyLimit, got: %v", err)
	}

	token.Release()
	token.Release() // double release must be a no-op

	token2, err := cl.Acquire(context.Background(), "m1")
	if err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
	token2.Release()
}

func TestRetryPolicyExecute(t *testing.T) {
	config := DefaultRetryConfig()
	config.InitialDelay = time.Millisecond
	config.MaxDelay = 5 * time.Millisecond
	rp := NewRetryPolicy(config)

	// Transient failure succeeds after retries
	calls := 0
	err := rp.Execute(context.Background(), func() error {
		calls++
		if calls < 3 {
			return errors.New("connection refused")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}

	// Non-retryable error returns immediately
	calls = 0
	err = rp.Execute(context.Background(), func() error {
		calls++
		return errors.New("bad request")
	})
	if err == nil || calls != 1 {
		t.Fatalf("expected immediate non-retryable failure after 1 attempt, got err=%v calls=%d", err, calls)
	}
}

// TestHandleInferenceEnforcement drives the real request path end-to-end:
// rate limiting, concurrency limiting, and retry against a stub model server.
func TestHandleInferenceEnforcement(t *testing.T) {
	// Stub model server: fails with 502 on the first hit, then succeeds
	var hits int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&hits, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"error":{"message":"upstream 502"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"cmpl-1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer server.Close()

	s := NewInferenceService("test-node", t.TempDir())
	s.modelMappings["test/model"] = ModelMapping{Endpoint: server.URL}

	// Speed up retry for the test
	retryConfig := DefaultRetryConfig()
	retryConfig.InitialDelay = time.Millisecond
	s.retryPolicy = NewRetryPolicy(retryConfig)

	payload := InferencePayload{
		ModelID: "test/model",
		Request: json.RawMessage(`{"model":"test/model","messages":[]}`),
	}

	// Happy path: 502 is retried, second attempt succeeds
	resp, err := s.handleInference(payload)
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if resp == nil || len(resp.Response) == 0 {
		t.Fatal("expected non-empty response")
	}
	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Fatalf("expected 2 hits on model server (1 failure + 1 retry), got %d", got)
	}

	// Rate limited: zero-token bucket rejects with 429
	s.rateLimiter.SetModelLimit("test/model", 0, 0)
	_, err = s.handleInference(payload)
	var modelErr *ModelServerError
	if !errors.As(err, &modelErr) || modelErr.StatusCode != 429 {
		t.Fatalf("expected 429 ModelServerError, got: %v", err)
	}

	// Concurrency limited: no free slots rejects with 429
	s.rateLimiter = nil // disable rate limit to reach the concurrency check
	concConfig := DefaultConcurrencyConfig()
	concConfig.GlobalMaxConcurrent = 0
	concConfig.AcquireTimeout = 20 * time.Millisecond
	s.concurrencyLimiter = NewConcurrencyLimiter(concConfig, nil)
	_, err = s.handleInference(payload)
	if !errors.As(err, &modelErr) || modelErr.StatusCode != 429 {
		t.Fatalf("expected 429 ModelServerError for concurrency, got: %v", err)
	}
}
