package computing

import (
	"sort"
	"strings"
	"time"

	"github.com/swanchain/computing-provider-v2/internal/db"
)

// Usage over time, by where it came from.
//
// The earnings chart answers "what was I paid". This answers "what did the
// GPUs do": routed work, the node's own probes, the operator's local clients,
// and — from the model servers' counters — work that reached a backend without
// passing through the node at all. Set side by side, the gap between the two
// is GPU time that earned nothing.

// SourceDirect labels work a model server did that the node never handled,
// derived from the server's own counters.
//
// Its output tokens are the server's generated count minus what the node
// recorded. Its input tokens are a lower bound, never an estimate: the
// server's prompt counter excludes prompt-cache hits while recorded requests
// include them, so the true figure is at least the difference and can only be
// higher. Where the node's own traffic hits the cache heavily the difference
// is negative and the bound says nothing, which is reported as zero.
const SourceDirect = "direct"

// UsageTotals is traffic from one source.
type UsageTotals struct {
	Requests  int64 `json:"requests"`
	TokensIn  int64 `json:"tokens_in"`
	TokensOut int64 `json:"tokens_out"`
}

func (u *UsageTotals) add(o UsageTotals) {
	u.Requests += o.Requests
	u.TokensIn += o.TokensIn
	u.TokensOut += o.TokensOut
}

// UsagePoint is one bucket.
type UsagePoint struct {
	Timestamp time.Time              `json:"timestamp"`
	Sources   map[string]UsageTotals `json:"sources"`
	// Models splits the bucket by model, then by source. Direct usage is
	// attributed to the models on the endpoint it was measured at.
	Models map[string]map[string]UsageTotals `json:"models"`
}

// DirectCoverage says which endpoints direct usage could be measured on, and
// from when. Buckets before Since, and endpoints absent here, are not "no
// direct usage" — they were not measured.
type DirectCoverage struct {
	Endpoint string    `json:"endpoint"`
	Models   []string  `json:"models"`
	Since    time.Time `json:"since"`
}

// UsageSeries is the response of GET /inference/usage/history.
type UsageSeries struct {
	Points        []UsagePoint            `json:"points"`
	Duration      string                  `json:"duration"`
	BucketSeconds int                     `json:"bucket_seconds"`
	Totals        map[string]UsageTotals  `json:"totals"`
	Direct        []DirectCoverage        `json:"direct_coverage"`
	Endpoints     []BackendEndpointStatus `json:"endpoints"`
	// LocalGateway is where local clients should be pointed, or empty when
	// the gateway is off.
	LocalGateway string `json:"local_gateway"`
}

// usageRecord is the part of a recorded request the series needs.
type usageRecord struct {
	Model     string
	Source    string
	StartTime time.Time
	TokensIn  int64
	TokensOut int64
}

// BuildUsageSeries buckets recorded requests by source and adds direct usage
// for every endpoint with counter samples. samples are per endpoint, oldest
// first, and should include one reading before `from` as a baseline.
func BuildUsageSeries(records []usageRecord, samples map[string][]BackendUsageSample, endpointModels map[string][]string, from, to time.Time, bucket time.Duration, duration string) UsageSeries {
	bucketOf := func(t time.Time) time.Time { return t.UTC().Truncate(bucket) }
	s := UsageSeries{
		Duration:      duration,
		BucketSeconds: int(bucket / time.Second),
		Totals:        map[string]UsageTotals{},
		Direct:        []DirectCoverage{},
	}

	index := map[time.Time]int{}
	for t := bucketOf(from); !t.After(to); t = t.Add(bucket) {
		index[t] = len(s.Points)
		s.Points = append(s.Points, UsagePoint{Timestamp: t, Sources: map[string]UsageTotals{}, Models: map[string]map[string]UsageTotals{}})
	}
	add := func(p *UsagePoint, model, source string, u UsageTotals) {
		t := p.Sources[source]
		t.add(u)
		p.Sources[source] = t
		if p.Models[model] == nil {
			p.Models[model] = map[string]UsageTotals{}
		}
		m := p.Models[model][source]
		m.add(u)
		p.Models[model][source] = m
		tot := s.Totals[source]
		tot.add(u)
		s.Totals[source] = tot
	}

	// Tokens the node recorded, per model per bucket, from every source: the
	// node's own work on an endpoint is not direct usage.
	recordedOut := map[string]map[time.Time]int64{}
	recordedIn := map[string]map[time.Time]int64{}
	for _, r := range records {
		b := bucketOf(r.StartTime)
		i, ok := index[b]
		if !ok {
			continue
		}
		source := r.Source
		if source == "" {
			source = string(SourceHub) // written before sources were recorded
		}
		add(&s.Points[i], r.Model, source, UsageTotals{Requests: 1, TokensIn: r.TokensIn, TokensOut: r.TokensOut})
		if recordedOut[r.Model] == nil {
			recordedOut[r.Model] = map[time.Time]int64{}
			recordedIn[r.Model] = map[time.Time]int64{}
		}
		recordedOut[r.Model][b] += r.TokensOut
		recordedIn[r.Model][b] += r.TokensIn
	}

	endpoints := make([]string, 0, len(samples))
	for ep := range samples {
		endpoints = append(endpoints, ep)
	}
	sort.Strings(endpoints)
	for _, ep := range endpoints {
		ss := samples[ep]
		models := endpointModels[ep]
		if len(ss) < 2 || len(models) == 0 {
			continue
		}
		s.Direct = append(s.Direct, DirectCoverage{Endpoint: ep, Models: models, Since: ss[0].Time})
		label := strings.Join(models, ", ")
		generated := generatedDeltas(ss, bucketOf)
		prompted := promptDeltas(ss, bucketOf)

		// A request is recorded in the bucket it started in, but the server
		// counts its tokens as it produces them, so one straddling an edge
		// makes one bucket's residual negative and the next one's positive by
		// the same amount. A shortfall is therefore carried into the next
		// bucket only: it cancels that edge effect, and cannot swallow real
		// direct usage hours later.
		// The bucket the first sample falls in is only partly measured: the
		// counters start mid-bucket while the node's records cover all of it.
		// Its shortfall is not a timing edge, so it neither counts nor carries.
		firstMeasured := bucketOf(ss[0].Time)
		var carry int64
		for i := range s.Points {
			b := s.Points[i].Timestamp
			if !b.After(firstMeasured) {
				continue
			}
			var recOut, recIn int64
			for _, m := range models {
				recOut += recordedOut[m][b]
				recIn += recordedIn[m][b]
			}
			var u UsageTotals
			own := int64(generated[b]) - recOut
			if r := own + carry; r > 0 {
				u.TokensOut = r
			}
			// Only this bucket's own shortfall moves on, so none outlives the
			// bucket after it.
			carry = min(own, 0)
			if r := int64(prompted[b]) - recIn; r > 0 {
				u.TokensIn = r
			}
			if u.TokensOut > 0 || u.TokensIn > 0 {
				add(&s.Points[i], label, SourceDirect, u)
			}
		}
	}
	return s
}

// UsageHistory builds the usage series for a window from the stored request
// history and backend counter samples.
func (s *InferenceService) UsageHistory(duration string, window, bucket time.Duration) (UsageSeries, error) {
	to := time.Now()
	from := to.Add(-window)

	var records []usageRecord
	database := db.NewDbService()
	if database != nil && s.requestStore != nil {
		if err := database.Model(&RequestHistoryEntity{}).
			Select("model, source, start_time, tokens_in, tokens_out").
			Where("start_time >= ?", from).
			Scan(&records).Error; err != nil {
			return UsageSeries{}, err
		}
	} else if s.client != nil {
		for _, r := range s.client.metrics.QueryRequestHistory(RequestHistoryQuery{Limit: 1 << 30}).Requests {
			if r.StartTime.Before(from) {
				continue
			}
			records = append(records, usageRecord{Model: r.Model, Source: string(r.Source), StartTime: r.StartTime,
				TokensIn: int64(r.TokensIn), TokensOut: int64(r.TokensOut)})
		}
	}

	samples := map[string][]BackendUsageSample{}
	endpointModels := map[string][]string{}
	for ep, info := range s.modelEndpoints() {
		endpointModels[ep] = info.Models
	}
	if database != nil && s.usageSampler != nil {
		// One bucket earlier than the window, so the first bucket has a
		// baseline to difference against.
		var rows []BackendUsageSample
		if err := database.Where("time >= ?", from.Add(-bucket)).Order("endpoint, time").Find(&rows).Error; err != nil {
			return UsageSeries{}, err
		}
		for _, r := range rows {
			samples[r.Endpoint] = append(samples[r.Endpoint], r)
		}
	}

	series := BuildUsageSeries(records, samples, endpointModels, from, to, bucket, duration)
	series.Endpoints = s.usageSampler.Status()
	if series.Endpoints == nil {
		series.Endpoints = []BackendEndpointStatus{}
	}
	return series, nil
}

// modelEndpoints groups the configured models by the endpoint serving them.
func (s *InferenceService) modelEndpoints() map[string]endpointInfo {
	out := map[string]endpointInfo{}
	for id, m := range s.modelMappings {
		ep := strings.TrimRight(m.Endpoint, "/")
		if ep == "" {
			continue
		}
		info := out[ep]
		info.Models = append(info.Models, id)
		if info.APIKey == "" {
			info.APIKey = m.APIKey
		}
		out[ep] = info
	}
	for ep, info := range out {
		sort.Strings(info.Models)
		out[ep] = info
	}
	return out
}
