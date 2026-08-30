package conf

import (
	"testing"
	"time"
)

// An unset section must keep the built-in defaults: naming one knob should not
// silently reset the others.
func TestHealthCheckDefaultsWhenUnset(t *testing.T) {
	var h HealthCheck
	if got := h.DeepEvery(10); got != 10 {
		t.Errorf("DeepEvery = %d, want the default 10", got)
	}
	if got := h.DeepTimeout(30 * time.Second); got != 30*time.Second {
		t.Errorf("DeepTimeout = %v, want the default 30s", got)
	}
}

func TestHealthCheckOverrides(t *testing.T) {
	h := HealthCheck{DeepCheckEvery: 60, DeepCheckTimeout: 5}
	if got := h.DeepEvery(10); got != 60 {
		t.Errorf("DeepEvery = %d, want 60", got)
	}
	if got := h.DeepTimeout(30 * time.Second); got != 5*time.Second {
		t.Errorf("DeepTimeout = %v, want 5s", got)
	}
}

// 0 means "unset", so disabling needs a distinct value. Without -1 there would
// be no way to turn the engine probe off for a metered upstream.
func TestHealthCheckDisableIsDistinctFromUnset(t *testing.T) {
	if got := (HealthCheck{DeepCheckEvery: -1}).DeepEvery(10); got != 0 {
		t.Errorf("DeepEvery(-1) = %d, want 0 (disabled)", got)
	}
	if got := (HealthCheck{DeepCheckEvery: 0}).DeepEvery(10); got != 10 {
		t.Errorf("DeepEvery(0) = %d, want the default (unset, not disabled)", got)
	}
}
