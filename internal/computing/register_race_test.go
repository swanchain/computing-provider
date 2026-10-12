package computing

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Two health callbacks run updateClientModels concurrently. The first read the
// ready set before org/late turned healthy; if it publishes after the second,
// org/late is dropped from the registered list and stays dropped, since
// nothing changes afterwards to trigger another call.
func TestConcurrentRegistrationKeepsNewestReadySet(t *testing.T) {
	s := NewInferenceService("test-node", t.TempDir())
	s.registry = NewModelRegistry("", nil)
	s.registry.models["org/early"] = &RegisteredModel{ID: "org/early", Enabled: true, State: ModelStateReady, Health: ModelHealthHealthy}
	s.registry.models["org/late"] = &RegisteredModel{ID: "org/late", Enabled: true, State: ModelStateLoading, Health: ModelHealthUnknown}
	s.client = &InferenceClient{metrics: NewInferenceMetrics()}

	stalled := make(chan struct{})
	release := make(chan struct{})
	// Only the first call stalls. Not sync.Once: a concurrent Do waits for the
	// first to finish, which would serialise the calls and hide the race.
	var first atomic.Bool
	testHookAfterReadyRead = func() {
		if first.CompareAndSwap(false, true) {
			close(stalled)
			<-release
		}
	}
	defer func() { testHookAfterReadyRead = nil }()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.updateClientModels() }() // reads {early}, then stalls
	<-stalled

	s.registry.mu.Lock()
	s.registry.models["org/late"].State = ModelStateReady
	s.registry.models["org/late"].Health = ModelHealthHealthy
	s.registry.mu.Unlock()
	go func() { defer wg.Done(); s.updateClientModels() }() // reads {early, late}

	time.Sleep(50 * time.Millisecond) // let the second call finish if it is not held back
	close(release)
	wg.Wait()

	if got := s.client.ModelList(); !sameModelSet(got, []string{"org/early", "org/late"}) {
		t.Fatalf("registered %v, want both models — a stale read overwrote the newer set", got)
	}
}
