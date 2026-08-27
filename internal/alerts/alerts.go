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
}

// Notifier posts events to a webhook, suppressing repeats of the same event for
// a cooldown window. A Notifier with no webhook configured is a no-op, so
// callers never need to check whether alerting is enabled.
type Notifier struct {
	url      string
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
		cooldown: time.Duration(cfg.CooldownMinutes) * time.Minute,
		nodeID:   nodeID,
		nodeName: nodeName,
		client:   &http.Client{Timeout: 10 * time.Second},
		lastSent: make(map[string]time.Time),
		queue:    make(chan Event, queueSize),
	}
}

// Enabled reports whether a webhook is configured.
func (n *Notifier) Enabled() bool { return n != nil && n.url != "" }

// Fire delivers an event in the background. It never blocks the caller and
// never returns an error: alerting must not be able to break inference.
func (n *Notifier) Fire(event, modelID, message string, severity Severity, details map[string]string) {
	if !n.Enabled() {
		return
	}
	if !n.allow(event + "|" + modelID) {
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
		n.post(e)
	}
}

// allow reports whether key is outside its cooldown window, recording the send
// when it is. Recovery events bypass the cooldown so an incident always closes.
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

func modelSuffix(modelID string) string {
	if modelID == "" {
		return ""
	}
	return fmt.Sprintf(" for %s", modelID)
}
