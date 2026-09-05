package computing

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateWarmupResponse(t *testing.T) {
	for _, tc := range []struct {
		name, body                string
		wantError, wantTokenLimit bool
	}{
		{"greeting", `{"choices":[{"message":{"role":"assistant","content":"Hello!"},"finish_reason":"stop"}]}`, false, false},
		{"length", `{"choices":[{"message":{"content":"Hello there"},"finish_reason":"length"}]}`, true, true},
		{"Ollama reasoning", `{"choices":[{"message":{"content":null,"reasoning":"Let me think"},"finish_reason":"length"}]}`, true, true},
		{"OpenAI reasoning content", `{"choices":[{"message":{"content":null,"reasoning_content":"Let me think"},"finish_reason":"length"}]}`, true, true},
		{"empty length", `{"choices":[{"message":{"content":""},"finish_reason":"length"}]}`, true, true},
		{"chatml", `{"choices":[{"message":{"content":"Hi!<|im_start|>user"},"finish_reason":"stop"}]}`, true, false},
		{"llama", `{"choices":[{"message":{"content":"Hi!<|eot_id|>"},"finish_reason":"stop"}]}`, true, false},
		{"role lines", `{"choices":[{"message":{"content":"Hello\n  USER: Tell me more\nAssistant: Certainly"}}]}`, true, false},
		{"ordinary mention", `{"choices":[{"message":{"content":"Hello, I am your assistant: ready to help."},"finish_reason":"stop"}]}`, false, false},
		{"wrong role", `{"choices":[{"message":{"role":"user","content":"Hi"}}]}`, true, false},
		{"empty choices", `{"choices":[]}`, true, false},
		{"empty answer", `{"choices":[{"message":{"content":" "},"finish_reason":"stop"}]}`, true, false},
		{"http 200 error", `{"error":{"message":"engine unavailable","code":503}}`, true, false},
		{"invalid JSON", `not JSON`, true, false},
		{"multiple choices", `{"choices":[{"message":{"content":"Hello"}},{"message":{"content":"[INST]Hi"}}]}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWarmupResponse(json.RawMessage(tc.body))
			if (err != nil) != tc.wantError || errors.Is(err, errWarmupTokenLimit) != tc.wantTokenLimit {
				t.Fatalf("error=%v token_limit=%v", err, errors.Is(err, errWarmupTokenLimit))
			}
		})
	}
}

func TestWarmupReprobesTruncatedOutput(t *testing.T) {
	for _, stillTruncated := range []bool{false, true} {
		name := "finishes on retry"
		if stillTruncated {
			name = "still truncated"
		}
		t.Run(name, func(t *testing.T) {
			var budgets []int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					MaxTokens int `json:"max_tokens"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				budgets = append(budgets, request.MaxTokens)

				finishReason := "length"
				if len(budgets) == 2 && !stillTruncated {
					finishReason = "stop"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []any{map[string]any{
						"message":       map[string]string{"role": "assistant", "content": "A verbose but otherwise valid answer"},
						"finish_reason": finishReason,
					}},
				})
			}))
			defer server.Close()

			s := &InferenceService{registry: &ModelRegistry{models: map[string]*RegisteredModel{
				"m": {ID: "m", Endpoint: server.URL, Enabled: true},
			}}}
			response, err := s.handleWarmup(WarmupPayload{ModelID: "m"})
			if len(budgets) != 2 || budgets[0] != warmupMaxTokens || budgets[1] != warmupRetryMaxTokens {
				t.Fatalf("warmup budgets = %v", budgets)
			}
			if stillTruncated {
				if err == nil || !strings.Contains(err.Error(), "did not finish within 512 tokens") {
					t.Fatalf("truncated retry accepted: %+v %v", response, err)
				}
			} else if err != nil || !response.Success {
				t.Fatalf("completed retry failed: %+v %v", response, err)
			}
		})
	}
}

func TestWarmupValidatesBackendOutput(t *testing.T) {
	for _, broken := range []bool{false, true} {
		name := "valid"
		if broken {
			name = "broken template"
		}
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Model     string                           `json:"model"`
					MaxTokens int                              `json:"max_tokens"`
					Stream    bool                             `json:"stream"`
					Messages  []struct{ Role, Content string } `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request.Model != "local-model" || request.MaxTokens != warmupMaxTokens || request.Stream || len(request.Messages) != 1 || request.Messages[0].Content != "Hi" {
					t.Errorf("unexpected warmup: %+v", request)
				}
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("backend authentication lost")
				}

				content := "Hello!"
				if broken {
					content = "Hello!\nuser: What is next?"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []any{map[string]any{
						"message":       map[string]string{"role": "assistant", "content": content},
						"finish_reason": "stop",
					}},
				})
			}))
			defer server.Close()

			s := &InferenceService{registry: &ModelRegistry{models: map[string]*RegisteredModel{
				"m": {ID: "m", Endpoint: server.URL, Enabled: true, LocalModel: "local-model", APIKey: "test-token"},
			}}}
			response, err := s.handleWarmup(WarmupPayload{ModelID: "m"})
			if broken {
				if err == nil || !strings.Contains(err.Error(), "warmup validation failed") {
					t.Fatalf("broken warmup accepted: %+v %v", response, err)
				}
			} else if err != nil || !response.Success {
				t.Fatalf("valid warmup failed: %+v %v", response, err)
			}
		})
	}
}
