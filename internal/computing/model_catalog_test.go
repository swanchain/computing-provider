package computing

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCatalogContextPrecedenceAndHubOmission(t *testing.T) {
	s := newContextService(map[string]ModelMapping{
		"org/override": {ContextLength: 4096},
		"org/detected": {},
		"org/catalog":  {},
	})
	s.catalogContexts = map[string]int{"org/override": 100000, "org/detected": 100000, "org/catalog": 100000}
	s.healthChecker.recordDetectedContext("org/detected", map[string]int{"org/detected": 8192})
	if got := s.ModelContext("org/override"); got.Source != ContextSourceOverride || got.Length != 4096 {
		t.Fatalf("override = %+v", got)
	}
	if got := s.ModelContext("org/detected"); got.Source != ContextSourceDetected || got.Length != 8192 {
		t.Fatalf("detected = %+v", got)
	}
	if got := s.ModelContext("org/catalog"); got.Source != ContextSourceCatalog || got.Length != 100000 {
		t.Fatalf("catalog = %+v", got)
	}
	if _, ok := s.resolveModelContexts()["org/catalog"]; ok {
		t.Fatal("theoretical catalog window must not be declared as backend capacity")
	}
}

func TestFetchCatalogContexts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"org/a":{"limit":{"context":12345}},"org/b":{"limit":{"context":0}}}`))
	}))
	defer server.Close()
	got, err := fetchCatalogContexts(server.Client(), server.URL)
	if err != nil || got["org/a"] != 12345 || len(got) != 1 {
		t.Fatalf("catalog = %v, %v", got, err)
	}
}
