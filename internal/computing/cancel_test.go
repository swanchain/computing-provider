package computing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// When Swan Inference abandons a request it sends "cancel". The provider must
// stop the backend: an answer nobody reads still holds a slot that paying
// work queues behind, and a long generation can hold it for minutes.

func cancelMsg(t *testing.T, requestID, reason string) Message {
	t.Helper()
	b, _ := json.Marshal(CancelPayload{RequestID: requestID, Reason: reason})
	return Message{Type: MsgTypeCancel, Payload: b}
}

func TestCancelMessageCancelsTheInFlightRequest(t *testing.T) {
	c := &InferenceClient{metrics: NewInferenceMetrics()}
	ctx, done := c.beginRequest("r1")
	defer done()
	other, doneOther := c.beginRequest("r2")
	defer doneOther()

	c.handleMessage(cancelMsg(t, "r1", "attempt_timeout"))

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("the cancelled request's context is still live")
	}
	if c.cancelReason("r1") != "attempt_timeout" {
		t.Errorf("reason = %q", c.cancelReason("r1"))
	}
	if other.Err() != nil {
		t.Error("cancelling one request cancelled another")
	}
	// A cancel that races the request finishing is not an error.
	c.handleMessage(cancelMsg(t, "never-existed", "late"))
}

// slowBackend answers one request at a time and reports whether the caller
// went away before it finished.
func slowBackend(t *testing.T, stream bool) (*httptest.Server, *atomic.Bool, *atomic.Int32) {
	t.Helper()
	var aborted atomic.Bool
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		// Read the request first, as a real server does. Go's server only
		// notices a client hanging up once the body has been consumed.
		io.Copy(io.Discard, r.Body)
		if !stream {
			select {
			case <-time.After(10 * time.Second):
				fmt.Fprint(w, `{"choices":[{"message":{"content":"late"}}]}`)
			case <-r.Context().Done():
				aborted.Store(true)
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for i := 0; i < 200; i++ {
			select {
			case <-r.Context().Done():
				aborted.Store(true)
				return
			case <-time.After(50 * time.Millisecond):
			}
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"t%d\"}}]}\n\n", i)
			fl.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv, &aborted, &hits
}

func TestCancelStopsAStreamingBackend(t *testing.T) {
	srv, aborted, _ := slowBackend(t, true)
	s := NewInferenceService("test-node", t.TempDir())
	s.modelMappings["m"] = ModelMapping{Endpoint: srv.URL}

	ctx, cancel := context.WithCancel(context.Background())
	chunks := make(chan struct{}, 300)
	result := make(chan *StreamResult, 1)
	start := time.Now()
	go func() {
		result <- s.handleStreamingInference(ctx, "r1", InferencePayload{ModelID: "m", Request: json.RawMessage(`{"model":"m","messages":[]}`), Stream: true},
			func(chunk []byte, done bool) error { chunks <- struct{}{}; return nil })
	}()
	<-chunks
	<-chunks
	cancel()

	select {
	case r := <-result:
		if r.Error == nil {
			t.Error("a cancelled stream reported success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stream kept running after cancel")
	}
	if time.Since(start) > 3*time.Second {
		t.Error("cancel took too long to take effect")
	}
	deadline := time.Now().Add(time.Second)
	for !aborted.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !aborted.Load() {
		t.Error("the backend never saw the connection close, so it would keep generating")
	}
}

func TestCancelStopsANonStreamingBackendWithoutRetrying(t *testing.T) {
	srv, aborted, hits := slowBackend(t, false)
	s := NewInferenceService("test-node", t.TempDir())
	s.modelMappings["m"] = ModelMapping{Endpoint: srv.URL}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)
	start := time.Now()
	_, err := s.handleInference(ctx, InferencePayload{ModelID: "m", Request: json.RawMessage(`{"model":"m","messages":[]}`)})
	if err == nil {
		t.Fatal("a cancelled request reported success")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("returned after %v; cancel should end it almost at once", time.Since(start))
	}
	time.Sleep(100 * time.Millisecond)
	if !aborted.Load() {
		t.Error("the backend never saw the request abandoned")
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("backend hit %d times; a cancelled request must not be retried", n)
	}
}

func TestCancelledRequestSendsNothingBackAndIsRecorded(t *testing.T) {
	c := &InferenceClient{metrics: NewInferenceMetrics(), send: make(chan []byte, 8)}
	started := make(chan struct{})
	c.SetInferenceHandler(func(ctx context.Context, p InferencePayload) (*InferenceResponse, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	finished := make(chan struct{})
	go func() {
		c.handleInference("r1", InferencePayload{ModelID: "m"})
		close(finished)
	}()
	<-started
	c.handleMessage(cancelMsg(t, "r1", "consumer_disconnected"))

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("the request did not end after cancel")
	}
	if len(c.send) != 0 {
		t.Error("a response was sent for a request Swan Inference had abandoned")
	}
	page := c.metrics.QueryRequestHistory(RequestHistoryQuery{})
	if len(page.Requests) != 1 || page.Requests[0].Success || page.Requests[0].ErrorReason != "cancelled by Swan Inference: consumer_disconnected" {
		t.Errorf("history = %+v", page.Requests)
	}
	if c.cancelReason("r1") != "" {
		t.Error("the finished request is still registered as in flight")
	}
}

// A local client that disconnects stops its generation too.
func TestLocalGatewayClientDisconnectStopsTheBackend(t *testing.T) {
	srv, aborted, _ := slowBackend(t, false)
	s := NewInferenceService("test-node", t.TempDir())
	s.modelMappings["m"] = ModelMapping{Endpoint: srv.URL}
	s.client = &InferenceClient{metrics: NewInferenceMetrics()}
	gw := httptest.NewServer(s.LocalGatewayHandler())
	defer gw.Close()

	client := &http.Client{Timeout: 200 * time.Millisecond}
	if resp, err := client.Post(gw.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"m","messages":[]}`)); err == nil {
		resp.Body.Close()
		t.Fatal("expected the client to give up before the slow backend answered")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !aborted.Load() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !aborted.Load() {
		t.Error("the backend kept generating after the local client went away")
	}
}
