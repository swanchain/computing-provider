package alerts

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/swanchain/computing-provider-v2/conf"
)

// fakeSMTP speaks just enough SMTP to accept one message and record it, so
// delivery is exercised end to end rather than mocked at the boundary.
type fakeSMTP struct {
	ln       net.Listener
	mu       sync.Mutex
	received []string
	from     string
	rcpt     []string
	authSeen bool
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSMTP{ln: ln}
	go s.serve()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *fakeSMTP) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *fakeSMTP) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSMTP) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	w := func(format string, a ...interface{}) { fmt.Fprintf(conn, format+"\r\n", a...) }

	w("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			// No STARTTLS advertised: the sender must proceed unencrypted
			// rather than fail, which is the localhost-relay case.
			w("250-fake")
			w("250 AUTH PLAIN")
		case strings.HasPrefix(cmd, "HELO"):
			w("250 fake")
		case strings.HasPrefix(cmd, "AUTH"):
			s.mu.Lock()
			s.authSeen = true
			s.mu.Unlock()
			w("235 ok")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			s.mu.Lock()
			s.from = strings.TrimSpace(line)
			s.mu.Unlock()
			w("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO"):
			s.mu.Lock()
			s.rcpt = append(s.rcpt, strings.TrimSpace(line))
			s.mu.Unlock()
			w("250 ok")
		case strings.HasPrefix(cmd, "DATA"):
			w("354 go ahead")
			var body strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimSpace(l) == "." {
					break
				}
				body.WriteString(l)
			}
			s.mu.Lock()
			s.received = append(s.received, body.String())
			s.mu.Unlock()
			w("250 queued")
		case strings.HasPrefix(cmd, "QUIT"):
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func (s *fakeSMTP) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.received...)
}

func testEmailCfg(port int) conf.Email {
	return conf.Email{
		Host:     "127.0.0.1",
		Port:     port,
		Username: "node@example.com",
		Password: "secret",
		From:     "node@example.com",
		To:       []string{"ops@example.com", "oncall@example.com"},
	}
}

func TestEmailDelivery(t *testing.T) {
	srv := startFakeSMTP(t)
	s := newEmailSender(testEmailCfg(srv.port()), "cp-01")

	err := s.send(Event{
		Event:     EventModelUnhealthy,
		Severity:  SeverityCritical,
		NodeID:    "node-abc",
		ModelID:   "org/model-a",
		Message:   "org/model-a is unhealthy",
		Details:   map[string]string{"health": "unhealthy"},
		Timestamp: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	msgs := srv.messages()
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	m := msgs[0]

	// The subject has to be triageable without opening the mail.
	if !strings.Contains(m, "Subject: [cp-01] CRITICAL: model_unhealthy — org/model-a") {
		t.Errorf("subject wrong; message was:\n%s", m)
	}
	for _, want := range []string{"org/model-a is unhealthy", "Node:      cp-01", "Node ID:   node-abc", "health: unhealthy"} {
		if !strings.Contains(m, want) {
			t.Errorf("body missing %q", want)
		}
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.rcpt) != 2 {
		t.Errorf("got %d recipients, want 2: %v", len(srv.rcpt), srv.rcpt)
	}
	if !srv.authSeen {
		t.Error("expected the sender to authenticate")
	}
}

// A server that does not advertise STARTTLS must still work: an unauthenticated
// localhost relay is a legitimate setup, and failing there would be surprising.
func TestEmailWorksWithoutStartTLS(t *testing.T) {
	srv := startFakeSMTP(t)
	cfg := testEmailCfg(srv.port())
	cfg.Username = "" // no auth either
	s := newEmailSender(cfg, "cp-01")

	if err := s.send(Event{Event: "test", Severity: SeverityInfo, Message: "hello", Timestamp: time.Now()}); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if len(srv.messages()) != 1 {
		t.Fatal("message not delivered")
	}
}

func TestEmailDisabledWithoutHostOrRecipients(t *testing.T) {
	for name, cfg := range map[string]conf.Email{
		"no host":       {Port: 587, To: []string{"a@b.c"}},
		"no recipients": {Host: "smtp.example.com", Port: 587},
		"empty":         {},
	} {
		if newEmailSender(cfg, "cp").enabled() {
			t.Errorf("%s: expected disabled", name)
		}
	}
}

// A broken mail server must surface as an error to log, never as a panic or a
// hang that could reach the inference path.
func TestEmailFailureIsReturnedNotFatal(t *testing.T) {
	cfg := testEmailCfg(1) // port 1: refused
	s := newEmailSender(cfg, "cp-01")
	s.timeout = 2 * time.Second

	err := s.send(Event{Event: "test", Message: "x", Timestamp: time.Now()})
	if err == nil {
		t.Fatal("expected an error from an unreachable server")
	}
	if !strings.Contains(err.Error(), "smtp dial") {
		t.Errorf("error should name the failing step, got %v", err)
	}
}

func TestNotifierUsesEmailWithoutWebhook(t *testing.T) {
	srv := startFakeSMTP(t)
	cfg := conf.Alerts{Email: testEmailCfg(srv.port()), CooldownMinutes: 15}
	n := New(cfg, "node-abc", "cp-01")

	if !n.Enabled() {
		t.Fatal("a notifier with only email configured should be enabled")
	}
	if err := n.SendTest("hello from the test"); err != nil {
		t.Fatalf("SendTest failed: %v", err)
	}
	msgs := srv.messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0], "hello from the test") {
		t.Fatalf("test message not delivered: %v", msgs)
	}
}

func TestImplicitTLSSelectedByPort(t *testing.T) {
	if !(conf.Email{Port: 465}).ImplicitTLS() {
		t.Error("port 465 should use implicit TLS")
	}
	if (conf.Email{Port: 587}).ImplicitTLS() {
		t.Error("port 587 should use STARTTLS, not implicit TLS")
	}
}

func TestSenderFallsBackToUsername(t *testing.T) {
	if got := (conf.Email{Username: "u@example.com"}).Sender(); got != "u@example.com" {
		t.Errorf("Sender() = %q, want the username", got)
	}
	if got := (conf.Email{Username: "u@example.com", From: "f@example.com"}).Sender(); got != "f@example.com" {
		t.Errorf("Sender() = %q, want From to win", got)
	}
}
