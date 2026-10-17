package computing

import (
	"errors"
	"fmt"
	"testing"

	"github.com/swanchain/computing-provider-v2/internal/db"
)

// The stores that back the dashboard's history reads come up on their own,
// before and regardless of the hub connection: the tables exist as soon as
// the service has started, connected or not.
func TestLocalStoresStartWithoutTheHub(t *testing.T) {
	dir := t.TempDir()
	db.InitDb(dir)
	s := NewInferenceService("test-node", dir)
	s.client = &InferenceClient{metrics: NewInferenceMetrics()}
	s.requestStore = NewRequestStore(7, 1000)
	s.metricsHistory = NewMetricsHistory()
	t.Cleanup(func() {
		s.requestStore.Stop()
		s.metricsHistory.Stop()
		if s.usageSampler != nil {
			s.usageSampler.Stop()
		}
	})

	s.startLocalStores()

	for _, table := range []string{"request_history", "metrics_history"} {
		if !db.NewDbService().Migrator().HasTable(table) {
			t.Errorf("table %s missing after startLocalStores", table)
		}
	}
	if _, err := s.metricsHistory.GetHistory(1, 1); err != nil {
		t.Fatalf("metrics history read right after start: %v", err)
	}
}

func TestStartupRetryable(t *testing.T) {
	if startupRetryable(nil) {
		t.Fatal("nil error is not something to retry")
	}
	if !startupRetryable(errors.New("dial tcp 127.0.0.1:9: connect: connection refused")) {
		t.Fatal("a network failure should be retried")
	}
	if startupRetryable(fmt.Errorf("invalid API key: nope: %w", ErrProviderRefused)) {
		t.Fatal("a refusal by the hub should not be retried on its own")
	}
	if startupRetryable(fmt.Errorf("failed to start Inference client: %w", fmt.Errorf("provider cannot connect: pending (status: pending): %w", ErrProviderRefused))) {
		t.Fatal("a wrapped refusal should still not be retried")
	}
}
