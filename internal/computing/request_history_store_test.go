package computing

import (
	"testing"
	"time"

	"github.com/swanchain/computing-provider-v2/internal/db"
)

// withStore gives each test its own database file, so these exercise the real
// SQLite path rather than a stand-in.
func withStore(t *testing.T, retentionDays int, maxRows int64) *RequestStore {
	t.Helper()
	db.InitDb(t.TempDir())
	if db.NewDbService() == nil {
		t.Skip("database unavailable")
	}
	store := NewRequestStore(retentionDays, maxRows)
	if err := store.Start(); err != nil {
		t.Fatalf("start store: %v", err)
	}
	t.Cleanup(store.Stop)
	return store
}

// flush pushes queued records to disk by stopping the writer, which drains.
func flush(t *testing.T, store *RequestStore) {
	t.Helper()
	store.Stop()
}

func record(store *RequestStore, id, model string, source RequestSource, at time.Time) {
	store.Record(RequestMetric{
		RequestID: id, Model: model, Source: source,
		StartTime: at, EndTime: at.Add(time.Second),
		LatencyMs: 120, TokensIn: 10, TokensOut: 5, Success: true,
	})
}

func TestRequestStorePersistsAndPages(t *testing.T) {
	store := withStore(t, 7, 1000)
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		record(store, string(rune('a'+i)), "m", SourceHub, base.Add(time.Duration(i)*time.Minute))
	}
	flush(t, store)

	page, err := store.Query(RequestHistoryQuery{Limit: 2})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if page.Total != 5 {
		t.Errorf("total = %d, want 5", page.Total)
	}
	if len(page.Requests) != 2 {
		t.Fatalf("page held %d rows, want 2", len(page.Requests))
	}
	// Newest first.
	if page.Requests[0].RequestID != "e" || page.Requests[1].RequestID != "d" {
		t.Errorf("page 1 = %s,%s — want the two newest (e,d)", page.Requests[0].RequestID, page.Requests[1].RequestID)
	}

	second, err := store.Query(RequestHistoryQuery{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if second.Requests[0].RequestID != "c" {
		t.Errorf("page 2 started at %s, want c", second.Requests[0].RequestID)
	}
}

func TestRequestStoreFiltersByModelAndSource(t *testing.T) {
	store := withStore(t, 7, 1000)
	now := time.Now().UTC()
	record(store, "h1", "alpha", SourceHub, now)
	record(store, "p1", "alpha", SourceHealth, now)
	record(store, "h2", "beta", SourceHub, now)
	flush(t, store)

	hub, err := store.Query(RequestHistoryQuery{Source: string(SourceHub)})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if hub.Total != 2 {
		t.Errorf("hub total = %d, want 2", hub.Total)
	}

	combined, err := store.Query(RequestHistoryQuery{Model: "alpha", Source: string(SourceHub)})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if combined.Total != 1 || combined.Requests[0].RequestID != "h1" {
		t.Errorf("model+source = %+v, want just h1", combined.Requests)
	}
}

// Rows written before the source column carry none, and all of them arrived
// over the WebSocket. Filtering to Hub must include them, matching what the
// in-memory path does.
func TestRequestStoreTreatsSourcelessRowsAsHub(t *testing.T) {
	store := withStore(t, 7, 1000)
	now := time.Now().UTC()
	store.Record(RequestMetric{RequestID: "old", Model: "m", StartTime: now, Success: true}) // no Source
	record(store, "new", "m", SourceHub, now.Add(time.Minute))
	record(store, "probe", "m", SourceHealth, now.Add(2*time.Minute))
	flush(t, store)

	hub, err := store.Query(RequestHistoryQuery{Source: string(SourceHub)})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if hub.Total != 2 {
		t.Fatalf("hub total = %d, want 2 — the unlabelled row must count as hub", hub.Total)
	}
}

// History outliving a restart is the whole point: a second store over the same
// database must see what the first one wrote.
func TestRequestStoreSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	db.InitDb(dir)
	if db.NewDbService() == nil {
		t.Skip("database unavailable")
	}

	first := NewRequestStore(7, 1000)
	if err := first.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	record(first, "before-restart", "m", SourceHub, time.Now().UTC())
	first.Stop() // drains

	second := NewRequestStore(7, 1000)
	if err := second.Start(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer second.Stop()

	page, err := second.Query(RequestHistoryQuery{Limit: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if page.Total != 1 || page.Requests[0].RequestID != "before-restart" {
		t.Errorf("after restart the store held %+v, want the record written before it", page.Requests)
	}
}

func TestRequestStorePrunesByAge(t *testing.T) {
	store := withStore(t, 1, 1000)
	now := time.Now().UTC()
	record(store, "old", "m", SourceHub, now.AddDate(0, 0, -3))
	record(store, "fresh", "m", SourceHub, now)
	flush(t, store)

	store.prune()

	page, err := store.Query(RequestHistoryQuery{Limit: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if page.Total != 1 || page.Requests[0].RequestID != "fresh" {
		t.Errorf("after pruning: %+v, want only the row inside the retention window", page.Requests)
	}
}

func TestRequestStorePrunesByRowCap(t *testing.T) {
	store := withStore(t, 30, 3)
	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		record(store, string(rune('a'+i)), "m", SourceHub, now.Add(time.Duration(i)*time.Second))
	}
	flush(t, store)

	store.prune()

	page, err := store.Query(RequestHistoryQuery{Limit: 50})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if page.Total != 3 {
		t.Errorf("total = %d, want the 3-row cap enforced", page.Total)
	}
	// The cap must keep the newest, not an arbitrary three.
	if page.Requests[0].RequestID != "j" {
		t.Errorf("newest kept = %s, want j", page.Requests[0].RequestID)
	}
}

// Recording must never block serving, even when the writer cannot keep up.
func TestRequestStoreRecordNeverBlocks(t *testing.T) {
	store := NewRequestStore(7, 1000) // not started: nothing drains the queue
	now := time.Now().UTC()
	done := make(chan struct{})
	go func() {
		for i := 0; i < queueSize+500; i++ {
			store.Record(RequestMetric{RequestID: "x", Model: "m", StartTime: now})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Record blocked when the queue was full — it must drop instead")
	}
	if store.Dropped() == 0 {
		t.Error("overflow should be counted, not silent")
	}
}

// An upstream error body can be arbitrarily long, and there is one row per
// request.
func TestRequestStoreTruncatesLongErrors(t *testing.T) {
	store := withStore(t, 7, 1000)
	long := make([]byte, 4000)
	for i := range long {
		long[i] = 'x'
	}
	store.Record(RequestMetric{
		RequestID: "e", Model: "m", Source: SourceHub,
		StartTime: time.Now().UTC(), ErrorReason: string(long),
	})
	flush(t, store)

	page, err := store.Query(RequestHistoryQuery{Limit: 1})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(page.Requests) != 1 {
		t.Fatalf("expected the row back")
	}
	if got := len(page.Requests[0].ErrorReason); got != maxErrorReasonBytes {
		t.Errorf("stored error reason is %d bytes, want it capped at %d", got, maxErrorReasonBytes)
	}
}
