package computing

import (
	"context"
	"net/url"
)

// requestRoute is shared only by one request's synchronous forwarding path.
// It records what this node selected, not which credential a proxy used.
type requestRoute struct {
	endpoint string
	model    string
	attempts int
}

type requestRouteKey struct{}

func withRequestRoute(ctx context.Context) (context.Context, *requestRoute) {
	route := &requestRoute{}
	return context.WithValue(ctx, requestRouteKey{}, route), route
}

func selectedRoute(ctx context.Context, endpoint, requestedModel, localModel string) {
	route, _ := ctx.Value(requestRouteKey{}).(*requestRoute)
	if route == nil {
		return
	}
	route.endpoint = safeRouteEndpoint(endpoint)
	route.model = requestedModel
	if localModel != "" {
		route.model = localModel
	}
}

func routeAttempt(ctx context.Context) {
	if route, _ := ctx.Value(requestRouteKey{}).(*requestRoute); route != nil {
		route.attempts++
	}
}

func (route *requestRoute) apply(req *RequestMetric) {
	if route == nil {
		return
	}
	req.RouteRecorded = true
	req.BackendEndpoint = route.endpoint
	req.BackendModel = route.model
	req.BackendAttempts = route.attempts
}

// Keep only the origin. Backend URLs may contain credentials, paths or query
// parameters that must never end up in persistent request history.
func safeRouteEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return "(invalid backend URL)"
	}
	return u.Scheme + "://" + u.Host
}
