package computing

import "testing"

// A model that was auto-disabled and then re-enabled must not keep reporting
// state "disabled". DisableModel sets both the flag and the state, and
// onHealthStatusChange deliberately leaves a disabled model alone, so
// EnableModel is the only thing that can clear it.
func TestEnableModelRestoresStateFromHealth(t *testing.T) {
	cases := []struct {
		name   string
		health ModelHealth
		want   ModelState
	}{
		{"healthy backend becomes ready", ModelHealthHealthy, ModelStateReady},
		{"degraded backend still serves", ModelHealthDegraded, ModelStateReady},
		{"unhealthy backend is not claimed ready", ModelHealthUnhealthy, ModelStateUnhealthy},
		{"unknown health waits for the next check", ModelHealthUnknown, ModelStateLoading},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewModelRegistry("", nil)
			r.models["m"] = &RegisteredModel{ID: "m", Health: tc.health}

			if err := r.DisableModel("m"); err != nil {
				t.Fatalf("DisableModel: %v", err)
			}
			if got := r.models["m"].State; got != ModelStateDisabled {
				t.Fatalf("after disable, state = %v, want disabled", got)
			}

			if err := r.EnableModel("m"); err != nil {
				t.Fatalf("EnableModel: %v", err)
			}
			m := r.models["m"]
			if !m.Enabled {
				t.Error("model is not enabled")
			}
			if m.State != tc.want {
				t.Errorf("state = %v, want %v", m.State, tc.want)
			}
			if m.StateString != tc.want.String() {
				t.Errorf("state string = %q, want %q", m.StateString, tc.want.String())
			}
		})
	}
}

// Enabling a model that was never disabled must not rewrite a state the health
// checker owns.
func TestEnableModelLeavesANonDisabledStateAlone(t *testing.T) {
	r := NewModelRegistry("", nil)
	r.models["m"] = &RegisteredModel{
		ID:          "m",
		Enabled:     true,
		Health:      ModelHealthUnhealthy,
		State:       ModelStateLoading,
		StateString: ModelStateLoading.String(),
	}

	if err := r.EnableModel("m"); err != nil {
		t.Fatalf("EnableModel: %v", err)
	}
	if got := r.models["m"].State; got != ModelStateLoading {
		t.Errorf("state = %v, want it left as loading", got)
	}
}

func TestEnableModelUnknownModel(t *testing.T) {
	r := NewModelRegistry("", nil)
	if err := r.EnableModel("nope"); err != ErrModelNotFound {
		t.Errorf("err = %v, want ErrModelNotFound", err)
	}
}
