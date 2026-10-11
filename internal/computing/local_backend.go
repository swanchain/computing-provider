package computing

import (
	"context"
	"fmt"
	"net/http"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
)

// Reachability of the local gateway's own backends.
//
// The health checker probes each model's hub endpoint, and model state is
// built from that alone. A model with a separate local_endpoint could
// therefore show ready and healthy while every local request failed against
// a backend that was down. This checks each local endpoint the cheap way —
// GET /v1/models with its key — so the dashboard can say so.
//
// It deliberately sends no completion. A local endpoint is typically a proxy
// in front of a metered subscription, and a probe there spends the quota it
// would be checking; whether that quota is exhausted is a question for the
// proxy's own account status, not for a reachability check.

const localBackendInterval = time.Minute

// localBackendStatus is one endpoint's last check.
type localBackendStatus struct {
	OK        bool
	Error     string
	CheckedAt time.Time
}

type localBackendChecker struct {
	endpoints func() map[string]string // endpoint -> API key
	client    *http.Client

	mu     sync.Mutex
	status map[string]localBackendStatus
	stop   chan struct{}
	done   chan struct{}
}

func newLocalBackendChecker(endpoints func() map[string]string) *localBackendChecker {
	return &localBackendChecker{
		endpoints: endpoints,
		client:    &http.Client{Timeout: 10 * time.Second},
		status:    map[string]localBackendStatus{},
	}
}

func (c *localBackendChecker) Start() {
	c.stop = make(chan struct{})
	c.done = make(chan struct{})
	go func() {
		defer close(c.done)
		defer func() {
			if err := recover(); err != nil {
				logs.GetLogger().Errorf("[local_backend] panic recovered: %v\n%s", err, debug.Stack())
			}
		}()
		c.checkAll()
		ticker := time.NewTicker(localBackendInterval)
		defer ticker.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-ticker.C:
				c.checkAll()
			}
		}
	}()
}

func (c *localBackendChecker) Stop() {
	if c == nil || c.stop == nil {
		return
	}
	close(c.stop)
	<-c.done
}

func (c *localBackendChecker) checkAll() {
	eps := c.endpoints()
	keys := make([]string, 0, len(eps))
	for ep := range eps {
		keys = append(keys, ep)
	}
	sort.Strings(keys)
	for _, ep := range keys {
		st := localBackendStatus{CheckedAt: time.Now()}
		if err := c.check(ep, eps[ep]); err != nil {
			st.Error = err.Error()
		} else {
			st.OK = true
		}
		c.mu.Lock()
		prev, seen := c.status[ep]
		c.status[ep] = st
		c.mu.Unlock()
		if seen && prev.OK != st.OK {
			if st.OK {
				logs.GetLogger().Infof("Local gateway backend %s is reachable again", ep)
			} else {
				logs.GetLogger().Warnf("Local gateway backend %s is unreachable: %s", ep, st.Error)
			}
		}
	}
	c.mu.Lock()
	for ep := range c.status {
		if _, ok := eps[ep]; !ok {
			delete(c.status, ep)
		}
	}
	c.mu.Unlock()
}

func (c *localBackendChecker) check(endpoint, apiKey string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/v1/models", nil)
	if err != nil {
		return err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("unreachable: %v", err)
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("rejected local_api_key (HTTP %d)", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("HTTP %d from /v1/models", resp.StatusCode)
	}
	return nil
}

// Status returns an endpoint's last check; ok is false before the first.
func (c *localBackendChecker) Status(endpoint string) (localBackendStatus, bool) {
	if c == nil {
		return localBackendStatus{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.status[endpoint]
	return st, ok
}

// localBackends lists the distinct local endpoints in use, with their keys.
func (s *InferenceService) localBackends() map[string]string {
	out := map[string]string{}
	if s.registry == nil {
		return out
	}
	for _, m := range s.registry.GetAllModels() {
		if m.LocalEndpoint != "" && m.LocalEndpoint != m.Endpoint {
			if _, seen := out[m.LocalEndpoint]; !seen || out[m.LocalEndpoint] == "" {
				out[m.LocalEndpoint] = m.LocalAPIKey
			}
		}
	}
	return out
}

// withLocalHealth fills in each model's local backend status for display.
func (s *InferenceService) withLocalHealth(models []*RegisteredModel) []*RegisteredModel {
	for _, m := range models {
		if m.LocalEndpoint == "" {
			continue
		}
		st, ok := s.localChecker.Status(m.LocalEndpoint)
		switch {
		case !ok:
			m.LocalHealth = "unknown"
		case st.OK:
			m.LocalHealth = "healthy"
		default:
			m.LocalHealth, m.LocalError = "unreachable", st.Error
		}
	}
	return models
}
