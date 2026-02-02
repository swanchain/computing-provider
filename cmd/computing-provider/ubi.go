package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	cors "github.com/itsjamie/gin-cors"
	"github.com/olekukonko/tablewriter"
	"github.com/swanchain/computing-provider-v2/build"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/computing"
	"github.com/swanchain/computing-provider-v2/internal/models"
	"github.com/swanchain/computing-provider-v2/util"
	"github.com/urfave/cli/v2"
)

var ubiTaskCmd = &cli.Command{
	Name:  "ubi",
	Usage: "Manage ZK proof tasks",
	Subcommands: []*cli.Command{
		listCmd,
	},
}

var runCmd = &cli.Command{
	Name:  "run",
	Usage: "Start the computing provider",
	Action: func(cctx *cli.Context) error {
		return runDaemon()
	},
}

var listCmd = &cli.Command{
	Name:  "list",
	Usage: "List ubi task",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "show-failed",
			Usage: "show failed/failing ubi tasks",
		},
		&cli.BoolFlag{
			Name:    "verbose",
			Usage:   "--verbose",
			Aliases: []string{"v"},
		},
		&cli.IntFlag{
			Name:  "tail",
			Usage: "Show the last number of lines. If not specified, all are displayed by default",
		},
	},
	Action: func(cctx *cli.Context) error {
		fullFlag := cctx.Bool("verbose")
		cpRepoPath, _ := os.LookupEnv("CP_PATH")
		if err := conf.InitConfig(cpRepoPath, true); err != nil {
			return fmt.Errorf("load config file failed, error: %+v", err)
		}

		showFailed := cctx.Bool("show-failed")
		tailNum := cctx.Int("tail")
		var taskData [][]string
		var rowColorList []RowColor
		var taskList []*models.TaskEntity
		var err error
		if showFailed {
			taskList, err = computing.NewTaskService().GetTaskList(tailNum)
			if err != nil {
				return fmt.Errorf("failed get ubi task, error: %+v", err)
			}
		} else {
			taskList, err = computing.NewTaskService().GetTaskList(tailNum, []int{
				models.TASK_RECEIVED_STATUS,
				models.TASK_RUNNING_STATUS,
				models.TASK_SUBMITTED_STATUS,
				models.TASK_VERIFIED_STATUS,
				models.TASK_REWARDED_STATUS,
			}...)
			if err != nil {
				return fmt.Errorf("failed get ubi task, error: %+v", err)
			}
		}

		if fullFlag {
			for i, task := range taskList {
				createTime := time.Unix(task.CreateTime, 0).Format("2006-01-02 15:04:05")
				var sequencerStr string
				var contract string
				if task.Sequencer == 1 {
					sequencerStr = "YES"
					if task.SequenceTaskAddr != "" {
						contract = task.SequenceTaskAddr
					}
				} else if task.Sequencer == 0 {
					sequencerStr = "NO"
					contract = task.Contract
				} else {
					sequencerStr = ""
				}

				var taskId string
				if task.Type == models.Mining {
					taskId = task.Uuid
				} else {
					taskId = strconv.Itoa(int(task.Id))
				}

				taskData = append(taskData,
					[]string{taskId, contract, models.GetResourceTypeStr(task.ResourceType), models.UbiTaskTypeStr(task.Type),
						task.CheckCode, task.Sign, models.TaskStatusStr(task.Status), sequencerStr, createTime})

				rowColorList = append(rowColorList, RowColor{
					row:    i,
					column: []int{6},
					color:  getStatusColor(task.Status),
				})
			}
			header := []string{"TASK ID", "TASK CONTRACT", "TASK TYPE", "ZK TYPE", "CHECK CODE", "SIGNATURE", "STATUS", "SEQUENCER", "CREATE TIME"}
			NewVisualTable(header, taskData, rowColorList).Generate(false)

		} else {
			for i, task := range taskList {
				createTime := time.Unix(task.CreateTime, 0).Format("2006-01-02 15:04:05")
				var sequencerStr string
				var contract string
				if task.Sequencer == 1 {
					sequencerStr = "YES"
					if task.SequenceTaskAddr != "" {
						contract = task.SequenceTaskAddr
					}
				} else if task.Sequencer == 0 {
					sequencerStr = "NO"
					contract = task.Contract
				} else {
					sequencerStr = ""
				}

				var taskId string
				if task.Type == models.Mining {
					taskId = task.Uuid
				} else {
					taskId = strconv.Itoa(int(task.Id))
				}

				taskData = append(taskData,
					[]string{taskId, contract, models.GetResourceTypeStr(task.ResourceType), models.UbiTaskTypeStr(task.Type),
						models.TaskStatusStr(task.Status), sequencerStr, createTime})

				rowColorList = append(rowColorList, RowColor{
					row:    i,
					column: []int{4},
					color:  getStatusColor(task.Status),
				})
			}

			header := []string{"TASK ID", "TASK CONTRACT", "TASK TYPE", "ZK TYPE", "STATUS", "SEQUENCER", "CREATE TIME"}
			NewVisualTable(header, taskData, rowColorList).Generate(false)
		}
		return nil

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

	if dockerAvailable {
		computing.NewDockerService().CleanResourceForDocker(true)

		resourceExporterContainerName := "resource-exporter"
		rsExist, version, err := computing.NewDockerService().CheckRunningContainer(resourceExporterContainerName)
		if err != nil {
			logs.GetLogger().Warnf("check %s container failed: %v (continuing without it)", resourceExporterContainerName, err)
		} else {
			if version != "" {
				if errMsg := util.CheckVersion(build.ResourceExporterVersion, version); errMsg != nil {
					logs.GetLogger().Warnf("resource-exporter version mismatch: %s", errMsg)
				}
			}

			if !rsExist {
				if err = computing.RestartResourceExporter(); err != nil {
					logs.GetLogger().Errorf("restartResourceExporter failed, error: %v", err)
				}
			}
		}

		traefikServiceContainerName := "traefik-service"
		tsExist, _, err := computing.NewDockerService().CheckRunningContainer(traefikServiceContainerName)
		if err != nil {
			logs.GetLogger().Warnf("check %s container failed: %v (continuing without it)", traefikServiceContainerName, err)
		} else if !tsExist {
			if err = computing.RestartTraefikService(); err != nil {
				logs.GetLogger().Errorf("restartTraefikService failed, error: %v", err)
			}
		}
	} else {
		logs.GetLogger().Info("Docker not available - running in Inference-only mode (Ollama)")
		logs.GetLogger().Info("Some features (resource-exporter, traefik) will be disabled")
	}

	if err := conf.InitConfig(cpRepoPath, true); err != nil {
		logs.GetLogger().Fatal(err)
	}
	logs.GetLogger().Info("Your config file is:", filepath.Join(cpRepoPath, "config.toml"))

	computing.SyncCpAccountInfo()
	computing.CronTaskForEcp()

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
	router.GET("/cp", computing.GetCpResource)
	router.GET("/cp/metrics", computing.GetUbiResourceExporterMetrics)
	router.POST("/cp/ubi", computing.DoUbiTaskForDocker)
	router.POST("/cp/docker/receive/ubi", computing.ReceiveUbiProof)

	ecpImageService := computing.NewImageJobService()
	router.POST("/cp/deploy/check", ecpImageService.CheckJobCondition)
	router.GET("/cp/price", computing.GetPrice)
	router.POST("/cp/deploy", ecpImageService.DeployJob)
	router.GET("/cp/job/status", ecpImageService.GetJobStatus)
	router.GET("/cp/job/log", ecpImageService.DockerLogsHandler)
	router.DELETE("/cp/job/:job_uuid", ecpImageService.DeleteJob)
	router.POST("/cp/zk_task", computing.DoZkTask)

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

func getStatusColor(taskStatus int) []tablewriter.Colors {
	var rowColor []tablewriter.Colors
	switch taskStatus {
	case models.TASK_REJECTED_STATUS:
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgRedColor}}
	case models.TASK_RECEIVED_STATUS:
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgYellowColor}}
	case models.TASK_RUNNING_STATUS:
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgCyanColor}}
	case models.TASK_SUBMITTED_STATUS:
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgBlueColor}}
	case models.TASK_FAILED_STATUS:
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgRedColor}}
	case models.TASK_VERIFIED_STATUS:
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgBlueColor}}
	case models.TASK_REWARDED_STATUS:
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgGreenColor}}
	case models.TASK_INVALID_STATUS:
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgRedColor}}
	case models.TASK_TIMEOUT_STATUS:
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgRedColor}}
	case models.TASK_VERIFYFAILED_STATUS:
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgRedColor}}
	case models.TASK_REPEATED_STATUS:
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgGreenColor}}
	case models.TASK_NSC_STATUS:
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgRedColor}}
	case models.TASK_UNKNOWN_STATUS:
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgBlackColor}}
	}
	return rowColor
}
