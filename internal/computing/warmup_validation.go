package computing

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const warmupMaxTokens = 64

var warmupRoleBoundary = regexp.MustCompile(`(?im)^\s*(?:user|assistant|system)\s*:`)

// validateWarmupResponse checks the synthetic greeting only. Consumer prompts
// and completions may legitimately contain these strings and are not inspected.
// A reasoning model can spend the entire budget on hidden reasoning, so that
// specific case is reported as a warning instead of rejecting the model.
func validateWarmupResponse(response json.RawMessage) (string, error) {
	if err := checkForOpenAIError(response); err != nil {
		return "", err
	}

	var completion struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role             string `json:"role"`
				Content          string `json:"content"`
				Reasoning        string `json:"reasoning"`
				ReasoningContent string `json:"reasoning_content"`
				Refusal          string `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(response, &completion); err != nil {
		return "", fmt.Errorf("invalid warmup completion: %w", err)
	}
	if len(completion.Choices) == 0 {
		return "", fmt.Errorf("warmup returned no completion choices")
	}

	warning := ""
	for _, choice := range completion.Choices {
		if choice.Message.Role != "" && choice.Message.Role != "assistant" {
			return "", fmt.Errorf("warmup returned a non-assistant role; check the backend chat template")
		}

		content := choice.Message.Content
		for _, marker := range []string{
			"<|im_start|>", "<|im_end|>",
			"<|start_header_id|>", "<|end_header_id|>", "<|eot_id|>",
			"[INST]", "[/INST]",
		} {
			if strings.Contains(content, marker) {
				return "", fmt.Errorf("warmup leaked a chat delimiter; check the backend chat template and stop tokens")
			}
		}
		if warmupRoleBoundary.MatchString(content) {
			return "", fmt.Errorf("warmup generated a conversation role boundary; check the backend chat template and stop tokens")
		}

		reasoning := choice.Message.Reasoning + choice.Message.ReasoningContent
		if choice.FinishReason == "length" {
			if strings.TrimSpace(content+choice.Message.Refusal) != "" || strings.TrimSpace(reasoning) == "" {
				return "", fmt.Errorf("warmup exhausted the %d-token budget; check the backend chat template and stop tokens", warmupMaxTokens)
			}
			warning = fmt.Sprintf("greeting exhausted the %d-token warmup budget in hidden reasoning", warmupMaxTokens)
			continue
		}
		if strings.TrimSpace(content+reasoning+choice.Message.Refusal) == "" {
			return "", fmt.Errorf("warmup returned an empty completion")
		}
	}
	return warning, nil
}
