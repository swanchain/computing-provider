// Package alerts delivers operational notifications to a provider-configured
// webhook.
//
// The failures that cost a provider money are the quiet ones: the process stays
// up and healthy-looking while a model serves nothing. A model backend whose
// engine died behind a live HTTP server, a WebSocket that never reconnects, a
// model answering every request with an upstream error — none of these stop the
// daemon, so nobody notices until someone thinks to look at the metrics.
package alerts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/swanchain/computing-provider-v2/conf"
)

// Severity classifies an event for routing on the receiving end.
type Severity string

const (
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
	SeverityInfo     Severity = "info"
)

// Event kinds. Recovery events pair with their failure event so a receiver can
// close an incident.
const (
	EventModelUnhealthy  = "model_unhealthy"
	EventModelRecovered  = "model_recovered"
	EventDisconnected    = "disconnected"
	EventReconnected     = "reconnected"
	EventModelErrorRate  = "model_error_rate"
	EventErrorRateNormal = "model_error_rate_normal"
)

// Event is the JSON payload POSTed to the webhook.
type Event struct {
	Event     string            `json:"event"`
	Severity  Severity          `json:"severity"`
	NodeID    string            `json:"node_id,omitempty"`
	NodeName  string            `json:"node_name,omitempty"`
	ModelID   string            `json:"model_id,omitempty"`
	Message   string            `json:"message"`
	Details   map[string]string `json:"details,omitempty"`
	Timestamp time.Time         `json:"timestamp"`

	// Checks and Models carry the structured result of an audit so the mail can
	// show it as a table rather than a wall of prefixed lines. Both are
	// optional; events that have neither render exactly as before.
	Checks []CheckRow `json:"checks,omitempty"`
	Models []ModelRow `json:"models,omitempty"`
}

// Status classifies a single row for display.
type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// CheckRow is one audit check.
type CheckRow struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
}

// ModelRow is one model's outcome, so an operator can see at a glance which of
// their models are serving rather than reading it out of a summary sentence.
type ModelRow struct {
	Model   string `json:"model"`
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
}

// Notifier posts events to a webhook, suppressing repeats of the same event for
// a cooldown window. A Notifier with no webhook configured is a no-op, so
// callers never need to check whether alerting is enabled.
type Notifier struct {
	url      string
	email    *emailSender
	cooldown time.Duration
	nodeID   string
	nodeName string
	client   *http.Client

	mu       sync.Mutex
	lastSent map[string]time.Time

	// Deliveries go through a single worker so events arrive in the order they
	// were raised. Posting each event from its own goroutine lets a recovery
	// overtake the failure it resolves, which would leave a receiver holding an
	// incident that is already closed.
	queue     chan Event
	startOnce sync.Once
}

// queueSize bounds memory if the webhook endpoint is slow or wedged. Alerts are
// dropped rather than allowed to accumulate; the cooldown keeps the normal rate
// far below this.
const queueSize = 64

// New returns a Notifier for the given config. When cfg.WebhookURL is empty the
// returned Notifier silently drops every event.
func New(cfg conf.Alerts, nodeID, nodeName string) *Notifier {
	return &Notifier{
		url:      cfg.WebhookURL,
		email:    newEmailSender(cfg.Email, nodeName),
		cooldown: time.Duration(cfg.CooldownMinutes) * time.Minute,
		nodeID:   nodeID,
		nodeName: nodeName,
		client:   &http.Client{Timeout: 10 * time.Second},
		lastSent: make(map[string]time.Time),
		queue:    make(chan Event, queueSize),
	}
}

// Enabled reports whether any delivery transport is configured.
func (n *Notifier) Enabled() bool {
	return n != nil && (n.url != "" || n.email.enabled())
}

// Fire delivers an event in the background. It never blocks the caller and
// never returns an error: alerting must not be able to break inference.
func (n *Notifier) Fire(event, modelID, message string, severity Severity, details map[string]string) {
	if !n.Enabled() {
		return
	}
	// Recovery events are never rate-limited. Suppressing one leaves the
	// receiver holding an incident that has already resolved, which is worse
	// than an extra message.
	if severity != SeverityInfo && !n.allow(event+"|"+modelID) {
		return
	}
	e := Event{
		Event:     event,
		Severity:  severity,
		NodeID:    n.nodeID,
		NodeName:  n.nodeName,
		ModelID:   modelID,
		Message:   message,
		Details:   details,
		Timestamp: time.Now().UTC(),
	}
	n.enqueue(e)
}

// FireRows is Fire with the structured rows an audit produces, so the mail can
// render a table instead of restating them in prose.
func (n *Notifier) FireRows(event, message string, severity Severity, checks []CheckRow, models []ModelRow, details map[string]string) {
	if !n.Enabled() {
		return
	}
	if severity != SeverityInfo && !n.allow(event+"|") {
		return
	}
	n.enqueue(Event{
		Event:     event,
		Severity:  severity,
		NodeID:    n.nodeID,
		NodeName:  n.nodeName,
		Message:   message,
		Details:   details,
		Checks:    checks,
		Models:    models,
		Timestamp: time.Now().UTC(),
	})
}

func (n *Notifier) enqueue(e Event) {
	n.startOnce.Do(func() { go n.run() })
	select {
	case n.queue <- e:
	default:
		logs.GetLogger().Warnf("alerts: queue full, dropped %s%s", e.Event, modelSuffix(e.ModelID))
	}
}

// run delivers queued events one at a time, preserving their order.
func (n *Notifier) run() {
	for e := range n.queue {
		n.deliver(e)
	}
}

// deliver sends an event over every configured transport. One failing must not
// stop the other.
func (n *Notifier) deliver(e Event) {
	if n.url != "" {
		n.post(e)
	}
	if n.email.enabled() {
		if err := n.email.send(e); err != nil {
			logs.GetLogger().Warnf("alerts: email delivery failed for %s%s: %v", e.Event, modelSuffix(e.ModelID), err)
			return
		}
		logs.GetLogger().Infof("alerts: emailed %s%s", e.Event, modelSuffix(e.ModelID))
	}
}

// SendTest delivers a synthetic event over every configured transport and
// reports the outcome. Configuring SMTP and finding out it was wrong during the
// first real incident is the worst possible time to learn it.
func (n *Notifier) SendTest(message string) error {
	if !n.Enabled() {
		return fmt.Errorf("no alert transport configured: set [Alerts] WebhookURL or [Alerts.Email]")
	}
	e := Event{
		Event:     "test",
		Severity:  SeverityInfo,
		NodeID:    n.nodeID,
		NodeName:  n.nodeName,
		Message:   message,
		Timestamp: time.Now().UTC(),
	}
	if n.email.enabled() {
		if err := n.email.send(e); err != nil {
			return err
		}
	}
	if n.url != "" {
		n.post(e)
	}
	return nil
}

// allow reports whether key is outside its cooldown window, recording the send
// when it is. Only failure events pass through here; see Fire.
func (n *Notifier) allow(key string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.cooldown <= 0 {
		return true
	}
	if last, ok := n.lastSent[key]; ok && time.Since(last) < n.cooldown {
		return false
	}
	n.lastSent[key] = time.Now()
	return true
}

// ClearCooldown forgets a key so the next Fire for it is delivered immediately.
// Used when a subject recovers, so a later failure alerts without waiting out
// the previous cooldown.
func (n *Notifier) ClearCooldown(event, modelID string) {
	if !n.Enabled() {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.lastSent, event+"|"+modelID)
}

func (n *Notifier) post(e Event) {
	body, err := json.Marshal(e)
	if err != nil {
		logs.GetLogger().Warnf("alerts: failed to encode event %s: %v", e.Event, err)
		return
	}
	req, err := http.NewRequest("POST", n.url, bytes.NewReader(body))
	if err != nil {
		logs.GetLogger().Warnf("alerts: failed to build request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		logs.GetLogger().Warnf("alerts: failed to deliver %s: %v", e.Event, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		logs.GetLogger().Warnf("alerts: webhook returned %d for %s", resp.StatusCode, e.Event)
		return
	}
	logs.GetLogger().Infof("alerts: delivered %s%s", e.Event, modelSuffix(e.ModelID))
}

// RedactURL keeps a webhook URL loggable. Slack, Discord and PagerDuty all put
// the secret in the path, and these logs are rotated to disk and pasted into
// support threads.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(configured)"
	}
	return u.Scheme + "://" + u.Host + "/…"
}

func modelSuffix(modelID string) string {
	if modelID == "" {
		return ""
	}
	return fmt.Sprintf(" for %s", modelID)
}
