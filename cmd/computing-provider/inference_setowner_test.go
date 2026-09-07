package main

import "testing"

func TestValidateEthAddress(t *testing.T) {
	valid := "0x46f2b8De0B7Fa1B1cF6C0dB5C2E8a9d40f13F93c"
	if err := validateEthAddress(valid); err != nil {
		t.Errorf("a well-formed address was rejected: %v", err)
	}

	// Each of these is a shape an operator actually types.
	for name, addr := range map[string]string{
		"no 0x prefix":   "46f2b8De0B7Fa1B1cF6C0dB5C2E8a9d40f13F93c",
		"one char short": "0x46f2b8De0B7Fa1B1cF6C0dB5C2E8a9d40f13F93",
		"one char long":  "0x46f2b8De0B7Fa1B1cF6C0dB5C2E8a9d40f13F93cc",
		"empty":          "",
		// The length check alone passes this one, which is the point: a
		// transposed character that lands on a non-hex letter must not be
		// accepted and written to a field that cannot be corrected.
		"non-hex char": "0x46f2b8De0B7Fa1B1cF6C0dB5C2E8a9d40f13F93z",
	} {
		if err := validateEthAddress(addr); err == nil {
			t.Errorf("%s: %q was accepted, want rejected", name, addr)
		}
	}
}
