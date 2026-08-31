package alerts

import (
	"crypto/tls"
	"fmt"
	"html"
	"net"
	"net/smtp"
	"sort"
	"strings"
	"time"

	"github.com/swanchain/computing-provider-v2/conf"
)

// emailSender delivers an alert as a plain-text message over SMTP.
//
// Two transport styles are both common and an operator should not have to know
// which their provider wants: port 465 expects the connection to be wrapped in
// TLS from the start, everything else expects STARTTLS after the greeting.
type emailSender struct {
	cfg      conf.Email
	nodeName string
	timeout  time.Duration
}

func newEmailSender(cfg conf.Email, nodeName string) *emailSender {
	return &emailSender{cfg: cfg, nodeName: nodeName, timeout: 20 * time.Second}
}

func (s *emailSender) enabled() bool { return s != nil && s.cfg.Enabled() }

// send delivers one event. Errors are returned for the caller to log; email is
// slower and fails in more ways than an HTTP POST — a wrong password, a
// greylisting server, a timeout — and none of that may reach inference.
func (s *emailSender) send(e Event) error {
	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprintf("%d", s.cfg.Port))
	msg := s.compose(e)

	conn, err := s.dial(addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer client.Quit()

	if !s.cfg.ImplicitTLS() {
		// Upgrade opportunistically: a server that does not offer STARTTLS is
		// usually a relay on localhost, which is a legitimate setup.
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: s.cfg.Host}); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		}
	}

	if s.cfg.Username != "" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(s.cfg.Sender()); err != nil {
		return fmt.Errorf("smtp from: %w", err)
	}
	for _, to := range s.cfg.To {
		if err := client.Rcpt(to); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", to, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	return w.Close()
}

func (s *emailSender) dial(addr string) (net.Conn, error) {
	if s.cfg.ImplicitTLS() {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: s.timeout}, "tcp", addr, &tls.Config{ServerName: s.cfg.Host})
		if err != nil {
			return nil, fmt.Errorf("smtp tls dial %s: %w", addr, err)
		}
		return conn, nil
	}
	conn, err := net.DialTimeout("tcp", addr, s.timeout)
	if err != nil {
		return nil, fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	return conn, nil
}

// compose renders the message as multipart/alternative: a styled HTML part and
// a plain-text part carrying the same information.
//
// Both, not just HTML. Alerts are read on phones, in terminals, and by filters;
// an HTML-only operational mail is unreadable in some of those and scores worse
// in others. The text part is the one that must always make sense.
func (s *emailSender) compose(e Event) string {
	node := s.nodeName
	if node == "" {
		node = e.NodeID
	}
	if node == "" {
		node = "computing-provider"
	}

	// The glyph is added in front of the existing format, not instead of it.
	// Operators filter on the severity word, and replacing it would silently
	// break every rule already pointed at these mails.
	subject := fmt.Sprintf("%s [%s] %s: %s", severityMark(e.Severity), node, strings.ToUpper(string(e.Severity)), e.Event)
	if e.ModelID != "" {
		subject += " — " + e.ModelID
	}

	// Fixed rather than random: a boundary is only required to be absent from
	// the body, and the body is escaped or plain text that cannot contain this.
	const boundary = "cp-alert-boundary-8f2a1c"

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", s.cfg.Sender())
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(s.cfg.To, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", e.Timestamp.Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n", boundary)
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	b.WriteString(s.composeText(e, node))

	fmt.Fprintf(&b, "\r\n--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
	b.WriteString(s.composeHTML(e, node))

	fmt.Fprintf(&b, "\r\n--%s--\r\n", boundary)
	return b.String()
}

// severityMark gives the subject line a glyph, so a phone notification shows
// the severity before any of the text.
func severityMark(sev Severity) string {
	switch sev {
	case SeverityCritical:
		return "\u274c" // cross mark
	case SeverityWarning:
		return "\u26a0\ufe0f" // warning sign
	default:
		return "\u2705" // check mark
	}
}

func statusMark(st Status) string {
	switch st {
	case StatusFail:
		return "\u274c"
	case StatusWarn:
		return "\u26a0\ufe0f"
	default:
		return "\u2705"
	}
}

// statusColour is paired with a glyph everywhere it is used. Colour alone
// carries no meaning for a colour-blind reader, and none at all in the text
// part, so it is decoration on top of a symbol rather than the signal itself.
func statusColour(st Status) string {
	switch st {
	case StatusFail:
		return "#b42318"
	case StatusWarn:
		return "#b54708"
	default:
		return "#067647"
	}
}

func (s *emailSender) composeText(e Event, node string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\r\n\r\n", e.Message)

	if len(e.Checks) > 0 {
		b.WriteString("Checks\r\n")
		for _, c := range e.Checks {
			fmt.Fprintf(&b, "  %s  %-32s %s\r\n", statusMark(c.Status), c.Name, c.Message)
		}
		b.WriteString("\r\n")
	}
	if len(e.Models) > 0 {
		b.WriteString("Models\r\n")
		for _, m := range e.Models {
			fmt.Fprintf(&b, "  %s  %-40s %s\r\n", statusMark(m.Status), m.Model, m.Message)
		}
		b.WriteString("\r\n")
	}

	fmt.Fprintf(&b, "Node:      %s\r\n", node)
	if e.NodeID != "" {
		fmt.Fprintf(&b, "Node ID:   %s\r\n", e.NodeID)
	}
	if e.ModelID != "" {
		fmt.Fprintf(&b, "Model:     %s\r\n", e.ModelID)
	}
	fmt.Fprintf(&b, "Event:     %s\r\n", e.Event)
	fmt.Fprintf(&b, "Severity:  %s\r\n", e.Severity)
	fmt.Fprintf(&b, "Time:      %s\r\n", e.Timestamp.Format(time.RFC3339))
	// Sorted: ranging a map put the same details in a different order in every
	// mail, which makes two alerts impossible to compare by eye.
	for _, k := range sortedKeys(e.Details) {
		fmt.Fprintf(&b, "  %s: %s\r\n", k, e.Details[k])
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// composeHTML renders the alert as a self-contained HTML fragment.
//
// Everything interpolated is escaped. Event text is not necessarily written by
// this node: a notice pushed from Swan Inference carries a message and details
// chosen remotely, and rendering those into markup unescaped would put
// attacker-chosen HTML in the operator's mail client. The sanitiser on the
// notice path is the first line of defence; this is the second, because the
// rendering must be safe on its own terms rather than by trusting a caller.
//
// Inline styles only, and a table for layout. Mail clients strip <style>
// blocks, ignore most modern CSS, and Outlook still renders through Word — so
// this is written the way HTML mail has to be written rather than the way a web
// page would be.
func (s *emailSender) composeHTML(e Event, node string) string {
	esc := html.EscapeString
	var b strings.Builder

	b.WriteString(`<div style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;font-size:14px;line-height:1.5;color:#101828;max-width:720px">`)

	fmt.Fprintf(&b, `<div style="border-left:4px solid %s;padding:12px 16px;background:#f9fafb;border-radius:4px">`, statusColour(severityStatus(e.Severity)))
	fmt.Fprintf(&b, `<div style="font-size:16px;font-weight:600;color:%s">%s %s</div>`,
		statusColour(severityStatus(e.Severity)), statusMark(e.Severity2Status()), esc(e.Event))
	fmt.Fprintf(&b, `<div style="margin-top:6px;white-space:pre-wrap">%s</div>`, esc(e.Message))
	b.WriteString(`</div>`)

	if len(e.Checks) > 0 {
		b.WriteString(`<h3 style="font-size:13px;text-transform:uppercase;letter-spacing:.04em;color:#475467;margin:20px 0 6px">Checks</h3>`)
		b.WriteString(`<table cellpadding="0" cellspacing="0" style="width:100%;border-collapse:collapse">`)
		for _, c := range e.Checks {
			row(&b, statusMark(c.Status), statusColour(c.Status), esc(c.Name), esc(c.Message))
		}
		b.WriteString(`</table>`)
	}

	if len(e.Models) > 0 {
		b.WriteString(`<h3 style="font-size:13px;text-transform:uppercase;letter-spacing:.04em;color:#475467;margin:20px 0 6px">Models</h3>`)
		b.WriteString(`<table cellpadding="0" cellspacing="0" style="width:100%;border-collapse:collapse">`)
		for _, m := range e.Models {
			row(&b, statusMark(m.Status), statusColour(m.Status), esc(m.Model), esc(m.Message))
		}
		b.WriteString(`</table>`)
	}

	b.WriteString(`<h3 style="font-size:13px;text-transform:uppercase;letter-spacing:.04em;color:#475467;margin:20px 0 6px">Node</h3>`)
	b.WriteString(`<table cellpadding="0" cellspacing="0" style="border-collapse:collapse;font-size:13px;color:#475467">`)
	meta(&b, "Node", esc(node))
	if e.NodeID != "" {
		short := e.NodeID
		if len(short) > 24 {
			// The full ID is 130 hex characters and wraps into an unreadable
			// block; the prefix is what an operator actually recognises.
			short = short[:24] + "…"
		}
		meta(&b, "Node ID", `<span style="font-family:ui-monospace,SFMono-Regular,Menlo,monospace" title="`+esc(e.NodeID)+`">`+esc(short)+`</span>`)
	}
	if e.ModelID != "" {
		meta(&b, "Model", esc(e.ModelID))
	}
	meta(&b, "Severity", esc(string(e.Severity)))
	meta(&b, "Time", esc(e.Timestamp.Format(time.RFC1123Z)))
	for _, k := range sortedKeys(e.Details) {
		meta(&b, esc(k), esc(e.Details[k]))
	}
	b.WriteString(`</table></div>`)
	return b.String()
}

func row(b *strings.Builder, mark, colour, name, message string) {
	fmt.Fprintf(b, `<tr>`+
		`<td style="padding:6px 8px 6px 0;vertical-align:top;width:20px">%s</td>`+
		`<td style="padding:6px 12px 6px 0;vertical-align:top;font-weight:500;white-space:nowrap">%s</td>`+
		`<td style="padding:6px 0;vertical-align:top;color:%s">%s</td>`+
		`</tr>`, mark, name, colour, message)
}

func meta(b *strings.Builder, k, v string) {
	fmt.Fprintf(b, `<tr><td style="padding:2px 16px 2px 0;color:#98a2b3;white-space:nowrap">%s</td><td style="padding:2px 0">%s</td></tr>`, k, v)
}

// severityStatus maps a severity onto the shared status palette so the banner
// and the rows below it cannot drift into using different colours for the same
// meaning.
func severityStatus(sev Severity) Status {
	switch sev {
	case SeverityCritical:
		return StatusFail
	case SeverityWarning:
		return StatusWarn
	default:
		return StatusPass
	}
}

// Severity2Status is the same mapping, as a method, for use in templates.
func (e Event) Severity2Status() Status { return severityStatus(e.Severity) }
