package setup

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// PrerequisiteResult represents the result of a prerequisite check
type PrerequisiteResult struct {
	Name    string
	Status  bool
	Version string
	Message string
}

// PrerequisiteChecker handles system prerequisite checks
type PrerequisiteChecker struct {
	results []PrerequisiteResult
}

// NewPrerequisiteChecker creates a new prerequisite checker
func NewPrerequisiteChecker() *PrerequisiteChecker {
	return &PrerequisiteChecker{
		results: make([]PrerequisiteResult, 0),
	}
}

// CheckAll runs all prerequisite checks
func (pc *PrerequisiteChecker) CheckAll() []PrerequisiteResult {
	pc.results = []PrerequisiteResult{}

	// Platform-specific checks
	if runtime.GOOS == "darwin" {
		// macOS: Check both Ollama and Docker
		pc.checkOllama()
		pc.checkDocker()
	} else {
		// Linux: Docker + NVIDIA Container Toolkit required
		pc.checkDocker()
		pc.checkNvidiaContainerToolkit()
	}

	// GPU detection
	pc.checkGPU()

	return pc.results
}

// HasCriticalFailures returns true if any critical prerequisite failed
// For Inference mode: need Docker OR Ollama (not both)
func (pc *PrerequisiteChecker) HasCriticalFailures() bool {
	hasDocker := false
	hasOllama := false

	for _, r := range pc.results {
		if r.Name == "Docker" && r.Status {
			hasDocker = true
		}
		if r.Name == "Ollama" && r.Status {
			hasOllama = true
		}
	}

	// Need at least one inference backend
	if !hasDocker && !hasOllama {
		return true
	}

	return false
}

// commandTimeout is the timeout for running commands
const commandTimeout = 10 * time.Second

// checkDocker checks if Docker is installed and running
func (pc *PrerequisiteChecker) checkDocker() {
	result := PrerequisiteResult{
		Name: "Docker",
	}

	// Check if docker command exists
	_, err := exec.LookPath("docker")
	if err != nil {
		result.Status = false
		result.Message = "Docker not found. Please install Docker: https://docs.docker.com/get-docker/"
		pc.results = append(pc.results, result)
		return
	}

	// Get Docker version with timeout
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	versionOut, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.Status = false
			result.Message = "Docker not responding (timeout). Please check Docker daemon."
		} else {
			result.Status = false
			result.Message = "Docker not running. Please start Docker daemon."
		}
		pc.results = append(pc.results, result)
		return
	}

	result.Version = strings.TrimSpace(string(versionOut))

	// Check if Docker daemon is responsive with timeout
	ctx2, cancel2 := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel2()

	_, err = exec.CommandContext(ctx2, "docker", "info").Output()
	if err != nil {
		if ctx2.Err() == context.DeadlineExceeded {
			result.Status = false
			result.Message = "Docker daemon not responding (timeout)."
		} else {
			result.Status = false
			result.Message = "Docker daemon not accessible. Please check Docker is running."
		}
		pc.results = append(pc.results, result)
		return
	}

	result.Status = true
	result.Message = fmt.Sprintf("v%s (running)", result.Version)
	pc.results = append(pc.results, result)
}

// checkNvidiaContainerToolkit checks if NVIDIA Container Toolkit is installed
func (pc *PrerequisiteChecker) checkNvidiaContainerToolkit() {
	result := PrerequisiteResult{
		Name: "NVIDIA Container Toolkit",
	}

	// Check if nvidia-container-cli exists
	_, err := exec.LookPath("nvidia-container-cli")
	if err != nil {
		// Try alternative check via docker info
		out, dockerErr := exec.Command("docker", "info", "--format", "{{.Runtimes}}").Output()
		if dockerErr == nil && strings.Contains(string(out), "nvidia") {
			result.Status = true
			result.Message = "nvidia runtime available"
			pc.results = append(pc.results, result)
			return
		}

		result.Status = false
		result.Message = "Not found. Install: https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html"
		pc.results = append(pc.results, result)
		return
	}

	// Get version
	versionOut, err := exec.Command("nvidia-container-cli", "--version").Output()
	if err == nil {
		lines := strings.Split(string(versionOut), "\n")
		if len(lines) > 0 {
			result.Version = strings.TrimSpace(lines[0])
		}
	}

	result.Status = true
	result.Message = result.Version
	pc.results = append(pc.results, result)
}

// checkOllama checks if Ollama is installed (macOS)
func (pc *PrerequisiteChecker) checkOllama() {
	result := PrerequisiteResult{
		Name: "Ollama",
	}

	// Check if ollama command exists
	_, err := exec.LookPath("ollama")
	if err != nil {
		result.Status = false
		result.Message = "Not found. Install: https://ollama.ai/download"
		pc.results = append(pc.results, result)
		return
	}

	// Check Ollama version
	versionOut, err := exec.Command("ollama", "--version").Output()
	if err == nil {
		result.Version = strings.TrimSpace(string(versionOut))
	}

	// Check if Ollama is running by trying to list models
	_, err = exec.Command("ollama", "list").Output()
	if err != nil {
		result.Status = false
		result.Message = fmt.Sprintf("%s (not running - start with 'ollama serve')", result.Version)
		pc.results = append(pc.results, result)
		return
	}

	result.Status = true
	result.Message = fmt.Sprintf("%s (running)", result.Version)
	pc.results = append(pc.results, result)
}

// checkGPU checks for available GPUs
func (pc *PrerequisiteChecker) checkGPU() {
	result := PrerequisiteResult{
		Name: "GPU",
	}

	if runtime.GOOS == "darwin" {
		// Check for Apple Silicon
		out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
		if err == nil {
			cpuBrand := strings.TrimSpace(string(out))
			if strings.Contains(cpuBrand, "Apple") {
				result.Status = true
				result.Message = fmt.Sprintf("Apple Silicon (%s)", cpuBrand)
				pc.results = append(pc.results, result)
				return
			}
		}
		result.Status = false
		result.Message = "No Apple Silicon detected"
		pc.results = append(pc.results, result)
		return
	}

	// Linux: Check for NVIDIA GPU
	out, err := exec.Command("nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits").Output()
	if err != nil {
		result.Status = false
		result.Message = "No NVIDIA GPU detected or nvidia-smi not available"
		pc.results = append(pc.results, result)
		return
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 {
		result.Status = false
		result.Message = "No GPU detected"
		pc.results = append(pc.results, result)
		return
	}

	// Parse GPU info
	var gpus []string
	for _, line := range lines {
		parts := strings.Split(line, ",")
		if len(parts) >= 2 {
			name := strings.TrimSpace(parts[0])
			memMB := strings.TrimSpace(parts[1])
			memGB := 0
			if mb, err := fmt.Sscanf(memMB, "%d", &memGB); err == nil && mb > 0 {
				memGB = memGB / 1024
			}
			gpus = append(gpus, fmt.Sprintf("%s (%dGB)", name, memGB))
		}
	}

	if len(gpus) == 1 {
		result.Status = true
		result.Message = gpus[0]
	} else {
		result.Status = true
		result.Message = fmt.Sprintf("%d GPUs: %s", len(gpus), strings.Join(gpus, ", "))
	}
	pc.results = append(pc.results, result)
}
