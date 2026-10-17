package computing

import (
	"fmt"
	"sync"
	"testing"
)

// The registry fires one callback goroutine per model when models.json is
// loaded, so a node with several models had several goroutines writing the
// mappings map at once; on a three-model node this was "fatal error:
// concurrent map writes" at startup. Run under -race, this test exercises the
// callbacks, the readers the HTTP handlers use and the hub client's provider
// together.
func TestModelMappingsAreSafeForConcurrentUse(t *testing.T) {
	s := NewInferenceService("test-node", t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("org/model-%d", i)
		wg.Add(3)
		go func() {
			defer wg.Done()
			s.setMapping(id, ModelMapping{Endpoint: "http://127.0.0.1:1", MaxConcurrent: i})
		}()
		go func() {
			defer wg.Done()
			_, _ = s.mapping(id)
			_ = s.GetActiveModels()
			_ = s.ModelContexts()
		}()
		go func() {
			defer wg.Done()
			for range s.mappings() {
			}
			if i%2 == 0 {
				s.deleteMapping(id)
			}
		}()
	}
	wg.Wait()
	if got := len(s.mappings()); got < 4 || got > 8 {
		// An even id is deleted only when its delete ran after its set.
		t.Fatalf("mappings left = %d, want between 4 and 8", got)
	}
}
