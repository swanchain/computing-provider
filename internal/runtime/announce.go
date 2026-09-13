package runtime

// Announcer is told when this node starts or stops serving a model.
//
// An interface rather than a direct dependency on the alerts package so that
// the manager stays testable without a mail server, and so that a caller with
// no alerting configured costs nothing: a nil Announcer is a no-op.
//
// The events are fired per act — one model started, one model stopped —
// because that is what this layer actually observes. A scheduler switching
// several models at once knows the whole plan and can say so in the Reason it
// passes down, which is what carries through to the operator's mail.
type Announcer interface {
	AnnounceSwitch(event SwitchEvent)
}

// Switch actions.
const (
	SwitchStarted = "started"
	SwitchStopped = "stopped"
)

// Who decided a switch.
const (
	// DeciderOperator is a person running models serve or models stop.
	DeciderOperator = "operator"

	// DeciderAutoSwitch is the auto-switch scheduler acting on a planner
	// decision that passed the guardrails.
	DeciderAutoSwitch = "auto-switch"
)

// SwitchEvent describes one change to the set of models this node serves.
type SwitchEvent struct {
	// Action is SwitchStarted or SwitchStopped.
	Action string

	ModelID  string
	Backend  string
	Endpoint string

	// Decider says who asked for this: an operator at a terminal, or the
	// scheduler. An operator reading the mail needs to know whether the node
	// did this on its own, and it is the first thing they will look for.
	Decider string

	// Reason is why. For an operator it is whatever they typed after
	// --reason; for the scheduler it is the planner's own stated reason,
	// carried through unchanged so the mail records the argument the node
	// actually acted on.
	Reason string
}

// announce fires an event, tolerating a nil Announcer.
func (m *Manager) announce(event SwitchEvent) {
	if m.Announcer == nil {
		return
	}
	if event.Decider == "" {
		event.Decider = DeciderOperator
	}
	m.Announcer.AnnounceSwitch(event)
}
