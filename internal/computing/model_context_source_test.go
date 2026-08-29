package computing

import "testing"

func newContextService(mappings map[string]ModelMapping) *InferenceService {
	return &InferenceService{
		modelMappings: mappings,
		healthChecker: NewModelHealthChecker(DefaultHealthCheckConfig()),
	}
}

func TestModelContextSources(t *testing.T) {
	s := newContextService(map[string]ModelMapping{
		"org/override": {Endpoint: "http://backend", ContextLength: 128000},
		"org/detected": {Endpoint: "http://backend"},
		"org/unknown":  {Endpoint: "http://proxy"},
		"org/pending":  {Endpoint: "http://backend"},
	})

	// vLLM-style backend declares a window.
	s.healthChecker.recordDetectedContext("org/detected", map[string]int{"org/detected": 45056})
	// A proxy answers /v1/models with no max_model_len: probed, nothing found.
	s.healthChecker.recordDetectedContext("org/unknown", nil)
	// org/pending has not been checked at all.

	for _, tc := range []struct {
		id         string
		wantLen    int
		wantSource string
	}{
		{"org/override", 128000, ContextSourceOverride},
		{"org/detected", 45056, ContextSourceDetected},
		{"org/unknown", 0, ContextSourceUnknown},
		{"org/pending", 0, ContextSourcePending},
	} {
		got := s.ModelContext(tc.id)
		if got.Length != tc.wantLen || got.Source != tc.wantSource {
			t.Errorf("%s: got %d/%s, want %d/%s", tc.id, got.Length, got.Source, tc.wantLen, tc.wantSource)
		}
	}
}

// An operator-set window must win over detection: they know something the
// backend will not say, and detection can be wrong for a proxied model.
func TestOverrideBeatsDetection(t *testing.T) {
	s := newContextService(map[string]ModelMapping{
		"org/a": {Endpoint: "http://backend", ContextLength: 32768},
	})
	s.healthChecker.recordDetectedContext("org/a", map[string]int{"org/a": 131072})

	got := s.ModelContext("org/a")
	if got.Length != 32768 || got.Source != ContextSourceOverride {
		t.Fatalf("got %d/%s, want 32768/%s", got.Length, got.Source, ContextSourceOverride)
	}
}

// resolveModelContexts feeds registration and heartbeats; it must carry every
// known window and omit the ones nobody can determine.
func TestResolveOmitsUnknownContext(t *testing.T) {
	s := newContextService(map[string]ModelMapping{
		"org/known":   {Endpoint: "http://backend"},
		"org/unknown": {Endpoint: "http://proxy"},
	})
	s.healthChecker.recordDetectedContext("org/known", map[string]int{"org/known": 8192})
	s.healthChecker.recordDetectedContext("org/unknown", nil)

	got := s.resolveModelContexts()
	if len(got) != 1 || got["org/known"] != 8192 {
		t.Fatalf("got %v, want only org/known=8192", got)
	}
}

// The warning is the whole point of the change, but it must not repeat: this
// runs on every registration and heartbeat.
func TestUnknownContextWarnsOnlyOnce(t *testing.T) {
	s := newContextService(map[string]ModelMapping{
		"org/unknown": {Endpoint: "http://proxy"},
	})
	s.healthChecker.recordDetectedContext("org/unknown", nil)

	for i := 0; i < 5; i++ {
		s.resolveModelContexts()
	}
	if !s.contextWarned["org/unknown"] {
		t.Fatal("expected the model to be marked warned")
	}
	if len(s.contextWarned) != 1 {
		t.Fatalf("warned map = %v, want exactly one entry", s.contextWarned)
	}
}

// A model whose first health check has not run yet is not "unknown" — warning
// there would fire on every restart before anything had been probed.
func TestPendingModelIsNotWarned(t *testing.T) {
	s := newContextService(map[string]ModelMapping{
		"org/pending": {Endpoint: "http://backend"},
	})
	s.resolveModelContexts()
	if len(s.contextWarned) != 0 {
		t.Fatalf("warned before any probe: %v", s.contextWarned)
	}
}

func TestModelContextsCoversEveryMapping(t *testing.T) {
	s := newContextService(map[string]ModelMapping{
		"org/a": {Endpoint: "http://backend", ContextLength: 4096},
		"org/b": {Endpoint: "http://proxy"},
	})
	got := s.ModelContexts()
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got["org/b"].Source != ContextSourcePending {
		t.Errorf("org/b source = %s, want %s", got["org/b"].Source, ContextSourcePending)
	}
}
