package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	cors "github.com/itsjamie/gin-cors"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/computing"
	"github.com/swanchain/computing-provider-v2/util"
	"github.com/urfave/cli/v2"
)

var runCmd = &cli.Command{
	Name:  "run",
	Usage: "Start the computing provider",
	Action: func(cctx *cli.Context) error {
		return runDaemon()
	},
}

// checkDockerAvailable checks if Docker is available and responding with a timeout
// Returns true if Docker is available, false otherwise
func checkDockerAvailable() bool {
	// Check if docker command exists
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}

	// Check if Docker daemon is responding with timeout
	cmd := exec.Command("docker", "info")
	if err := cmd.Start(); err != nil {
		return false
	}

	done := make(chan error)
	go func() { done <- cmd.Wait() }()

	select {
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		return false
	case err := <-done:
		if err != nil {
			return false
		}
	}

	return true
}

// runDaemon starts the computing provider daemon
func runDaemon() error {
	logs.GetLogger().Info("Starting computing provider...")
	cpRepoPath, _ := os.LookupEnv("CP_PATH")

	// Check Docker availability (optional for Inference-only mode)
	dockerAvailable := checkDockerAvailable()

	if !dockerAvailable {
		logs.GetLogger().Info("Docker not available - running in Inference-only mode (Ollama)")
	}

	if err := conf.InitConfig(cpRepoPath, true); err != nil {
		logs.GetLogger().Fatal(err)
	}
	logs.GetLogger().Info("Your config file is:", filepath.Join(cpRepoPath, "config.toml"))

	// Check if private_key was copied from another machine
	computing.CheckMachineIdentity(cpRepoPath)

	// Start Inference mode (Swan Inference marketplace) if enabled
	nodeID := computing.GetNodeId(cpRepoPath)
	inferenceService := computing.NewInferenceService(nodeID, cpRepoPath)
	if err := inferenceService.Start(); err != nil {
		logs.GetLogger().Errorf("Failed to start Inference service: %v", err)
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	r.Use(cors.Middleware(cors.Config{
		Origins:         "*",
		Methods:         "GET, PUT, POST, DELETE",
		RequestHeaders:  "Origin, Authorization, Content-Type",
		ExposedHeaders:  "",
		MaxAge:          50 * time.Second,
		ValidateHeaders: false,
	}))
	pprof.Register(r)

	router := r.Group("/api/v1/computing")

	// Inference metrics endpoints
	router.GET("/inference/metrics", func(c *gin.Context) {
		metrics := inferenceService.GetMetrics()
		if metrics == nil {
			c.JSON(503, gin.H{"error": "Inference service not running"})
			return
		}
		c.JSON(200, metrics)
	})
	router.GET("/inference/metrics/prometheus", func(c *gin.Context) {
		prometheusMetrics := inferenceService.GetMetricsPrometheus()
		if prometheusMetrics == "" {
			c.String(503, "# Inference service not running\n")
			return
		}
		c.Header("Content-Type", "text/plain; charset=utf-8")
		c.String(200, prometheusMetrics)
	})
	router.GET("/inference/status", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"connected":     inferenceService.IsConnected(),
			"active_models": inferenceService.GetActiveModels(),
		})
	})

	// Model management endpoints
	router.GET("/inference/models", func(c *gin.Context) {
		models := inferenceService.GetAllModels()
		summary := inferenceService.GetModelsSummary()
		c.JSON(200, gin.H{
			"models":  models,
			"summary": summary,
		})
	})
	router.GET("/inference/models/:model_id", func(c *gin.Context) {
		modelID := c.Param("model_id")
		model, ok := inferenceService.GetModelStatus(modelID)
		if !ok {
			c.JSON(404, gin.H{"error": "model not found"})
			return
		}
		c.JSON(200, model)
	})
	router.GET("/inference/models/:model_id/health", func(c *gin.Context) {
		modelID := c.Param("model_id")
		health, ok := inferenceService.GetModelHealth(modelID)
		if !ok {
			c.JSON(404, gin.H{"error": "model not found"})
			return
		}
		c.JSON(200, health)
	})
	router.GET("/inference/health", func(c *gin.Context) {
		health := inferenceService.GetAllModelHealth()
		c.JSON(200, health)
	})
	router.POST("/inference/models/:model_id/enable", func(c *gin.Context) {
		modelID := c.Param("model_id")
		if err := inferenceService.EnableModel(modelID); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"status": "enabled", "model_id": modelID})
	})
	router.POST("/inference/models/:model_id/disable", func(c *gin.Context) {
		modelID := c.Param("model_id")
		if err := inferenceService.DisableModel(modelID); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"status": "disabled", "model_id": modelID})
	})
	router.POST("/inference/models/:model_id/healthcheck", func(c *gin.Context) {
		modelID := c.Param("model_id")
		inferenceService.ForceHealthCheck(modelID)
		c.JSON(200, gin.H{"status": "health check triggered", "model_id": modelID})
	})
	router.POST("/inference/models/reload", func(c *gin.Context) {
		if err := inferenceService.ReloadModels(); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"status": "models reloaded"})
	})

	// Request management endpoints
	router.GET("/inference/ratelimit", func(c *gin.Context) {
		metrics := inferenceService.GetRateLimiterMetrics()
		if metrics == nil {
			c.JSON(503, gin.H{"error": "Rate limiter not available"})
			return
		}
		c.JSON(200, metrics)
	})
	router.GET("/inference/concurrency", func(c *gin.Context) {
		metrics := inferenceService.GetConcurrencyMetrics()
		if metrics == nil {
			c.JSON(503, gin.H{"error": "Concurrency limiter not available"})
			return
		}
		c.JSON(200, metrics)
	})
	router.GET("/inference/retries", func(c *gin.Context) {
		metrics := inferenceService.GetRetryMetrics()
		if metrics == nil {
			c.JSON(503, gin.H{"error": "Retry policy not available"})
			return
		}
		c.JSON(200, metrics)
	})
	router.GET("/inference/request-management", func(c *gin.Context) {
		status := inferenceService.GetRequestManagementStatus()
		c.JSON(200, status)
	})
	router.POST("/inference/ratelimit/global", func(c *gin.Context) {
		var req struct {
			Rate float64 `json:"rate"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "invalid request body"})
			return
		}
		if req.Rate <= 0 {
			c.JSON(400, gin.H{"error": "rate must be positive"})
			return
		}
		inferenceService.SetGlobalRateLimit(req.Rate)
		c.JSON(200, gin.H{"status": "rate limit updated", "rate": req.Rate})
	})
	router.POST("/inference/ratelimit/model/:model_id", func(c *gin.Context) {
		modelID := c.Param("model_id")
		var req struct {
			Rate  float64 `json:"rate"`
			Burst int     `json:"burst"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "invalid request body"})
			return
		}
		if req.Rate <= 0 || req.Burst <= 0 {
			c.JSON(400, gin.H{"error": "rate and burst must be positive"})
			return
		}
		inferenceService.SetModelRateLimit(modelID, req.Rate, req.Burst)
		c.JSON(200, gin.H{"status": "model rate limit updated", "model_id": modelID, "rate": req.Rate, "burst": req.Burst})
	})
	router.POST("/inference/concurrency/global", func(c *gin.Context) {
		var req struct {
			Max int `json:"max"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "invalid request body"})
			return
		}
		if req.Max <= 0 {
			c.JSON(400, gin.H{"error": "max must be positive"})
			return
		}
		inferenceService.SetGlobalConcurrencyLimit(req.Max)
		c.JSON(200, gin.H{"status": "concurrency limit updated", "max": req.Max})
	})
	router.POST("/inference/concurrency/model/:model_id", func(c *gin.Context) {
		modelID := c.Param("model_id")
		var req struct {
			Max int `json:"max"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "invalid request body"})
			return
		}
		if req.Max <= 0 {
			c.JSON(400, gin.H{"error": "max must be positive"})
			return
		}
		inferenceService.SetModelConcurrencyLimit(modelID, req.Max)
		c.JSON(200, gin.H{"status": "model concurrency limit updated", "model_id": modelID, "max": req.Max})
	})

	// Request history endpoint
	router.GET("/inference/requests", func(c *gin.Context) {
		limitStr := c.DefaultQuery("limit", "100")
		modelFilter := c.Query("model")

		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit <= 0 {
			limit = 100
		}
		if limit > 1000 {
			limit = 1000
		}

		history := inferenceService.GetRequestHistory(limit, modelFilter)
		c.JSON(200, gin.H{"requests": history})
	})

	// Model detailed metrics endpoint
	router.GET("/inference/models/:model_id/metrics", func(c *gin.Context) {
		modelID := c.Param("model_id")
		metrics := inferenceService.GetModelDetailedMetrics(modelID)
		if metrics == nil || len(metrics) == 0 {
			c.JSON(404, gin.H{"error": "model not found"})
			return
		}
		c.JSON(200, metrics)
	})

	// Historical metrics endpoint
	router.GET("/inference/metrics/history", func(c *gin.Context) {
		durationStr := c.DefaultQuery("duration", "1h")
		resolutionStr := c.DefaultQuery("resolution", "1m")

		duration, err := time.ParseDuration(durationStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "invalid duration format"})
			return
		}

		resolution, err := time.ParseDuration(resolutionStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "invalid resolution format"})
			return
		}

		// Limit duration to 7 days max
		if duration > 7*24*time.Hour {
			duration = 7 * 24 * time.Hour
		}

		history, err := inferenceService.GetMetricsHistory(duration, resolution)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, gin.H{
			"data":       history,
			"duration":   durationStr,
			"resolution": resolutionStr,
		})
	})

	shutdownChan := make(chan struct{})
	httpStopper, err := util.ServeHttp(r, "cp-api", ":"+strconv.Itoa(conf.GetConfig().API.Port), false)
	if err != nil {
		logs.GetLogger().Fatalf("failed to start cp-api endpoint: %s", err)
	}
	logs.GetLogger().Infof("Computing provider started successfully, listening on port: %d", conf.GetConfig().API.Port)

	finishCh := util.MonitorShutdown(shutdownChan,
		util.ShutdownHandler{Component: "cp-api", StopFunc: httpStopper},
	)
	<-finishCh

	return nil
}
