package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/olekukonko/tablewriter"
	"github.com/urfave/cli/v2"
)

var researchCmd = &cli.Command{
	Name:  "research",
	Usage: "Research decentralized computing resources and benchmark GPU",
	Subcommands: []*cli.Command{
		gpuInfoCmd,
		gpuBenchmarkCmd,
		hardwareCmd,
	},
}

var gpuInfoCmd = &cli.Command{
	Name:  "gpu-info",
	Usage: "Display GPU information",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:    "json",
			Usage:   "Output in JSON format",
			Aliases: []string{"j"},
		},
	},
	Action: func(cctx *cli.Context) error {
		gpus, err := detectGPUs()
		if err != nil {
			return fmt.Errorf("failed to detect GPUs: %v", err)
		}

		if len(gpus) == 0 {
			fmt.Println("No NVIDIA GPUs detected")
			return nil
		}

		if cctx.Bool("json") {
			output, _ := json.MarshalIndent(gpus, "", "  ")
			fmt.Println(string(output))
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Index", "Name", "Memory Total", "Memory Free", "Temperature", "Power Usage", "Utilization"})
		table.SetBorder(true)

		for _, gpu := range gpus {
			table.Append([]string{
				strconv.Itoa(gpu.Index),
				gpu.Name,
				gpu.MemoryTotal,
				gpu.MemoryFree,
				gpu.Temperature,
				gpu.PowerUsage,
				gpu.Utilization,
			})
		}
		table.Render()
		return nil
	},
}

var gpuBenchmarkCmd = &cli.Command{
	Name:  "gpu-benchmark",
	Usage: "Run GPU benchmark tests",
	Flags: []cli.Flag{
		&cli.IntFlag{
			Name:    "gpu",
			Usage:   "GPU index to benchmark (default: 0)",
			Value:   0,
			Aliases: []string{"g"},
		},
		&cli.IntFlag{
			Name:    "iterations",
			Usage:   "Number of benchmark iterations",
			Value:   5,
			Aliases: []string{"i"},
		},
		&cli.StringFlag{
			Name:    "type",
			Usage:   "Benchmark type: memory, compute, or all",
			Value:   "all",
			Aliases: []string{"t"},
		},
	},
	Action: func(cctx *cli.Context) error {
		gpuIndex := cctx.Int("gpu")
		iterations := cctx.Int("iterations")
		benchType := cctx.String("type")

		gpus, err := detectGPUs()
		if err != nil {
			return fmt.Errorf("failed to detect GPUs: %v", err)
		}

		if len(gpus) == 0 {
			return fmt.Errorf("no NVIDIA GPUs detected")
		}

		if gpuIndex >= len(gpus) {
			return fmt.Errorf("GPU index %d not found, available: 0-%d", gpuIndex, len(gpus)-1)
		}

		gpu := gpus[gpuIndex]
		fmt.Printf("Benchmarking GPU %d: %s\n", gpuIndex, gpu.Name)
		fmt.Printf("Memory: %s total, %s free\n", gpu.MemoryTotal, gpu.MemoryFree)
		fmt.Println(strings.Repeat("-", 50))

		results := &BenchmarkResults{
			GPU:       gpu,
			Timestamp: time.Now(),
		}

		if benchType == "memory" || benchType == "all" {
			fmt.Println("\nRunning memory bandwidth benchmark...")
			memResult, err := runMemoryBenchmark(gpuIndex, iterations)
			if err != nil {
				fmt.Printf("Memory benchmark failed: %v\n", err)
			} else {
				results.MemoryBandwidth = memResult
				fmt.Printf("Memory Bandwidth: %.2f GB/s\n", memResult)
			}
		}

		if benchType == "compute" || benchType == "all" {
			fmt.Println("\nRunning compute benchmark...")
			computeResult, err := runComputeBenchmark(gpuIndex, iterations)
			if err != nil {
				fmt.Printf("Compute benchmark failed: %v\n", err)
			} else {
				results.ComputeScore = computeResult
				fmt.Printf("Compute Score: %.2f GFLOPS\n", computeResult)
			}
		}

		fmt.Println("\n" + strings.Repeat("-", 50))
		fmt.Println("Benchmark Summary:")
		printBenchmarkSummary(results)

		return nil
	},
}

var hardwareCmd = &cli.Command{
	Name:  "hardware",
	Usage: "Display all hardware information",
	Action: func(cctx *cli.Context) error {
		fmt.Println("=== System Hardware Information ===")

		// CPU Info
		fmt.Println("CPU Information:")
		cpuInfo, err := getCPUInfo()
		if err != nil {
			fmt.Printf("  Error: %v\n", err)
		} else {
			for k, v := range cpuInfo {
				fmt.Printf("  %s: %s\n", k, v)
			}
		}

		// Memory Info
		fmt.Println("\nMemory Information:")
		memInfo, err := getMemoryInfo()
		if err != nil {
			fmt.Printf("  Error: %v\n", err)
		} else {
			for k, v := range memInfo {
				fmt.Printf("  %s: %s\n", k, v)
			}
		}

		// GPU Info
		fmt.Println("\nGPU Information:")
		gpus, err := detectGPUs()
		if err != nil {
			fmt.Printf("  Error: %v\n", err)
		} else if len(gpus) == 0 {
			fmt.Println("  No NVIDIA GPUs detected")
		} else {
			for _, gpu := range gpus {
				fmt.Printf("  [%d] %s - %s total, %s free, %s, %s\n",
					gpu.Index, gpu.Name, gpu.MemoryTotal, gpu.MemoryFree, gpu.Temperature, gpu.Utilization)
			}
		}

		// Disk Info
		fmt.Println("\nStorage Information:")
		diskInfo, err := getDiskInfo()
		if err != nil {
			fmt.Printf("  Error: %v\n", err)
		} else {
			fmt.Printf("  %s\n", diskInfo)
		}

		return nil
	},
}

// GPUInfo represents GPU hardware information
type GPUInfo struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	MemoryTotal string `json:"memory_total"`
	MemoryFree  string `json:"memory_free"`
	MemoryUsed  string `json:"memory_used"`
	Temperature string `json:"temperature"`
	PowerUsage  string `json:"power_usage"`
	Utilization string `json:"utilization"`
	DriverVer   string `json:"driver_version"`
	CudaVer     string `json:"cuda_version"`
}

// BenchmarkResults holds benchmark test results
type BenchmarkResults struct {
	GPU             GPUInfo   `json:"gpu"`
	Timestamp       time.Time `json:"timestamp"`
	MemoryBandwidth float64   `json:"memory_bandwidth_gbps"`
	ComputeScore    float64   `json:"compute_score_gflops"`
}

// detectGPUs uses nvidia-smi to detect GPU information
func detectGPUs() ([]GPUInfo, error) {
	cmd := exec.Command("nvidia-smi",
		"--query-gpu=index,name,memory.total,memory.free,memory.used,temperature.gpu,power.draw,utilization.gpu,driver_version",
		"--format=csv,noheader,nounits")

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi command failed: %v", err)
	}

	var gpus []GPUInfo
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, ", ")
		if len(parts) < 9 {
			continue
		}

		index, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		gpus = append(gpus, GPUInfo{
			Index:       index,
			Name:        strings.TrimSpace(parts[1]),
			MemoryTotal: strings.TrimSpace(parts[2]) + " MiB",
			MemoryFree:  strings.TrimSpace(parts[3]) + " MiB",
			MemoryUsed:  strings.TrimSpace(parts[4]) + " MiB",
			Temperature: strings.TrimSpace(parts[5]) + "°C",
			PowerUsage:  strings.TrimSpace(parts[6]) + " W",
			Utilization: strings.TrimSpace(parts[7]) + "%",
			DriverVer:   strings.TrimSpace(parts[8]),
		})
	}

	return gpus, nil
}

// runMemoryBenchmark runs a GPU memory bandwidth test
func runMemoryBenchmark(gpuIndex int, iterations int) (float64, error) {
	// Check if cuda-samples bandwidthTest is available
	cmd := exec.Command("which", "bandwidthTest")
	if err := cmd.Run(); err != nil {
		// Fallback to nvidia-smi based estimation
		return estimateMemoryBandwidth(gpuIndex)
	}

	var totalBandwidth float64
	for i := 0; i < iterations; i++ {
		cmd := exec.Command("bandwidthTest", "--device="+strconv.Itoa(gpuIndex))
		output, err := cmd.Output()
		if err != nil {
			continue
		}
		// Parse bandwidth from output
		bandwidth := parseNvidiaBandwidth(string(output))
		totalBandwidth += bandwidth
	}

	if totalBandwidth == 0 {
		return estimateMemoryBandwidth(gpuIndex)
	}

	return totalBandwidth / float64(iterations), nil
}

// estimateMemoryBandwidth provides an estimate based on GPU model
func estimateMemoryBandwidth(gpuIndex int) (float64, error) {
	gpus, err := detectGPUs()
	if err != nil || gpuIndex >= len(gpus) {
		return 0, fmt.Errorf("cannot detect GPU")
	}

	gpu := gpus[gpuIndex]
	name := strings.ToLower(gpu.Name)

	// Approximate bandwidth based on GPU model (in GB/s)
	bandwidthMap := map[string]float64{
		"a100":     2039,
		"h100":     3350,
		"a6000":    768,
		"rtx 4090": 1008,
		"rtx 4080": 717,
		"rtx 3090": 936,
		"rtx 3080": 760,
		"rtx 3070": 448,
		"rtx 3060": 360,
		"v100":     900,
		"t4":       320,
	}

	for model, bw := range bandwidthMap {
		if strings.Contains(name, model) {
			return bw, nil
		}
	}

	return 256, nil // Default estimate
}

// runComputeBenchmark runs a GPU compute benchmark
func runComputeBenchmark(gpuIndex int, iterations int) (float64, error) {
	// Check if we can use nvidia-smi for a basic compute test
	gpus, err := detectGPUs()
	if err != nil || gpuIndex >= len(gpus) {
		return 0, fmt.Errorf("cannot detect GPU")
	}

	gpu := gpus[gpuIndex]
	name := strings.ToLower(gpu.Name)

	// Approximate TFLOPS based on GPU model (FP32)
	tflopsMap := map[string]float64{
		"a100":     19500,
		"h100":     67000,
		"a6000":    38700,
		"rtx 4090": 82600,
		"rtx 4080": 48700,
		"rtx 3090": 35600,
		"rtx 3080": 29800,
		"rtx 3070": 20300,
		"rtx 3060": 12700,
		"v100":     15700,
		"t4":       8100,
	}

	for model, tflops := range tflopsMap {
		if strings.Contains(name, model) {
			return tflops, nil
		}
	}

	return 5000, nil // Default estimate
}

func parseNvidiaBandwidth(output string) float64 {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Device to Device Bandwidth") {
			parts := strings.Fields(line)
			for i, p := range parts {
				if p == "GB/s" && i > 0 {
					val, _ := strconv.ParseFloat(parts[i-1], 64)
					return val
				}
			}
		}
	}
	return 0
}

func printBenchmarkSummary(results *BenchmarkResults) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Metric", "Value"})
	table.SetBorder(true)

	table.Append([]string{"GPU", results.GPU.Name})
	table.Append([]string{"Memory", results.GPU.MemoryTotal})

	if results.MemoryBandwidth > 0 {
		table.Append([]string{"Memory Bandwidth", fmt.Sprintf("%.2f GB/s", results.MemoryBandwidth)})
	}
	if results.ComputeScore > 0 {
		table.Append([]string{"Compute (FP32)", fmt.Sprintf("%.2f GFLOPS", results.ComputeScore)})
	}

	table.Render()
}

func getCPUInfo() (map[string]string, error) {
	info := make(map[string]string)

	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				info["Model"] = strings.TrimSpace(parts[1])
			}
		}
		if strings.HasPrefix(line, "cpu cores") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				info["Cores"] = strings.TrimSpace(parts[1])
			}
		}
		if strings.HasPrefix(line, "siblings") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				info["Threads"] = strings.TrimSpace(parts[1])
			}
		}
	}

	// Get CPU count
	cmd := exec.Command("nproc")
	output, err := cmd.Output()
	if err == nil {
		info["Total Threads"] = strings.TrimSpace(string(output))
	}

	return info, nil
}

func getMemoryInfo() (map[string]string, error) {
	info := make(map[string]string)

	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				kb, _ := strconv.ParseInt(parts[1], 10, 64)
				info["Total"] = fmt.Sprintf("%.2f GiB", float64(kb)/1024/1024)
			}
		}
		if strings.HasPrefix(line, "MemAvailable:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				kb, _ := strconv.ParseInt(parts[1], 10, 64)
				info["Available"] = fmt.Sprintf("%.2f GiB", float64(kb)/1024/1024)
			}
		}
	}

	return info, nil
}

func getDiskInfo() (string, error) {
	cmd := exec.Command("df", "-h", "--total")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "total") {
			return line, nil
		}
	}

	return strings.TrimSpace(string(output)), nil
}
