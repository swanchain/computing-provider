package computing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckMachineIdentity(t *testing.T) {
	dir := t.TempDir()
	fpPath := filepath.Join(dir, "machine_fingerprint")

	// First run: no file — writes fingerprint, no error
	if err := CheckMachineIdentity(dir); err != nil {
		t.Fatalf("first run should pass: %v", err)
	}
	data, err := os.ReadFile(fpPath)
	if err != nil {
		t.Fatalf("fingerprint file not written: %v", err)
	}
	if !strings.HasPrefix(string(data), fingerprintVersion) {
		t.Fatalf("fingerprint should be versioned, got %q", data)
	}

	// Matching fingerprint: passes
	if err := CheckMachineIdentity(dir); err != nil {
		t.Fatalf("matching fingerprint should pass: %v", err)
	}

	// Legacy (unversioned) fingerprint: migrates instead of failing
	if err := os.WriteFile(fpPath, []byte("deadbeefdeadbeefdeadbeefdeadbeef"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := CheckMachineIdentity(dir); err != nil {
		t.Fatalf("legacy fingerprint should migrate, got error: %v", err)
	}
	data, _ = os.ReadFile(fpPath)
	if !strings.HasPrefix(string(data), fingerprintVersion) {
		t.Fatalf("legacy fingerprint should be rewritten as versioned, got %q", data)
	}

	// Genuinely different versioned fingerprint: rejected (non-interactive)
	if err := os.WriteFile(fpPath, []byte(fingerprintVersion+"0000000000000000000000000000000"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := CheckMachineIdentity(dir); err == nil {
		t.Fatal("mismatched versioned fingerprint should fail in non-interactive mode")
	}
}

func TestGetMachineFingerprintStable(t *testing.T) {
	a := getMachineFingerprint()
	b := getMachineFingerprint()
	if a != b {
		t.Fatalf("fingerprint not deterministic: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, fingerprintVersion) {
		t.Fatalf("fingerprint missing version prefix: %q", a)
	}
}
