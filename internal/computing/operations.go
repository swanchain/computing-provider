package computing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// What a request asks the backend to do.
//
// Swan Inference accepts chat, image, embedding and transcription requests
// from customers, and sends all of them to a provider as the same `inference`
// message. Newer hubs say which one in `operation`; older ones say nothing,
// and the node used to forward everything to /v1/chat/completions — so an
// image request reached a chat endpoint and failed.
//
// The value is the OpenAI path after /v1/, which is also where the request is
// forwarded.
const (
	OpChatCompletions   = "chat/completions"
	OpImagesGenerations = "images/generations"
	OpEmbeddings        = "embeddings"
	OpTranscriptions    = "audio/transcriptions"
)

// resolveOperation decides what a request is. The hub's own statement wins;
// without one, the model's declared category decides, and only a model with
// no telling category falls back to the request's shape.
func resolveOperation(declared, category string, request json.RawMessage) string {
	switch declared {
	case OpChatCompletions, OpImagesGenerations, OpEmbeddings, OpTranscriptions:
		return declared
	}
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "image", "text-to-image", "image-generation":
		return OpImagesGenerations
	case "embedding", "embeddings", "text-embedding":
		return OpEmbeddings
	case "audio", "speech-to-text", "transcription":
		return OpTranscriptions
	}
	var shape map[string]json.RawMessage
	if json.Unmarshal(request, &shape) != nil {
		return OpChatCompletions
	}
	if _, ok := shape["messages"]; ok {
		return OpChatCompletions
	}
	if _, ok := shape["file"]; ok {
		return OpTranscriptions
	}
	if _, ok := shape["input"]; ok {
		return OpEmbeddings
	}
	if _, ok := shape["prompt"]; ok {
		return OpImagesGenerations
	}
	return OpChatCompletions
}

// forwardJSONOperation sends a non-chat JSON request to the backend path for
// its operation. The chat path's post-processing (fence stripping, token
// detail backfill) is specific to completions and is not applied.
func (s *InferenceService) forwardJSONOperation(ctx context.Context, op, endpoint string, request json.RawMessage, modelID, localModel, apiKey string) (json.RawMessage, error) {
	modified := s.substituteModelName(request, modelID, localModel)
	var headers http.Header
	if apiKey != "" {
		headers = http.Header{}
		headers.Set("Authorization", "Bearer "+apiKey)
	}
	var response json.RawMessage
	if err := NewHttpClient(endpoint, headers).PostJSONContext(ctx, "/v1/"+op, modified, &response); err != nil {
		return nil, fmt.Errorf("failed to forward %s request: %w", op, err)
	}
	if err := checkForOpenAIError(response); err != nil {
		return nil, err
	}
	return withChatUsageNames(response), nil
}

// withChatUsageNames adds prompt_tokens / completion_tokens to a usage block
// that reports only input_tokens / output_tokens, as OpenAI's image and
// realtime APIs do. Billing and this node's own counters read the chat names;
// without them an image is recorded, and charged, as zero tokens. The
// original fields are kept.
func withChatUsageNames(response json.RawMessage) json.RawMessage {
	var body map[string]json.RawMessage
	if json.Unmarshal(response, &body) != nil || body["usage"] == nil {
		return response
	}
	var usage map[string]any
	if json.Unmarshal(body["usage"], &usage) != nil || usage == nil {
		return response
	}
	_, hasPrompt := usage["prompt_tokens"]
	in, hasIn := usage["input_tokens"]
	if hasPrompt || !hasIn {
		return response
	}
	usage["prompt_tokens"] = in
	if out, ok := usage["output_tokens"]; ok {
		usage["completion_tokens"] = out
	}
	raw, err := json.Marshal(usage)
	if err != nil {
		return response
	}
	body["usage"] = raw
	out, err := json.Marshal(body)
	if err != nil {
		return response
	}
	return out
}
