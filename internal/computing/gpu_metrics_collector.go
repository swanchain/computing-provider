package computing

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/filswan/go-swan-lib/logs"
)

// GPUMetricsCollector collects real-time GPU metrics using nvidia-smi
type GPUMetricsCollector struct{}

// NewGPUMetricsCollector creates a new GPU metrics collector
func NewGPUMetricsCollector() *GPUMetricsCollector {
	return &GPUMetricsCollector{}
}

// CollectGPUMetrics collects real-time metrics from all available GPUs
func (c *GPUMetricsCollector) CollectGPUMetrics() []GPUMetrics {
	// Check if we're on macOS (Apple Silicon)
	if runtime.GOOS == "darwin" {
		return c.collectAppleSiliconMetrics()
	}

	// For Linux/Windows, use nvidia-smi
	return c.collectNvidiaMetrics()
}

// collectNvidiaMetrics collects metrics using nvidia-smi
func (c *GPUMetricsCollector) collectNvidiaMetrics() []GPUMetrics {
	// Query GPU metrics using nvidia-smi
	// Format: index, name, uuid, utilization.gpu, memory.used, memory.total, temperature.gpu, power.draw, power.limit, fan.speed, compute_processes
	cmd := exec.Command("nvidia-smi",
		"--query-gpu=index,name,uuid,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw,power.limit,fan.speed",
		"--format=csv,noheader,nounits")

	output, err := cmd.Output()
	if err != nil {
		logs.GetLogger().Debugf("Failed to run nvidia-smi for metrics: %v", err)
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var gpuMetrics []GPUMetrics

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		parts := strings.Split(line, ", ")
		if len(parts) < 10 {
			logs.GetLogger().Debugf("Unexpected nvidia-smi output format: %s", line)
			continue
		}

		index, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		name := strings.TrimSpace(parts[1])
		uuid := strings.TrimSpace(parts[2])
		utilization, _ := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
		memUsed, _ := strconv.ParseFloat(strings.TrimSpace(parts[4]), 64)
		memTotal, _ := strconv.ParseFloat(strings.TrimSpace(parts[5]), 64)
		temperature, _ := strconv.ParseFloat(strings.TrimSpace(parts[6]), 64)
		powerDraw, _ := strconv.ParseFloat(strings.TrimSpace(parts[7]), 64)
		powerLimit, _ := strconv.ParseFloat(strings.TrimSpace(parts[8]), 64)
		fanSpeed, _ := strconv.ParseFloat(strings.TrimSpace(parts[9]), 64)

		// Calculate memory usage percentage
		var memUsagePercent float64
		if memTotal > 0 {
			memUsagePercent = (memUsed / memTotal) * 100
		}

		// Get compute processes count
		processCount := c.getGPUProcessCount(index)

		gpuMetrics = append(gpuMetrics, GPUMetrics{
			Index:            index,
			Name:             name,
			UUID:             uuid,
			UtilizationPct:   utilization,
			MemoryUsedMB:     memUsed,
			MemoryTotalMB:    memTotal,
			MemoryUsagePct:   memUsagePercent,
			TemperatureC:     temperature,
			PowerDrawW:       powerDraw,
			PowerLimitW:      powerLimit,
			FanSpeedPct:      fanSpeed,
			ComputeProcesses: processCount,
		})
	}

	return gpuMetrics
}

// getGPUProcessCount gets the number of compute processes on a specific GPU
func (c *GPUMetricsCollector) getGPUProcessCount(gpuIndex int) int {
	cmd := exec.Command("nvidia-smi",
		"-i", strconv.Itoa(gpuIndex),
		"--query-compute-apps=pid",
		"--format=csv,noheader")

	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	count := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

// collectAppleSiliconMetrics collects metrics for Apple Silicon (M1/M2/M3/M4)
// Note: Apple Silicon doesn't have nvidia-smi, so we return basic info from system
func (c *GPUMetricsCollector) collectAppleSiliconMetrics() []GPUMetrics {
	// Try to get GPU info using system_profiler
	cmd := exec.Command("system_profiler", "SPDisplaysDataType", "-json")
	output, err := cmd.Output()
	if err != nil {
		logs.GetLogger().Debugf("Failed to get Apple Silicon GPU info: %v", err)
		return nil
	}

	// For Apple Silicon, we can't get real-time utilization metrics
	// Return a basic GPU entry with placeholder values
	gpuName := "Apple Silicon GPU"

	// Try to parse the output to get GPU name
	outputStr := string(output)
	if strings.Contains(outputStr, "Apple M1") {
		gpuName = "Apple M1 GPU"
	} else if strings.Contains(outputStr, "Apple M2") {
		gpuName = "Apple M2 GPU"
	} else if strings.Contains(outputStr, "Apple M3") {
		gpuName = "Apple M3 GPU"
	} else if strings.Contains(outputStr, "Apple M4") {
		gpuName = "Apple M4 GPU"
	}

	return []GPUMetrics{
		{
			Index:            0,
			Name:             gpuName,
			UUID:             "",
			UtilizationPct:   0, // Not available on Apple Silicon
			MemoryUsedMB:     0, // Shared memory, not directly measurable
			MemoryTotalMB:    0,
			MemoryUsagePct:   0,
			TemperatureC:     0, // Not directly accessible
			PowerDrawW:       0,
			PowerLimitW:      0,
			FanSpeedPct:      0,
			ComputeProcesses: 0,
		},
	}
}

// GetAggregatedGPUMetrics returns aggregated metrics across all GPUs
func (c *GPUMetricsCollector) GetAggregatedGPUMetrics() (avgUtilization, avgMemoryUsage float64) {
	metrics := c.CollectGPUMetrics()
	if len(metrics) == 0 {
		return 0, 0
	}

	var totalUtilization, totalMemoryUsage float64
	for _, gpu := range metrics {
		totalUtilization += gpu.UtilizationPct
		totalMemoryUsage += gpu.MemoryUsagePct
	}

	return totalUtilization / float64(len(metrics)), totalMemoryUsage / float64(len(metrics))
}

// IsGPUAvailable checks if any GPU is available
func (c *GPUMetricsCollector) IsGPUAvailable() bool {
	if runtime.GOOS == "darwin" {
		// Apple Silicon always has GPU
		return true
	}

	cmd := exec.Command("nvidia-smi", "--version")
	err := cmd.Run()
	return err == nil
}
