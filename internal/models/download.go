package models

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ModelFile represents a single file in a model's weight repository.
type ModelFile struct {
	Filename  string `json:"filename"`
	Hash      string `json:"hash"`
	Algorithm string `json:"algorithm"`
	SizeBytes int64  `json:"size_bytes"`
	URL       string `json:"url"`
}

// ModelFilesResponse is the response from GET /api/v1/models/:model_id/files.
type ModelFilesResponse struct {
	ModelID        string      `json:"model_id"`
	Files          []ModelFile `json:"files"`
	TotalSizeBytes int64       `json:"total_size_bytes"`
	FileCount      int         `json:"file_count"`
}

// VerifyResult describes the result of verifying a single file.
type VerifyResult struct {
	Filename string
	Status   string // "pass", "fail", "missing"
	Expected string
	Actual   string
}

// CatalogModel represents a model available in the Swan Model Repository.
type CatalogModel struct {
	ModelID        string `json:"model_id"`
	Name           string `json:"name"`
	Category       string `json:"category"`
	FileCount      int    `json:"file_count"`
	TotalSizeBytes int64  `json:"total_size_bytes"`
	WeightSourceURL string `json:"weight_source_url,omitempty"`
}

// CatalogResponse is the response from GET /api/v1/models/catalog.
type CatalogResponse struct {
	Models []CatalogModel `json:"models"`
	Total  int            `json:"total"`
}

// FetchCatalog calls the swan-inference API to get the list of available models.
func FetchCatalog(serviceURL string) (*CatalogResponse, error) {
	url := fmt.Sprintf("%s/api/v1/models/catalog", serviceURL)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch catalog: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var wrapper struct {
		Data CatalogResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &wrapper.Data, nil
}

// FetchModelFiles calls the swan-inference API to get the file manifest for a model.
func FetchModelFiles(serviceURL, modelID string) (*ModelFilesResponse, error) {
	url := fmt.Sprintf("%s/api/v1/models/%s/files", serviceURL, url.PathEscape(modelID))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch model files: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var wrapper struct {
		Data ModelFilesResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &wrapper.Data, nil
}

// DownloadModel downloads all model files to destDir.
// It skips files that already exist with the correct size and hash.
func DownloadModel(ctx context.Context, files []ModelFile, destDir string) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", destDir, err)
	}

	for i, f := range files {
		destPath := filepath.Join(destDir, f.Filename)

		// Ensure subdirectory exists (for files like tokenizer/config.json)
		if dir := filepath.Dir(destPath); dir != destDir {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dir, err)
			}
		}

		// Check resume: skip if file exists with matching size and hash
		if info, err := os.Stat(destPath); err == nil {
			if f.Hash != "" && info.Size() == f.SizeBytes && f.SizeBytes > 0 {
				hash, err := hashFile(destPath)
				if err == nil && hash == f.Hash {
					fmt.Printf("[%d/%d] skip %s (already verified)\n", i+1, len(files), f.Filename)
					continue
				}
			} else if f.Hash == "" && info.Size() > 0 {
				fmt.Printf("[%d/%d] skip %s (already exists)\n", i+1, len(files), f.Filename)
				continue
			}
		}

		fmt.Printf("[%d/%d] downloading %s (%s)...\n", i+1, len(files), f.Filename, humanSize(f.SizeBytes))

		if err := downloadFile(ctx, f.URL, destPath, f.SizeBytes); err != nil {
			return fmt.Errorf("failed to download %s: %w", f.Filename, err)
		}

		// Verify hash after download (only if expected hash is available)
		if f.Hash != "" {
			hash, err := hashFile(destPath)
			if err != nil {
				return fmt.Errorf("failed to hash %s: %w", f.Filename, err)
			}
			if hash != f.Hash {
				os.Remove(destPath)
				return fmt.Errorf("hash mismatch for %s: expected %s, got %s", f.Filename, f.Hash, hash)
			}
			fmt.Printf("[%d/%d] verified %s\n", i+1, len(files), f.Filename)
		}
	}

	return nil
}

// DownloadModelAndSaveManifest downloads a model and saves the hash manifest.
// This is the recommended entry point for model downloads.
func DownloadModelAndSaveManifest(ctx context.Context, modelID string, files []ModelFile, destDir string) error {
	if err := DownloadModel(ctx, files, destDir); err != nil {
		return err
	}

	if err := SaveHashManifest(modelID, destDir, files); err != nil {
		fmt.Printf("Warning: failed to save hash manifest: %v\n", err)
		// Non-fatal — model is still usable without manifest
	} else {
		fmt.Printf("Saved hash manifest for %s\n", modelID)
	}

	return nil
}

// VerifyModel checks each local file against expected hashes.
func VerifyModel(files []ModelFile, destDir string) []VerifyResult {
	results := make([]VerifyResult, 0, len(files))

	for _, f := range files {
		destPath := filepath.Join(destDir, f.Filename)
		result := VerifyResult{
			Filename: f.Filename,
			Expected: f.Hash,
		}

		if _, err := os.Stat(destPath); os.IsNotExist(err) {
			result.Status = "missing"
			results = append(results, result)
			continue
		}

		hash, err := hashFile(destPath)
		if err != nil {
			result.Status = "fail"
			result.Actual = fmt.Sprintf("error: %v", err)
			results = append(results, result)
			continue
		}

		result.Actual = hash
		if hash == f.Hash {
			result.Status = "pass"
		} else {
			result.Status = "fail"
		}
		results = append(results, result)
	}

	return results
}

// hashFile computes the SHA256 hash of a file.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// downloadFile downloads a URL to a local file, using a temp file for atomicity.
func downloadFile(ctx context.Context, url, destPath string, expectedSize int64) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "SwanProvider/2.0")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Write to temp file first, then rename for atomicity
	tmpPath := destPath + ".tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	var written int64
	if expectedSize > 0 {
		// Print progress
		pr := &progressReader{
			reader:   resp.Body,
			total:    expectedSize,
			filename: filepath.Base(destPath),
		}
		written, err = io.Copy(out, pr)
		fmt.Println() // newline after progress
	} else {
		written, err = io.Copy(out, resp.Body)
	}
	out.Close()

	if err != nil {
		os.Remove(tmpPath)
		return err
	}

	if expectedSize > 0 && written != expectedSize {
		os.Remove(tmpPath)
		return fmt.Errorf("size mismatch: expected %d, got %d", expectedSize, written)
	}

	return os.Rename(tmpPath, destPath)
}

// progressReader wraps a reader and prints download progress.
type progressReader struct {
	reader   io.Reader
	total    int64
	read     int64
	filename string
	lastPct  int
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.read += int64(n)

	pct := int(float64(pr.read) / float64(pr.total) * 100)
	if pct != pr.lastPct && pct%5 == 0 {
		pr.lastPct = pct
		fmt.Printf("\r  %s: %s / %s (%d%%)", pr.filename, humanSize(pr.read), humanSize(pr.total), pct)
	}

	return n, err
}

func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// --- Hash Manifest for Model Verification ---

// HashManifest stores per-file hashes and a composite hash for model weight verification.
// Saved as .swan-hash-manifest.json in the model directory.
type HashManifest struct {
	ModelID       string             `json:"model_id"`
	CompositeHash string             `json:"composite_hash"`
	Algorithm     string             `json:"algorithm"`
	Files         []HashManifestFile `json:"files"`
	CreatedAt     string             `json:"created_at"`
}

// HashManifestFile describes a single file's hash in the manifest
type HashManifestFile struct {
	Filename  string `json:"filename"`
	Hash      string `json:"hash"`
	SizeBytes int64  `json:"size_bytes"`
}

const hashManifestFilename = ".swan-hash-manifest.json"

// ComputeCompositeHash computes a deterministic composite hash from per-file hashes.
// Algorithm: sort filenames alphabetically, concatenate "filename:hash\n" strings, SHA256 the result.
// This must match the server-side algorithm in swan-inference.
func ComputeCompositeHash(files []HashManifestFile) string {
	if len(files) == 0 {
		return ""
	}

	sorted := make([]HashManifestFile, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Filename < sorted[j].Filename
	})

	h := sha256.New()
	for _, f := range sorted {
		h.Write([]byte(f.Filename + ":" + f.Hash + "\n"))
	}

	return fmt.Sprintf("%x", h.Sum(nil))
}

// SaveHashManifest saves a hash manifest for a downloaded model.
// Should be called after DownloadModel() succeeds.
func SaveHashManifest(modelID, destDir string, files []ModelFile) error {
	manifestFiles := make([]HashManifestFile, len(files))
	for i, f := range files {
		manifestFiles[i] = HashManifestFile{
			Filename:  f.Filename,
			Hash:      f.Hash,
			SizeBytes: f.SizeBytes,
		}
	}

	manifest := HashManifest{
		ModelID:       modelID,
		CompositeHash: ComputeCompositeHash(manifestFiles),
		Algorithm:     "sha256",
		Files:         manifestFiles,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal hash manifest: %w", err)
	}

	manifestPath := filepath.Join(destDir, hashManifestFilename)
	if err := os.WriteFile(manifestPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write hash manifest: %w", err)
	}

	return nil
}

// LoadHashManifest loads a hash manifest from a model directory.
// Returns nil, nil if the manifest does not exist.
func LoadHashManifest(destDir string) (*HashManifest, error) {
	manifestPath := filepath.Join(destDir, hashManifestFilename)

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read hash manifest: %w", err)
	}

	var manifest HashManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse hash manifest: %w", err)
	}

	return &manifest, nil
}
