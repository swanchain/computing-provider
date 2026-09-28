package computing

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/swanchain/computing-provider-v2/internal/db"
)

// Usage the node did not see.
//
// Every request that passes through the node is recorded with its source. A
// client that calls a model server directly passes through nothing, so the
// only record of that work is the server's own token counters. Sampling them
// and subtracting what the node recorded for the same models gives "direct"
// usage: GPU time spent on something the node never handled and nobody paid
// for.
//
// Only generated tokens are compared. llama.cpp's prompt counter excludes
// tokens served from its prompt cache while a request's prompt_tokens does
// not, so subtracting the two would report cache hits as missing work.

// backendCounterNames maps each engine's cumulative counters to prompt and
// generated tokens. vLLM and SGLang label theirs per model; the lines are
// summed, since an endpoint's samples are compared against every model on it.
var backendCounterNames = map[string]string{
	"llamacpp:prompt_tokens_total":    "prompt",
	"llamacpp:tokens_predicted_total": "generated",
	"vllm:prompt_tokens_total":        "prompt",
	"vllm:generation_tokens_total":    "generated",
	"sglang:prompt_tokens_total":      "prompt",
	"sglang:generation_tokens_total":  "generated",
}

// parseBackendCounters reads cumulative prompt and generated token counts from
// a Prometheus text exposition. ok is false when no generated-token counter is
// present — an engine or build this does not know how to read.
func parseBackendCounters(r io.Reader) (prompt, generated float64, ok bool) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		name, rest, found := strings.Cut(line, " ")
		if i := strings.IndexByte(name, '{'); i >= 0 {
			// Labels may contain spaces, so re-split after the closing brace.
			end := strings.LastIndexByte(line, '}')
			if end < 0 {
				continue
			}
			name, rest = line[:i], strings.TrimSpace(line[end+1:])
		} else if !found {
			continue
		}
		kind, known := backendCounterNames[name]
		if !known {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		if kind == "prompt" {
			prompt += v
		} else {
			generated += v
			ok = true
		}
	}
	return prompt, generated, ok
}

// BackendUsageSample is one reading of an endpoint's cumulative counters.
type BackendUsageSample struct {
	ID        uint      `gorm:"primaryKey;autoIncrement"`
	Endpoint  string    `gorm:"index;not null"`
	Time      time.Time `gorm:"index;not null"`
	Prompt    float64
	Generated float64
}

func (BackendUsageSample) TableName() string { return "backend_usage_samples" }

// BackendEndpointStatus says whether an endpoint's counters can be read.
type BackendEndpointStatus struct {
	Endpoint string   `json:"endpoint"`
	Models   []string `json:"models"`
	Measured bool     `json:"measured"`
	// Reason explains an endpoint that cannot be measured, in terms of what to
	// change: the fix for llama.cpp is a flag, not a mystery.
	Reason   string    `json:"reason,omitempty"`
	LastRead time.Time `json:"last_read,omitempty"`
}

const (
	backendSampleInterval  = time.Minute
	backendSampleRetention = 35 * 24 * time.Hour
)

// BackendUsageSampler polls each model endpoint's counters and stores them.
type BackendUsageSampler struct {
	endpoints func() map[string]endpointInfo
	client    *http.Client

	mu     sync.Mutex
	status map[string]BackendEndpointStatus
	stop   chan struct{}
	done   chan struct{}
}

// endpointInfo is what the sampler needs to know about one endpoint.
type endpointInfo struct {
	Models []string
	APIKey string
}

func NewBackendUsageSampler(endpoints func() map[string]endpointInfo) *BackendUsageSampler {
	return &BackendUsageSampler{
		endpoints: endpoints,
		client:    &http.Client{Timeout: 10 * time.Second},
		status:    make(map[string]BackendEndpointStatus),
	}
}

// Start migrates the table and begins sampling. Without a database there is
// nowhere to keep samples, so the sampler does not run.
func (b *BackendUsageSampler) Start() error {
	database := db.NewDbService()
	if database == nil {
		return nil
	}
	if err := database.AutoMigrate(&BackendUsageSample{}); err != nil {
		return err
	}
	b.stop = make(chan struct{})
	b.done = make(chan struct{})
	go b.loop()
	return nil
}

func (b *BackendUsageSampler) Stop() {
	if b.stop == nil {
		return
	}
	close(b.stop)
	<-b.done
}

func (b *BackendUsageSampler) loop() {
	defer close(b.done)
	defer func() {
		if err := recover(); err != nil {
			logs.GetLogger().Errorf("[backend_usage] panic recovered: %v\n%s", err, debug.Stack())
		}
	}()
	b.sampleAll()
	ticker := time.NewTicker(backendSampleInterval)
	defer ticker.Stop()
	prune := time.NewTicker(6 * time.Hour)
	defer prune.Stop()
	for {
		select {
		case <-b.stop:
			return
		case <-ticker.C:
			b.sampleAll()
		case <-prune.C:
			if database := db.NewDbService(); database != nil {
				database.Where("time < ?", time.Now().Add(-backendSampleRetention)).Delete(&BackendUsageSample{})
			}
		}
	}
}

func (b *BackendUsageSampler) sampleAll() {
	eps := b.endpoints()
	now := time.Now()
	var rows []BackendUsageSample
	for ep, info := range eps {
		st := BackendEndpointStatus{Endpoint: ep, Models: info.Models}
		prompt, gen, err := b.read(ep, info.APIKey)
		if err != nil {
			st.Reason = err.Error()
		} else {
			st.Measured, st.LastRead = true, now
			rows = append(rows, BackendUsageSample{Endpoint: ep, Time: now, Prompt: prompt, Generated: gen})
		}
		b.mu.Lock()
		b.status[ep] = st
		b.mu.Unlock()
	}
	b.mu.Lock()
	for ep := range b.status {
		if _, ok := eps[ep]; !ok {
			delete(b.status, ep)
		}
	}
	b.mu.Unlock()
	if len(rows) > 0 {
		if database := db.NewDbService(); database != nil {
			if err := database.Create(&rows).Error; err != nil {
				logs.GetLogger().Warnf("Failed to store backend usage samples: %v", err)
			}
		}
	}
}

func (b *BackendUsageSampler) read(endpoint, apiKey string) (prompt, generated float64, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/metrics", nil)
	if err != nil {
		return 0, 0, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("unreachable: %v", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotImplemented:
		return 0, 0, fmt.Errorf("metrics disabled (HTTP 501) — llama.cpp needs --metrics")
	case resp.StatusCode != http.StatusOK:
		return 0, 0, fmt.Errorf("no metrics endpoint (HTTP %d)", resp.StatusCode)
	}
	prompt, generated, ok := parseBackendCounters(resp.Body)
	if !ok {
		return 0, 0, fmt.Errorf("metrics carry no token counters this node can read")
	}
	return prompt, generated, nil
}

// Status reports every endpoint and whether it is measured.
func (b *BackendUsageSampler) Status() []BackendEndpointStatus {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]BackendEndpointStatus, 0, len(b.status))
	for _, st := range b.status {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Endpoint < out[j].Endpoint })
	return out
}

// generatedDeltas turns an endpoint's cumulative samples, oldest first, into
// tokens generated per bucket. A counter that goes down means the server
// restarted; the new reading is then the work done since.
func generatedDeltas(samples []BackendUsageSample, bucketOf func(time.Time) time.Time) map[time.Time]float64 {
	out := make(map[time.Time]float64)
	for i := 1; i < len(samples); i++ {
		d := samples[i].Generated - samples[i-1].Generated
		if d < 0 {
			d = samples[i].Generated
		}
		if d > 0 {
			out[bucketOf(samples[i].Time)] += d
		}
	}
	return out
}
