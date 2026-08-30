package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/swanchain/computing-provider-v2/build"
	"github.com/urfave/cli/v2"
)

const releasesAPI = "https://api.github.com/repos/swanchain/computing-provider/releases/latest"

var versionCmd = &cli.Command{
	Name:  "version",
	Usage: "Print the agent version",
	Action: func(cctx *cli.Context) error {
		fmt.Printf("computing-provider %s\n", build.UserVersion())
		fmt.Printf("  platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		return nil
	},
}

var updateCmd = &cli.Command{
	Name:  "update",
	Usage: "Check for a newer release and install it",
	Description: `Compares this build against the latest published release.

With --check it only reports. Otherwise it downloads the binary for this
platform, verifies its SHA-256 against the checksums published with the release
when one exists, and replaces the running executable in place.

The provider is NOT restarted: replacing the file leaves the running process on
the old build until you restart it yourself, which is deliberate — an agent that
restarts itself mid-request drops that request.`,
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "check",
			Usage: "Only report whether a newer release exists; change nothing",
		},
		&cli.BoolFlag{
			Name:  "yes",
			Usage: "Do not prompt before replacing the binary",
		},
	},
	Action: func(cctx *cli.Context) error {
		rel, err := latestRelease()
		if err != nil {
			return fmt.Errorf("could not check for updates: %w", err)
		}
		current := build.BuildVersion
		latest := strings.TrimPrefix(rel.TagName, "v")

		fmt.Printf("installed: v%s\nlatest:    %s\n", current, rel.TagName)
		if compareVersions(current, latest) >= 0 {
			fmt.Println("\nAlready up to date.")
			return nil
		}
		fmt.Printf("\nA newer release is available: %s\n", rel.TagName)
		if rel.Body != "" {
			fmt.Printf("\n%s\n", strings.TrimSpace(firstLines(rel.Body, 12)))
		}
		if cctx.Bool("check") {
			fmt.Println("\nRun `computing-provider update` to install it.")
			return nil
		}

		assetName := fmt.Sprintf("computing-provider-%s-%s", runtime.GOOS, runtime.GOARCH)
		asset := rel.findAsset(assetName)
		if asset == nil {
			return fmt.Errorf("release %s publishes no binary for %s/%s — build from source instead:\n"+
				"  git clean -fd && git pull && make clean && make mainnet && sudo make install",
				rel.TagName, runtime.GOOS, runtime.GOARCH)
		}

		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("cannot locate the running binary: %w", err)
		}
		exe, _ = filepath.EvalSymlinks(exe)

		if !cctx.Bool("yes") {
			fmt.Printf("\nThis replaces %s (%s, %.1f MB).\nContinue? [y/N] ", exe, asset.Name, float64(asset.Size)/(1<<20))
			var answer string
			fmt.Scanln(&answer)
			if !strings.EqualFold(strings.TrimSpace(answer), "y") {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		fmt.Printf("\nDownloading %s ...\n", asset.Name)
		blob, err := download(asset.BrowserDownloadURL)
		if err != nil {
			return fmt.Errorf("download failed: %w", err)
		}

		sum := sha256.Sum256(blob)
		got := hex.EncodeToString(sum[:])
		switch want, err := rel.checksumFor(asset.Name); {
		case err != nil:
			// A release without a checksums file is not a reason to refuse —
			// the download is still HTTPS from GitHub — but the operator should
			// know the difference between "verified" and "trusted the transport".
			fmt.Printf("  sha256 %s (release publishes no checksums file; verified by HTTPS only)\n", got)
		case !strings.EqualFold(want, got):
			return fmt.Errorf("checksum mismatch for %s:\n  expected %s\n  got      %s\nrefusing to install", asset.Name, want, got)
		default:
			fmt.Printf("  sha256 %s (verified against the published checksums)\n", got)
		}

		if err := replaceBinary(exe, blob); err != nil {
			return err
		}

		fmt.Printf("\nInstalled %s to %s\n", rel.TagName, exe)
		fmt.Println("The running provider is still on the old build — restart it to pick this up:")
		fmt.Println("  systemctl restart computing-provider     # if running under systemd")
		fmt.Println("  # otherwise stop the current process and run `computing-provider run` again")
		return nil
	},
}

// replaceBinary writes the new build beside the target and renames it into
// place. The rename is atomic within a filesystem, so a crash mid-update cannot
// leave a half-written executable where the agent used to be.
func replaceBinary(exe string, blob []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".computing-provider-update-*")
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("no permission to write to %s — re-run with sudo", dir)
		}
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(blob); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpName, exe); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("no permission to replace %s — re-run with sudo", exe)
		}
		return err
	}
	return nil
}

type ghAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Body    string    `json:"body"`
	Assets  []ghAsset `json:"assets"`
}

func (r *ghRelease) findAsset(name string) *ghAsset {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i]
		}
	}
	return nil
}

// checksumFor looks for a checksums asset and returns the digest recorded for
// the named binary. Returns an error when the release publishes none.
func (r *ghRelease) checksumFor(binary string) (string, error) {
	var sums *ghAsset
	for i := range r.Assets {
		n := strings.ToLower(r.Assets[i].Name)
		if strings.Contains(n, "checksum") || strings.HasSuffix(n, ".sha256") {
			sums = &r.Assets[i]
			break
		}
	}
	if sums == nil {
		return "", fmt.Errorf("no checksums asset")
	}
	body, err := download(sums.BrowserDownloadURL)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.TrimPrefix(fields[1], "*") == binary {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no entry for %s", binary)
}

func latestRelease() (*ghRelease, error) {
	body, err := download(releasesAPI)
	if err != nil {
		return nil, err
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, err
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("no published release found")
	}
	return &rel, nil
}

func download(url string) ([]byte, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream, application/vnd.github+json")
	req.Header.Set("User-Agent", "computing-provider/"+build.BuildVersion)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + "\n..."
}

// compareVersions returns -1, 0 or 1. Missing or non-numeric components sort as
// zero and any pre-release suffix is ignored: this decides whether to offer an
// update, so being approximately right on an odd tag beats refusing to answer.
func compareVersions(a, b string) int {
	pa, pb := splitVersion(a), splitVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func splitVersion(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, part := range strings.SplitN(v, ".", 3) {
		if i > 2 {
			break
		}
		if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			out[i] = n
		}
	}
	return out
}
