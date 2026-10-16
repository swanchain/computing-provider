package computing

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
)

// The local gateway's backends: a hub model's local_endpoint, and the
// endpoint of a local-only model.
//
// Only models registered with Swan Inference are health-checked. A check
// exists to keep the hub from routing to a model that cannot serve; nothing
// routes to these but the operator's own clients, which see a failure the
// moment it happens. Probing them anyway costs requests — every 30 seconds
// against what is often a metered proxy or a third-party API — to learn what
// the next real request would have said.
//
// So a gateway backend's status is the outcome of the last request the
// gateway sent it: reachable if the backend answered, failing if it did not.
// Before the first request it is unknown.

// localBackendStatus is one endpoint's last observed request.
type localBackendStatus struct {
	OK        bool
	Error     string
	CheckedAt time.Time
}

type localBackendChecker struct {
	mu     sync.Mutex
	status map[string]localBackendStatus // keyed by safeRouteEndpoint form
}

func newLocalBackendChecker() *localBackendChecker {
	return &localBackendChecker{status: map[string]localBackendStatus{}}
}

// Observe records the outcome of a gateway request to endpoint.
func (c *localBackendChecker) Observe(endpoint string, st localBackendStatus) {
	if c == nil || endpoint == "" {
		return
	}
	c.mu.Lock()
	prev, seen := c.status[endpoint]
	c.status[endpoint] = st
	c.mu.Unlock()
	if seen && prev.OK != st.OK {
		if st.OK {
			logs.GetLogger().Infof("Local gateway backend %s is answering again", endpoint)
		} else {
			logs.GetLogger().Warnf("Local gateway backend %s is failing: %s", endpoint, st.Error)
		}
	}
}

// Status returns an endpoint's last observed request; ok is false before the
// first.
func (c *localBackendChecker) Status(endpoint string) (localBackendStatus, bool) {
	if c == nil {
		return localBackendStatus{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.status[safeRouteEndpoint(endpoint)]
	return st, ok
}

// backendAnswered reports whether a gateway response status means the backend
// itself answered. A 400 or 429 is the request or the load, not the backend;
// a 5xx, a 401/403/404, or no response at all (reported as 502) is the
// backend. This is the classification the self-check uses.
func backendAnswered(code int) bool {
	switch {
	case code < 400:
		return true
	case code == http.StatusBadRequest, code == http.StatusRequestEntityTooLarge,
		code == http.StatusUnprocessableEntity, code == http.StatusTooManyRequests:
		return true
	}
	return false
}

// observeLocal files a finished gateway request as its backend's status. A
// request the client abandoned says nothing about the backend.
func (s *InferenceService) observeLocal(r *http.Request, rec *RequestMetric) {
	if !rec.RouteRecorded || rec.BackendEndpoint == "" || r.Context().Err() != nil {
		return
	}
	st := localBackendStatus{OK: rec.Success || backendAnswered(capturedStatus(r)), CheckedAt: rec.EndTime}
	if !st.OK {
		st.Error = rec.ErrorReason
	}
	s.localChecker.Observe(rec.BackendEndpoint, st)
}

// gatewayEndpoint is the backend the local gateway uses for a model.
func gatewayEndpoint(m *RegisteredModel) string {
	if m.LocalEndpoint != "" {
		return m.LocalEndpoint
	}
	if m.LocalOnly {
		return m.Endpoint
	}
	return ""
}

// withLocalHealth fills in each model's local backend status for display.
func (s *InferenceService) withLocalHealth(models []*RegisteredModel) []*RegisteredModel {
	for _, m := range models {
		ep := gatewayEndpoint(m)
		if ep == "" {
			continue
		}
		st, ok := s.localChecker.Status(ep)
		switch {
		case !ok:
			m.LocalHealth = "unknown"
		case st.OK:
			m.LocalHealth = "healthy"
		default:
			m.LocalHealth, m.LocalError = "unreachable", st.Error
		}
	}
	return models
}

// onThisMachine reports whether an endpoint's host is a loopback address.
// The CLIProxyAPI management key is sent only to these: anything else may be
// a third party, and a 404 from its /metrics is no evidence it is a proxy.
func onThisMachine(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// statusCapture records the status a gateway handler wrote, so the request
// can be filed against its backend once it finishes.
type statusCapture struct {
	http.ResponseWriter
	code int
}

func (w *statusCapture) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusCapture) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Flush keeps streaming working through the wrapper.
func (w *statusCapture) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type statusCaptureKey struct{}

func withStatusCapture(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc := &statusCapture{ResponseWriter: w}
		next.ServeHTTP(sc, r.WithContext(context.WithValue(r.Context(), statusCaptureKey{}, sc)))
	})
}

// capturedStatus is the status written so far for a gateway request.
func capturedStatus(r *http.Request) int {
	if sc, ok := r.Context().Value(statusCaptureKey{}).(*statusCapture); ok {
		return sc.code
	}
	return 0
}
