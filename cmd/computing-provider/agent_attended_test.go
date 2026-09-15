package main

import (
	"strings"
	"testing"
)

// An acting run assumes an operator is reading the trace, so it is refused when
// detached. A read-only run is not: it changes nothing, and reporting on a node
// from a script is a reasonable thing to do.
func TestCheckAttended(t *testing.T) {
	cases := []struct {
		name         string
		allowActions bool
		interactive  bool
		wantErr      bool
	}{
		{"acting run at a terminal", true, true, false},
		{"acting run detached is refused", true, false, true},
		{"read-only run detached is fine", false, false, false},
		{"read-only run at a terminal", false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkAttended(tc.allowActions, tc.interactive)
			if tc.wantErr && err == nil {
				t.Error("expected a refusal, got none")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected refusal: %v", err)
			}
		})
	}
}

// The refusal has to name the supported unattended path, or the operator's next
// move is to look for a --force flag.
func TestCheckAttendedNamesTheScheduler(t *testing.T) {
	err := checkAttended(true, false)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"AutoSwitch", "scheduler"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}
