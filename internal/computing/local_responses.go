package computing

import (
	"encoding/json"
	"net/http"
)

// The OpenAI Responses API at /v1/responses, for Codex. The backend gets the
// node's key as a bearer token; the client's bearer token stays here. Codex
// identifies itself in headers of its own (originator, session_id) that the
// backend may want to see, so those go through as well.
var responsesWire = passthroughWire{
	vendor:              "openai",
	headerPrefix:        "openai-",
	extraRequestHeaders: []string{"originator", "session_id", "conversation_id"},
	setKey:              func(req *http.Request, key string) { req.Header.Set("Authorization", "Bearer "+key) },
	newUsage:            func() usageReader { return &lineUsage{parse: parseResponsesUsage} },
	writeError:          writeLocalError,
}

func (s *InferenceService) serveLocalResponses(w http.ResponseWriter, r *http.Request) {
	s.forwardLocalPassthrough(w, r, responsesWire, "/v1/responses", true)
}

// parseResponsesUsage reads usage from one Responses body or from the
// stream's response.completed event. input_tokens already includes the
// cached part.
func parseResponsesUsage(line []byte) (in, out int, ok bool) {
	type counts struct {
		Input  int `json:"input_tokens"`
		Output int `json:"output_tokens"`
	}
	var event struct {
		Usage    *counts `json:"usage"`
		Response struct {
			Usage *counts `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(line, &event) != nil {
		return 0, 0, false
	}
	c := event.Usage
	if c == nil {
		c = event.Response.Usage
	}
	if c == nil {
		return 0, 0, false
	}
	return c.Input, c.Output, true
}
