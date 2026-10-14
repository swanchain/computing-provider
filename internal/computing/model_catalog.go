package computing

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const modelsDevURL = "https://models.dev/models.json"

// fetchCatalogContexts retrieves public, theoretical model windows. They are
// useful as a labelled fallback in local status, but are not evidence that a
// particular backend can serve a prompt of that length.
func fetchCatalogContexts(client *http.Client, endpoint string) (map[string]int, error) {
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog returned HTTP %d", resp.StatusCode)
	}
	var models map[string]struct {
		Limit struct {
			Context int `json:"context"`
		} `json:"limit"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 12<<20)).Decode(&models); err != nil {
		return nil, err
	}
	contexts := make(map[string]int, len(models))
	for id, model := range models {
		if model.Limit.Context > 0 {
			contexts[id] = model.Limit.Context
		}
	}
	return contexts, nil
}

func loadCatalogContexts() (map[string]int, error) {
	return fetchCatalogContexts(&http.Client{Timeout: 3 * time.Second}, modelsDevURL)
}
