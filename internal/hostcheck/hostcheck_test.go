package hostcheck

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestAllowed(t *testing.T) {
	name, _ := os.Hostname()
	for _, tc := range []struct {
		host string
		want bool
	}{
		{"127.0.0.1:9087", true},
		{"[::1]:9088", true},
		{"40.49.6.2:9088", true}, // LAN address, as a forward sends it
		{"localhost:3060", true},
		{"dev.localhost", true},
		{name, true},
		{"node.example.lan", true}, // in extra
		{"attacker.example", false},
		{"attacker.example:9087", false},
		{"localhost.attacker.example", false},
		{"", false},
	} {
		if got := Allowed(tc.host, "node.example.lan"); got != tc.want {
			t.Errorf("Allowed(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestMiddlewareRejectsForeignHost(t *testing.T) {
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	for host, want := range map[string]int{"127.0.0.1:9087": 200, "rebind.attacker.example": http.StatusMisdirectedRequest} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %q: got %d, want %d", host, rec.Code, want)
		}
	}
}
