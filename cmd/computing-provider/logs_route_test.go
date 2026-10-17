package main

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/swanchain/computing-provider-v2/internal/logview"
)

func logQueryFor(t *testing.T, rawQuery string) (logview.Query, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/v1/computing/inference/logs?"+rawQuery, nil)
	return logQuery(c)
}

func TestLogQueryParsesEveryParameter(t *testing.T) {
	q, err := logQueryFor(t, "levels=error,warn&since=2026-10-17T01:00:00Z&until=2026-10-17T02:00:00%2B08:00&q=Skyfall&limit=50")
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Levels) != 2 || q.Levels[1] != logview.LevelWarning {
		t.Errorf("levels = %v", q.Levels)
	}
	if !q.Since.Equal(time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC)) {
		t.Errorf("since = %v", q.Since)
	}
	if !q.Until.Equal(time.Date(2026, 10, 16, 18, 0, 0, 0, time.UTC)) {
		t.Errorf("until = %v, want the offset honoured", q.Until)
	}
	if q.Contains != "Skyfall" || q.Limit != 50 {
		t.Errorf("q=%q limit=%d", q.Contains, q.Limit)
	}
}

// A malformed parameter is the caller's mistake and must say so, not quietly
// widen the query to everything.
func TestLogQueryRejectsBadInput(t *testing.T) {
	for _, raw := range []string{"levels=eror", "since=yesterday", "until=2026-10-17", "limit=0", "limit=ten"} {
		if _, err := logQueryFor(t, raw); err == nil {
			t.Errorf("%s: want an error", raw)
		}
	}
}
