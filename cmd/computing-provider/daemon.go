package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	cors "github.com/itsjamie/gin-cors"
	"github.com/swanchain/computing-provider-v2/build"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/alerts"
	"github.com/swanchain/computing-provider-v2/internal/computing"
	"github.com/swanchain/computing-provider-v2/internal/dashboard"
	"github.com/swanchain/computing-provider-v2/internal/logging"
	"github.com/swanchain/computing-provider-v2/internal/logview"
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
		&cli.BoolFlag{
			Name:  "no-auth",
			Usage: "skip the dashboard control token; loopback --host only, and every local user can then control the node",
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
	if cctx.Bool("no-auth") {
		if !isLoopbackHost(cctx.String("host")) {
			return fmt.Errorf("--no-auth is only allowed with a loopback --host, not %q: without the token anyone who can reach the port can take models out of service", cctx.String("host"))
		}
		dashboard.SetAuthDisabled(true)
		logs.GetLogger().Warn("Dashboard control token disabled (--no-auth): any process on this machine can change the node")
	} else {
		logs.GetLogger().Infof("Dashboard control token: %s", tokenPath)
	}
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

	// The context window the hub assumes for each model, shown locally where
	// the backend reports none. Refreshed in the background so a status call
	// or a registration never waits on the platform.
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			if models, err := providerStats.Models(ctx); err == nil {
				inferenceService.SetHubContexts(models)
			} else {
				logs.GetLogger().Debugf("hub context windows unavailable: %v", err)
			}
			cancel()
			time.Sleep(10 * time.Minute)
		}
	}()

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
	// The node's own log, for the dashboard's Logs tab. Behind the control
	// token even though it only reads: log lines carry request IDs, backend
	// endpoints and upstream error bodies, and the API may listen on more
	// than loopback.
	// Read-only like the other monitoring routes: an operator looking at why
	// a model failed should not need the control token first.
	router.GET("/inference/logs", func(c *gin.Context) {
		q, err := logQuery(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		res, err := logview.Read(conf.GetConfig().Log.Dir, q)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, res)
	})
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
			// The hub's own list, from the register ack. It can differ from
			// registered_models above, which is what this node sent; a model
			// in one and not the other receives no traffic. Null until a hub
			// new enough to report it has acked a registration.
			"hub_registered_models": inferenceService.GetHubRegisteredModels(),
			"auto_disabled_models":  inferenceService.AutoDisabledModels(),
			"upgrade_available":     inferenceService.GetUpgradeAvailable(),
			// How often each model has left the registered set since the
			// daemon started, and why it last did; a model that flaps is
			// unavailable to consumers for those minutes.
			"registration_drops": inferenceService.GetRegistrationDrops(),
			// The build actually serving requests, which is what the operator
			// needs to see. `update` replaces the binary without restarting, so
			// after an update this deliberately keeps reporting the old version
			// until the provider is restarted — the same version the platform
			// was told at registration.
			"version": build.BuildVersion,
			// The node's own name, so the dashboard can say which machine its
			// usage figures describe: earnings are provider-wide, usage is not.
			"node_name": conf.GetConfig().API.NodeName,
			"peers":     peerNames(conf.GetConfig().Peers),
			"build":     build.UserVersion(),
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
			// The concurrency each model is declared to the hub with: its own
			// max_concurrent, else the default, capped by the global limit.
			"capacity": inferenceService.ModelCapacities(),
		})
	})
	// A model's own limits, kept in its models.json entry. Zero returns a
	// field to the global default. Hub requests over either answer 429 at once,
	// never queue: the hub reassigns a refused request, while a queued one runs
	// into its first-chunk timeout and counts as a failure.
	router.POST("/inference/models/:model_id/limits", func(c *gin.Context) {
		modelID := c.Param("model_id")
		var req struct {
			MaxConcurrent *int     `json:"max_concurrent"`
			RPM           *float64 `json:"rpm"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "invalid request body"})
			return
		}
		limits := inferenceService.ModelLimitsFor(modelID)
		if req.MaxConcurrent != nil {
			limits.MaxConcurrent = *req.MaxConcurrent
		}
		if req.RPM != nil {
			limits.RPM = *req.RPM
		}
		if err := inferenceService.SetModelLimits(modelID, limits); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"status": "model limits updated", "model_id": modelID, "limits": limits})
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
			Rate  float64  `json:"rate"`
			Burst int      `json:"burst"`
			RPM   *float64 `json:"rpm"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "invalid request body"})
			return
		}
		if req.RPM != nil {
			// Per-minute form: saved to models.json and kept across restarts.
			limits := inferenceService.ModelLimitsFor(modelID)
			limits.RPM = *req.RPM
			if err := inferenceService.SetModelLimits(modelID, limits); err != nil {
				c.JSON(400, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{"status": "model rate limit updated", "model_id": modelID, "rpm": *req.RPM})
			return
		}
		// Per-second form with an explicit burst: this process only.
		if req.Rate <= 0 || req.Burst <= 0 {
			c.JSON(400, gin.H{"error": "rate and burst must be positive, or send rpm"})
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
		// Saved to models.json and declared to the hub; 0 restores the default.
		limits := inferenceService.ModelLimitsFor(modelID)
		limits.MaxConcurrent = req.Max
		if err := inferenceService.SetModelLimits(modelID, limits); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"status": "model concurrency limit updated", "model_id": modelID, "max": req.Max})
	})

	// Traffic over a recent window as source -> model -> backend links, with
	// every configured backend's state, for the dashboard's Routing view.
	router.GET("/inference/routes", func(c *gin.Context) {
		c.JSON(200, inferenceService.Routes(c.DefaultQuery("window", "15m")))
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
		case "", computing.SourceHub, computing.SourceHealth, computing.SourceSelfCheck, computing.SourceLocal:
		default:
			c.JSON(400, gin.H{"error": "unknown source", "valid": []string{
				string(computing.SourceHub), string(computing.SourceHealth), string(computing.SourceSelfCheck), string(computing.SourceLocal),
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

	// Earnings over time. The platform's own earnings history is the figure
	// that is paid, split by model from its settlement records, so it is used
	// whenever it can be read. The node's estimate, priced from its own stored
	// history, is the fallback, and the response says which one it is and why.
	// Windows longer than the retained local history report what they cover.
	router.GET("/inference/earnings/history", func(c *gin.Context) {
		durationStr := c.DefaultQuery("duration", "24h")
		nodeOnly := c.Query("scope") == "node"
		var platformErr string
		if span, bucketSpan, ok := computing.PlatformSpans(durationStr); ok && !nodeOnly && c.Query("source") != computing.EarningsSourceLocal {
			h, err := providerStats.EarningsHistory(c.Request.Context(), span, bucketSpan)
			if err == nil {
				c.JSON(200, computing.PlatformSeries(h, durationStr))
				return
			}
			platformErr = err.Error()
		}
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
		calculate := computing.CalculateEarningsHistory
		if nodeOnly {
			calculate = computing.CalculateNodeEarningsHistory
		}
		series := calculate(c.Request.Context(), points, inferenceService.GetMetrics(), modelPrices, durationStr, bucket)
		series.PlatformError = platformErr
		c.JSON(200, series)
	})

	// Usage over time by source: routed work, probes, local clients, and work
	// the model servers did that never passed through the node. Bucketed the
	// same way as earnings so the two charts line up.
	router.GET("/inference/usage/history", func(c *gin.Context) {
		durationStr := c.DefaultQuery("duration", "24h")
		var window, bucket time.Duration
		switch durationStr {
		case "24h":
			window, bucket = 24*time.Hour, time.Hour
		case "7d":
			window, bucket = 7*24*time.Hour, 24*time.Hour
		case "30d":
			window, bucket = 30*24*time.Hour, 24*time.Hour
		default:
			c.JSON(400, gin.H{"error": "duration must be 24h, 7d or 30d"})
			return
		}
		series, err := inferenceService.UsageHistory(durationStr, window, bucket)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		if port := conf.GetConfig().Inference.GatewayPort(); port > 0 {
			series.LocalGateway = fmt.Sprintf("http://127.0.0.1:%d/v1", port)
		}
		// scope=all adds the provider's other nodes, so usage covers the same
		// machines the account-wide earnings do. Peers are only read.
		if peers := conf.GetConfig().Peers; c.Query("scope") == "all" && len(peers) > 0 {
			got, errs := fetchPeerUsage(c.Request.Context(), peers, durationStr)
			series = computing.CombineUsage(conf.GetConfig().API.NodeName, series, got, errs)
		}
		c.JSON(200, series)
	})

	// The platform's per-model view of this provider beside the node's own
	// registered list. A model this node registers that the platform does not
	// hold as offered can still be sent requests, but is missing from the
	// platform's offerings and may not be credited; nothing else shows it.
	// The accounts behind each CLIProxyAPI instance: plan, usage windows,
	// cooldowns and credential expiry, read from the proxies themselves.
	router.GET("/inference/proxy-accounts", func(c *gin.Context) {
		c.JSON(200, inferenceService.ProxyAccounts(c.Request.Context()))
	})

	router.GET("/inference/hub/models", func(c *gin.Context) {
		platform, err := providerStats.Models(c.Request.Context())
		if err != nil {
			c.JSON(200, computing.HubModelView{Models: []computing.HubModelRow{}, NotListed: []string{}, Error: err.Error()})
			return
		}
		c.JSON(200, computing.BuildHubModelView(platform, inferenceService.GetRegisteredModels()))
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

	// Started last, so that a cycle firing immediately finds a node that is
	// already serving and connected rather than one still coming up.
	autoSwitch := startAutoSwitch(context.Background(), cpRepoPath,
		conf.GetConfig(), alerts.New(conf.GetConfig().Alerts, nodeID, conf.GetConfig().API.NodeName))

	handlers := []util.ShutdownHandler{
		{Component: "cp-api", StopFunc: httpStopper},
	}

	// The local gateway records the operator's own clients, which otherwise
	// call the model servers directly and never appear anywhere. A failure to
	// bind is logged, not fatal: the node serves the hub either way.
	if port := conf.GetConfig().Inference.GatewayPort(); port > 0 {
		if gw, err := inferenceService.StartLocalGateway(port); err != nil {
			logs.GetLogger().Warnf("Local gateway not started: %v", err)
		} else {
			handlers = append(handlers, util.ShutdownHandler{Component: "local-gateway", StopFunc: gw.Shutdown})
		}
	}
	if autoSwitch != nil {
		// Stopped before the inference service: a cycle in flight is still
		// starting and stopping containers, and it needs the node intact to
		// finish and record what it did.
		handlers = append(handlers, util.ShutdownHandler{
			Component: "auto-switch",
			StopFunc: func(ctx context.Context) error {
				autoSwitch.Stop()
				return nil
			},
		})
	}
	handlers = append(handlers, util.ShutdownHandler{
		Component: "inference-service",
		StopFunc: func(ctx context.Context) error {
			inferenceService.Stop()
			return nil
		},
	})

	finishCh := util.MonitorShutdown(shutdownChan, handlers...)
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

// fetchPeerUsage reads each peer node's usage series in parallel. A peer that
// cannot be read is reported with its error, never dropped.
func fetchPeerUsage(ctx context.Context, peers []conf.Peer, duration string) (map[string]computing.UsageSeries, map[string]string) {
	type result struct {
		name   string
		series computing.UsageSeries
		err    error
	}
	ch := make(chan result, len(peers))
	client := &http.Client{Timeout: 10 * time.Second}
	for _, p := range peers {
		go func(p conf.Peer) {
			r := result{name: p.Name}
			endpoint := strings.TrimRight(p.URL, "/") + "/api/v1/computing/inference/usage/history?duration=" + url.QueryEscape(duration)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			if err == nil {
				var resp *http.Response
				if resp, err = client.Do(req); err == nil {
					if resp.StatusCode != http.StatusOK {
						err = fmt.Errorf("HTTP %d", resp.StatusCode)
					} else {
						err = json.NewDecoder(resp.Body).Decode(&r.series)
					}
					resp.Body.Close()
				}
			}
			r.err = err
			ch <- r
		}(p)
	}
	got, errs := map[string]computing.UsageSeries{}, map[string]string{}
	for range peers {
		r := <-ch
		if r.err != nil {
			errs[r.name] = r.err.Error()
		} else {
			got[r.name] = r.series
		}
	}
	return got, errs
}

func peerNames(peers []conf.Peer) []string {
	out := make([]string, 0, len(peers))
	for _, p := range peers {
		out = append(out, p.Name)
	}
	return out
}

// logQuery reads the /inference/logs parameters: levels (comma-separated),
// since and until (RFC 3339), q (text) and limit.
func logQuery(c *gin.Context) (logview.Query, error) {
	var q logview.Query
	levels, err := logview.ParseLevels(c.Query("levels"))
	if err != nil {
		return q, err
	}
	q.Levels = levels
	for name, dst := range map[string]*time.Time{"since": &q.Since, "until": &q.Until} {
		if v := c.Query(name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return q, fmt.Errorf("%s must be an RFC 3339 time: %w", name, err)
			}
			*dst = t
		}
	}
	q.Contains = c.Query("q")
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return q, fmt.Errorf("limit must be a positive number")
		}
		q.Limit = n
	}
	return q, nil
}

// isLoopbackHost reports whether a --host value keeps the API on this machine.
// --no-auth is refused on anything else: a bare "" or "0.0.0.0" binds every
// interface, which is exactly the exposure the token was added for.
func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
