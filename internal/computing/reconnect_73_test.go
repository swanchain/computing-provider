package computing

import (
	"testing"
)

// #73: on 2026-08-28 the client lost its connection during an in-flight
// reconnect and never recovered. The reconnect request from the new
// connection's pump was dropped because a reconnect was "already in progress",
// and nothing ever asked again. These pin the request/pending semantics that
// replaced the drop.

func newIdleClient() *InferenceClient {
	return &InferenceClient{
		send:     make(chan []byte, 8),
		stopCh:   make(chan struct{}),
		pumpDone: make(chan struct{}),
		metrics:  NewInferenceMetrics(),
	}
}

// A request that arrives while a reconnect is in progress must be RECORDED,
// and honoured afterwards if it was for the connection that reconnect built.
func TestReconnectRequestDuringReconnectIsRecordedNotDropped(t *testing.T) {
	c := newIdleClient()
	c.reconnectMu.Lock()
	c.reconnecting = true
	c.reconnectMu.Unlock()
	c.mu.Lock()
	c.connGeneration = 7 // the generation the in-flight reconnect is producing
	c.mu.Unlock()

	// The new connection's pump reports it was lost.
	c.requestReconnect(7)

	if !c.takePendingReconnect(7) {
		t.Fatal("a reconnect requested for the connection being built must be honoured once that reconnect finishes — dropping it is #73")
	}
	if c.takePendingReconnect(7) {
		t.Error("taking the pending request must clear it; a second take must find nothing")
	}
}

// A request from the OLD connection — the one the in-flight reconnect is
// tearing down — must not cause a spurious second cycle once the new one is up.
func TestStaleReconnectRequestDoesNotTriggerASecondCycle(t *testing.T) {
	c := newIdleClient()
	c.reconnectMu.Lock()
	c.reconnecting = true
	c.reconnectMu.Unlock()
	c.mu.Lock()
	c.connGeneration = 7
	c.mu.Unlock()

	// A pump of generation 6 (the connection being replaced) finally errors out.
	c.requestReconnect(6)

	if c.takePendingReconnect(7) {
		t.Fatal("a request about the superseded connection must not make the reconnect go round again")
	}
}

// The newest request wins: if a stale request is followed by a live one, the
// live one is what gets honoured.
func TestLatestPendingGenerationWins(t *testing.T) {
	c := newIdleClient()
	c.reconnectMu.Lock()
	c.reconnecting = true
	c.reconnectMu.Unlock()

	c.requestReconnect(6) // stale
	c.requestReconnect(7) // the connection being built
	if !c.takePendingReconnect(7) {
		t.Fatal("the request for the current generation must be honoured even if a stale one preceded it")
	}
}

// Metrics must not flap when a stale pump reports the loss of a connection that
// has already been replaced by a live one.
func TestMarkDisconnectedIgnoresStaleGenerations(t *testing.T) {
	c := newIdleClient()
	c.mu.Lock()
	c.connGeneration = 3
	c.mu.Unlock()
	c.metrics.RecordConnectionState("connected")

	c.markDisconnected(2) // an old pump, late
	if got := c.metrics.GetSnapshot().ConnectionState; got != "connected" {
		t.Fatalf("a stale pump must not overwrite the live state, got %q", got)
	}

	c.markDisconnected(3) // the live connection really is gone
	if got := c.metrics.GetSnapshot().ConnectionState; got != "disconnected" {
		t.Fatalf("the current generation's loss must be recorded, got %q", got)
	}
}
