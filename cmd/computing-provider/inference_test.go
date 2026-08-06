package main

import (
	"testing"

	"github.com/swanchain/computing-provider-v2/internal/setup"
)

func TestAutoSelectScoreCompetitionPenalty(t *testing.T) {
	niche := modelDemandEntry{EstDailyEarnings: 10, OnlineProviders: 0, DemandTrend: "stable"}
	crowded := modelDemandEntry{EstDailyEarnings: 10, OnlineProviders: 9, DemandTrend: "stable"}
	if autoSelectScore(niche) <= autoSelectScore(crowded) {
		t.Errorf("expected niche model (%.2f) to outscore crowded model (%.2f)",
			autoSelectScore(niche), autoSelectScore(crowded))
	}
}

func TestAutoSelectScoreTrendFactor(t *testing.T) {
	up := modelDemandEntry{EstDailyEarnings: 10, DemandTrend: "up"}
	stable := modelDemandEntry{EstDailyEarnings: 10, DemandTrend: "stable"}
	down := modelDemandEntry{EstDailyEarnings: 10, DemandTrend: "down"}
	if !(autoSelectScore(up) > autoSelectScore(stable) && autoSelectScore(stable) > autoSelectScore(down)) {
		t.Errorf("expected up > stable > down, got %.2f / %.2f / %.2f",
			autoSelectScore(up), autoSelectScore(stable), autoSelectScore(down))
	}
}

func TestAutoSelectScoreRevenueFallback(t *testing.T) {
	e := modelDemandEntry{Revenue24h: 100, OnlineProviders: 4, DemandTrend: "stable"}
	if got, want := autoSelectScore(e), 100.0/5.0; got != want {
		t.Errorf("expected revenue-share fallback %.2f, got %.2f", want, got)
	}
}

func TestMatchEntryToServersExactHFID(t *testing.T) {
	servers := []setup.DiscoveredServer{
		{Endpoint: "http://localhost:30000", Type: setup.ServerTypeSGLang, Healthy: true, Models: []string{"meta-llama/Llama-3.2-3B-Instruct"}},
	}
	e := modelDemandEntry{ModelID: "meta-llama/Llama-3.2-3B-Instruct", Name: "Llama 3.2 3B", Category: "text-generation"}

	m := matchEntryToServers(e, servers)
	if m == nil {
		t.Fatal("expected a match, got nil")
	}
	if m.confidence != 1.0 {
		t.Errorf("expected confidence 1.0 for exact HF-ID match, got %.2f", m.confidence)
	}
}

func TestMatchEntryToServersOllamaAlias(t *testing.T) {
	servers := []setup.DiscoveredServer{
		{Endpoint: "http://localhost:11434", Type: setup.ServerTypeOllama, Healthy: true, Models: []string{"llama3.2:3b"}},
	}
	e := modelDemandEntry{ModelID: "meta-llama/Llama-3.2-3B-Instruct", Name: "Llama 3.2 3B", Category: "text-generation"}

	m := matchEntryToServers(e, servers)
	if m == nil {
		t.Fatal("expected an alias match, got nil")
	}
	if m.localModel != "llama3.2:3b" {
		t.Errorf("expected local model llama3.2:3b, got %s", m.localModel)
	}
	if m.confidence < autoMatchConfidenceMin {
		t.Errorf("expected confidence >= %.2f, got %.2f", autoMatchConfidenceMin, m.confidence)
	}
}

func TestMatchEntryToServersSkipsUnhealthy(t *testing.T) {
	servers := []setup.DiscoveredServer{
		{Endpoint: "http://localhost:30000", Type: setup.ServerTypeSGLang, Healthy: false, Models: []string{"meta-llama/Llama-3.2-3B-Instruct"}},
	}
	e := modelDemandEntry{ModelID: "meta-llama/Llama-3.2-3B-Instruct", Name: "Llama 3.2 3B"}
	if m := matchEntryToServers(e, servers); m != nil {
		t.Errorf("expected no match from unhealthy server, got %s", m.server.Endpoint)
	}
}

func TestMatchEntryToServersNoMatch(t *testing.T) {
	servers := []setup.DiscoveredServer{
		{Endpoint: "http://localhost:11434", Type: setup.ServerTypeOllama, Healthy: true, Models: []string{"qwen2.5:7b"}},
	}
	e := modelDemandEntry{ModelID: "black-forest-labs/FLUX.1-schnell", Name: "FLUX.1 Schnell", Category: "image-generation"}
	if m := matchEntryToServers(e, servers); m != nil {
		t.Errorf("expected no match for unserved model, got %s (%.2f)", m.localModel, m.confidence)
	}
}
