package autoswitch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DecisionSchema is the JSON schema the planner is constrained to.
//
// Constrained decoding rather than "please reply in JSON": the decision is
// parsed by code that then acts on it, and a planner that drifts into prose or
// wraps its answer in a fenced block turns into an unparseable decision, which
// is a refused cycle. The schema makes that impossible at the decoding step
// rather than catching it afterwards.
var DecisionSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"action": map[string]interface{}{
			"type": "string",
			"enum": []string{ActionKeep, ActionSwitch},
		},
		"serve": map[string]interface{}{
			"type":  "array",
			"items": map[string]interface{}{"type": "string"},
		},
		"stop": map[string]interface{}{
			"type":  "array",
			"items": map[string]interface{}{"type": "string"},
		},
		"expected_daily_usd": map[string]interface{}{"type": "number"},
		"reason":             map[string]interface{}{"type": "string"},
	},
	"required":             []string{"action", "serve", "stop", "expected_daily_usd", "reason"},
	"additionalProperties": false,
}

// systemPrompt tells the planner what it is deciding and what it must not
// assume.
//
// It states the constraints the guardrails will enforce anyway. Not because
// stating them makes the guardrails unnecessary — a planner cannot be trusted
// to respect them — but because a planner that knows a model is ineligible
// spends its reasoning on the models that are not, instead of proposing one
// that will be refused.
const systemPrompt = `You decide which AI models a GPU node should serve on the Swan Inference marketplace.

You are given the node's hardware, what it serves now, and the live market.

Rules your answer must respect:
- Only propose serving a model whose vram_fit is "fits". A model whose
  requirement is not published ("unknown") cannot be assumed to fit and will be
  refused.
- Never propose stopping a pinned model.
- Only propose stopping a model the node is actually serving and that it
  manages.
- Switching costs real time at zero revenue. Propose "switch" only when the gain
  is clearly worth it; otherwise answer "keep".
- expected_daily_usd is your estimate of what the node earns per day AFTER the
  change, in USD, across all models it would then serve.
- If the market data does not justify a change, answer "keep" with an empty
  serve and stop.

Judge on: what the node would actually be paid (provider_input_price and
provider_output_price), how many providers already serve a model, demand and its
trend, and whether previous estimates proved accurate. Be sceptical of
est_entrant_daily_usd; compare it against what the node really earned.

Reply only with the decision object.`

// Planner asks a model to decide.
type Planner struct {
	// Endpoint is the OpenAI-compatible base URL of the planner's server.
	Endpoint string

	// Model is the model ID to ask.
	Model string

	// APIKey authenticates to the endpoint, when it needs one.
	APIKey string

	// Timeout bounds the request. A planner is a local model on the node's
	// own GPU and is answering a short structured question, but it may be
	// busy serving customers.
	Timeout time.Duration

	client *http.Client
}

// NewPlanner builds a Planner.
func NewPlanner(endpoint, model, apiKey string) *Planner {
	return &Planner{
		Endpoint: strings.TrimRight(endpoint, "/"),
		Model:    model,
		APIKey:   apiKey,
		Timeout:  2 * time.Minute,
	}
}

// Raw is the planner's unparsed reply, kept so a refused cycle can show the
// operator what was actually said.
type Raw struct {
	Content string `json:"content"`
}

// Decide asks the planner and parses its answer.
//
// A parse failure is reported as an error with the raw content attached rather
// than as a decision, so the caller can record what went wrong. It is never
// turned into a made-up keep: the caller decides that, and the distinction
// matters because a planner that cannot be parsed is a fault to investigate,
// not a quiet no-op.
func (p *Planner) Decide(ctx context.Context, snapshot *Snapshot) (*Decision, *Raw, error) {
	snapshotJSON, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, nil, err
	}

	body := map[string]interface{}{
		"model": p.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": "Node and market snapshot:\n" + string(snapshotJSON)},
		},
		// Low but not zero: the decision is constrained by the schema, and a
		// little sampling avoids the planner locking onto one phrasing of a
		// bad plan across consecutive cycles, which would defeat the
		// consecutive-agreement rule meant to filter noise.
		"temperature": 0.3,
		"max_tokens":  800,
		"response_format": map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name":   "switch_decision",
				"strict": true,
				"schema": DecisionSchema,
			},
		},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}

	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	if p.client == nil {
		p.client = &http.Client{Timeout: timeout}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.Endpoint+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("planner %s is unreachable at %s: %w", p.Model, p.Endpoint, err)
	}
	defer resp.Body.Close()

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, nil, fmt.Errorf("planner returned HTTP %d with an unreadable body: %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := "no detail"
		if parsed.Error != nil {
			msg = parsed.Error.Message
		}
		return nil, nil, fmt.Errorf("planner returned HTTP %d: %s", resp.StatusCode, msg)
	}
	if len(parsed.Choices) == 0 {
		return nil, nil, fmt.Errorf("planner returned no choices")
	}

	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	raw := &Raw{Content: content}

	decision, err := ParseDecision(content)
	if err != nil {
		return nil, raw, err
	}
	return decision, raw, nil
}

// ParseDecision reads a decision out of a planner's reply.
//
// Constrained decoding should make the reply exact JSON, but a fenced block is
// the one deviation a model still manages under some servers, and stripping it
// is cheaper than a refused cycle. Anything beyond that is a parse failure: a
// decision extracted by guesswork is worse than none, because it would be acted
// on.
func ParseDecision(content string) (*Decision, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		if idx := strings.Index(content, "\n"); idx >= 0 {
			content = content[idx+1:]
		}
		content = strings.TrimSuffix(strings.TrimSpace(content), "```")
		content = strings.TrimSpace(content)
	}

	var decision Decision
	if err := json.Unmarshal([]byte(content), &decision); err != nil {
		return nil, fmt.Errorf("planner reply is not a decision object: %w", err)
	}
	if strings.TrimSpace(decision.Action) == "" {
		return nil, fmt.Errorf("planner reply has no action")
	}
	return &decision, nil
}
