package computing

import (
	"context"
	"testing"
)

func TestRequestRouteRecordsSelectionAndAttempts(t *testing.T) {
	ctx, route := withRequestRoute(context.Background())
	selectedRoute(ctx, "https://user:secret@backend.example:8318/v1?token=secret", "public/model", "backend/model")
	routeAttempt(ctx)
	routeAttempt(ctx)
	rec := RequestMetric{}
	route.apply(&rec)
	if !rec.RouteRecorded || rec.BackendEndpoint != "https://backend.example:8318" || rec.BackendModel != "backend/model" || rec.BackendAttempts != 2 {
		t.Fatalf("route = %+v", rec)
	}
}

func TestRequestRouteNotForwarded(t *testing.T) {
	_, route := withRequestRoute(context.Background())
	rec := RequestMetric{}
	route.apply(&rec)
	if !rec.RouteRecorded || rec.BackendEndpoint != "" || rec.BackendAttempts != 0 {
		t.Fatalf("route = %+v", rec)
	}
}
