package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Step is one decision from the model.
//
// The shape is fixed by a JSON schema the model is constrained to, so a reply
// is always parseable. A model that drifted into prose would otherwise end the
// run, and the useful thing about a local planner is that it is cheap to ask
// again — not that it is reliable at free-form formatting.
type Step struct {
	// Thought is the model's reasoning, shown to the operator so a surprising
	// action can be understood before it happens rather than after.
	Thought string `json:"thought"`

	// Tool is the tool to run, or "" when finishing.
	Tool string `json:"tool"`

	// Args are the tool's arguments.
	Args Args `json:"args"`

	// Done ends the run.
	Done bool `json:"done"`

	// Answer is the reply to the operator's goal, when Done.
	Answer string `json:"answer"`
}

// Result is the outcome of a run.
type Result struct {
	Answer string
	Steps  int

	// Acted reports whether anything actually changed the node, so a caller
	// can say so plainly rather than the operator having to infer it.
	Acted bool
}

// Observer is told what the agent is doing, step by step.
type Observer interface {
	Thinking(step int, thought string)
	Using(step int, tool string, args Args, permitted bool)
	Observed(step int, output string, err error)

	// Ungrounded reports an answer the model tried to give before looking at
	// anything. Surfaced rather than silently retried: it is the operator's
	// clearest signal that the model is willing to invent, which is worth
	// knowing even when the retry then succeeds.
	Ungrounded(step int, answer string)

	Finished(answer string)
}

// Agent runs a goal to completion against a local model.
type Agent struct {
	// Endpoint and Model name the local model doing the reasoning. It is one
	// this node already serves, so asking it costs nothing marginal.
	Endpoint string
	Model    string
	APIKey   string

	// Context is cp.md — what the node is, what it has run, and the rules.
	Context string

	Tools *Registry

	// AllowActions permits tools that change the node. Off by default: an
	// agent that can only look is useful and cannot break anything, and that
	// is the right default for the first thing an operator tries.
	AllowActions bool

	// MaxSteps bounds a run. A model that loops — re-reading the same table
	// because it is not making progress — must stop costing GPU time
	// eventually.
	MaxSteps int

	// Timeout bounds one model call.
	Timeout time.Duration

	Observer Observer

	client *http.Client
}

// DefaultMaxSteps bounds a run that never decides it is finished.
const DefaultMaxSteps = 12

func (a *Agent) maxSteps() int {
	if a.MaxSteps > 0 {
		return a.MaxSteps
	}
	return DefaultMaxSteps
}

func (a *Agent) timeout() time.Duration {
	if a.Timeout > 0 {
		return a.Timeout
	}
	return 3 * time.Minute
}

// stepSchema constrains the model's reply.
var stepSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"thought": map[string]interface{}{"type": "string"},
		"tool":    map[string]interface{}{"type": "string"},
		"args":    map[string]interface{}{"type": "object"},
		"done":    map[string]interface{}{"type": "boolean"},
		"answer":  map[string]interface{}{"type": "string"},
	},
	"required":             []string{"thought", "tool", "args", "done", "answer"},
	"additionalProperties": false,
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Run pursues a goal, returning what the model concluded.
func (a *Agent) Run(ctx context.Context, goal string) (*Result, error) {
	if a.Tools == nil {
		return nil, fmt.Errorf("the agent has no tools")
	}

	history := []message{
		{Role: "system", Content: a.systemPrompt()},
		{Role: "user", Content: "Goal: " + goal},
	}

	result := &Result{}

	// observations counts tool results the model has actually seen.
	//
	// It exists because of a real failure: asked what the node was earning,
	// the model wrote "I need to check node_status", then set done on the
	// same turn and produced a confident table of figures it had invented —
	// $142.80/day against a true $0.0513. It never called a tool. An operator
	// reading that would have acted on fiction.
	//
	// Prose in the system prompt does not fix this; a model that ignores one
	// instruction will ignore another. So finishing without evidence is
	// refused in code below.
	observations := 0

	for step := 1; step <= a.maxSteps(); step++ {
		decision, err := a.ask(ctx, history)
		if err != nil {
			return result, err
		}
		result.Steps = step

		if a.Observer != nil && decision.Thought != "" {
			a.Observer.Thinking(step, decision.Thought)
		}

		if decision.Done || decision.Tool == "" {
			if observations == 0 {
				// Nothing has been looked at, so anything stated here came
				// from the model rather than from the node. Push back and
				// keep going rather than returning it.
				if a.Observer != nil {
					a.Observer.Ungrounded(step, decision.Answer)
				}
				history = append(history,
					message{Role: "assistant", Content: mustJSON(decision)},
					message{Role: "user", Content: ungroundedRebuke(a.Tools.Names())},
				)
				continue
			}

			result.Answer = decision.Answer
			if result.Answer == "" {
				result.Answer = decision.Thought
			}
			if a.Observer != nil {
				a.Observer.Finished(result.Answer)
			}
			return result, nil
		}

		output, acted, err := a.invoke(ctx, step, decision)
		if acted {
			result.Acted = true
		}

		// A failure is reported to the model rather than ending the run: a
		// wrong argument is something it can correct, and aborting would
		// throw away the episode over a typo.
		observation := output
		if err != nil {
			observation = "ERROR: " + err.Error()
		} else {
			observations++
		}

		history = append(history,
			message{Role: "assistant", Content: mustJSON(decision)},
			message{Role: "user", Content: "Result of `" + decision.Tool + "`:\n" + observation},
		)
	}

	// Out of steps. Reported as a result rather than an error, because the
	// work done so far is usually worth showing.
	result.Answer = fmt.Sprintf("Stopped after %d steps without reaching a conclusion.", a.maxSteps())
	if observations == 0 {
		result.Answer += " No tool returned anything, so there is nothing here that came from the node itself."
	}
	if a.Observer != nil {
		a.Observer.Finished(result.Answer)
	}
	return result, nil
}

// invoke runs one tool, enforcing the action permission.
func (a *Agent) invoke(ctx context.Context, step int, decision *Step) (output string, acted bool, err error) {
	tool, ok := a.Tools.Get(decision.Tool)
	if !ok {
		if a.Observer != nil {
			a.Observer.Using(step, decision.Tool, decision.Args, false)
		}
		// Listing the real names back is what lets the model recover rather
		// than guessing again.
		err = fmt.Errorf("no such tool %q; available: %s", decision.Tool, strings.Join(a.Tools.Names(), ", "))
		if a.Observer != nil {
			a.Observer.Observed(step, "", err)
		}
		return "", false, err
	}

	permitted := !tool.Acts || a.AllowActions
	if a.Observer != nil {
		a.Observer.Using(step, tool.Name, decision.Args, permitted)
	}

	if !permitted {
		// Refused here, not by the tool. The permission is the operator's
		// decision about this run, and it should not depend on every tool
		// remembering to check it.
		err = fmt.Errorf("%s changes the node, which this run does not permit; re-run with --allow-actions to let the agent act", tool.Name)
		if a.Observer != nil {
			a.Observer.Observed(step, "", err)
		}
		return "", false, err
	}

	if err := checkArgs(tool, decision.Args); err != nil {
		if a.Observer != nil {
			a.Observer.Observed(step, "", err)
		}
		return "", false, err
	}

	output, err = tool.Run(ctx, decision.Args)
	if a.Observer != nil {
		a.Observer.Observed(step, output, err)
	}
	return output, tool.Acts && err == nil, err
}

// checkArgs verifies required arguments are present before a tool runs.
func checkArgs(tool *Tool, args Args) error {
	var missing []string
	for _, spec := range tool.Args {
		if !spec.Required {
			continue
		}
		if v, ok := args[spec.Name]; !ok || v == nil || v == "" {
			missing = append(missing, spec.Name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s needs %s", tool.Name, strings.Join(missing, " and "))
	}
	return nil
}

// ask puts the conversation to the model and parses one step back.
func (a *Agent) ask(ctx context.Context, history []message) (*Step, error) {
	body := map[string]interface{}{
		"model":       a.Model,
		"messages":    history,
		"temperature": 0.2,
		"max_tokens":  900,
		"response_format": map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name":   "agent_step",
				"strict": true,
				"schema": stepSchema,
			},
		},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	if a.client == nil {
		a.client = &http.Client{Timeout: a.timeout()}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(a.Endpoint, "/")+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.APIKey)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s is unreachable at %s: %w", a.Model, a.Endpoint, err)
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
		return nil, fmt.Errorf("%s returned HTTP %d with an unreadable body: %w", a.Model, resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		detail := "no detail"
		if parsed.Error != nil {
			detail = parsed.Error.Message
		}
		return nil, fmt.Errorf("%s returned HTTP %d: %s", a.Model, resp.StatusCode, detail)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("%s returned no reply", a.Model)
	}

	return ParseStep(parsed.Choices[0].Message.Content)
}

// ParseStep reads a step out of a model's reply.
func ParseStep(content string) (*Step, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		if i := strings.Index(content, "\n"); i >= 0 {
			content = content[i+1:]
		}
		content = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(content), "```"))
	}

	var step Step
	if err := json.Unmarshal([]byte(content), &step); err != nil {
		return nil, fmt.Errorf("the model's reply was not a step: %w", err)
	}
	if step.Args == nil {
		step.Args = Args{}
	}
	return &step, nil
}

func mustJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// ungroundedRebuke is sent when the model tries to answer without looking.
//
// Names the tools again, because the most common cause is a model that has
// forgotten it has any.
func ungroundedRebuke(tools []string) string {
	return "You have not run a single tool, so you have not observed anything about this node. " +
		"Do not answer from memory or from what is typical — every figure you give must come from a tool result. " +
		"Run one of these first: " + strings.Join(tools, ", ") + "."
}
