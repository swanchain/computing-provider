package computing

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A server that closes idle keep-alive connections quickly, as uvicorn does at
// its 5-second default. Reusing such a connection yields "connection reset by
// peer" and the health checker reports a healthy backend as unreachable.
func TestHealthProbeSurvivesServerSideKeepAliveClose(t *testing.T) {
	var hits int
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	srv.Config.IdleTimeout = 50 * time.Millisecond
	srv.Start()
	defer srv.Close()

	h := NewModelHealthChecker(DefaultHealthCheckConfig())

	for i := 0; i < 4; i++ {
		if _, err := h.probeEndpoint(srv.URL, ""); err != nil {
			t.Fatalf("probe %d failed: %v — a healthy backend must not read as unreachable", i+1, err)
		}
		// Longer than the server's idle timeout, so any pooled connection is
		// closed by the server before the next probe.
		time.Sleep(120 * time.Millisecond)
	}
	if hits != 4 {
		t.Errorf("server saw %d requests, want 4", hits)
	}
}

// Each probe must use a fresh connection, so none can be reused after the
// server has closed it.
func TestHealthProbeDoesNotReuseConnections(t *testing.T) {
	conns := make(map[string]bool)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	srv.Config.ConnState = func(c net.Conn, st http.ConnState) {
		if st == http.StateNew {
			conns[c.RemoteAddr().String()] = true
		}
	}
	srv.Start()
	defer srv.Close()

	h := NewModelHealthChecker(DefaultHealthCheckConfig())
	for i := 0; i < 3; i++ {
		if _, err := h.probeEndpoint(srv.URL, ""); err != nil {
			t.Fatal(err)
		}
	}
	if len(conns) != 3 {
		t.Errorf("server saw %d distinct connections for 3 probes, want 3 (no reuse)", len(conns))
	}
}
