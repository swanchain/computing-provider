package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scriptedModel replays prepared steps, so a run can be driven without a GPU.
type scriptedModel struct {
	steps    []Step
	served   int
	prompts  []string
	fallback Step
}

func (m *scriptedModel) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) > 0 {
			m.prompts = append(m.prompts, body.Messages[len(body.Messages)-1].Content)
		}

		step := m.fallback
		if m.served < len(m.steps) {
			step = m.steps[m.served]
		}
		m.served++

		content, _ := json.Marshal(step)
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%s}}]}`, mustQuote(string(content)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mustQuote(s string) string {
	q, _ := json.Marshal(s)
	return string(q)
}

// recordingObserver captures what the operator would have been shown.
type recordingObserver struct {
	used       []string
	refused    []string
	ungrounded int
}

func (o *recordingObserver) Thinking(int, string) {}
func (o *recordingObserver) Using(_ int, tool string, _ Args, permitted bool) {
	if permitted {
		o.used = append(o.used, tool)
	} else {
		o.refused = append(o.refused, tool)
	}
}
func (o *recordingObserver) Observed(int, string, error) {}
func (o *recordingObserver) Ungrounded(int, string)      { o.ungrounded++ }
func (o *recordingObserver) Finished(string)             {}

func testTools(calls *[]string) *Registry {
	return NewRegistry(
		&Tool{
			Name:        "node_status",
			Description: "node status",
			Run: func(context.Context, Args) (string, error) {
				*calls = append(*calls, "node_status")
				return "Earnings, last 24h: $0.0683/day total", nil
			},
		},
		&Tool{
			Name:        "needs_id",
			Description: "needs a model id",
			Args:        []Arg{{Name: "model_id", Type: "string", Required: true}},
			Run: func(_ context.Context, a Args) (string, error) {
				*calls = append(*calls, "needs_id:"+a.String("model_id"))
				return "ok", nil
			},
		},
		&Tool{
			Name:        "stop_model",
			Description: "stops a model",
			Acts:        true,
			Run: func(context.Context, Args) (string, error) {
				*calls = append(*calls, "stop_model")
				return "stopped", nil
			},
		},
	)
}

func newAgent(t *testing.T, model *scriptedModel, tools *Registry, allowActions bool) *Agent {
	t.Helper()
	srv := model.server(t)
	return &Agent{
		Endpoint:     srv.URL,
		Model:        "test",
		Tools:        tools,
		AllowActions: allowActions,
		MaxSteps:     6,
	}
}

// The failure this rule exists for: asked what the node was earning, the model
// wrote "I need to check node_status", set done on the same turn, and produced
// a confident table of invented figures — $142.80/day against a true $0.05. It
// never called a tool.
func TestAnAnswerWithoutEvidenceIsRefused(t *testing.T) {
	var calls []string
	model := &scriptedModel{
		steps: []Step{
			{Thought: "I need to check node_status.", Done: true,
				Answer: "This node earned $142.80 in the last 24 hours."},
			{Thought: "Checking properly.", Tool: "node_status", Args: Args{}},
			{Thought: "Now I know.", Done: true, Answer: "This node earned $0.0683/day."},
		},
	}
	obs := &recordingObserver{}
	a := newAgent(t, model, testTools(&calls), false)
	a.Observer = obs

	result, err := a.Run(context.Background(), "what is this node earning?")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(result.Answer, "142.80") {
		t.Fatalf("an invented answer was returned to the operator: %q", result.Answer)
	}
	if !strings.Contains(result.Answer, "0.0683") {
		t.Errorf("answer = %q, want the figure that came from the tool", result.Answer)
	}
	if obs.ungrounded != 1 {
		t.Errorf("ungrounded attempts surfaced = %d, want 1", obs.ungrounded)
	}
	if len(calls) != 1 || calls[0] != "node_status" {
		t.Errorf("tool calls = %v", calls)
	}
}

// The push-back has to name the tools, because the usual cause is a model that
// has forgotten it has any.
func TestTheRebukeNamesTheTools(t *testing.T) {
	var calls []string
	model := &scriptedModel{
		steps: []Step{
			{Thought: "guessing", Done: true, Answer: "$999"},
			{Thought: "looking", Tool: "node_status", Args: Args{}},
			{Thought: "done", Done: true, Answer: "grounded"},
		},
	}
	a := newAgent(t, model, testTools(&calls), false)
	if _, err := a.Run(context.Background(), "goal"); err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(model.prompts, "\n")
	if !strings.Contains(joined, "node_status") || !strings.Contains(joined, "not run a single tool") {
		t.Errorf("the rebuke did not name the tools:\n%s", joined)
	}
}

// A model that never looks at anything must not produce an answer at all.
func TestARunThatNeverLooksProducesNoFigures(t *testing.T) {
	var calls []string
	model := &scriptedModel{
		fallback: Step{Thought: "still guessing", Done: true, Answer: "$500/day"},
	}
	a := newAgent(t, model, testTools(&calls), false)

	result, err := a.Run(context.Background(), "goal")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Answer, "500") {
		t.Fatalf("an invented figure survived to the operator: %q", result.Answer)
	}
	if !strings.Contains(result.Answer, "nothing here that came from the node") {
		t.Errorf("answer should say it learned nothing: %q", result.Answer)
	}
	if len(calls) != 0 {
		t.Errorf("tools were called: %v", calls)
	}
}

// A read-only run must refuse a tool that changes the node, whatever the model
// intends.
func TestActingToolsAreRefusedWithoutPermission(t *testing.T) {
	var calls []string
	model := &scriptedModel{
		steps: []Step{
			{Thought: "look first", Tool: "node_status", Args: Args{}},
			{Thought: "now stop it", Tool: "stop_model", Args: Args{"model_id": "a/Model"}},
			{Thought: "fine", Done: true, Answer: "reported instead"},
		},
	}
	obs := &recordingObserver{}
	a := newAgent(t, model, testTools(&calls), false)
	a.Observer = obs

	result, err := a.Run(context.Background(), "stop the worst model")
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range calls {
		if c == "stop_model" {
			t.Fatal("a state-changing tool ran in a read-only run")
		}
	}
	if len(obs.refused) != 1 || obs.refused[0] != "stop_model" {
		t.Errorf("refusals = %v", obs.refused)
	}
	if result.Acted {
		t.Error("a read-only run reported that it acted")
	}
	if !strings.Contains(strings.Join(model.prompts, "\n"), "--allow-actions") {
		t.Error("the refusal did not tell the model how the operator could permit it")
	}
}

func TestActingToolsRunWhenPermitted(t *testing.T) {
	var calls []string
	model := &scriptedModel{
		steps: []Step{
			{Thought: "stop it", Tool: "stop_model", Args: Args{"model_id": "a/Model"}},
			{Thought: "done", Done: true, Answer: "stopped"},
		},
	}
	a := newAgent(t, model, testTools(&calls), true)

	result, err := a.Run(context.Background(), "stop it")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "stop_model" {
		t.Errorf("calls = %v", calls)
	}
	if !result.Acted {
		t.Error("a run that stopped a model did not report acting")
	}
}

// A wrong tool name is something the model can correct; aborting would waste
// the episode on a typo.
func TestAnUnknownToolIsCorrectableRatherThanFatal(t *testing.T) {
	var calls []string
	model := &scriptedModel{
		steps: []Step{
			{Thought: "try", Tool: "node_stats", Args: Args{}},
			{Thought: "ah", Tool: "node_status", Args: Args{}},
			{Thought: "done", Done: true, Answer: "recovered"},
		},
	}
	a := newAgent(t, model, testTools(&calls), false)

	result, err := a.Run(context.Background(), "goal")
	if err != nil {
		t.Fatalf("an unknown tool ended the run: %v", err)
	}
	if result.Answer != "recovered" {
		t.Errorf("answer = %q", result.Answer)
	}
	if !strings.Contains(strings.Join(model.prompts, "\n"), "node_status") {
		t.Error("the error did not list the real tool names")
	}
}

// A missing required argument must be reported before the tool runs.
func TestAMissingRequiredArgumentIsReported(t *testing.T) {
	var calls []string
	model := &scriptedModel{
		steps: []Step{
			{Thought: "call it", Tool: "needs_id", Args: Args{}},
			{Thought: "with the id", Tool: "needs_id", Args: Args{"model_id": "a/Model"}},
			{Thought: "done", Done: true, Answer: "ok"},
		},
	}
	a := newAgent(t, model, testTools(&calls), false)
	if _, err := a.Run(context.Background(), "goal"); err != nil {
		t.Fatal(err)
	}

	if len(calls) != 1 || calls[0] != "needs_id:a/Model" {
		t.Errorf("calls = %v; the tool should not have run without its argument", calls)
	}
	if !strings.Contains(strings.Join(model.prompts, "\n"), "needs model_id") {
		t.Error("the model was not told which argument was missing")
	}
}

// A model that loops must stop costing GPU time eventually.
func TestMaxStepsBoundsARun(t *testing.T) {
	var calls []string
	model := &scriptedModel{fallback: Step{Thought: "again", Tool: "node_status", Args: Args{}}}
	a := newAgent(t, model, testTools(&calls), false)
	a.MaxSteps = 3

	result, err := a.Run(context.Background(), "goal")
	if err != nil {
		t.Fatal(err)
	}
	if result.Steps != 3 {
		t.Errorf("steps = %d, want 3", result.Steps)
	}
	if !strings.Contains(result.Answer, "Stopped after 3 steps") {
		t.Errorf("answer = %q", result.Answer)
	}
}

func TestParseStep(t *testing.T) {
	step, err := ParseStep(`{"thought":"t","tool":"node_status","args":{"limit":5},"done":false,"answer":""}`)
	if err != nil {
		t.Fatal(err)
	}
	if step.Tool != "node_status" || step.Args.Int("limit") != 5 {
		t.Errorf("step = %+v", step)
	}

	fenced, err := ParseStep("```json\n{\"thought\":\"t\",\"tool\":\"\",\"args\":{},\"done\":true,\"answer\":\"a\"}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if !fenced.Done {
		t.Error("a fenced reply was not parsed")
	}

	if _, err := ParseStep("I think you should check the node status."); err == nil {
		t.Error("prose was parsed as a step")
	}
}

func TestArgsCoercion(t *testing.T) {
	a := Args{"n": float64(42), "s": " spaced ", "b": true, "quoted": "17"}
	if a.Int("n") != 42 {
		t.Errorf("Int = %d", a.Int("n"))
	}
	if a.Int("quoted") != 17 {
		t.Errorf("a quoted integer was not read: %d", a.Int("quoted"))
	}
	if a.String("s") != "spaced" {
		t.Errorf("String = %q", a.String("s"))
	}
	if !a.Bool("b") || a.Bool("missing") {
		t.Error("Bool is wrong")
	}
	if a.Int("missing") != 0 || a.String("missing") != "" {
		t.Error("missing arguments should be zero values")
	}
}

// The prompt must state which tools change the node and whether this run may
// use them.
func TestPromptMarksActingToolsAndPermission(t *testing.T) {
	var calls []string
	tools := testTools(&calls)

	readOnly := (&Agent{Tools: tools, AllowActions: false}).systemPrompt()
	if !strings.Contains(readOnly, "NOT permitted") || !strings.Contains(readOnly, "READ-ONLY") {
		t.Errorf("read-only prompt does not say actions are refused:\n%s", readOnly)
	}

	permitted := (&Agent{Tools: tools, AllowActions: true}).systemPrompt()
	if strings.Contains(permitted, "NOT permitted") {
		t.Error("a permitted run was told actions are refused")
	}
	if !strings.Contains(permitted, "MAY change the node") {
		t.Error("a permitted run was not told it may act")
	}
}
