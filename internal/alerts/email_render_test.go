package alerts

import (
	"strings"

	"github.com/swanchain/computing-provider-v2/conf"
	"testing"
	"time"
)

func renderTestEvent(e Event) string {
	s := &emailSender{cfg: conf.Email{From: "a@b.c", To: []string{"d@e.f"}}, nodeName: "cp-01"}
	return s.compose(e)
}

// A notice pushed from Swan Inference carries a message and details chosen
// remotely. Rendering those into markup unescaped would put attacker-chosen
// HTML in the operator's mail client.
func TestHTMLPartEscapesRemotelySuppliedText(t *testing.T) {
	m := renderTestEvent(Event{
		Event:     "hub_notice",
		Severity:  SeverityWarning,
		Message:   `<img src=x onerror="alert(1)">`,
		Details:   map[string]string{"<b>key</b>": `</td><script>alert(2)</script>`},
		Models:    []ModelRow{{Model: `<script>a</script>`, Status: StatusFail, Message: `<i>b</i>`}},
		Checks:    []CheckRow{{Name: `<u>c</u>`, Status: StatusWarn, Message: `<em>d</em>`}},
		Timestamp: time.Unix(1750000000, 0).UTC(),
	})
	// Only the HTML part. The plain-text part carries the raw characters, which
	// is correct there — text/plain is not markup.
	htmlPart := m[strings.Index(m, "text/html"):]
	// The escape turns "<" into "&lt;", so no tag can form. Substrings like
	// "onerror=" survive inside the escaped text and are inert there — what
	// matters is that no opening bracket does.
	for _, raw := range []string{"<script", "<img", "<u>c</u>", "<em>d</em>", "<i>b</i>", "</td><script"} {
		if strings.Contains(htmlPart, raw) {
			t.Errorf("unescaped %q reached the HTML part", raw)
		}
	}
	if !strings.Contains(htmlPart, "&lt;script&gt;") {
		t.Error("expected the script tag to appear escaped in the HTML part")
	}
}

// Alerts are read in terminals and on phones, and scored by filters. An
// HTML-only operational mail is unreadable in some of those.
func TestMessageIsMultipartWithAPlainTextPart(t *testing.T) {
	m := renderTestEvent(Event{
		Event: "startup_check", Severity: SeverityInfo, Message: "Provider started.",
		Checks:    []CheckRow{{Name: "daemon", Status: StatusPass, Message: "responding"}},
		Models:    []ModelRow{{Model: "org/a", Status: StatusPass, Message: "served a request in 12 ms"}},
		Timestamp: time.Unix(1750000000, 0).UTC(),
	})
	for _, want := range []string{
		"MIME-Version: 1.0",
		"multipart/alternative",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Type: text/html; charset=utf-8",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("missing %q", want)
		}
	}
	// The text part must carry the same information, not a stub.
	textPart := m[strings.Index(m, "text/plain"):strings.Index(m, "text/html")]
	for _, want := range []string{"daemon", "responding", "org/a", "served a request"} {
		if !strings.Contains(textPart, want) {
			t.Errorf("plain-text part missing %q", want)
		}
	}
}

// Colour alone carries nothing for a colour-blind reader and nothing at all in
// the text part, so every row must be marked with a glyph too.
func TestEveryRowCarriesAGlyphNotJustColour(t *testing.T) {
	m := renderTestEvent(Event{
		Event: "selfcheck_failed", Severity: SeverityCritical, Message: "problems",
		Checks: []CheckRow{
			{Name: "ok-check", Status: StatusPass},
			{Name: "warn-check", Status: StatusWarn},
			{Name: "fail-check", Status: StatusFail},
		},
		Timestamp: time.Unix(1750000000, 0).UTC(),
	})
	for _, glyph := range []string{"✅", "⚠️", "❌"} {
		if !strings.Contains(m, glyph) {
			t.Errorf("missing status glyph %q", glyph)
		}
	}
}

// Existing filters point at the severity word; the glyph is additive.
func TestSubjectKeepsTheSeverityWord(t *testing.T) {
	m := renderTestEvent(Event{Event: "model_unhealthy", Severity: SeverityCritical, Message: "x", Timestamp: time.Now()})
	if !strings.Contains(m, "[cp-01] CRITICAL: model_unhealthy") {
		t.Errorf("subject lost the severity word:\n%s", m[:200])
	}
	if !strings.Contains(m, "❌") {
		t.Error("subject should also carry a glyph")
	}
}

// Ranging a map put details in a different order in every mail, which makes two
// alerts impossible to compare by eye.
func TestDetailsAreOrdered(t *testing.T) {
	m := renderTestEvent(Event{
		Event: "x", Severity: SeverityInfo, Message: "m",
		Details:   map[string]string{"zebra": "1", "alpha": "2", "middle": "3"},
		Timestamp: time.Now(),
	})
	a, mid, z := strings.Index(m, "alpha"), strings.Index(m, "middle"), strings.Index(m, "zebra")
	if !(a < mid && mid < z) {
		t.Errorf("details not sorted: alpha=%d middle=%d zebra=%d", a, mid, z)
	}
}
