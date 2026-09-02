package computing

import "testing"

// seed records n requests, newest last, alternating the source so a filter has
// something to separate.
func seedHistory(m *InferenceMetrics, ids []string, sources []RequestSource, model string) {
	for i, id := range ids {
		m.RecordRequest(RequestMetric{RequestID: id, Model: model, Source: sources[i], Success: true})
	}
}

func idsOf(page RequestHistoryPage) []string {
	out := make([]string, 0, len(page.Requests))
	for _, r := range page.Requests {
		out = append(out, r.RequestID)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestQueryRequestHistoryPaginates(t *testing.T) {
	m := NewInferenceMetrics()
	seedHistory(m,
		[]string{"1", "2", "3", "4", "5"},
		[]RequestSource{SourceHub, SourceHub, SourceHub, SourceHub, SourceHub},
		"m")

	first := m.QueryRequestHistory(RequestHistoryQuery{Limit: 2})
	if got := idsOf(first); !equal(got, []string{"5", "4"}) {
		t.Errorf("page 1 = %v, want newest first [5 4]", got)
	}
	if first.Total != 5 {
		t.Errorf("total = %d, want 5 — the count is of matches, not of the page", first.Total)
	}

	second := m.QueryRequestHistory(RequestHistoryQuery{Limit: 2, Offset: 2})
	if got := idsOf(second); !equal(got, []string{"3", "2"}) {
		t.Errorf("page 2 = %v, want [3 2]", got)
	}

	last := m.QueryRequestHistory(RequestHistoryQuery{Limit: 2, Offset: 4})
	if got := idsOf(last); !equal(got, []string{"1"}) {
		t.Errorf("final page = %v, want the single remaining [1]", got)
	}

	past := m.QueryRequestHistory(RequestHistoryQuery{Limit: 2, Offset: 99})
	if len(past.Requests) != 0 {
		t.Errorf("offset past the end returned %d rows, want none", len(past.Requests))
	}
	if past.Total != 5 {
		t.Errorf("total past the end = %d, want 5 so the UI can still show the range", past.Total)
	}
}

func TestQueryRequestHistoryFiltersBySource(t *testing.T) {
	m := NewInferenceMetrics()
	seedHistory(m,
		[]string{"h1", "p1", "h2", "s1"},
		[]RequestSource{SourceHub, SourceHealth, SourceHub, SourceSelfCheck},
		"m")

	hub := m.QueryRequestHistory(RequestHistoryQuery{Source: string(SourceHub)})
	if got := idsOf(hub); !equal(got, []string{"h2", "h1"}) {
		t.Errorf("hub filter = %v, want [h2 h1]", got)
	}
	if hub.Total != 2 {
		t.Errorf("hub total = %d, want 2", hub.Total)
	}

	health := m.QueryRequestHistory(RequestHistoryQuery{Source: string(SourceHealth)})
	if got := idsOf(health); !equal(got, []string{"p1"}) {
		t.Errorf("health filter = %v, want [p1]", got)
	}

	all := m.QueryRequestHistory(RequestHistoryQuery{})
	if all.Total != 4 {
		t.Errorf("unfiltered total = %d, want all 4", all.Total)
	}
}

// Records predating the Source field all arrived over the WebSocket. Filtering
// to Hub has to include them, or an operator's older history vanishes when they
// touch the filter.
func TestQueryRequestHistorySourcelessRecordsCountAsHub(t *testing.T) {
	m := NewInferenceMetrics()
	m.RecordRequest(RequestMetric{RequestID: "old", Model: "m", Success: true}) // no Source
	m.RecordRequest(RequestMetric{RequestID: "new", Model: "m", Source: SourceHub, Success: true})
	m.RecordRequest(RequestMetric{RequestID: "probe", Model: "m", Source: SourceHealth, Success: true})

	hub := m.QueryRequestHistory(RequestHistoryQuery{Source: string(SourceHub)})
	if got := idsOf(hub); !equal(got, []string{"new", "old"}) {
		t.Errorf("hub filter = %v, want the unlabelled record included: [new old]", got)
	}

	health := m.QueryRequestHistory(RequestHistoryQuery{Source: string(SourceHealth)})
	if got := idsOf(health); !equal(got, []string{"probe"}) {
		t.Errorf("health filter = %v, want only [probe]", got)
	}
}

func TestQueryRequestHistoryCombinesModelAndSource(t *testing.T) {
	m := NewInferenceMetrics()
	m.RecordRequest(RequestMetric{RequestID: "a", Model: "alpha", Source: SourceHub, Success: true})
	m.RecordRequest(RequestMetric{RequestID: "b", Model: "beta", Source: SourceHub, Success: true})
	m.RecordRequest(RequestMetric{RequestID: "c", Model: "alpha", Source: SourceHealth, Success: true})

	page := m.QueryRequestHistory(RequestHistoryQuery{Model: "alpha", Source: string(SourceHub)})
	if got := idsOf(page); !equal(got, []string{"a"}) {
		t.Errorf("model+source filter = %v, want [a]", got)
	}
	if page.Total != 1 {
		t.Errorf("total = %d, want 1", page.Total)
	}
}

// The old two-argument helper is still used by the model detail view.
func TestGetRequestHistoryStillHonoursModelFilter(t *testing.T) {
	m := NewInferenceMetrics()
	m.RecordRequest(RequestMetric{RequestID: "a", Model: "alpha", Source: SourceHub, Success: true})
	m.RecordRequest(RequestMetric{RequestID: "b", Model: "beta", Source: SourceHub, Success: true})

	got := m.GetRequestHistory(10, "beta")
	if len(got) != 1 || got[0].RequestID != "b" {
		t.Errorf("got %+v, want the single beta record", got)
	}
}
