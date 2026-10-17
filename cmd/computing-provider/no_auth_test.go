package main

import "testing"

func TestIsLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1":   true,
		"127.0.0.2":   true,
		"::1":         true,
		"[::1]":       true,
		"localhost":   true,
		"LOCALHOST":   true,
		" 127.0.0.1 ": true,
		"":            false,
		"0.0.0.0":     false,
		"::":          false,
		"10.0.0.5":    false,
		"40.49.8.1":   false,
		"example.com": false,
	} {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}
