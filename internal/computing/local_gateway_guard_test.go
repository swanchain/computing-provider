package computing

import (
	"net/http"
	"strings"
	"testing"
)

// A web page must not be able to spend the accounts behind the gateway: a
// cross-origin POST carries Origin, and a DNS-rebinding page carries its own
// domain as Host. Neither may reach the backend. Ordinary clients send
// neither and must be unaffected, whatever Content-Type they use.
func TestGatewayRefusesBrowserRequests(t *testing.T) {
	reached := 0
	gw, _ := messagesGateway(t, func(w http.ResponseWriter, r *http.Request) {
		reached++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[]}`))
	})
	body := `{"model":"anthropic/claude-x","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`
	for _, c := range []struct {
		name, origin, host, ctype string
		want                      int
		reaches                   bool
	}{
		{"cross-site text/plain post", "https://attacker.example", "", "text/plain", http.StatusForbidden, false},
		{"sandboxed page (Origin: null)", "null", "", "application/json", http.StatusForbidden, false},
		{"dns rebinding", "", "rebind.attacker.example:9088", "application/json", http.StatusMisdirectedRequest, false},
		{"sdk client", "", "", "application/json", http.StatusOK, true},
		{"curl -d (form content type)", "", "", "application/x-www-form-urlencoded", http.StatusOK, true},
		{"lan forward by address", "", "40.49.6.2:9088", "application/json", http.StatusOK, true},
	} {
		before := reached
		req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/messages", strings.NewReader(body))
		req.Header.Set("Content-Type", c.ctype)
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		if c.host != "" {
			req.Host = c.host
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s: status %d, want %d", c.name, resp.StatusCode, c.want)
		}
		if got := reached > before; got != c.reaches {
			t.Errorf("%s: reached backend = %v, want %v", c.name, got, c.reaches)
		}
	}
}
