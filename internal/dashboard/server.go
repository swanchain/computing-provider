package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/gin-gonic/gin"
)

//go:embed ui/dist/*
var staticFiles embed.FS

// Server serves the dashboard UI
type Server struct {
	router    *gin.Engine
	port      string
	apiTarget string
}

// NewServer creates a new dashboard server
func NewServer(port string, apiTarget string) *Server {
	return &Server{
		port:      port,
		apiTarget: apiTarget,
	}
}

// Start starts the dashboard server
func (s *Server) Start() error {
	gin.SetMode(gin.ReleaseMode)
	s.router = gin.New()
	s.router.Use(gin.Recovery())

	// Serve static files from embedded filesystem
	staticFS, err := fs.Sub(staticFiles, "ui/dist")
	if err != nil {
		return err
	}

	// Serve static assets
	s.router.GET("/assets/*filepath", func(c *gin.Context) {
		c.FileFromFS(c.Request.URL.Path, http.FS(staticFS))
	})

	// Proxy API requests to the main server
	s.router.Any("/api/*path", s.proxyAPI)

	// Serve index.html for all other routes (SPA support)
	s.router.NoRoute(func(c *gin.Context) {
		// Don't serve index.html for API routes
		if strings.HasPrefix(c.Request.URL.Path, "/api") {
			c.JSON(404, gin.H{"error": "not found"})
			return
		}
		c.FileFromFS("/", http.FS(staticFS))
	})

	logs.GetLogger().Infof("Dashboard server starting on port %s", s.port)
	return s.router.Run(":" + s.port)
}

// proxyAPI proxies requests to the main API server
func (s *Server) proxyAPI(c *gin.Context) {
	// Create proxy request
	targetURL := s.apiTarget + c.Request.URL.Path
	if c.Request.URL.RawQuery != "" {
		targetURL += "?" + c.Request.URL.RawQuery
	}

	req, err := http.NewRequest(c.Request.Method, targetURL, c.Request.Body)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create proxy request"})
		return
	}

	// Copy headers
	for key, values := range c.Request.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Execute request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(502, gin.H{"error": "failed to reach API server"})
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			c.Header(key, value)
		}
	}

	c.Status(resp.StatusCode)
	buf := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			c.Writer.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
}
