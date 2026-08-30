package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestProxyAPIPreservesEncodedModelSlash(t *testing.T) {
	requestURI := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURI <- r.RequestURI
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	gin.SetMode(gin.TestMode)
	server := &Server{apiTarget: upstream.URL}
	router := gin.New()
	router.UseRawPath = true
	router.UnescapePathValues = true
	router.Any("/api/*path", server.proxyAPI)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/computing/inference/models/openai%2Fgpt-5.4/metrics?window=recent", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected upstream response, got status %d", response.Code)
	}
	if got := <-requestURI; got != "/api/v1/computing/inference/models/openai%2Fgpt-5.4/metrics?window=recent" {
		t.Fatalf("proxy changed encoded model path: %q", got)
	}
}
