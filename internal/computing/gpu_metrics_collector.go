package computing

import (
	"fmt"
	"os"
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
		gpu, ok := parseNvidiaMetricsLine(line, systemMemoryMB)
		if !ok {
			logs.GetLogger().Debugf("Unexpected nvidia-smi output format: %s", line)
			continue
		}
		gpu.ComputeProcesses = c.getGPUProcessCount(gpu.Index)
		gpuMetrics = append(gpuMetrics, gpu)
	}

	return gpuMetrics
}

// parseNvidiaMetricsLine reads one row of the --query-gpu output above.
//
// A unified-memory GPU (GB10, Jetson, Thor) answers "[N/A]" for both memory
// fields, which used to parse as 0/0 and left the dashboard with no memory
// figure for the one machine where memory is the constraint. The GPU
// allocates from system RAM, so system RAM in use over system RAM is the
// honest figure; systemMemory supplies it and the row is marked unified.
func parseNvidiaMetricsLine(line string, systemMemory func() (totalMB, availableMB float64, ok bool)) (GPUMetrics, bool) {
	parts := strings.Split(line, ", ")
	if len(parts) < 10 {
		return GPUMetrics{}, false
	}

	index, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
	name := strings.TrimSpace(parts[1])
	uuid := strings.TrimSpace(parts[2])
	utilization, _ := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
	memUsed, usedErr := strconv.ParseFloat(strings.TrimSpace(parts[4]), 64)
	memTotal, totalErr := strconv.ParseFloat(strings.TrimSpace(parts[5]), 64)
	temperature, _ := strconv.ParseFloat(strings.TrimSpace(parts[6]), 64)
	powerDraw, _ := strconv.ParseFloat(strings.TrimSpace(parts[7]), 64)
	powerLimit, _ := strconv.ParseFloat(strings.TrimSpace(parts[8]), 64)
	fanSpeed, _ := strconv.ParseFloat(strings.TrimSpace(parts[9]), 64)

	unified := false
	if (usedErr != nil || totalErr != nil || memTotal <= 0) && systemMemory != nil {
		if total, available, ok := systemMemory(); ok && total > 0 {
			memTotal, memUsed, unified = total, total-available, true
		}
	}

	// Calculate memory usage percentage
	var memUsagePercent float64
	if memTotal > 0 {
		memUsagePercent = (memUsed / memTotal) * 100
	}

	return GPUMetrics{
		Index:          index,
		Name:           name,
		UUID:           uuid,
		UtilizationPct: utilization,
		MemoryUsedMB:   memUsed,
		MemoryTotalMB:  memTotal,
		MemoryUsagePct: memUsagePercent,
		TemperatureC:   temperature,
		PowerDrawW:     powerDraw,
		PowerLimitW:    powerLimit,
		FanSpeedPct:    fanSpeed,
		UnifiedMemory:  unified,
	}, true
}

// systemMemoryMB reads MemTotal and MemAvailable from /proc/meminfo.
// MemAvailable, not MemFree: the kernel's own estimate of what can be
// claimed without swapping, which counts reclaimable cache as free the way
// a model load will find it.
func systemMemoryMB() (totalMB, availableMB float64, ok bool) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	return parseMeminfo(string(data))
}

func parseMeminfo(meminfo string) (totalMB, availableMB float64, ok bool) {
	var haveTotal, haveAvail bool
	for _, line := range strings.Split(meminfo, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			totalMB, haveTotal = kb/1024, true
		case "MemAvailable:":
			availableMB, haveAvail = kb/1024, true
		}
	}
	return totalMB, availableMB, haveTotal && haveAvail
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
	gpuName := "Apple Silicon GPU"

	// Detect chip model via sysctl
	chipCmd := exec.Command("sysctl", "-n", "machdep.cpu.brand_string")
	if chipOutput, err := chipCmd.Output(); err == nil {
		chip := strings.TrimSpace(string(chipOutput))
		if chip != "" {
			gpuName = fmt.Sprintf("%s GPU", chip)
		}
	}

	// Get total unified memory
	var memTotalMB float64
	memCmd := exec.Command("sysctl", "-n", "hw.memsize")
	if memOutput, err := memCmd.Output(); err == nil {
		memBytes, _ := strconv.ParseInt(strings.TrimSpace(string(memOutput)), 10, 64)
		if memBytes > 0 {
			memTotalMB = float64(memBytes) / (1024 * 1024)
		}
	}

	return []GPUMetrics{
		{
			Index:         0,
			Name:          gpuName,
			MemoryTotalMB: memTotalMB,
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
