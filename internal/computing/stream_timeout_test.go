package computing

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A streaming request is bounded by how long the backend takes to start and by
// how long it then goes quiet, never by its total length: a long generation
// that keeps producing tokens is a healthy one.

func shortStreamTimeouts(t *testing.T, header, idle time.Duration) {
	t.Helper()
	oldHeader, oldIdle := streamResponseHeaderTimeout, streamIdleTimeout
	streamResponseHeaderTimeout, streamIdleTimeout = header, idle
	t.Cleanup(func() { streamResponseHeaderTimeout, streamIdleTimeout = oldHeader, oldIdle })
}

// sseBackend sends n chunks gap apart, then stalls for stallAfter if set, then
// ends the stream.
func sseBackend(t *testing.T, headerDelay time.Duration, n int, gap time.Duration, stall time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		sleep := func(d time.Duration) bool {
			select {
			case <-time.After(d):
				return true
			case <-r.Context().Done():
				return false
			}
		}
		if !sleep(headerDelay) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fl.Flush()
		for i := 0; i < n; i++ {
			if !sleep(gap) {
				return
			}
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"t%d\"}}]}\n\n", i)
			fl.Flush()
		}
		if stall > 0 && !sleep(stall) {
			return
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runStream(t *testing.T, endpoint string) (*StreamResult, int) {
	t.Helper()
	s := &InferenceService{}
	chunks := 0
	res := s.streamFromDockerModelContext(context.Background(), endpoint,
		[]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`), "m", "", "",
		func(chunk []byte, done bool) error {
			if !done {
				chunks++
			}
			return nil
		})
	return res, chunks
}

func TestLongStreamThatKeepsSendingIsNotCutOff(t *testing.T) {
	shortStreamTimeouts(t, 200*time.Millisecond, 200*time.Millisecond)
	// 15 chunks 50ms apart: the stream runs well past both limits in total.
	srv := sseBackend(t, 0, 15, 50*time.Millisecond, 0)

	res, chunks := runStream(t, srv.URL)
	if res.Error != nil {
		t.Fatalf("healthy long stream failed: %v", res.Error)
	}
	if chunks != 15 {
		t.Errorf("forwarded %d chunks, want 15", chunks)
	}
}

func TestStreamThatGoesQuietIsAbandoned(t *testing.T) {
	shortStreamTimeouts(t, time.Second, 150*time.Millisecond)
	srv := sseBackend(t, 0, 2, 10*time.Millisecond, 10*time.Second)

	start := time.Now()
	res, chunks := runStream(t, srv.URL)
	if res.Error == nil || !strings.Contains(res.Error.Error(), "no data from backend") {
		t.Fatalf("error = %v, want an idle-timeout error", res.Error)
	}
	if chunks != 2 {
		t.Errorf("forwarded %d chunks before stalling, want 2", chunks)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("took %s to give up on a stalled stream", took)
	}
}

func TestBackendThatNeverAnswersIsAbandoned(t *testing.T) {
	shortStreamTimeouts(t, 150*time.Millisecond, time.Second)
	srv := sseBackend(t, 10*time.Second, 1, 0, 0)

	res, _ := runStream(t, srv.URL)
	if res.Error == nil || !strings.Contains(res.Error.Error(), "no response within") {
		t.Fatalf("error = %v, want a response-timeout error", res.Error)
	}
}

func TestSlowFirstTokenWithinTheStartLimitIsServed(t *testing.T) {
	// Waiting for the first data is governed by the start limit, not the
	// shorter idle one, whether the backend holds back its headers or sends
	// them at once and the first token only after a long prefill.
	shortStreamTimeouts(t, time.Second, 100*time.Millisecond)
	for name, srv := range map[string]*httptest.Server{
		"late headers": sseBackend(t, 300*time.Millisecond, 3, 10*time.Millisecond, 0),
		"late token":   sseBackend(t, 0, 1, 300*time.Millisecond, 0),
	} {
		res, chunks := runStream(t, srv.URL)
		if res.Error != nil {
			t.Errorf("%s: slow-to-start stream failed: %v", name, res.Error)
		} else if chunks == 0 {
			t.Errorf("%s: no chunks forwarded", name)
		}
	}
}
