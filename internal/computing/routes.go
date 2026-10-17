package computing

import (
	"sort"
	"strings"
	"time"

	"github.com/swanchain/computing-provider-v2/internal/db"
)

// RouteMap is the traffic through this node over a recent window, as links
// from where requests entered (source) through a model to the backend that
// served it, beside every backend the node is configured to use. A backend
// with no traffic is listed too: a server that stopped or moved shows as a
// backend with no links and a down state, which is what an operator needs to
// see.
type RouteMap struct {
	Window   string         `json:"window"`
	Since    time.Time      `json:"since"`
	Links    []RouteLink    `json:"links"`
	Backends []RouteBackend `json:"backends"`
}

// RouteLink aggregates the requests from one source for one model to one
// backend.
//
// A link whose source is "direct" is work a backend did that never passed
// through the node, derived from the backend's own counters as in the usage
// history: it carries tokens and no requests, latency or errors, because none
// of those were observed.
type RouteLink struct {
	Source       string    `json:"source"`
	Model        string    `json:"model"`
	Backend      string    `json:"backend"`
	Requests     int       `json:"requests"`
	Failures     int       `json:"failures"`
	TokensIn     int64     `json:"tokens_in"`
	TokensOut    int64     `json:"tokens_out"`
	AvgLatencyMs float64   `json:"avg_latency_ms"`
	LastAt       time.Time `json:"last_at"`
	LastError    string    `json:"last_error,omitempty"`
}

// RouteBackend is one backend origin and its current state: "up" when a model
// it serves is healthy, "down" when every one is failing, "unknown" before a
// check has completed.
type RouteBackend struct {
	Endpoint string   `json:"endpoint"`
	Roles    []string `json:"roles"`
	Models   []string `json:"models"`
	Health   string   `json:"health"`
	Error    string   `json:"error,omitempty"`
}

// RouteWindows are the windows the route map accepts.
var RouteWindows = map[string]time.Duration{
	"5m": 5 * time.Minute, "15m": 15 * time.Minute, "1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour,
}

// routeRow is one grouped row of request history.
type routeRow struct {
	Source          string
	Model           string
	BackendEndpoint string
	RouteRecorded   bool
	Requests        int
	Failures        int
	TokensIn        int64
	TokensOut       int64
	LatencySum      float64
	LastAt          string
}

// Routes summarises the request history over window.
func (s *InferenceService) Routes(window string) RouteMap {
	d, ok := RouteWindows[window]
	if !ok {
		window, d = "15m", RouteWindows["15m"]
	}
	since := time.Now().UTC().Add(-d)
	var rows []routeRow
	var failed []RequestHistoryEntity
	if database := db.NewDbService(); database != nil {
		database.Model(&RequestHistoryEntity{}).
			Select("source, model, backend_endpoint, route_recorded, COUNT(*) AS requests, "+
				"SUM(CASE WHEN success THEN 0 ELSE 1 END) AS failures, "+
				"SUM(tokens_in) AS tokens_in, SUM(tokens_out) AS tokens_out, "+
				"SUM(latency_ms) AS latency_sum, MAX(start_time) AS last_at").
			Where("start_time >= ?", since).
			Group("source, model, backend_endpoint, route_recorded").
			Scan(&rows)
		database.Model(&RequestHistoryEntity{}).
			Select("source, model, backend_endpoint, route_recorded, error_reason, start_time").
			Where("start_time >= ? AND success = ?", since, false).
			Order("start_time desc").Limit(500).Find(&failed)
	}
	var models []*RegisteredModel
	if s.registry != nil {
		models = s.withLocalHealth(s.registry.GetAllModels())
	}
	m := buildRouteMap(rows, failed, models, s.directRoutes(window, since, d))
	m.Window, m.Since = window, since
	return m
}

type directRouteEntry struct {
	at    time.Time
	links []RouteLink
}

// directRouteTTL matches the backend sampler's interval: nothing newer can
// exist sooner.
const directRouteTTL = time.Minute

// directRouteBucket is the resolution direct usage is derived at. A request
// is recorded when it starts while a backend counts tokens as it produces
// them, so a bucket much shorter than the requests on it reports one as
// direct work in the bucket it finished in.
func directRouteBucket(window time.Duration) time.Duration {
	switch {
	case window <= time.Hour:
		return 5 * time.Minute
	case window <= 6*time.Hour:
		return 15 * time.Minute
	}
	return time.Hour
}

// directRoutes is the direct usage per backend and model over the window.
func (s *InferenceService) directRoutes(window string, since time.Time, d time.Duration) []RouteLink {
	s.directRouteMu.Lock()
	defer s.directRouteMu.Unlock()
	if e, ok := s.directRouteCache[window]; ok && time.Since(e.at) < directRouteTTL {
		return e.links
	}
	bucket := directRouteBucket(d)
	in, err := s.usageInputs(since, bucket)
	if err != nil {
		return nil
	}
	links := directRouteLinks(in, usageClassOf(s.mappings()), since, time.Now(), bucket)
	if s.directRouteCache == nil {
		s.directRouteCache = map[string]directRouteEntry{}
	}
	s.directRouteCache[window] = directRouteEntry{at: time.Now(), links: links}
	return links
}

// directRouteLinks derives direct usage separately for each counter, so work
// sent straight to a proxy's local instance is not merged with the same
// model's work on the hub instance. Only output tokens make a link: the input
// figure is a lower bound that prompt caching can push either way, and a
// backend that generated nothing the node did not record did no direct work.
func directRouteLinks(in usageData, classOf func(key, model string) string, from, to time.Time, bucket time.Duration) []RouteLink {
	out := []RouteLink{}
	for key, ss := range in.samples {
		models := in.endpointModels[key]
		if len(models) == 0 {
			continue
		}
		series := buildUsageSeries(in.records, map[string][]BackendUsageSample{key: ss}, in.endpointModels, classOf, from, to, bucket, "")
		t := series.Totals[SourceDirect]
		if t.TokensOut <= 0 {
			continue
		}
		var last time.Time
		for _, p := range series.Points {
			if p.Sources[SourceDirect].TokensOut > 0 {
				last = p.Timestamp.Add(bucket)
			}
		}
		if last.After(to) {
			last = to
		}
		ep, _, _ := strings.Cut(key, perModelSep)
		out = append(out, RouteLink{Source: SourceDirect, Model: strings.Join(models, ", "), Backend: safeRouteEndpoint(ep),
			TokensIn: t.TokensIn, TokensOut: t.TokensOut, LastAt: last})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TokensOut != out[j].TokensOut {
			return out[i].TokensOut > out[j].TokensOut
		}
		return out[i].Model+out[i].Backend < out[j].Model+out[j].Backend
	})
	return out
}

// routeSource maps a stored source to its name; rows written before sources
// existed all arrived over the WebSocket.
func routeSource(source string) string {
	if source == "" {
		return string(SourceHub)
	}
	return source
}

// buildRouteMap merges grouped history with the configured models. Rows that
// predate route recording, and probes, carry no backend; they are attributed
// to the backend the model is configured to use for that source, which is
// where they went. A row that recorded its route with no backend was not
// forwarded, and neither was a hub request for a local-only model, which the
// node refuses: those keep no backend.
//
// failed holds recent failures, newest first; each link shows the newest one
// that took the same route.
//
// direct holds the links for work that bypassed the node; they follow the
// recorded links.
func buildRouteMap(rows []routeRow, failed []RequestHistoryEntity, models []*RegisteredModel, direct []RouteLink) RouteMap {
	byID := map[string]*RegisteredModel{}
	for _, m := range models {
		byID[m.ID] = m
	}
	backendFor := func(source, model string) string {
		m, ok := byID[model]
		if !ok {
			return ""
		}
		if source != string(SourceLocal) && m.LocalOnly {
			return ""
		}
		if source == string(SourceLocal) && m.LocalEndpoint != "" {
			return safeRouteEndpoint(m.LocalEndpoint)
		}
		return safeRouteEndpoint(m.Endpoint)
	}

	routeKey := func(source, model, backend string, recorded bool) [3]string {
		source = routeSource(source)
		if backend == "" && !recorded {
			backend = backendFor(source, model)
		}
		return [3]string{source, model, backend}
	}
	lastErrors := map[[3]string]string{}
	for _, f := range failed {
		key := routeKey(f.Source, f.Model, f.BackendEndpoint, f.RouteRecorded)
		if _, seen := lastErrors[key]; !seen {
			lastErrors[key] = f.ErrorReason
		}
	}

	links := map[[3]string]*RouteLink{}
	latency := map[[3]string]float64{}
	for _, r := range rows {
		key := routeKey(r.Source, r.Model, r.BackendEndpoint, r.RouteRecorded)
		source, backend := key[0], key[2]
		l := links[key]
		if l == nil {
			l = &RouteLink{Source: source, Model: r.Model, Backend: backend}
			links[key] = l
		}
		l.Requests += r.Requests
		l.Failures += r.Failures
		l.TokensIn += r.TokensIn
		l.TokensOut += r.TokensOut
		latency[key] += r.LatencySum
		if at := parseRouteTime(r.LastAt); at.After(l.LastAt) {
			l.LastAt = at
		}
	}

	out := RouteMap{Links: []RouteLink{}, Backends: []RouteBackend{}}
	for key, l := range links {
		if l.Requests > 0 {
			l.AvgLatencyMs = latency[key] / float64(l.Requests)
		}
		if l.Failures > 0 {
			l.LastError = lastErrors[key]
		}
		out.Links = append(out.Links, *l)
	}
	sort.Slice(out.Links, func(i, j int) bool {
		a, b := out.Links[i], out.Links[j]
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		return a.Source+a.Model+a.Backend < b.Source+b.Model+b.Backend
	})
	out.Links = append(out.Links, direct...)

	type state struct {
		roles, models   map[string]bool
		up, down, known bool
		lastErr         string
	}
	backends := map[string]*state{}
	note := func(ep, role, model string) *state {
		if ep == "" {
			return nil
		}
		st := backends[ep]
		if st == nil {
			st = &state{roles: map[string]bool{}, models: map[string]bool{}}
			backends[ep] = st
		}
		st.roles[role] = true
		if model != "" {
			st.models[model] = true
		}
		return st
	}
	for _, m := range models {
		// A disabled model is out of service; its backend is listed only if
		// it still carried traffic in the window.
		if !m.Enabled {
			continue
		}
		local := m.LocalEndpoint != "" && safeRouteEndpoint(m.LocalEndpoint) != safeRouteEndpoint(m.Endpoint)
		role := "hub"
		if m.LocalOnly {
			role = "local"
		}
		if st := note(safeRouteEndpoint(m.Endpoint), role, m.ID); st != nil && !(m.LocalOnly && local) {
			switch m.Health {
			case ModelHealthHealthy, ModelHealthDegraded:
				st.up, st.known = true, true
			case ModelHealthUnhealthy:
				st.down, st.known = true, true
			}
		}
		if local {
			if st := note(safeRouteEndpoint(m.LocalEndpoint), "local", m.ID); st != nil {
				switch m.LocalHealth {
				case "healthy":
					st.up, st.known = true, true
				case "unreachable":
					st.down, st.known = true, true
					st.lastErr = m.LocalError
				}
			}
		}
	}
	for _, l := range out.Links {
		role := "hub"
		switch l.Source {
		case string(SourceLocal):
			role = "local"
		case SourceDirect:
			// Whatever called the backend directly gives it no role here,
			// unless it has none from the node's own configuration.
			if _, known := backends[l.Backend]; known {
				continue
			}
			role = "direct"
		}
		note(l.Backend, role, l.Model)
	}
	for ep, st := range backends {
		b := RouteBackend{Endpoint: ep, Roles: sortedKeys(st.roles), Models: sortedKeys(st.models), Health: "unknown"}
		switch {
		case st.up:
			b.Health = "up"
		case st.down:
			b.Health, b.Error = "down", st.lastErr
		}
		out.Backends = append(out.Backends, b)
	}
	sort.Slice(out.Backends, func(i, j int) bool { return out.Backends[i].Endpoint < out.Backends[j].Endpoint })
	return out
}

// parseRouteTime reads MAX(start_time) as SQLite returns it: text in the
// layout the driver wrote, which is not a type the scan can convert.
func parseRouteTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02 15:04:05.999999999-07:00", "2006-01-02T15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999Z07:00", time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
