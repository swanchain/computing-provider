package main

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestSplitVersion(t *testing.T) {
	for in, want := range map[string][3]int{
		"0.4.0":         {0, 4, 0},
		"v0.4.0":        {0, 4, 0},
		"1.2.3":         {1, 2, 3},
		"0.4.0+mainnet": {0, 4, 0},
		"0.5.0-rc1":     {0, 5, 0},
		"1.2":           {1, 2, 0},
		"7":             {7, 0, 0},
		"":              {0, 0, 0},
		"garbage":       {0, 0, 0},
	} {
		if got := splitVersion(in); got != want {
			t.Errorf("splitVersion(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.4.0", "0.4.0", 0},
		{"0.4.0", "0.5.0", -1},
		{"0.5.0", "0.4.0", 1},
		{"0.4.0", "0.4.1", -1},
		{"1.0.0", "0.9.9", 1},
		{"0.4.0+mainnet+git.abc", "0.4.0", 0}, // build metadata must not read as newer
		{"0.10.0", "0.9.0", 1},                // numeric, not lexical
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// A release with no checksums must be distinguishable from one whose checksums
// could not be verified: the caller warns on the first and refuses on the
// second, so collapsing them would silently install an unverified binary.
func TestChecksumForReportsAbsenceDistinctly(t *testing.T) {
	rel := &ghRelease{Assets: []ghAsset{{Name: "computing-provider-linux-amd64"}}}
	_, err := rel.checksumFor("computing-provider-linux-amd64")
	if !errors.Is(err, errNoChecksums) {
		t.Errorf("err = %v, want errNoChecksums", err)
	}
}

// Picking the first asset containing "checksum" selects the wrong file as soon
// as a release publishes one per platform, or adds a detached signature.
func TestFindChecksumAssetPicksTheRightFile(t *testing.T) {
	binary := "computing-provider-linux-amd64"

	perPlatform := &ghRelease{Assets: []ghAsset{
		{Name: "computing-provider-darwin-arm64.sha256"},
		{Name: "computing-provider-linux-amd64.sha256"},
	}}
	if got := perPlatform.findChecksumAsset(binary); got == nil || got.Name != binary+".sha256" {
		t.Errorf("per-platform: picked %v, want the entry for this platform", got)
	}

	signed := &ghRelease{Assets: []ghAsset{
		{Name: "checksums.txt.sig"},
		{Name: "checksums.txt"},
	}}
	if got := signed.findChecksumAsset(binary); got == nil || got.Name != "checksums.txt" {
		t.Errorf("signed: picked %v, want checksums.txt and not the signature", got)
	}

	none := &ghRelease{Assets: []ghAsset{{Name: binary}}}
	if got := none.findChecksumAsset(binary); got != nil {
		t.Errorf("picked %v, want nil when no checksums are published", got)
	}
}

// chmod +x over the running agent should not happen on trust alone: a captive
// portal or proxy can return an HTML error page with a 200.
func TestLooksExecutable(t *testing.T) {
	elf := []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01}
	if !looksExecutable(elf) {
		t.Error("ELF should be accepted")
	}

	machO := make([]byte, 8)
	binary.BigEndian.PutUint32(machO, 0xcffaedfe)
	if !looksExecutable(machO) {
		t.Error("Mach-O should be accepted")
	}

	for _, bad := range [][]byte{
		[]byte("<!DOCTYPE html><html><body>404 Not Found"),
		[]byte("{\"message\":\"Not Found\"}"),
		[]byte(""),
		[]byte("ELF"), // too short to be anything
	} {
		if looksExecutable(bad) {
			t.Errorf("accepted non-executable payload %.20q", bad)
		}
	}
}
