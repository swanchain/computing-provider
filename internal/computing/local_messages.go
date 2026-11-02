package computing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// The Anthropic Messages API on the local gateway, for clients that speak only
// that (Claude Code among them). The request and the response pass through
// unchanged apart from the model name and the backend's key, so this works
// only for a backend that serves /v1/messages itself; any other backend's
// answer reaches the client as it is.

// localMessagesUsageCap bounds how much of one line is kept to read usage from.
// A longer line is still forwarded; only its token counts are lost.
const localMessagesUsageCap = 8 << 20

func (s *InferenceService) serveLocalMessages(w http.ResponseWriter, r *http.Request) {
	s.forwardLocalMessages(w, r, "/v1/messages", true)
}

func (s *InferenceService) serveLocalCountTokens(w http.ResponseWriter, r *http.Request) {
	s.forwardLocalMessages(w, r, "/v1/messages/count_tokens", false)
}

func (s *InferenceService) forwardLocalMessages(w http.ResponseWriter, r *http.Request, path string, record bool) {
	if r.Method != http.MethodPost {
		writeMessagesError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, localGatewayMaxBody+1))
	if err != nil {
		writeMessagesError(w, http.StatusBadRequest, "could not read request body")
		return
	}
	if len(body) > localGatewayMaxBody {
		writeMessagesError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	var head struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &head); err != nil || head.Model == "" {
		writeMessagesError(w, http.StatusBadRequest, "request must be JSON with a model field")
		return
	}
	modelID, endpoint, localModel, apiKey, mse := s.resolveMessagesModel(head.Model)
	if mse != nil {
		writeMessagesError(w, mse.StatusCode, mse.Message)
		return
	}
	forward := func(rec *RequestMetric) {
		s.proxyMessages(w, r, endpoint+path, s.substituteModelName(body, modelID, localModel), apiKey, rec)
	}
	if !record {
		forward(&RequestMetric{})
		return
	}
	s.recordLocal(r, modelID, func(rec *RequestMetric) {
		setLocalRoute(rec, endpoint, modelID, localModel)
		var stream struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(body, &stream)
		rec.Streaming = stream.Stream
		forward(rec)
	})
}

// resolveMessagesModel accepts the node's model id or the bare name Anthropic
// clients send: claude-sonnet-5-5 for anthropic/claude-sonnet-5-5.
func (s *InferenceService) resolveMessagesModel(name string) (modelID, endpoint, localModel, apiKey string, mse *ModelServerError) {
	endpoint, localModel, apiKey, mse = s.resolveGatewayEndpoint(name)
	if mse == nil {
		return name, endpoint, localModel, apiKey, nil
	}
	if !strings.Contains(name, "/") {
		full := "anthropic/" + name
		if ep, lm, key, fullErr := s.resolveGatewayEndpoint(full); fullErr == nil {
			return full, ep, lm, key, nil
		}
	}
	return name, "", "", "", mse
}

func (s *InferenceService) proxyMessages(w http.ResponseWriter, r *http.Request, url string, body []byte, apiKey string, rec *RequestMetric) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		rec.ErrorReason = err.Error()
		writeMessagesError(w, http.StatusBadGateway, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// The caller's own credentials stay here: the backend gets the node's key.
	for name, values := range r.Header {
		if strings.HasPrefix(strings.ToLower(name), "anthropic-") || strings.EqualFold(name, "Accept") {
			req.Header[name] = values
		}
	}
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}
	resp, err := streamingHttpClient.Do(req)
	if err != nil {
		rec.ErrorReason = err.Error()
		writeMessagesError(w, http.StatusBadGateway, "backend unreachable")
		return
	}
	defer resp.Body.Close()

	for _, name := range []string{"Content-Type", "Cache-Control", "Retry-After", "Request-Id"} {
		if v := resp.Header.Get(name); v != "" {
			w.Header().Set(name, v)
		}
	}
	for name, values := range resp.Header {
		if strings.HasPrefix(strings.ToLower(name), "anthropic-") {
			w.Header()[name] = values
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}

	var usage messagesUsage
	buf := make([]byte, 32<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			usage.feed(buf[:n])
			if _, err := w.Write(buf[:n]); err != nil {
				rec.ErrorReason = err.Error()
				break
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			rec.ErrorReason = readErr.Error()
			break
		}
	}
	usage.finish()
	rec.TokensIn, rec.TokensOut = usage.in, usage.out
	if resp.StatusCode >= 400 {
		rec.ErrorReason = fmt.Sprintf("backend returned %d", resp.StatusCode)
	}
	rec.Success = rec.ErrorReason == ""
}

// messagesUsage reads token counts out of a Messages response as it passes,
// from one JSON body or from the event stream's message_start and
// message_delta.
type messagesUsage struct {
	line     []byte
	overflow bool
	in, out  int
}

func (u *messagesUsage) feed(p []byte) {
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		if end < 0 {
			u.keep(p)
			return
		}
		u.keep(p[:end])
		u.finish()
		p = p[end+1:]
	}
}

func (u *messagesUsage) keep(p []byte) {
	if len(u.line)+len(p) > localMessagesUsageCap {
		u.line, u.overflow = nil, true
	}
	if !u.overflow {
		u.line = append(u.line, p...)
	}
}

func (u *messagesUsage) finish() {
	line := bytes.TrimPrefix(bytes.TrimSpace(u.line), []byte("data:"))
	u.line, u.overflow = u.line[:0], false
	if !bytes.Contains(line, []byte(`"usage"`)) {
		return
	}
	type counts struct {
		Input         int `json:"input_tokens"`
		Output        int `json:"output_tokens"`
		CacheRead     int `json:"cache_read_input_tokens"`
		CacheCreation int `json:"cache_creation_input_tokens"`
	}
	var event struct {
		Usage   *counts `json:"usage"`
		Message struct {
			Usage *counts `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &event) != nil {
		return
	}
	c := event.Usage
	if c == nil {
		c = event.Message.Usage
	}
	if c == nil {
		return
	}
	if in := c.Input + c.CacheRead + c.CacheCreation; in > 0 {
		u.in = in
	}
	if c.Output > u.out {
		u.out = c.Output
	}
}

func writeMessagesError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	typ := "api_error"
	switch {
	case status == http.StatusNotFound:
		typ = "not_found_error"
	case status < 500:
		typ = "invalid_request_error"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":  "error",
		"error": map[string]string{"type": typ, "message": strings.TrimSpace(msg)},
	})
}
