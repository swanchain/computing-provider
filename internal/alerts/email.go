package alerts

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
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

// compose renders the message. The subject carries node, severity and event so
// an operator can filter and triage without opening it.
func (s *emailSender) compose(e Event) string {
	node := s.nodeName
	if node == "" {
		node = e.NodeID
	}
	if node == "" {
		node = "computing-provider"
	}

	subject := fmt.Sprintf("[%s] %s: %s", node, strings.ToUpper(string(e.Severity)), e.Event)
	if e.ModelID != "" {
		subject += " — " + e.ModelID
	}

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", s.cfg.Sender())
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(s.cfg.To, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", e.Timestamp.Format(time.RFC1123Z))
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "%s\r\n\r\n", e.Message)
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
	for k, v := range e.Details {
		fmt.Fprintf(&b, "  %s: %s\r\n", k, v)
	}
	return b.String()
}
