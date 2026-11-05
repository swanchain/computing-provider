package computing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// The Anthropic Messages API and the OpenAI Responses API on the local
// gateway, for clients that speak only one of them (Claude Code and Codex
// among them). The request and the response pass through unchanged apart from
// the model name and the backend's key, so this works only for a backend that
// serves the route itself; any other backend's answer reaches the client as it
// is.

// localPassthroughUsageCap bounds how much of one line is kept to read usage
// from. A longer line is still forwarded; only its token counts are lost.
const localPassthroughUsageCap = 8 << 20

// passthroughWire is what differs between the APIs passed through: how the
// backend's key is sent, which headers travel with the request and the
// answer, how a bare model name is completed, how usage is read, and the
// shape of the gateway's own errors.
type passthroughWire struct {
	// vendor completes a bare model name: claude-x for anthropic/claude-x.
	vendor string
	// headerPrefix names the client headers forwarded (anthropic-, openai-)
	// and the backend headers returned.
	headerPrefix string
	// extraRequestHeaders are forwarded as they are, besides Accept.
	extraRequestHeaders []string
	setKey              func(req *http.Request, key string)
	newUsage            func() usageReader
	writeError          func(w http.ResponseWriter, status int, msg string)
}

// usageReader reads token counts out of a response as it passes.
type usageReader interface {
	feed(p []byte)
	finish()
	counts() (in, out int)
}

var messagesWire = passthroughWire{
	vendor:              "anthropic",
	headerPrefix:        "anthropic-",
	setKey:              func(req *http.Request, key string) { req.Header.Set("x-api-key", key) },
	newUsage:            func() usageReader { return &messagesUsage{} },
	writeError:          writeMessagesError,
	extraRequestHeaders: nil,
}

func (s *InferenceService) serveLocalMessages(w http.ResponseWriter, r *http.Request) {
	s.forwardLocalPassthrough(w, r, messagesWire, "/v1/messages", true)
}

func (s *InferenceService) serveLocalCountTokens(w http.ResponseWriter, r *http.Request) {
	s.forwardLocalPassthrough(w, r, messagesWire, "/v1/messages/count_tokens", false)
}

func (s *InferenceService) forwardLocalPassthrough(w http.ResponseWriter, r *http.Request, wire passthroughWire, path string, record bool) {
	if r.Method != http.MethodPost {
		wire.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, localGatewayMaxBody+1))
	if err != nil {
		wire.writeError(w, http.StatusBadRequest, "could not read request body")
		return
	}
	if len(body) > localGatewayMaxBody {
		wire.writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	var head struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &head); err != nil || head.Model == "" {
		wire.writeError(w, http.StatusBadRequest, "request must be JSON with a model field")
		return
	}
	modelID, endpoint, localModel, apiKey, mse := s.resolveVendorModel(wire.vendor, head.Model)
	if mse != nil {
		wire.writeError(w, mse.StatusCode, mse.Message)
		return
	}
	forward := func(rec *RequestMetric) {
		s.proxyPassthrough(w, r, wire, endpoint+path, s.substituteModelName(body, modelID, localModel), apiKey, rec)
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

// resolveVendorModel accepts the node's model id or the bare name a vendor's
// clients send: claude-sonnet-5-5 for anthropic/claude-sonnet-5-5.
func (s *InferenceService) resolveVendorModel(vendor, name string) (modelID, endpoint, localModel, apiKey string, mse *ModelServerError) {
	endpoint, localModel, apiKey, mse = s.resolveGatewayEndpoint(name)
	if mse == nil {
		return name, endpoint, localModel, apiKey, nil
	}
	if !strings.Contains(name, "/") {
		full := vendor + "/" + name
		if ep, lm, key, fullErr := s.resolveGatewayEndpoint(full); fullErr == nil {
			return full, ep, lm, key, nil
		}
	}
	return name, "", "", "", mse
}

func (s *InferenceService) proxyPassthrough(w http.ResponseWriter, r *http.Request, wire passthroughWire, url string, body []byte, apiKey string, rec *RequestMetric) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		rec.ErrorReason = err.Error()
		wire.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// The caller's own credentials stay here: the backend gets the node's key.
	for name, values := range r.Header {
		if strings.HasPrefix(strings.ToLower(name), wire.headerPrefix) || strings.EqualFold(name, "Accept") || containsFold(wire.extraRequestHeaders, name) {
			req.Header[name] = values
		}
	}
	if apiKey != "" {
		wire.setKey(req, apiKey)
	}
	resp, err := streamingHttpClient.Do(req)
	if err != nil {
		rec.ErrorReason = err.Error()
		wire.writeError(w, http.StatusBadGateway, "backend unreachable")
		return
	}
	defer resp.Body.Close()

	for _, name := range []string{"Content-Type", "Cache-Control", "Retry-After", "Request-Id", "X-Request-Id"} {
		if v := resp.Header.Get(name); v != "" {
			w.Header().Set(name, v)
		}
	}
	for name, values := range resp.Header {
		if strings.HasPrefix(strings.ToLower(name), wire.headerPrefix) {
			w.Header()[name] = values
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}

	usage := wire.newUsage()
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
	rec.TokensIn, rec.TokensOut = usage.counts()
	if resp.StatusCode >= 400 {
		rec.ErrorReason = fmt.Sprintf("backend returned %d", resp.StatusCode)
	}
	rec.Success = rec.ErrorReason == ""
}

func containsFold(names []string, name string) bool {
	for _, n := range names {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// lineUsage collects a response line by line for a usage parser, from one
// JSON body or from an event stream's data: lines.
type lineUsage struct {
	line     []byte
	overflow bool
	in, out  int
	parse    func(line []byte) (in, out int, ok bool)
}

func (u *lineUsage) feed(p []byte) {
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

func (u *lineUsage) keep(p []byte) {
	if len(u.line)+len(p) > localPassthroughUsageCap {
		u.line, u.overflow = nil, true
	}
	if !u.overflow {
		u.line = append(u.line, p...)
	}
}

func (u *lineUsage) finish() {
	line := bytes.TrimPrefix(bytes.TrimSpace(u.line), []byte("data:"))
	u.line, u.overflow = u.line[:0], false
	if !bytes.Contains(line, []byte(`"usage"`)) {
		return
	}
	in, out, ok := u.parse(line)
	if !ok {
		return
	}
	if in > 0 {
		u.in = in
	}
	if out > u.out {
		u.out = out
	}
}

func (u *lineUsage) counts() (in, out int) { return u.in, u.out }

// messagesUsage reads token counts out of a Messages response, from one JSON
// body or from the event stream's message_start and message_delta.
type messagesUsage struct{ lineUsage }

func (u *messagesUsage) feed(p []byte) {
	if u.parse == nil {
		u.parse = parseMessagesUsage
	}
	u.lineUsage.feed(p)
}

func (u *messagesUsage) finish() {
	if u.parse == nil {
		u.parse = parseMessagesUsage
	}
	u.lineUsage.finish()
}

func parseMessagesUsage(line []byte) (in, out int, ok bool) {
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
		return 0, 0, false
	}
	c := event.Usage
	if c == nil {
		c = event.Message.Usage
	}
	if c == nil {
		return 0, 0, false
	}
	return c.Input + c.CacheRead + c.CacheCreation, c.Output, true
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
