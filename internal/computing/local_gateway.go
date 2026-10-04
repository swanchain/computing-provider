package computing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
)

// The local gateway is an OpenAI-compatible endpoint for the operator's own
// clients — benchmarks, agents, anything run on this machine against a model
// the node serves.
//
// Those clients used to call the model server directly, which the node never
// sees: a benchmark could keep a GPU busy for hours and the dashboard would
// show an idle model. Sent here instead, each request is forwarded to the same
// backend the hub's traffic uses and recorded with source "local", so the
// history and the usage chart account for it.
//
// Two deliberate differences from the hub path:
//
//   - It does not go through the rate or concurrency limiters. Those protect
//     routed work from overload; the operator's own traffic is not something
//     the node should refuse on the hub's behalf.
//   - It is recorded in the request history only, never in the aggregate
//     counters the earnings estimate is priced from. Local work earns nothing.
//
// The listener is bound to loopback and has no authentication of its own, the
// same trust the model servers behind it already extend to localhost.

// localGatewayMaxBody caps a request body. Large enough for a long-context
// prompt, small enough that a runaway client cannot exhaust memory.
const localGatewayMaxBody = 64 << 20

// LocalGatewayHandler returns the gateway's HTTP handler.
func (s *InferenceService) LocalGatewayHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", s.serveLocalChat)
	mux.HandleFunc("/v1/models", s.serveLocalModels)
	return mux
}

// StartLocalGateway serves the gateway on 127.0.0.1:port until the returned
// server is shut down. A non-loopback bind is refused: the gateway has no
// authentication, and the model servers it fronts have none either.
func (s *InferenceService) StartLocalGateway(port int) (*http.Server, error) {
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("local gateway: %w", err)
	}
	srv := &http.Server{
		Addr:              ln.Addr().String(), // informational: Serve uses ln
		Handler:           s.LocalGatewayHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logs.GetLogger().Errorf("Local gateway stopped: %v", err)
		}
	}()
	logs.GetLogger().Infof("Local gateway listening on http://%s/v1 — point local clients here so their usage is recorded", addr)
	return srv, nil
}

func (s *InferenceService) serveLocalModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeLocalError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	type model struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	out := struct {
		Object string  `json:"object"`
		Data   []model `json:"data"`
	}{Object: "list", Data: []model{}}
	for _, id := range s.GetActiveModels() {
		out.Data = append(out.Data, model{ID: id, Object: "model", OwnedBy: "computing-provider"})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *InferenceService) serveLocalChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeLocalError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, localGatewayMaxBody+1))
	if err != nil {
		writeLocalError(w, http.StatusBadRequest, "could not read request body")
		return
	}
	if len(body) > localGatewayMaxBody {
		writeLocalError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	var head struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &head); err != nil || head.Model == "" {
		writeLocalError(w, http.StatusBadRequest, "request must be JSON with a model field")
		return
	}

	endpoint, localModel, apiKey, mse := s.resolveModelEndpoint(head.Model)
	if mse != nil {
		writeLocalError(w, mse.StatusCode, mse.Message)
		return
	}

	rec := RequestMetric{
		RequestID: fmt.Sprintf("local-%d", time.Now().UnixNano()),
		Model:     head.Model,
		StartTime: time.Now(),
		Streaming: head.Stream,
		Source:    SourceLocal,
	}

	// Bound to the client's own request: a local client that disconnects
	// stops the generation instead of leaving it to run for nobody.
	if head.Stream {
		s.serveLocalStream(r.Context(), w, body, head.Model, endpoint, localModel, apiKey, &rec)
	} else {
		s.serveLocalOnce(r.Context(), w, body, head.Model, endpoint, localModel, apiKey, &rec)
	}

	rec.EndTime = time.Now()
	rec.LatencyMs = float64(rec.EndTime.Sub(rec.StartTime).Milliseconds())
	if s.client != nil {
		if m := s.client.Metrics(); m != nil {
			m.RecordRequest(rec)
		}
	}
}

func (s *InferenceService) serveLocalOnce(ctx context.Context, w http.ResponseWriter, body []byte, modelID, endpoint, localModel, apiKey string, rec *RequestMetric) {
	resp, err := s.forwardToDockerModelContext(ctx, endpoint, body, modelID, localModel, apiKey)
	if err != nil {
		status := http.StatusBadGateway
		var mse *ModelServerError
		if errors.As(err, &mse) && mse.StatusCode > 0 {
			status = mse.StatusCode
		}
		rec.ErrorReason = err.Error()
		writeLocalError(w, status, err.Error())
		return
	}
	in, out := extractTokenCounts(resp)
	rec.TokensIn, rec.TokensOut, rec.Success = in, out, true
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(resp)
}

func (s *InferenceService) serveLocalStream(ctx context.Context, w http.ResponseWriter, body []byte, modelID, endpoint, localModel, apiKey string, rec *RequestMetric) {
	flusher, _ := w.(http.Flusher)
	started := false
	send := func(chunk []byte, done bool) error {
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			started = true
		}
		var err error
		if done {
			_, err = io.WriteString(w, "data: [DONE]\n\n")
		} else {
			_, err = fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		if flusher != nil {
			flusher.Flush()
		}
		return err
	}

	result := s.streamFromDockerModelContext(ctx, endpoint, body, modelID, localModel, apiKey, send)
	rec.TokensIn, rec.TokensOut = int(result.TokensInput), int(result.TokensOutput)
	if result.Error != nil {
		rec.ErrorReason = result.Error.Error()
		if !started {
			status := http.StatusBadGateway
			var mse *ModelServerError
			if errors.As(result.Error, &mse) && mse.StatusCode > 0 {
				status = mse.StatusCode
			}
			writeLocalError(w, status, result.Error.Error())
		}
		return
	}
	rec.Success = true
}

// resolveModelEndpoint finds where a model is served: the registry first, then
// the models.json mapping for backward compatibility. A model the registry has
// taken out of service is refused — the mapping still names its backend, and
// falling through to it would keep forwarding to what was disabled.
func (s *InferenceService) resolveModelEndpoint(modelID string) (endpoint, localModel, apiKey string, mse *ModelServerError) {
	if s.registry != nil {
		if ep, ok := s.registry.GetModelEndpoint(modelID); ok {
			return ep, s.registry.GetLocalModelName(modelID), s.registry.GetModelAPIKey(modelID), nil
		}
		if _, disabled := s.disabledInRegistry(modelID); disabled {
			return "", "", "", &ModelServerError{StatusCode: 503, Message: fmt.Sprintf("model %s is disabled on this provider", modelID)}
		}
	}
	mapping, ok := s.modelMappings[modelID]
	if !ok {
		return "", "", "", &ModelServerError{StatusCode: 404, Message: fmt.Sprintf("model %s not deployed on this provider", modelID)}
	}
	return mapping.Endpoint, mapping.LocalModel, mapping.APIKey, nil
}

func writeLocalError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	typ := "server_error"
	if status < 500 {
		typ = "invalid_request_error"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"message": strings.TrimSpace(msg), "type": typ},
	})
}
