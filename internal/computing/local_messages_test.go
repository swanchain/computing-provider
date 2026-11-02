package computing

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func messagesGateway(t *testing.T, backend http.HandlerFunc) (*httptest.Server, *InferenceService) {
	t.Helper()
	srv := httptest.NewServer(backend)
	t.Cleanup(srv.Close)
	s := NewInferenceService("test-node", t.TempDir())
	s.modelMappings["anthropic/claude-x"] = ModelMapping{Endpoint: srv.URL, LocalModel: "claude-x", APIKey: "backend-key"}
	s.client = &InferenceClient{metrics: NewInferenceMetrics()}
	gw := httptest.NewServer(s.LocalGatewayHandler())
	t.Cleanup(gw.Close)
	return gw, s
}

func postMessages(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("x-api-key", "caller-key")
	req.Header.Set("Authorization", "Bearer caller-token")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "some-beta")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// oneLocalRequest waits for the request the gateway records after answering.
func oneLocalRequest(t *testing.T, s *InferenceService) RequestMetric {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if page := s.client.Metrics().QueryRequestHistory(RequestHistoryQuery{}); len(page.Requests) > 0 {
			if len(page.Requests) != 1 {
				t.Fatalf("history = %+v", page.Requests)
			}
			return page.Requests[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("nothing recorded")
	panic("unreachable")
}

// A bare model name resolves, the backend gets its own name and key and never
// the caller's credentials, and the answer comes back unchanged.
func TestLocalMessagesForwards(t *testing.T) {
	const answer = `{"id":"msg_1","type":"message","content":[],"usage":{"input_tokens":3,"cache_read_input_tokens":4,"output_tokens":5}}`
	gw, s := messagesGateway(t, func(w http.ResponseWriter, r *http.Request) {
		var got struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		if r.URL.Path != "/v1/messages" || got.Model != "claude-x" {
			t.Errorf("path %s model %q", r.URL.Path, got.Model)
		}
		if r.Header.Get("x-api-key") != "backend-key" || r.Header.Get("Authorization") != "" {
			t.Errorf("credentials: key %q auth %q", r.Header.Get("x-api-key"), r.Header.Get("Authorization"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("anthropic-beta") != "some-beta" {
			t.Errorf("anthropic headers: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("anthropic-ratelimit-tokens-remaining", "9")
		_, _ = io.WriteString(w, answer)
	})
	resp := postMessages(t, gw.URL+"/v1/messages", `{"model":"claude-x","max_tokens":1,"messages":[]}`)
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(got) != answer {
		t.Fatalf("%d %s", resp.StatusCode, got)
	}
	if resp.Header.Get("anthropic-ratelimit-tokens-remaining") != "9" {
		t.Errorf("response headers: %v", resp.Header)
	}
	r := oneLocalRequest(t, s)
	if !r.Success || r.Source != SourceLocal || r.Model != "anthropic/claude-x" || r.TokensIn != 7 || r.TokensOut != 5 {
		t.Errorf("recorded %+v", r)
	}
}

// Each event reaches the client before the backend sends the next, and the
// stream's usage is recorded.
func TestLocalMessagesStreams(t *testing.T) {
	release := make(chan struct{})
	gw, s := messagesGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11,\"output_tokens\":1}}}\n\n")
		f.Flush()
		<-release
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":22}}\n\n")
		f.Flush()
	})
	resp := postMessages(t, gw.URL+"/v1/messages", `{"model":"anthropic/claude-x","stream":true,"messages":[]}`)
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type %q", resp.Header.Get("Content-Type"))
	}
	rd := bufio.NewReader(resp.Body)
	first := make(chan string, 1)
	go func() {
		line, _ := rd.ReadString('\n')
		first <- line
	}()
	select {
	case line := <-first:
		if !strings.HasPrefix(line, "event: message_start") {
			t.Fatalf("first line %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first event was held back")
	}
	close(release)
	if rest, _ := io.ReadAll(rd); !strings.Contains(string(rest), "message_delta") {
		t.Fatalf("rest %q", rest)
	}
	if r := oneLocalRequest(t, s); !r.Success || !r.Streaming || r.TokensIn != 11 || r.TokensOut != 22 {
		t.Errorf("recorded %+v", r)
	}
}

// A backend's refusal keeps its status and body, and counts as a failure.
func TestLocalMessagesBackendError(t *testing.T) {
	const refusal = `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`
	gw, s := messagesGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, refusal)
	})
	resp := postMessages(t, gw.URL+"/v1/messages", `{"model":"claude-x","messages":[]}`)
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 429 || string(got) != refusal || resp.Header.Get("Retry-After") != "7" {
		t.Fatalf("%d %s %v", resp.StatusCode, got, resp.Header)
	}
	if r := oneLocalRequest(t, s); r.Success || !strings.Contains(r.ErrorReason, "429") {
		t.Errorf("recorded %+v", r)
	}
}

// Token counting goes to the backend too, and is not a request worth recording.
func TestLocalMessagesCountTokens(t *testing.T) {
	gw, s := messagesGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/count_tokens" {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"input_tokens":42}`)
	})
	resp := postMessages(t, gw.URL+"/v1/messages/count_tokens", `{"model":"claude-x","messages":[]}`)
	if got, _ := io.ReadAll(resp.Body); string(got) != `{"input_tokens":42}` {
		t.Fatalf("%s", got)
	}
	if page := s.client.Metrics().QueryRequestHistory(RequestHistoryQuery{}); len(page.Requests) != 0 {
		t.Errorf("recorded %+v", page.Requests)
	}
}

// Errors the gateway raises itself are in the Messages API's shape.
func TestLocalMessagesOwnErrors(t *testing.T) {
	gw, _ := messagesGateway(t, func(w http.ResponseWriter, r *http.Request) { t.Error("reached the backend") })
	for _, c := range []struct {
		body   string
		status int
		typ    string
	}{
		{`{"model":"no-such-model"}`, 404, "not_found_error"},
		{`not json`, 400, "invalid_request_error"},
		{`{"messages":[]}`, 400, "invalid_request_error"},
	} {
		resp := postMessages(t, gw.URL+"/v1/messages", c.body)
		var out struct {
			Type  string `json:"type"`
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode != c.status || out.Type != "error" || out.Error.Type != c.typ {
			t.Errorf("%s: %d %+v", c.body, resp.StatusCode, out)
		}
	}
	resp, err := http.Get(gw.URL + "/v1/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", resp.StatusCode)
	}
}
