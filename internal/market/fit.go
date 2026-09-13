// Package market holds what the node knows about the model marketplace, and
// the rules for reading it.
//
// The VRAM fit rule lives here rather than in the command that displays it
// because two very different callers need to agree on it: `recommend-models`,
// which shows a human a column, and the auto-switch guardrails, which decide
// whether a model may be started without anyone watching. A rule with two
// implementations would eventually give those two callers different answers,
// and the one that mattered would be the unattended one.
package market

// The three states a model's VRAM requirement can be in relative to a node.
//
// Three, not two. Collapsing "unknown" into either neighbour is the defect
// this replaces: the previous rule read a missing requirement as "fits", and
// because the server publishes no requirement for any model today, every model
// on the network passed as compatible with every node — including 70B and
// larger models against 10 GB cards.
const (
	FitFits     = "fits"
	FitTooLarge = "too-large"
	FitUnknown  = "unknown"
)

// VRAMFit classifies a model's VRAM requirement against a node's total VRAM,
// both in GB.
//
// vramKnown is the server's own statement about whether minVRAMGB means
// anything. Unknown wins over every other answer: a requirement nobody has
// measured cannot be compared, and a node that cannot see its own hardware
// cannot do the comparing. Neither is evidence that a model fits.
func VRAMFit(minVRAMGB int, vramKnown bool, totalVRAMGB int) string {
	if totalVRAMGB <= 0 {
		return FitUnknown
	}
	if !vramKnown || minVRAMGB <= 0 {
		return FitUnknown
	}
	if minVRAMGB <= totalVRAMGB {
		return FitFits
	}
	return FitTooLarge
}

// FitsKnown reports whether a model is *known* to fit.
//
// This is the question every automatic decision must ask. "Not known too
// large" is a different and much weaker statement, and using it is how an
// unmeasured 600B model ends up scheduled onto a 10 GB card.
func FitsKnown(minVRAMGB int, vramKnown bool, totalVRAMGB int) bool {
	return VRAMFit(minVRAMGB, vramKnown, totalVRAMGB) == FitFits
}
