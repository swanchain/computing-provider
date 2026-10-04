package computing

import "testing"

func TestParseNvidiaVRAMGB(t *testing.T) {
	sys := func() int { return 121 }
	cases := []struct {
		field string
		want  int
	}{
		{"10240", 10},
		{"24576", 24},
		{"512", 1},
		// GB10, Jetson, Thor: unified memory. Reporting 1 GB here got the
		// machine rejected by the hub's 8 GB minimum.
		{"[N/A]", 121},
		{"", 121},
	}
	for _, c := range cases {
		if got := parseNvidiaVRAMGB(c.field, sys); got != c.want {
			t.Errorf("parseNvidiaVRAMGB(%q) = %d, want %d", c.field, got, c.want)
		}
	}
	if got := parseNvidiaVRAMGB("[N/A]", func() int { return 0 }); got != 0 {
		t.Errorf("unreadable system memory = %d, want 0 (unknown), never a small number", got)
	}
}
