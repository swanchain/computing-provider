package runtime

import (
	"fmt"
	"time"

	"github.com/swanchain/computing-provider-v2/internal/alerts"
)

// AlertAnnouncer forwards serving changes to the operator's configured
// [Alerts] transports.
//
// The operator's address stays on their own machine and the message goes over
// whatever they already configured — webhook, email, or both — reusing the
// ordering and delivery the Notifier already applies rather than growing a
// second way to send mail.
type AlertAnnouncer struct {
	notifier *alerts.Notifier
}

// NewAlertAnnouncer wraps a Notifier. A nil or unconfigured Notifier yields an
// announcer that does nothing, so a node without [Alerts] pays no attention to
// any of this.
func NewAlertAnnouncer(notifier *alerts.Notifier) *AlertAnnouncer {
	if notifier == nil || !notifier.Enabled() {
		return nil
	}
	return &AlertAnnouncer{notifier: notifier}
}

// AnnounceSwitch mails the operator that a model started or stopped.
func (a *AlertAnnouncer) AnnounceSwitch(event SwitchEvent) {
	if a == nil || a.notifier == nil {
		return
	}

	kind := alerts.EventModelServingStarted
	verb := "Started serving"
	if event.Action == SwitchStopped {
		kind = alerts.EventModelServingStopped
		verb = "Stopped serving"
	}

	// The decider is in the message rather than only in the details because
	// it is the first thing an operator needs from the subject line onward:
	// whether the node did this by itself.
	message := fmt.Sprintf("%s %s (decided by %s)", verb, event.ModelID, decider(event.Decider))
	if event.Reason != "" {
		message += ": " + event.Reason
	}

	details := map[string]string{
		"action":     event.Action,
		"decided_by": decider(event.Decider),
		"reason":     reasonOrDefault(event.Reason, event.Decider),
	}
	if event.Backend != "" {
		details["backend"] = event.Backend
	}
	if event.Endpoint != "" {
		details["endpoint"] = event.Endpoint
	}

	// Info severity: a switch that worked is not a fault. It does mean these
	// are not subject to the failure cooldown, which is why the scheduler
	// caps switches per day — that cap, not the alerting, is what stops a
	// flapping planner filling an inbox.
	a.notifier.Fire(kind, event.ModelID, message, alerts.SeverityInfo, details)
}

// Flush waits for queued announcements to be delivered.
//
// A command that fires an announcement and returns immediately would otherwise
// exit with the mail still sitting in the queue. The daemon has no need of
// this; every CLI path does.
func (a *AlertAnnouncer) Flush(timeout time.Duration) {
	if a == nil || a.notifier == nil {
		return
	}
	a.notifier.Flush(timeout)
}

func decider(d string) string {
	switch d {
	case DeciderAutoSwitch:
		return "auto-switch"
	case DeciderAgent:
		return "agent"
	default:
		return "operator"
	}
}

// reasonOrDefault keeps the reason field present even when nobody gave one, so
// the mail never shows a blank row where the explanation should be.
func reasonOrDefault(reason, decidedBy string) string {
	if reason != "" {
		return reason
	}
	if decidedBy == DeciderAutoSwitch || decidedBy == DeciderAgent {
		return "no reason recorded by the planner"
	}
	return "no reason given (pass --reason to record one)"
}
