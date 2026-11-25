// Package hostcheck rejects requests whose Host header names a host this
// machine does not answer to.
//
// The API, the dashboard and the local gateway answer on loopback or a LAN
// address and trust whoever can reach them. A web page cannot read their
// responses across origins, but DNS rebinding gets around that: the page's
// own domain is re-pointed at 127.0.0.1, so the browser treats the node as
// same-origin. The one thing that gives it away is the Host header, which
// still carries the attacker's domain. Requiring an IP literal, localhost or
// this machine's own name closes that.
package hostcheck

import (
	"net"
	"net/http"
	"os"
	"strings"
)

// Allowed reports whether host (a Host header value, port optional) is one a
// legitimate client of this machine would send. extra names are accepted as
// well, for operators who reach the node by a DNS name of their own.
//
// The port is not compared: a tunnel or port forward legitimately changes it,
// and rebinding is identified by the name, not the port.
func Allowed(host string, extra ...string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.ToLower(strings.TrimSuffix(strings.Trim(h, "[]"), "."))
	if h == "" {
		return false
	}
	if net.ParseIP(h) != nil {
		return true // a rebinding attack always arrives under a name
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if name, err := os.Hostname(); err == nil && strings.EqualFold(h, name) {
		return true
	}
	for _, e := range extra {
		if e != "" && strings.EqualFold(h, strings.TrimSuffix(e, ".")) {
			return true
		}
	}
	return false
}

// Middleware wraps next, answering 421 Misdirected Request to a request whose
// Host is not Allowed.
func Middleware(next http.Handler, extra ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Allowed(r.Host, extra...) {
			http.Error(w, `{"error":"host not allowed"}`, http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}
