package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestConfigureEncodedPathParametersPreservesModelID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	configureEncodedPathParameters(router)

	var modelID string
	router.GET("/models/:model_id/metrics", func(c *gin.Context) {
		modelID = c.Param("model_id")
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/models/openai%2Fgpt-5.4/metrics", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected model metrics route to match, got status %d", response.Code)
	}
	if modelID != "openai/gpt-5.4" {
		t.Fatalf("expected decoded model ID, got %q", modelID)
	}
}
