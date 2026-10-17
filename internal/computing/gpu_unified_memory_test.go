package computing

import "testing"

const gb10Row = "0, NVIDIA GB10, GPU-449b2602-e2aa-32a0-192c-a564cd35965d, 0, [N/A], [N/A], 41, 7.40, [N/A], [N/A]"
const discreteRow = "2, NVIDIA GeForce RTX 3080, GPU-aaaa, 37, 9512, 10240, 37, 120.5, 320.0, 30"

func fakeSystemMemory(total, available float64) func() (float64, float64, bool) {
	return func() (float64, float64, bool) { return total, available, true }
}

// A unified-memory GPU reports [N/A] for memory; the metrics then carry
// system memory and say so, instead of 0 of 0.
func TestUnifiedMemoryGPUReportsSystemMemory(t *testing.T) {
	gpu, ok := parseNvidiaMetricsLine(gb10Row, fakeSystemMemory(124544, 59704))
	if !ok {
		t.Fatal("row not parsed")
	}
	if !gpu.UnifiedMemory {
		t.Fatal("GB10 row not marked unified")
	}
	if gpu.MemoryTotalMB != 124544 || gpu.MemoryUsedMB != 124544-59704 {
		t.Fatalf("memory = %.0f of %.0f MB, want system memory in use over total", gpu.MemoryUsedMB, gpu.MemoryTotalMB)
	}
	if gpu.MemoryUsagePct < 52 || gpu.MemoryUsagePct > 53 {
		t.Fatalf("usage = %.1f%%, want ~52%%", gpu.MemoryUsagePct)
	}
	if gpu.TemperatureC != 41 || gpu.Name != "NVIDIA GB10" {
		t.Fatalf("other fields lost: %+v", gpu)
	}
}

// A discrete card's own figures are never replaced by system memory.
func TestDiscreteGPUKeepsItsOwnMemory(t *testing.T) {
	gpu, ok := parseNvidiaMetricsLine(discreteRow, fakeSystemMemory(124544, 59704))
	if !ok {
		t.Fatal("row not parsed")
	}
	if gpu.UnifiedMemory || gpu.MemoryTotalMB != 10240 || gpu.MemoryUsedMB != 9512 {
		t.Fatalf("discrete card rewritten: %+v", gpu)
	}
}

// Without a readable /proc/meminfo the row still parses, with memory unknown.
func TestUnifiedMemoryWithoutMeminfoStaysUnknown(t *testing.T) {
	gpu, ok := parseNvidiaMetricsLine(gb10Row, func() (float64, float64, bool) { return 0, 0, false })
	if !ok || gpu.UnifiedMemory || gpu.MemoryTotalMB != 0 {
		t.Fatalf("got ok=%v %+v", ok, gpu)
	}
}

func TestParseMeminfo(t *testing.T) {
	total, avail, ok := parseMeminfo("MemTotal:       127533336 kB\nMemFree:         2775820 kB\nMemAvailable:   61136992 kB\n")
	if !ok || int(total) != 124544 || int(avail) != 59704 {
		t.Fatalf("got ok=%v total=%.0f avail=%.0f", ok, total, avail)
	}
	if _, _, ok := parseMeminfo("MemTotal:       127533336 kB\n"); ok {
		t.Fatal("meminfo without MemAvailable read as complete")
	}
}
