package build

var CurrentCommit string

var NetWorkTag string

// Inference URL defaults — overridable via ldflags at build time.
// Production defaults; dev builds override these in the Makefile.
var DefaultInferenceURL = "https://api.swanchain.io"
var DefaultInferenceWSURL = "wss://inference-ws.swanchain.io"
var DefaultInferenceAPIURL = "https://api.swanchain.io/api/v1"
var DefaultInferenceDashboardURL = "https://inference.swanchain.io"

const BuildVersion = "0.2.0"

const UBITaskImageIntelCpu = "swanhub/ubi-worker-cpu-intel:latest"
const UBITaskImageIntelGpu = "swanhub/ubi-worker-gpu-intel:latest"
const UBITaskImageAmdCpu = "swanhub/ubi-worker-cpu-amd:latest"
const UBITaskImageAmdGpu = "swanhub/ubi-worker-gpu-amd:latest"
const UBIResourceExporterDockerImage = "swanhub/resource-exporter:v13.0.0"
const TraefikServerDockerImage = "traefik:v2.10"

const ResourceExporterVersion = "v13.0.0"

func UserVersion() string {
	return BuildVersion + "+" + NetWorkTag + CurrentCommit
}

