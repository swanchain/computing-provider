package computing

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/swanchain/computing-provider-v2/internal/alerts"
)

// Swan Inference knows things about this provider that the provider cannot see
// from inside: that its reputation has dropped, that traffic is being withheld,
// that a model declaration was rejected, that a payout is pending. A notice
// carries one of those facts down the existing WebSocket so it reaches the
// operator through whatever [Alerts] transports they already configured.
//
// Forwarding rather than having the hub mail the operator directly keeps the
// operator's address on their own machine, reuses the cooldown and ordering the
// Notifier already has, and reaches the webhook as well as email.
//
// The hub is a remote party, so nothing it sends is trusted verbatim. The event
// name reaches an SMTP Subject: header, where an embedded CRLF would let a
// notice inject headers of its own and quietly Bcc somebody else's mail server.
// Everything below is therefore whitelisted, clamped, and rate-limited before
// it can reach a transport.
const (
	maxNoticeEventLen   = 64
	maxNoticeMessageLen = 1000
	maxNoticeModelIDLen = 200
	maxNoticeDetails    = 16
	maxNoticeDetailLen  = 200

	// noticeEventPrefix marks an event as hub-originated. It also stops the hub
	// from forging a local event name: a notice can never arrive claiming to be
	// this node's own "model_auto_disabled".
	noticeEventPrefix = "hub_"

	// A hub that malfunctions must not be able to empty an operator's SMTP quota
	// or bury a real local alert. Genuine notices are rare — a suspension, a
	// reputation change — so a ceiling this high is invisible in normal use.
	noticeRateLimit  = 20
	noticeRateWindow = time.Hour
)

// NoticePayload is an operational notice pushed from Swan Inference.
type NoticePayload struct {
	Event    string            `json:"event"`
	Severity string            `json:"severity,omitempty"`
	ModelID  string            `json:"model_id,omitempty"`
	Message  string            `json:"message"`
	Details  map[string]string `json:"details,omitempty"`
}

// hubNotice is a notice that has been validated and is safe to hand to a
// transport.
type hubNotice struct {
	Event    string
	Severity alerts.Severity
	ModelID  string
	Message  string
	Details  map[string]string
}

// sanitize validates a notice from the hub, returning an error describing why
// it was rejected. A rejected notice is dropped and logged, never delivered.
func (p NoticePayload) sanitize() (hubNotice, error) {
	event, ok := sanitizeNoticeEvent(p.Event)
	if !ok {
		return hubNotice{}, fmt.Errorf("event name %q is empty or contains characters outside [a-z0-9_]", p.Event)
	}
	message := clampText(p.Message, maxNoticeMessageLen)
	if message == "" {
		return hubNotice{}, fmt.Errorf("notice %q carries no message", event)
	}
	return hubNotice{
		Event:    noticeEventPrefix + event,
		Severity: sanitizeNoticeSeverity(p.Severity),
		ModelID:  sanitizeModelID(p.ModelID),
		Message:  message,
		Details:  sanitizeNoticeDetails(p.Details),
	}, nil
}

// sanitizeNoticeEvent whitelists the event name. This is the check that closes
// SMTP header injection, so it allows only characters that cannot terminate a
// header line — not a blocklist of CR and LF, which would miss the next
// encoding somebody thinks of.
func sanitizeNoticeEvent(raw string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" || len(s) > maxNoticeEventLen {
		return "", false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return "", false
		}
	}
	// A hub-supplied name that already starts with the prefix would render as
	// "hub_hub_x"; accept it once rather than doubling it.
	return strings.TrimPrefix(s, noticeEventPrefix), true
}

// sanitizeNoticeSeverity clamps to the known set. Severity is upper-cased into
// the mail subject, so an unrecognised value must never reach it. An unknown
// severity becomes a warning: a notice worth sending is not worth silencing,
// but it should not be able to claim to be critical either.
func sanitizeNoticeSeverity(raw string) alerts.Severity {
	switch alerts.Severity(strings.ToLower(strings.TrimSpace(raw))) {
	case alerts.SeverityInfo:
		return alerts.SeverityInfo
	case alerts.SeverityCritical:
		return alerts.SeverityCritical
	default:
		return alerts.SeverityWarning
	}
}

// sanitizeModelID keeps the characters a HuggingFace repo ID actually uses.
func sanitizeModelID(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) > maxNoticeModelIDLen {
		s = s[:maxNoticeModelIDLen]
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '/' || r == '-' || r == '.' || r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sanitizeNoticeDetails bounds the map and cleans both halves of every pair.
// Details reach the plaintext mail body, so control characters are stripped;
// the map is bounded so a notice cannot arrive carrying megabytes of keys.
func sanitizeNoticeDetails(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	// Sorted so that truncating a too-large map drops the same keys every time
	// rather than a random subset per delivery.
	sort.Strings(keys)

	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if len(out) >= maxNoticeDetails {
			break
		}
		key, ok := sanitizeNoticeEvent(k)
		if !ok {
			continue
		}
		out[key] = clampText(in[k], maxNoticeDetailLen)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// clampText strips control characters and truncates to n runes. Newlines
// survive in message bodies; carriage returns never do.
func clampText(raw string, n int) string {
	var b strings.Builder
	for _, r := range raw {
		if r == '\n' || r >= 0x20 && r != 0x7f {
			b.WriteRune(r)
		}
	}
	s := strings.TrimSpace(b.String())
	if runes := []rune(s); len(runes) > n {
		// Marked so a reader can tell the hub said more than they are seeing.
		s = strings.TrimSpace(string(runes[:n])) + "… (truncated)"
	}
	return s
}

// noticeLimiter caps how many notices the hub can turn into deliveries per
// window, using a fixed window because the exact boundary behaviour does not
// matter for a ceiling this far above the real rate.
type noticeLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	count  int
	start  time.Time
	// dropped counts suppressed notices so the log can say how many were lost
	// rather than going quiet, which would look identical to the hub stopping.
	dropped int
}

func newNoticeLimiter(limit int, window time.Duration) *noticeLimiter {
	return &noticeLimiter{limit: limit, window: window}
}

// allow reports whether a notice may be delivered now, and how many were
// dropped since the last one that was allowed.
func (l *noticeLimiter) allow(now time.Time) (ok bool, dropped int) {
	if l == nil {
		return true, 0 // Unconfigured: never a reason to swallow a notice.
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.start.IsZero() || now.Sub(l.start) >= l.window {
		l.start = now
		l.count = 0
	}
	if l.count >= l.limit {
		l.dropped++
		return false, l.dropped
	}
	l.count++
	dropped, l.dropped = l.dropped, 0
	return true, dropped
}
