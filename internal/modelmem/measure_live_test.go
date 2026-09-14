package modelmem

import (
	"context"
	"os"
	"testing"
)

// Measured against containers already running on the host, so the figures can
// be checked against nvidia-smi by hand.
//
//	MODELMEM_LIVE=llama-qwen38,vllm-cydonia go test ./internal/modelmem/ -run TestLiveMeasure -v
func TestLiveMeasure(t *testing.T) {
	names := os.Getenv("MODELMEM_LIVE")
	if names == "" {
		t.Skip("set MODELMEM_LIVE=<container>[,<container>] to measure running containers")
	}
	for _, name := range splitComma(names) {
		m, err := Measure(context.Background(), name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		t.Logf("%-16s %.2f GiB across %d GPU(s) %v", name, m.VRAMGiB, len(m.PerGPUGiB), m.PerGPUGiB)
		if m.VRAMGiB <= 0 {
			t.Errorf("%s measured at zero", name)
		}
	}
}

func splitComma(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
