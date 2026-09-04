package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	cors "github.com/itsjamie/gin-cors"
	"github.com/swanchain/computing-provider-v2/build"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/computing"
	"github.com/swanchain/computing-provider-v2/internal/dashboard"
	"github.com/swanchain/computing-provider-v2/internal/logging"
	"github.com/swanchain/computing-provider-v2/util"
	"github.com/urfave/cli/v2"
)

var runCmd = &cli.Command{
	Name:  "run",
	Usage: "Start the computing provider",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "host",
			Usage: "API listen address (use 0.0.0.0 only on a trusted network)",
			Value: "127.0.0.1",
		},
	},
	Action: func(cctx *cli.Context) error {
		return runDaemon(cctx)
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
func runDaemon(cctx *cli.Context) error {
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
	if err := logging.Setup(conf.GetConfig().Log); err != nil {
		logs.GetLogger().Warnf("Failed to configure logging, continuing with defaults: %v", err)
	}
	controlToken, tokenPath, err := dashboard.EnsureAccessToken(cpRepoPath)
	if err != nil {
		return fmt.Errorf("initialize dashboard access token: %w", err)
	}
	logs.GetLogger().Infof("Dashboard control token: %s", tokenPath)
	logs.GetLogger().Info("Your config file is:", filepath.Join(cpRepoPath, "config.toml"))
	logs.GetLogger().Infof("Logging to %s (rotate at %dMB, keep %d)",
		conf.GetConfig().Log.Dir, conf.GetConfig().Log.MaxSizeMB, conf.GetConfig().Log.MaxBackups)

	// Check if private_key was copied from another machine
	if err := computing.CheckMachineIdentity(cpRepoPath); err != nil {
		logs.GetLogger().Fatal(err)
	}

	// Start Inference mode (Swan Inference marketplace) if enabled
	nodeID := computing.GetNodeId(cpRepoPath)
	inferenceService := computing.NewInferenceService(nodeID, cpRepoPath)
	if err := inferenceService.Start(); err != nil {
		logs.GetLogger().Errorf("Failed to start Inference service: %v", err)
	}
	modelPrices := computing.NewModelPriceCatalog(conf.GetConfig().Inference.ServiceURL)
	// The platform's own account of what this provider served and earned. The
	// node's counters reset on restart, so they cannot answer "what have I
	// earned" — only "what has this process served".
	providerStats := computing.NewProviderStatsClient(
		conf.GetConfig().Inference.ServiceURL, conf.GetConfig().Inference.ApiKey)

	// Record the platform's lifetime earnings with every metrics snapshot, so
	// the earnings series can be differenced from the ledger that governs
	// rather than recomputed from token counts at published rates. The client
	// caches, so this costs one upstream call every couple of minutes at most.
	inferenceService.SetPlatformEarningsProvider(func() (float64, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		stats, err := providerStats.Stats(ctx)
		if err != nil || stats == nil {
			return 0, false
		}
		return stats.TotalEarningsUSDC, true
	})

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	configureEncodedPathParameters(r)
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
	router.Use(dashboard.ProtectWrites(controlToken))
	settingsRouter := router.Group("/inference/settings")
	settingsRouter.Use(dashboard.RequireAccess(controlToken))
	settingsRouter.GET("", func(c *gin.Context) {
		settings, err := conf.LoadDashboardSettings(cpRepoPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, settings)
	})
	settingsRouter.PUT("/alerts", func(c *gin.Context) {
		var settings conf.AlertSettings
		if err := c.ShouldBindJSON(&settings); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid alert settings"})
			return
		}
		if err := conf.UpdateAlertSettings(cpRepoPath, settings); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "saved", "restart_required": true})
	})
	settingsRouter.PUT("/self-check", func(c *gin.Context) {
		var settings conf.SelfCheckSettings
		if err := c.ShouldBindJSON(&settings); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid self-check settings"})
			return
		}
		if err := conf.UpdateSelfCheckSettings(cpRepoPath, settings); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "saved", "restart_required": true})
	})
	settingsRouter.PUT("/logging", func(c *gin.Context) {
		var settings conf.LogSettings
		if err := c.ShouldBindJSON(&settings); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid logging settings"})
			return
		}
		if err := conf.UpdateLogSettings(cpRepoPath, settings); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "saved", "restart_required": true})
	})
	settingsRouter.PUT("/limits", func(c *gin.Context) {
		var settings conf.RequestLimitSettings
		if err := c.ShouldBindJSON(&settings); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request limits"})
			return
		}
		if err := conf.UpdateRequestLimitSettings(cpRepoPath, settings); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		inferenceService.SetGlobalRateLimit(settings.RequestsPerSecond)
		inferenceService.SetGlobalConcurrencyLimit(settings.MaxConcurrent)
		c.JSON(http.StatusOK, gin.H{"status": "saved", "restart_required": false})
	})
	settingsRouter.PUT("/models", func(c *gin.Context) {
		var request struct {
			Models []conf.DashboardModel `json:"models"`
		}
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid model settings"})
			return
		}
		if err := conf.UpdateDashboardModels(cpRepoPath, request.Models); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := inferenceService.ReloadModels(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "settings saved but model reload failed: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "saved", "restart_required": false})
	})

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
			"connected":         inferenceService.IsConnected(),
			"active_models":     inferenceService.GetActiveModels(),
			"registered_models": inferenceService.GetRegisteredModels(),
			// The build actually serving requests, which is what the operator
			// needs to see. `update` replaces the binary without restarting, so
			// after an update this deliberately keeps reporting the old version
			// until the provider is restarted — the same version the platform
			// was told at registration.
			"version": build.BuildVersion,
			"build":   build.UserVersion(),
		})
	})

	// Model management endpoints
	router.GET("/inference/models", func(c *gin.Context) {
		models := inferenceService.GetAllModels()
		summary := inferenceService.GetModelsSummary()
		modelIDs := make([]string, 0, len(models))
		for _, model := range models {
			modelIDs = append(modelIDs, model.ID)
		}
		prices, err := modelPrices.Prices(c.Request.Context(), modelIDs)
		if err != nil {
			logs.GetLogger().Debugf("Model pricing is temporarily unavailable: %v", err)
		}
		// A short rolling health record per model, so each row can show whether
		// it has been steady or flapping rather than only its state right now.
		healthLog := make(map[string][]string, len(models))
		for _, model := range models {
			if log := inferenceService.ModelHealthLog(model.ID); len(log) > 0 {
				healthLog[model.ID] = log
			}
		}
		c.JSON(200, gin.H{
			"models":     models,
			"summary":    summary,
			"prices":     prices,
			"health_log": healthLog,
			// Which window is reported upstream for each model and where it came
			// from, so an operator can see an unreported window without reading
			// the log (#75).
			"contexts": inferenceService.ModelContexts(),
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
		limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
		if err != nil || limit <= 0 {
			limit = 100
		}
		if limit > 1000 {
			limit = 1000
		}

		offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
		if err != nil || offset < 0 {
			offset = 0
		}

		// An unrecognised source is rejected rather than quietly matching
		// nothing: an empty table is indistinguishable from "no such traffic",
		// and a typo would read as a working filter.
		source := c.Query("source")
		switch computing.RequestSource(source) {
		case "", computing.SourceHub, computing.SourceHealth, computing.SourceSelfCheck:
		default:
			c.JSON(400, gin.H{"error": "unknown source", "valid": []string{
				string(computing.SourceHub), string(computing.SourceHealth), string(computing.SourceSelfCheck),
			}})
			return
		}

		page := inferenceService.QueryRequestHistory(computing.RequestHistoryQuery{
			Limit:  limit,
			Offset: offset,
			Model:  c.Query("model"),
			Source: source,
		})
		c.JSON(200, page)
	})

	// Model detailed metrics endpoint
	router.GET("/inference/models/:model_id/metrics", func(c *gin.Context) {
		modelID := c.Param("model_id")
		metrics := inferenceService.GetModelDetailedMetrics(modelID)
		if metrics == nil || len(metrics) == 0 {
			c.JSON(404, gin.H{"error": "model not found"})
			return
		}
		if price, ok, err := modelPrices.Price(c.Request.Context(), modelID); err == nil && ok {
			metrics["price"] = price
		} else if err != nil {
			logs.GetLogger().Debugf("Model pricing is temporarily unavailable: %v", err)
		}
		c.JSON(200, metrics)
	})

	// Earnings, computed from this node's own served-token counters at the
	// provider payout rates. Probes never reach those counters, so the node
	// cannot bill itself for checking itself.
	router.GET("/inference/earnings", func(c *gin.Context) {
		earnings := computing.CalculateEarnings(c.Request.Context(), inferenceService.GetMetrics(), modelPrices, providerStats)
		c.JSON(200, earnings)
	})

	// Earnings over time, priced from the node's own stored history. Windows
	// longer than the retained history report what they actually cover.
	router.GET("/inference/earnings/history", func(c *gin.Context) {
		durationStr := c.DefaultQuery("duration", "24h")
		duration, err := time.ParseDuration(durationStr)
		if err != nil {
			// 30d is not a Go duration, but it is the window an operator asks
			// for, so it is accepted rather than rejected as malformed.
			switch durationStr {
			case "7d":
				duration = 7 * 24 * time.Hour
			case "30d":
				duration = 30 * 24 * time.Hour
			default:
				c.JSON(400, gin.H{"error": "invalid duration"})
				return
			}
		}
		// Display bucket. The samples themselves are always fetched at the
		// finest resolution: the deltas have to be computed before any
		// down-sampling, or a restart inside a bucket swallows the traffic that
		// preceded it.
		// Anything past a day is bucketed by day. An hourly bucket over a week
		// is 168 bars in the width of 24, too thin to hover and too fine to
		// read a trend from, and it labels a multi-day window with times.
		bucket := time.Hour
		if duration > 24*time.Hour {
			bucket = 24 * time.Hour
		}
		points, err := inferenceService.GetMetricsHistory(duration, time.Minute)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, computing.CalculateEarningsHistory(
			c.Request.Context(), points, inferenceService.GetMetrics(), modelPrices, durationStr, bucket))
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
	listenAddress := net.JoinHostPort(cctx.String("host"), strconv.Itoa(conf.GetConfig().API.Port))
	httpStopper, err := util.ServeHttp(r, "cp-api", listenAddress, false)
	if err != nil {
		logs.GetLogger().Fatalf("failed to start cp-api endpoint: %s", err)
	}
	logs.GetLogger().Infof("Computing provider started successfully, listening on %s", listenAddress)

	finishCh := util.MonitorShutdown(shutdownChan,
		util.ShutdownHandler{Component: "cp-api", StopFunc: httpStopper},
		util.ShutdownHandler{Component: "inference-service", StopFunc: func(ctx context.Context) error {
			inferenceService.Stop()
			return nil
		}},
	)
	<-finishCh

	return nil
}

// configureEncodedPathParameters lets model IDs containing slashes travel as a
// single route parameter (for example, openai%2Fgpt-5.4). Gin otherwise treats
// the decoded slash as a path separator and returns 404 before the handler runs.
func configureEncodedPathParameters(engine *gin.Engine) {
	engine.UseRawPath = true
	engine.UnescapePathValues = true
}
