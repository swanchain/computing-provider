package computing

import "testing"

// Where the backend reports no window, the one Swan Inference assumes is
// shown locally, below an override and a detected value, and never sent back
// to the hub as this backend's capacity.
func TestHubContextPrecedenceAndHubOmission(t *testing.T) {
	s := newContextService(map[string]ModelMapping{
		"org/override": {ContextLength: 4096},
		"org/detected": {},
		"org/assumed":  {},
	})
	s.SetHubContexts([]PlatformModel{
		{ModelID: "org/override", ContextLength: 100000},
		{ModelID: "org/detected", ContextLength: 100000},
		{ModelID: "org/assumed", ContextLength: 100000},
		{ModelID: "org/zero", ContextLength: 0},
	})
	s.healthChecker.recordDetectedContext("org/detected", map[string]int{"org/detected": 8192})
	if got := s.ModelContext("org/override"); got.Source != ContextSourceOverride || got.Length != 4096 {
		t.Fatalf("override = %+v", got)
	}
	if got := s.ModelContext("org/detected"); got.Source != ContextSourceDetected || got.Length != 8192 {
		t.Fatalf("detected = %+v", got)
	}
	if got := s.ModelContext("org/assumed"); got.Source != ContextSourceHub || got.Length != 100000 {
		t.Fatalf("assumed = %+v", got)
	}
	if got := s.ModelContext("org/zero"); got.Source == ContextSourceHub {
		t.Fatalf("a zero hub window was used: %+v", got)
	}
	if _, ok := s.resolveModelContexts()["org/assumed"]; ok {
		t.Fatal("the hub's own assumption must not be declared back to it as backend capacity")
	}
}
