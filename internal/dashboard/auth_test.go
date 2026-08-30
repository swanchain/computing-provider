package dashboard

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestEnsureAccessTokenCreatesAndReusesOwnerOnlyToken(t *testing.T) {
	dir := t.TempDir()
	token, path, err := EnsureAccessToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) < 40 {
		t.Fatalf("token length = %d, want a 256-bit encoded token", len(token))
	}
	if path != filepath.Join(dir, AccessTokenFile) {
		t.Fatalf("path = %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %04o, want 0600", got)
	}
	again, _, err := EnsureAccessToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again != token {
		t.Fatal("token changed between reads")
	}
}

func TestProtectWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ProtectWrites("secret"))
	r.GET("/resource", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.POST("/resource", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for _, test := range []struct {
		name   string
		method string
		header string
		want   int
	}{
		{name: "read stays public", method: http.MethodGet, want: http.StatusNoContent},
		{name: "write without token", method: http.MethodPost, want: http.StatusUnauthorized},
		{name: "write with wrong token", method: http.MethodPost, header: "Bearer nope", want: http.StatusUnauthorized},
		{name: "write with token", method: http.MethodPost, header: "Bearer secret", want: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, "/resource", nil)
			if test.header != "" {
				req.Header.Set("Authorization", test.header)
			}
			response := httptest.NewRecorder()
			r.ServeHTTP(response, req)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}
