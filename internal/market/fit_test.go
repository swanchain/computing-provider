package market

import "testing"

// The rule this replaces read a missing requirement as "fits". The server
// currently publishes no requirement for any model, so that rule passed every
// model on the network as compatible with every node.
func TestVRAMFitTreatsAnUnpublishedRequirementAsUnknown(t *testing.T) {
	cases := []struct {
		name      string
		minVRAM   int
		known     bool
		totalVRAM int
		want      string
	}{
		{"published and fits", 8, true, 10, FitFits},
		{"published and fits exactly", 10, true, 10, FitFits},
		{"published and too large", 24, true, 10, FitTooLarge},

		// The case that matters: vram_known false.
		{"not published", 0, false, 10, FitUnknown},
		{"not published but carries a number", 8, false, 10, FitUnknown},

		// A zero requirement is not a requirement of zero.
		{"published as zero", 0, true, 10, FitUnknown},

		// A node that cannot see its own hardware cannot judge fit either.
		{"no hardware detected", 8, true, 0, FitUnknown},
		{"negative hardware", 8, true, -1, FitUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := VRAMFit(tc.minVRAM, tc.known, tc.totalVRAM)
			if got != tc.want {
				t.Errorf("VRAMFit(%d, %v, %d) = %q, want %q",
					tc.minVRAM, tc.known, tc.totalVRAM, got, tc.want)
			}
		})
	}
}

// Compatible is what automatic paths read. It must mean "known to fit" and
// nothing looser, or an unmeasured model is eligible for selection.
func TestOnlyAKnownFitIsCompatible(t *testing.T) {
	for _, fit := range []string{FitUnknown, FitTooLarge} {
		if fit == FitFits {
			t.Fatal("test setup is wrong")
		}
	}

	if VRAMFit(8, true, 10) != FitFits {
		t.Error("a published requirement within VRAM should be a fit")
	}
	if VRAMFit(0, false, 10) == FitFits {
		t.Error("an unpublished requirement must never report as a fit")
	}
}
