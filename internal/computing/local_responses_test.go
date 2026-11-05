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

func responsesGateway(t *testing.T, backend http.HandlerFunc) (*httptest.Server, *InferenceService) {
	t.Helper()
	srv := httptest.NewServer(backend)
	t.Cleanup(srv.Close)
	s := NewInferenceService("test-node", t.TempDir())
	s.modelMappings["openai/gpt-x"] = ModelMapping{Endpoint: srv.URL, LocalModel: "gpt-x", APIKey: "backend-key"}
	s.client = &InferenceClient{metrics: NewInferenceMetrics()}
	gw := httptest.NewServer(s.LocalGatewayHandler())
	t.Cleanup(gw.Close)
	return gw, s
}

func postResponses(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer caller-token")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("originator", "codex_cli_rs")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// A bare model name resolves to openai/, the backend gets its own name and
// key as a bearer token and never the caller's, and the answer comes back
// unchanged with its usage recorded.
func TestLocalResponsesForwards(t *testing.T) {
	const answer = `{"id":"resp_1","object":"response","output":[],"usage":{"input_tokens":7,"input_tokens_details":{"cached_tokens":4},"output_tokens":5}}`
	gw, s := responsesGateway(t, func(w http.ResponseWriter, r *http.Request) {
		var got struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		if r.URL.Path != "/v1/responses" || got.Model != "gpt-x" {
			t.Errorf("path %s model %q", r.URL.Path, got.Model)
		}
		if r.Header.Get("Authorization") != "Bearer backend-key" || r.Header.Get("x-api-key") != "" {
			t.Errorf("credentials: auth %q key %q", r.Header.Get("Authorization"), r.Header.Get("x-api-key"))
		}
		if r.Header.Get("OpenAI-Beta") != "responses=experimental" || r.Header.Get("originator") != "codex_cli_rs" {
			t.Errorf("client headers: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-request-id", "req_9")
		_, _ = io.WriteString(w, answer)
	})
	resp := postResponses(t, gw.URL+"/v1/responses", `{"model":"gpt-x","input":[]}`)
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(got) != answer {
		t.Fatalf("%d %s", resp.StatusCode, got)
	}
	if resp.Header.Get("x-request-id") != "req_9" {
		t.Errorf("response headers: %v", resp.Header)
	}
	r := oneLocalRequest(t, s)
	if !r.Success || r.Source != SourceLocal || r.Model != "openai/gpt-x" || r.TokensIn != 7 || r.TokensOut != 5 {
		t.Errorf("recorded %+v", r)
	}
}

// Each event reaches the client before the backend sends the next, and the
// usage on response.completed is recorded.
func TestLocalResponsesStreams(t *testing.T) {
	release := make(chan struct{})
	gw, s := responsesGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"usage\":null}}\n\n")
		f.Flush()
		<-release
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":11,\"output_tokens\":22}}}\n\n")
		f.Flush()
	})
	resp := postResponses(t, gw.URL+"/v1/responses", `{"model":"openai/gpt-x","stream":true,"input":[]}`)
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
		if !strings.HasPrefix(line, "event: response.created") {
			t.Fatalf("first line %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first event was held back")
	}
	close(release)
	if rest, _ := io.ReadAll(rd); !strings.Contains(string(rest), "response.completed") {
		t.Fatalf("rest %q", rest)
	}
	if r := oneLocalRequest(t, s); !r.Success || !r.Streaming || r.TokensIn != 11 || r.TokensOut != 22 {
		t.Errorf("recorded %+v", r)
	}
}

// A backend's refusal keeps its status and body, and counts as a failure.
func TestLocalResponsesBackendError(t *testing.T) {
	const refusal = `{"error":{"message":"slow down","type":"rate_limit_error"}}`
	gw, s := responsesGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, refusal)
	})
	resp := postResponses(t, gw.URL+"/v1/responses", `{"model":"gpt-x","input":[]}`)
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 429 || string(got) != refusal || resp.Header.Get("Retry-After") != "7" {
		t.Fatalf("%d %s %v", resp.StatusCode, got, resp.Header)
	}
	if r := oneLocalRequest(t, s); r.Success || !strings.Contains(r.ErrorReason, "429") {
		t.Errorf("recorded %+v", r)
	}
}

// Errors the gateway raises itself are in the OpenAI error shape.
func TestLocalResponsesOwnErrors(t *testing.T) {
	gw, _ := responsesGateway(t, func(w http.ResponseWriter, r *http.Request) { t.Error("reached the backend") })
	for _, c := range []struct {
		body   string
		status int
	}{
		{`{"model":"no-such-model"}`, 404},
		{`not json`, 400},
		{`{"input":[]}`, 400},
	} {
		resp := postResponses(t, gw.URL+"/v1/responses", c.body)
		var out struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode != c.status || out.Error.Type != "invalid_request_error" || out.Error.Message == "" {
			t.Errorf("%s: %d %+v", c.body, resp.StatusCode, out)
		}
	}
	resp, err := http.Get(gw.URL + "/v1/responses")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", resp.StatusCode)
	}
}
