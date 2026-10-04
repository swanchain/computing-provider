package computing

import (
	"context"
	"encoding/json"
	"testing"
)

// Swan Inference classifies a failed stream from its first chunk. An error
// carried only in stream_end is dropped and the stream reads as "closed before
// any chunk": a provider failure, with the same oversized prompt resent.

func drain(t *testing.T, c *InferenceClient) []Message {
	t.Helper()
	var out []Message
	for len(c.send) > 0 {
		var m Message
		if err := json.Unmarshal(<-c.send, &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func TestStreamErrorBeforeAnyDataIsSentAsAChunk(t *testing.T) {
	c := &InferenceClient{metrics: NewInferenceMetrics(), send: make(chan []byte, 8)}
	overflow := "model server returned HTTP 400: request (38282 tokens) exceeds the available context size (24576 tokens), try increasing it"
	c.SetStreamingInferenceHandler(func(ctx context.Context, id string, p InferencePayload, send func([]byte, bool) error) *StreamResult {
		return &StreamResult{Error: &ModelServerError{StatusCode: 400, Message: overflow}}
	})
	c.handleInference("r1", InferencePayload{ModelID: "m", Stream: true})

	msgs := drain(t, c)
	if len(msgs) < 2 || msgs[0].Type != MsgTypeStreamChunk {
		t.Fatalf("messages = %+v, want an error chunk first", msgs)
	}
	var chunk StreamChunkPayload
	json.Unmarshal(msgs[0].Payload, &chunk)
	if !chunk.Done || chunk.Error == "" || chunk.RequestID != "r1" {
		t.Errorf("error chunk = %+v", chunk)
	}
	if msgs[len(msgs)-1].Type != MsgTypeStreamEnd {
		t.Error("stream_end must still follow, for accounting")
	}
}

func TestStreamErrorAfterDataSendsNoExtraChunk(t *testing.T) {
	c := &InferenceClient{metrics: NewInferenceMetrics(), send: make(chan []byte, 8)}
	c.SetStreamingInferenceHandler(func(ctx context.Context, id string, p InferencePayload, send func([]byte, bool) error) *StreamResult {
		send([]byte(`{"choices":[{"delta":{"content":"hi"}}]}`), false)
		return &StreamResult{Error: &ModelServerError{StatusCode: 502, Message: "backend died"}}
	})
	c.handleInference("r1", InferencePayload{ModelID: "m", Stream: true})

	for _, m := range drain(t, c) {
		if m.Type != MsgTypeStreamChunk {
			continue
		}
		var chunk StreamChunkPayload
		json.Unmarshal(m.Payload, &chunk)
		if chunk.Error != "" {
			t.Error("a stream that already sent data must not get a trailing error chunk")
		}
	}
}
