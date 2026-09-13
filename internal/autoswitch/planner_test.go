package autoswitch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseDecision(t *testing.T) {
	decision, err := ParseDecision(`{"action":"switch","serve":["a/B"],"stop":[],"expected_daily_usd":1.5,"reason":"why"}`)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != "switch" || len(decision.Serve) != 1 || decision.ExpectedDailyUSD != 1.5 {
		t.Errorf("decision = %+v", decision)
	}
}

// Constrained decoding should make this unnecessary, but a fenced block is the
// one deviation some servers still produce, and stripping it is cheaper than a
// refused cycle.
func TestParseDecisionStripsAFencedBlock(t *testing.T) {
	for _, body := range []string{
		"```json\n{\"action\":\"keep\",\"serve\":[],\"stop\":[],\"expected_daily_usd\":0,\"reason\":\"r\"}\n```",
		"```\n{\"action\":\"keep\",\"serve\":[],\"stop\":[],\"expected_daily_usd\":0,\"reason\":\"r\"}\n```",
	} {
		decision, err := ParseDecision(body)
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if decision.Action != "keep" {
			t.Errorf("action = %q", decision.Action)
		}
	}
}

// A decision extracted by guesswork is worse than none, because it would be
// acted on.
func TestParseDecisionRefusesProse(t *testing.T) {
	for _, body := range []string{
		"I think you should switch to model X because it earns more.",
		"",
		"{not json",
		`{"serve":["a/B"]}`, // no action
	} {
		if _, err := ParseDecision(body); err == nil {
			t.Errorf("parsed %q as a decision", body)
		}
	}
}

// The request must constrain the planner to the schema, or a reply that drifts
// into prose becomes a refused cycle.
func TestDecideSendsAConstrainedSchema(t *testing.T) {
	var got map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"action\":\"keep\",\"serve\":[],\"stop\":[],\"expected_daily_usd\":0,\"reason\":\"steady\"}"}}]}`))
	}))
	defer srv.Close()

	planner := NewPlanner(srv.URL, "planner/Model", "")
	decision, _, err := planner.Decide(context.Background(), node(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionKeep {
		t.Errorf("action = %q", decision.Action)
	}

	format, ok := got["response_format"].(map[string]interface{})
	if !ok {
		t.Fatal("no response_format was sent")
	}
	if format["type"] != "json_schema" {
		t.Errorf("response_format type = %v", format["type"])
	}
	schema, ok := format["json_schema"].(map[string]interface{})
	if !ok || schema["strict"] != true {
		t.Errorf("schema was not strict: %v", format["json_schema"])
	}
}

// The snapshot has to reach the planner, or it is deciding on nothing.
func TestDecideSendsTheSnapshot(t *testing.T) {
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sent = string(body)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"action\":\"keep\",\"serve\":[],\"stop\":[],\"expected_daily_usd\":0,\"reason\":\"r\"}"}}]}`))
	}))
	defer srv.Close()

	snapshot := node(
		[]ServingModel{served("current/Model", true, true, 3)},
		[]MarketModel{fitting("candidate/Model", 8, 9)},
	)
	if _, _, err := NewPlanner(srv.URL, "p", "").Decide(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"current/Model", "candidate/Model", "vram_fit"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the snapshot sent to the planner omits %q", want)
		}
	}
}

// An unreachable or failing planner must be an error the caller can report,
// not a fabricated decision.
func TestDecideReportsFailuresRatherThanInventingADecision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"model is loading"}}`, http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	decision, _, err := NewPlanner(srv.URL, "p", "").Decide(context.Background(), node(nil, nil))
	if err == nil {
		t.Fatal("expected an error")
	}
	if decision != nil {
		t.Errorf("a decision was returned alongside the error: %+v", decision)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should carry the status: %v", err)
	}
}

// A reply that cannot be parsed must come back with what was actually said, so
// an operator can see why the cycle was refused.
func TestDecideReturnsTheRawReplyOnAParseFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"I would keep things as they are."}}]}`))
	}))
	defer srv.Close()

	decision, raw, err := NewPlanner(srv.URL, "p", "").Decide(context.Background(), node(nil, nil))
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if decision != nil {
		t.Error("a decision was returned for unparseable content")
	}
	if raw == nil || !strings.Contains(raw.Content, "keep things as they are") {
		t.Errorf("raw reply = %+v", raw)
	}
}

// An unparseable reply, fed to the guardrails, must be refused rather than
// treated as agreement to do nothing.
func TestAnUnparseableReplyFlowsThroughToARefusal(t *testing.T) {
	verdict := Evaluate(nil, node(nil, nil), Policy{})
	if verdict.Allowed {
		t.Fatal("an unparseable planner reply was allowed")
	}
	if verdict.Effective.Action != ActionKeep {
		t.Errorf("effective action = %q", verdict.Effective.Action)
	}
}
