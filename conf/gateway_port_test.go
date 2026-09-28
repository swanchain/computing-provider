package conf

import "testing"

func TestGatewayPort(t *testing.T) {
	for set, want := range map[int]int{0: DefaultLocalGatewayPort, -1: 0, 9100: 9100} {
		if got := (Inference{LocalGatewayPort: set}).GatewayPort(); got != want {
			t.Errorf("LocalGatewayPort=%d -> %d, want %d", set, got, want)
		}
	}
}
