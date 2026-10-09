package computing

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// --- backend counters -----------------------------------------------------

func TestParseBackendCountersLlamaCpp(t *testing.T) {
	text := `# HELP llamacpp:prompt_tokens_total Number of prompt tokens processed.
# TYPE llamacpp:prompt_tokens_total counter
llamacpp:prompt_tokens_total 1200
llamacpp:tokens_predicted_total 3400
llamacpp:requests_processing 1
`
	p, g, ok := parseBackendCounters(strings.NewReader(text))
	if !ok || p != 1200 || g != 3400 {
		t.Errorf("got prompt=%v generated=%v ok=%v", p, g, ok)
	}
}

func TestParseBackendCountersSumsLabelledLines(t *testing.T) {
	text := `vllm:generation_tokens_total{model_name="a b"} 10
vllm:generation_tokens_total{model_name="c"} 5
vllm:prompt_tokens_total{model_name="c"} 7
`
	p, g, ok := parseBackendCounters(strings.NewReader(text))
	if !ok || g != 15 || p != 7 {
		t.Errorf("got prompt=%v generated=%v ok=%v", p, g, ok)
	}
}

func TestParseBackendCountersUnknownEngine(t *testing.T) {
	if _, _, ok := parseBackendCounters(strings.NewReader("process_cpu_seconds_total 3\n")); ok {
		t.Error("a metrics page with no token counter must not read as zero usage")
	}
}

func TestGeneratedDeltasSurviveARestart(t *testing.T) {
	t0 := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)
	samples := []BackendUsageSample{
		{Time: t0, Generated: 100},
		{Time: t0.Add(10 * time.Minute), Generated: 150},
		{Time: t0.Add(70 * time.Minute), Generated: 30}, // restarted, then 30 more
		{Time: t0.Add(80 * time.Minute), Generated: 50},
	}
	d := generatedDeltas(samples, func(t time.Time) time.Time { return t.Truncate(time.Hour) })
	if d[t0] != 50 || d[t0.Add(time.Hour)] != 50 {
		t.Errorf("deltas = %v, want 50 in each hour", d)
	}
}

// --- usage series ---------------------------------------------------------

func TestUsageSeriesBySourceAndDirect(t *testing.T) {
	from := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	to := from.Add(2*time.Hour + 30*time.Minute)
	records := []usageRecord{
		{Model: "q", Source: "hub", StartTime: from.Add(5 * time.Minute), TokensIn: 100, TokensOut: 40},
		{Model: "q", Source: "", StartTime: from.Add(6 * time.Minute), TokensIn: 10, TokensOut: 10}, // legacy row
		{Model: "q", Source: "local", StartTime: from.Add(65 * time.Minute), TokensIn: 5, TokensOut: 20},
		{Model: "q", Source: "health", StartTime: from.Add(66 * time.Minute), TokensIn: 1, TokensOut: 1},
		{Model: "other", Source: "hub", StartTime: from.Add(70 * time.Minute), TokensOut: 999}, // a different endpoint
	}
	samples := map[string][]BackendUsageSample{
		"http://q": {
			{Time: from.Add(-time.Minute), Generated: 0},
			{Time: from.Add(30 * time.Minute), Generated: 50},   // hour 0: 50 generated, 50 recorded
			{Time: from.Add(90 * time.Minute), Generated: 1071}, // hour 1: 1021 generated, 21 recorded
		},
	}
	s := BuildUsageSeries(records, samples, map[string][]string{"http://q": {"q"}}, from, to, time.Hour, "24h")

	if len(s.Points) != 3 {
		t.Fatalf("points = %d, want a bucket per hour including empty ones", len(s.Points))
	}
	h0, h1 := s.Points[0], s.Points[1]
	if got := h0.Sources["hub"]; got.Requests != 2 || got.TokensOut != 50 {
		t.Errorf("hour 0 hub = %+v (a row with no source is hub traffic)", got)
	}
	if _, ok := h0.Sources[SourceDirect]; ok {
		t.Error("hour 0: everything the server generated was recorded, so there is no direct usage")
	}
	if got := h1.Sources[SourceDirect].TokensOut; got != 1000 {
		t.Errorf("hour 1 direct = %d, want 1021 generated - 21 recorded", got)
	}
	if got := h1.Models["q"][SourceDirect].TokensOut; got != 1000 {
		t.Errorf("direct usage not attributed to the endpoint's model: %d", got)
	}
	if got := s.Totals["local"].Requests; got != 1 {
		t.Errorf("local requests = %d", got)
	}
	if len(s.Direct) != 1 || !s.Direct[0].Since.Equal(from.Add(-time.Minute)) {
		t.Errorf("direct coverage = %+v", s.Direct)
	}
}

func TestUsageSeriesClampsDirectAtZero(t *testing.T) {
	from := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	records := []usageRecord{{Model: "q", Source: "hub", StartTime: from.Add(59 * time.Minute), TokensOut: 500}}
	samples := map[string][]BackendUsageSample{"http://q": {
		{Time: from, Generated: 0},
		{Time: from.Add(58 * time.Minute), Generated: 10},
	}}
	s := BuildUsageSeries(records, samples, map[string][]string{"http://q": {"q"}}, from, from.Add(time.Hour), time.Hour, "24h")
	if _, ok := s.Points[0].Sources[SourceDirect]; ok {
		t.Error("recorded exceeding counted is timing across a bucket edge, not negative direct usage")
	}
}

func TestUsageSeriesSkipsUnmeasuredEndpoints(t *testing.T) {
	from := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	s := BuildUsageSeries(nil, map[string][]BackendUsageSample{"http://q": {{Time: from, Generated: 5}}},
		map[string][]string{"http://q": {"q"}}, from, from.Add(time.Hour), time.Hour, "24h")
	if len(s.Direct) != 0 {
		t.Error("one sample has nothing to difference; the endpoint is not yet measured")
	}
}

// --- local gateway --------------------------------------------------------

func newGatewayService(t *testing.T, backend http.HandlerFunc) (*InferenceService, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(backend)
	t.Cleanup(srv.Close)
	s := NewInferenceService("test-node", t.TempDir())
	s.modelMappings["org/model"] = ModelMapping{Endpoint: srv.URL, LocalModel: "local-name"}
	// The WebSocket client is created by Start; the gateway only needs its
	// metrics recorder.
	s.client = &InferenceClient{metrics: NewInferenceMetrics()}
	return s, srv
}

func lastLocalRecord(t *testing.T, s *InferenceService) RequestMetric {
	t.Helper()
	page := s.client.metrics.QueryRequestHistory(RequestHistoryQuery{Source: string(SourceLocal)})
	if len(page.Requests) == 0 {
		t.Fatal("the request was not recorded as local")
	}
	return page.Requests[0]
}

func TestLocalGatewayForwardsAndRecords(t *testing.T) {
	var sawModel string
	s, _ := newGatewayService(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"local-name"`) {
			sawModel = "local-name"
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`)
	})
	gw := httptest.NewServer(s.LocalGatewayHandler())
	defer gw.Close()

	resp, err := http.Post(gw.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"org/model","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"hi"`) {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if sawModel != "local-name" {
		t.Error("the backend was not sent its local model name")
	}
	rec := lastLocalRecord(t, s)
	if !rec.Success || rec.TokensIn != 12 || rec.TokensOut != 3 || rec.Model != "org/model" {
		t.Errorf("record = %+v", rec)
	}
	if got := s.client.metrics.GetSnapshot().TotalRequests; got != 0 {
		t.Errorf("local work reached the aggregate counters earnings are priced from (%d)", got)
	}
}

func TestLocalGatewayStreams(t *testing.T) {
	s, _ := newGatewayService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":9}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	gw := httptest.NewServer(s.LocalGatewayHandler())
	defer gw.Close()

	resp, err := http.Post(gw.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"org/model","stream":true,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if l := sc.Text(); l != "" {
			lines = append(lines, l)
		}
	}
	resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" || len(lines) == 0 || lines[len(lines)-1] != "data: [DONE]" {
		t.Fatalf("stream = %v (%s)", lines, resp.Header.Get("Content-Type"))
	}
	rec := lastLocalRecord(t, s)
	if !rec.Success || !rec.Streaming || rec.TokensIn != 4 || rec.TokensOut != 9 {
		t.Errorf("record = %+v", rec)
	}
}

// A local request records who sent it: the User-Agent, and on Linux the
// process holding the client end of the connection — here, this test binary.
func TestLocalGatewayRecordsTheClient(t *testing.T) {
	s, _ := newGatewayService(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	})
	gw := httptest.NewServer(s.LocalGatewayHandler())
	defer gw.Close()

	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/chat/completions", strings.NewReader(`{"model":"org/model"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "batch-judge/1.0\r\nX-Injected: 1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// net/http refuses a header value with CR/LF; send it without.
		req.Header.Set("User-Agent", "batch-judge/1.0")
		if resp, err = http.DefaultClient.Do(req); err != nil {
			t.Fatal(err)
		}
	}
	resp.Body.Close()

	client := lastLocalRecord(t, s).Client
	if !strings.Contains(client, "batch-judge/1.0") {
		t.Errorf("client = %q, want the User-Agent in it", client)
	}
	if strings.ContainsAny(client, "\r\n") {
		t.Errorf("client = %q carries control characters", client)
	}
	if _, err := os.Stat("/proc/net/tcp"); err == nil {
		if want := fmt.Sprintf("pid %d", os.Getpid()); !strings.Contains(client, want) {
			t.Errorf("client = %q, want the calling process (%s)", client, want)
		}
	}
}

func TestLocalGatewayRejectsUnknownModel(t *testing.T) {
	s, _ := newGatewayService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("an unknown model must not reach a backend")
	})
	gw := httptest.NewServer(s.LocalGatewayHandler())
	defer gw.Close()
	resp, err := http.Post(gw.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"nope"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// /v1/models must agree with /v1/chat/completions: a model the gateway would
// refuse as disabled is not listed, and enabling it brings it back.
func TestLocalGatewayModelsHidesDisabled(t *testing.T) {
	s, srv := newGatewayService(t, func(w http.ResponseWriter, r *http.Request) {})
	s.modelMappings["org/off"] = ModelMapping{Endpoint: srv.URL}
	s.registry = NewModelRegistry("", nil)
	s.registry.models["org/off"] = &RegisteredModel{ID: "org/off", Endpoint: srv.URL, Enabled: true}
	if err := s.registry.DisableModel("org/off"); err != nil {
		t.Fatal(err)
	}
	gw := httptest.NewServer(s.LocalGatewayHandler())
	defer gw.Close()

	list := func() []string {
		resp, err := http.Get(gw.URL + "/v1/models")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, m := range out.Data {
			ids = append(ids, m.ID)
		}
		return ids
	}

	if got := list(); len(got) != 1 || got[0] != "org/model" {
		t.Errorf("with org/off disabled, /v1/models = %v, want [org/model]", got)
	}
	if err := s.registry.EnableModel("org/off"); err != nil {
		t.Fatal(err)
	}
	if got := list(); len(got) != 2 || got[0] != "org/model" || got[1] != "org/off" {
		t.Errorf("after enabling org/off, /v1/models = %v, want [org/model org/off]", got)
	}
}

func TestLocalGatewayBindsLoopbackOnly(t *testing.T) {
	s := NewInferenceService("test-node", t.TempDir())
	srv, err := s.StartLocalGateway(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if !strings.HasPrefix(srv.Addr, "127.0.0.1:") {
		t.Errorf("gateway bound to %q; it has no authentication and must stay on loopback", srv.Addr)
	}
}

func glmSeries(records []usageRecord, samples []BackendUsageSample, from time.Time, hours int) UsageSeries {
	return BuildUsageSeries(records, map[string][]BackendUsageSample{"http://g": samples},
		map[string][]string{"http://g": {"g"}}, from, from.Add(time.Duration(hours)*time.Hour), time.Hour, "24h")
}

// A request recorded in one hour but generated mostly in the next leaves a
// shortfall then a surplus of the same size. That is timing, and must net out
// rather than surface as direct usage.
func TestDirectNetsAcrossABucketEdge(t *testing.T) {
	from := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	records := []usageRecord{{Model: "g", Source: "hub", StartTime: from.Add(59 * time.Minute), TokensOut: 500}}
	samples := []BackendUsageSample{
		{Time: from.Add(-time.Minute), Generated: 0},
		{Time: from.Add(59*time.Minute + 30*time.Second), Generated: 10},
		{Time: from.Add(62 * time.Minute), Generated: 500},
	}
	s := glmSeries(records, samples, from, 2)
	for i, p := range s.Points {
		if d, ok := p.Sources[SourceDirect]; ok && d.TokensOut != 0 {
			t.Errorf("hour %d reports %d direct tokens from a request that straddled the edge", i, d.TokensOut)
		}
	}
}

// The carry is one bucket deep: a shortfall must not hide direct usage that
// happens hours later.
func TestDirectCarryDoesNotPersist(t *testing.T) {
	from := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	records := []usageRecord{{Model: "g", Source: "hub", StartTime: from.Add(10 * time.Minute), TokensOut: 1000}}
	samples := []BackendUsageSample{
		{Time: from.Add(-time.Minute), Generated: 0},
		{Time: from.Add(30 * time.Minute), Generated: 0},    // hour 0: recorded 1000, counted 0
		{Time: from.Add(90 * time.Minute), Generated: 0},    // hour 1: nothing
		{Time: from.Add(150 * time.Minute), Generated: 700}, // hour 2: 700 nobody recorded
	}
	s := glmSeries(records, samples, from, 3)
	if got := s.Points[2].Sources[SourceDirect].TokensOut; got != 700 {
		t.Errorf("hour 2 direct = %d, want 700 — an old shortfall swallowed it", got)
	}
}

// Direct input is a floor: the server's prompt counter skips cache hits, so
// only the part it processed beyond what the node recorded is certain.
func TestDirectInputIsALowerBound(t *testing.T) {
	from := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	records := []usageRecord{
		{Model: "g", Source: "hub", StartTime: from.Add(5 * time.Minute), TokensIn: 5000}, // mostly cache hits
		{Model: "g", Source: "hub", StartTime: from.Add(65 * time.Minute), TokensIn: 100},
	}
	samples := []BackendUsageSample{
		{Time: from.Add(-time.Minute)},
		{Time: from.Add(30 * time.Minute), Prompt: 800},   // processed less than recorded: cache
		{Time: from.Add(90 * time.Minute), Prompt: 10800}, // 10000 processed, 100 recorded
	}
	s := glmSeries(records, samples, from, 2)
	if d, ok := s.Points[0].Sources[SourceDirect]; ok && d.TokensIn != 0 {
		t.Errorf("hour 0 direct input = %d; cache hits must not read as direct work", d.TokensIn)
	}
	if got := s.Points[1].Sources[SourceDirect].TokensIn; got != 9900 {
		t.Errorf("hour 1 direct input = %d, want at least 10000-100", got)
	}
}

// The bucket where measurement begins is partial and is not judged.
func TestDirectSkipsThePartlyMeasuredBucket(t *testing.T) {
	from := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	records := []usageRecord{{Model: "g", Source: "hub", StartTime: from.Add(5 * time.Minute), TokensOut: 900}}
	samples := []BackendUsageSample{
		{Time: from.Add(40 * time.Minute), Generated: 0}, // sampling starts mid-hour 0
		{Time: from.Add(50 * time.Minute), Generated: 20},
		{Time: from.Add(80 * time.Minute), Generated: 320}, // hour 1: 300 nobody recorded
	}
	s := glmSeries(records, samples, from, 2)
	if _, ok := s.Points[0].Sources[SourceDirect]; ok {
		t.Error("the partly measured hour was judged")
	}
	if got := s.Points[1].Sources[SourceDirect].TokensOut; got != 300 {
		t.Errorf("hour 1 direct = %d, want 300 (no carry from the unmeasured part of hour 0)", got)
	}
}

// CLIProxyAPI answers /metrics with 404; with a management key the sampler
// drains its usage queue into a running total, so two samples read as a
// cumulative counter.
func TestBackendUsageReadsCLIProxyQueue(t *testing.T) {
	queue := []string{
		`[{"tokens":{"input_tokens":100,"output_tokens":7}},{"tokens":{"input_tokens":50,"output_tokens":3}}]`,
		`[{"tokens":{"input_tokens":20,"output_tokens":5}}]`,
	}
	statsOn := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer mk" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v0/management/usage-statistics-enabled":
			fmt.Fprintf(w, `{"usage-statistics-enabled":%v}`, statsOn)
		case "/v0/management/usage-queue":
			if len(queue) == 0 {
				fmt.Fprint(w, `[]`)
				return
			}
			fmt.Fprint(w, queue[0])
			queue = queue[1:]
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	b := NewBackendUsageSampler(nil)
	if _, _, err := b.read(srv.URL, ""); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("without a key: err = %v, want unmeasured", err)
	}
	b.managementKey = "mk"
	p, g, err := b.read(srv.URL, "")
	if err != nil || p != 150 || g != 10 {
		t.Fatalf("first read = %v/%v, %v; want 150/10", p, g, err)
	}
	if p, g, _ = b.read(srv.URL, ""); p != 170 || g != 15 {
		t.Errorf("second read = %v/%v, want the running total 170/15", p, g)
	}
	statsOn = false
	if _, _, err := b.read(srv.URL, ""); err == nil || !strings.Contains(err.Error(), "usage statistics are off") {
		t.Errorf("stats off: err = %v, want it reported rather than read as idle", err)
	}
	b.managementKey = "wrong"
	if _, _, err := b.read(srv.URL, ""); err == nil || !strings.Contains(err.Error(), "rejected the management key") {
		t.Errorf("wrong key: err = %v", err)
	}
}
