package computing

import (
	"strings"
	"testing"
	"time"

	"github.com/swanchain/computing-provider-v2/internal/alerts"
)

// The event name is interpolated into an SMTP "Subject:" header. A notice that
// can smuggle a CRLF into it can append headers of its own — a Bcc to an
// address the operator never sees. This is the single most important test in
// the file.
func TestNoticeCannotInjectMailHeaders(t *testing.T) {
	for _, evil := range []string{
		"suspended\r\nBcc: attacker@example.com",
		"suspended\nBcc: attacker@example.com",
		"suspended\rX-Injected: 1",
		"suspended\x00nul",
		"subject: fake",
		"has space",
	} {
		p := NoticePayload{Event: evil, Message: "hello"}
		notice, err := p.sanitize()
		if err == nil {
			t.Errorf("event %q was accepted as %q, want rejection", evil, notice.Event)
		}
	}
}

// Details and the message body also reach the mail, so control characters must
// not survive there either.
func TestNoticeStripsControlCharactersFromText(t *testing.T) {
	p := NoticePayload{
		Event:   "provider_suspended",
		Message: "line one\r\nline two\x07",
		Details: map[string]string{"reason": "bad\rvalue"},
	}
	notice, err := p.sanitize()
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(notice.Message, "\r\x07") {
		t.Errorf("message kept control characters: %q", notice.Message)
	}
	if !strings.Contains(notice.Message, "line one\nline two") {
		t.Errorf("newline should survive in a body: %q", notice.Message)
	}
	if got := notice.Details["reason"]; strings.Contains(got, "\r") {
		t.Errorf("detail kept a carriage return: %q", got)
	}
}

// The hub must not be able to send an event that looks like one this node
// raised about itself, or an operator cannot tell a real local fault from a
// claim made remotely.
func TestNoticeEventIsAlwaysPrefixed(t *testing.T) {
	for raw, want := range map[string]string{
		"model_auto_disabled":    "hub_model_auto_disabled",
		"PROVIDER_SUSPENDED":     "hub_provider_suspended",
		"  reputation_dropped  ": "hub_reputation_dropped",
		"hub_already_prefixed":   "hub_already_prefixed",
	} {
		notice, err := p(raw).sanitize()
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		if notice.Event != want {
			t.Errorf("event %q -> %q, want %q", raw, notice.Event, want)
		}
	}
}

// Severity is upper-cased into the subject line and routes on the receiving
// end, so an unknown value must land on a known one rather than pass through.
func TestNoticeSeverityIsClamped(t *testing.T) {
	for raw, want := range map[string]alerts.Severity{
		"critical":  alerts.SeverityCritical,
		"CRITICAL":  alerts.SeverityCritical,
		"info":      alerts.SeverityInfo,
		"warning":   alerts.SeverityWarning,
		"":          alerts.SeverityWarning,
		"emergency": alerts.SeverityWarning,
		"\r\nBcc:":  alerts.SeverityWarning,
	} {
		if got := sanitizeNoticeSeverity(raw); got != want {
			t.Errorf("severity %q -> %q, want %q", raw, got, want)
		}
	}
}

func TestNoticeRequiresAnEventAndMessage(t *testing.T) {
	if _, err := (NoticePayload{Message: "no event"}).sanitize(); err == nil {
		t.Error("a notice with no event should be rejected")
	}
	if _, err := (NoticePayload{Event: "x"}).sanitize(); err == nil {
		t.Error("a notice with no message should be rejected")
	}
	// Whitespace and control characters alone are not a message.
	if _, err := (NoticePayload{Event: "x", Message: " \r\n "}).sanitize(); err == nil {
		t.Error("a blank message should be rejected")
	}
}

// An over-long message must be truncated visibly: silently cutting it would
// leave an operator acting on half a sentence.
func TestNoticeMessageIsTruncatedAndMarked(t *testing.T) {
	notice, err := NoticePayload{Event: "x", Message: strings.Repeat("a", maxNoticeMessageLen*2)}.sanitize()
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(notice.Message)) > maxNoticeMessageLen+len("… (truncated)") {
		t.Errorf("message not truncated: %d runes", len([]rune(notice.Message)))
	}
	if !strings.HasSuffix(notice.Message, "(truncated)") {
		t.Error("truncation should be visible to the reader")
	}
}

// Multi-byte text must not be cut mid-character.
func TestNoticeTruncationIsRuneSafe(t *testing.T) {
	notice, err := NoticePayload{Event: "x", Message: strings.Repeat("日", maxNoticeMessageLen*2)}.sanitize()
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(notice.Message, '�') {
		t.Error("truncation split a multi-byte rune")
	}
}

func TestNoticeDetailsAreBounded(t *testing.T) {
	in := map[string]string{}
	for i := 0; i < maxNoticeDetails*3; i++ {
		in[string(rune('a'+i%26))+strings.Repeat("x", i)] = strings.Repeat("v", 500)
	}
	in["bad key!"] = "dropped"
	notice, err := NoticePayload{Event: "x", Message: "m", Details: in}.sanitize()
	if err != nil {
		t.Fatal(err)
	}
	if len(notice.Details) > maxNoticeDetails {
		t.Errorf("kept %d details, want at most %d", len(notice.Details), maxNoticeDetails)
	}
	for k, v := range notice.Details {
		if strings.Contains(k, " ") || strings.Contains(k, "!") {
			t.Errorf("kept an invalid key %q", k)
		}
		if len([]rune(v)) > maxNoticeDetailLen+len("… (truncated)") {
			t.Errorf("detail %q not truncated", k)
		}
	}
}

func TestNoticeModelIDIsSanitized(t *testing.T) {
	notice, err := NoticePayload{
		Event:   "x",
		Message: "m",
		ModelID: "Qwen/Qwen3.8-27B\r\nBcc: x@y.z",
	}.sanitize()
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(notice.ModelID, "\r\n @:") {
		t.Errorf("model_id kept dangerous characters: %q", notice.ModelID)
	}
	// The legitimate shape must survive untouched.
	ok, err := NoticePayload{Event: "x", Message: "m", ModelID: "Qwen/Qwen3.8-27B"}.sanitize()
	if err != nil {
		t.Fatal(err)
	}
	if ok.ModelID != "Qwen/Qwen3.8-27B" {
		t.Errorf("a normal repo ID was altered: %q", ok.ModelID)
	}
}

// A hub stuck in a loop must not be able to drain an SMTP quota or bury local
// alerts under its own traffic.
func TestNoticeLimiterCapsAndReports(t *testing.T) {
	base := time.Unix(1750000000, 0)
	l := newNoticeLimiter(3, time.Hour)

	for i := 0; i < 3; i++ {
		if ok, _ := l.allow(base); !ok {
			t.Fatalf("notice %d should be allowed", i)
		}
	}
	if ok, _ := l.allow(base); ok {
		t.Error("the fourth notice should be rate limited")
	}

	// The window rolls, and the first notice through reports what was lost.
	ok, dropped := l.allow(base.Add(time.Hour + time.Second))
	if !ok {
		t.Fatal("a new window should allow again")
	}
	if dropped == 0 {
		t.Error("the suppressed notices should be reported, not silently forgotten")
	}
	if _, d := l.allow(base.Add(time.Hour + 2*time.Second)); d != 0 {
		t.Errorf("dropped count should reset after being reported, got %d", d)
	}
}

func TestNoticeLimiterNilIsPermissive(t *testing.T) {
	var l *noticeLimiter
	if ok, _ := l.allow(time.Unix(0, 0)); !ok {
		t.Error("an unconfigured limiter must not swallow notices")
	}
}

func p(event string) NoticePayload { return NoticePayload{Event: event, Message: "m"} }

// The wire format must actually reach the handler: a validation layer nothing
// dispatches to is dead code.
func TestNoticeMessageReachesTheHandler(t *testing.T) {
	c := &InferenceClient{}
	got := make(chan NoticePayload, 1)
	c.SetNoticeHandler(func(p NoticePayload) { got <- p })

	c.handleMessage(Message{
		Type:    MsgTypeNotice,
		Payload: []byte(`{"event":"provider_suspended","severity":"critical","message":"routing paused","details":{"until":"14:00Z"}}`),
	})

	select {
	case p := <-got:
		notice, err := p.sanitize()
		if err != nil {
			t.Fatal(err)
		}
		if notice.Event != "hub_provider_suspended" || notice.Severity != alerts.SeverityCritical {
			t.Errorf("got %+v", notice)
		}
		if notice.Details["until"] != "14:00Z" {
			t.Errorf("details lost: %+v", notice.Details)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notice never reached the handler")
	}
}

// A notice arriving with no handler set, or with malformed JSON, must not panic
// the read loop that also carries inference requests.
func TestNoticeDispatchIsSafeWithoutHandler(t *testing.T) {
	c := &InferenceClient{}
	c.handleMessage(Message{Type: MsgTypeNotice, Payload: []byte(`{"event":"x","message":"m"}`)})
	c.handleMessage(Message{Type: MsgTypeNotice, Payload: []byte(`not json`)})
}
