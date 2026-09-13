package runtime

import (
	"context"
	"strings"
	"testing"
)

// recorder captures announcements instead of mailing them.
type recorder struct{ events []SwitchEvent }

func (r *recorder) AnnounceSwitch(e SwitchEvent) { r.events = append(r.events, e) }

func TestServeAnnouncesWithTheReason(t *testing.T) {
	rec := &recorder{}
	docker := &fakeDocker{runOutput: "abc\n"}
	m := newTestManager(t, docker, t.TempDir())
	m.Announcer = rec

	if _, err := m.Serve(context.Background(), &ServeSpec{
		ModelID: "a/Model", Backend: "vllm", Weights: t.TempDir(), Port: 30000,
	}, ServeOptions{
		Reason:  "demand up 40%, two providers online",
		Decider: DeciderAutoSwitch,
	}); err != nil {
		t.Fatal(err)
	}

	if len(rec.events) != 1 {
		t.Fatalf("got %d announcements, want 1", len(rec.events))
	}
	e := rec.events[0]
	if e.Action != SwitchStarted {
		t.Errorf("action = %q", e.Action)
	}
	if e.ModelID != "a/Model" {
		t.Errorf("model = %q", e.ModelID)
	}
	if e.Reason != "demand up 40%, two providers online" {
		t.Errorf("the reason was not carried through: %q", e.Reason)
	}
	if e.Decider != DeciderAutoSwitch {
		t.Errorf("decider = %q", e.Decider)
	}
	if e.Endpoint != "http://localhost:30000" {
		t.Errorf("endpoint = %q", e.Endpoint)
	}
}

func TestStopAnnouncesWithTheReason(t *testing.T) {
	rec := &recorder{}
	docker := &fakeDocker{containers: []container{
		managed("a/Model", "vllm", ContainerName("a/Model"), 30000, true),
	}}
	m := newTestManager(t, docker, t.TempDir())
	m.Announcer = rec

	if _, err := m.Stop(context.Background(), "a/Model", StopOptions{
		Reason:  "replaced by b/Model",
		Decider: DeciderAutoSwitch,
	}); err != nil {
		t.Fatal(err)
	}

	if len(rec.events) != 1 {
		t.Fatalf("got %d announcements, want 1", len(rec.events))
	}
	if rec.events[0].Action != SwitchStopped {
		t.Errorf("action = %q", rec.events[0].Action)
	}
	if rec.events[0].Reason != "replaced by b/Model" {
		t.Errorf("reason = %q", rec.events[0].Reason)
	}
}

// Mailing the operator about a model that then failed to load would be worse
// than not mailing at all: they would go looking for a model that is not there.
func TestNoAnnouncementWhenTheModelNeverBecomesReady(t *testing.T) {
	rec := &recorder{}
	docker := &fakeDocker{runOutput: "abc\n"}
	m := newTestManager(t, docker, t.TempDir())
	m.Announcer = rec
	m.ReadyTimeout = 1

	_, err := m.Serve(context.Background(), &ServeSpec{
		ModelID: "a/Model", Backend: "vllm", Weights: t.TempDir(), Port: 30000,
	}, ServeOptions{WaitReady: true, Reason: "r"})

	if err == nil {
		t.Fatal("expected a readiness failure")
	}
	if len(rec.events) != 0 {
		t.Errorf("announced a model that never loaded: %+v", rec.events)
	}
}

// A refused serve must not announce either.
func TestNoAnnouncementWhenServeIsRefused(t *testing.T) {
	rec := &recorder{}
	docker := &fakeDocker{containers: []container{
		managed("a/Model", "vllm", ContainerName("a/Model"), 30000, true),
	}}
	m := newTestManager(t, docker, t.TempDir())
	m.Announcer = rec

	if _, err := m.Serve(context.Background(), &ServeSpec{
		ModelID: "a/Model", Backend: "vllm", Weights: t.TempDir(),
	}, ServeOptions{}); err == nil {
		t.Fatal("expected a refusal")
	}
	if len(rec.events) != 0 {
		t.Errorf("announced a refused switch: %+v", rec.events)
	}
}

// A node with no [Alerts] transports must cost nothing and crash nothing.
func TestANilAnnouncerIsSafe(t *testing.T) {
	m := newTestManager(t, &fakeDocker{runOutput: "abc\n"}, t.TempDir())
	m.Announcer = nil

	if _, err := m.Serve(context.Background(), &ServeSpec{
		ModelID: "a/Model", Backend: "vllm", Weights: t.TempDir(), Port: 30000,
	}, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
}

// NewAlertAnnouncer returns nil for an unconfigured Notifier, and a nil
// *AlertAnnouncer must still be safe to call through the interface.
func TestAlertAnnouncerIsInertWithoutTransports(t *testing.T) {
	if a := NewAlertAnnouncer(nil); a != nil {
		t.Error("an announcer was built from a nil notifier")
	}

	var a *AlertAnnouncer
	a.AnnounceSwitch(SwitchEvent{Action: SwitchStarted, ModelID: "a/Model"})
}

// An operator who gave no reason must still get a message that reads sensibly,
// and one that tells them how to record a reason next time.
func TestReasonDefaultsExplainThemselves(t *testing.T) {
	operator := reasonOrDefault("", DeciderOperator)
	if !strings.Contains(operator, "--reason") {
		t.Errorf("operator default should name the flag: %q", operator)
	}

	auto := reasonOrDefault("", DeciderAutoSwitch)
	if strings.Contains(auto, "--reason") {
		t.Errorf("the scheduler has no flag to pass: %q", auto)
	}

	if got := reasonOrDefault("because", DeciderOperator); got != "because" {
		t.Errorf("a given reason was replaced: %q", got)
	}
}
