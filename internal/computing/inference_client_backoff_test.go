package computing

import (
	"testing"
	"time"
)

// reconnectBackoff mirrors the delay calculation in InferenceClient.reconnect.
func reconnectBackoff(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}
	delay := maxReconnectDelay
	if shift := attempt - 1; shift < 32 {
		if d := time.Duration(1<<uint(shift)) * time.Second; d < maxReconnectDelay {
			delay = d
		}
	}
	return delay
}

func TestReconnectBackoffNeverCollapsesToZero(t *testing.T) {
	for _, attempt := range []int{1, 2, 3, 6, 62, 63, 64, 65, 1000, 22146097} {
		got := reconnectBackoff(attempt)
		if got <= 0 {
			t.Fatalf("attempt %d: delay %v, want > 0 (a zero delay hot-loops against the server)", attempt, got)
		}
		if got > maxReconnectDelay {
			t.Fatalf("attempt %d: delay %v exceeds cap %v", attempt, got, maxReconnectDelay)
		}
	}
}

func TestReconnectBackoffGrowsThenCaps(t *testing.T) {
	want := map[int]time.Duration{
		1: 1 * time.Second,
		2: 2 * time.Second,
		3: 4 * time.Second,
		4: 8 * time.Second,
		5: 16 * time.Second,
		6: maxReconnectDelay,
		7: maxReconnectDelay,
	}
	for attempt, expect := range want {
		if got := reconnectBackoff(attempt); got != expect {
			t.Errorf("attempt %d: delay %v, want %v", attempt, got, expect)
		}
	}
}
