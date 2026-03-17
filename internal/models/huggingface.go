package models

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const huggingFaceAPIBase = "https://huggingface.co"

// HFModelInfo is the response from the HuggingFace model API.
type HFModelInfo struct {
	Siblings []HFSibling `json:"siblings"`
}

// HFSibling represents a file in a HuggingFace model repository.
type HFSibling struct {
	RFilename string `json:"rfilename"`
	Size      int64  `json:"size"`
	LFS       *HFLFS `json:"lfs,omitempty"`
}

// HFLFS contains LFS metadata for a file.
type HFLFS struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// FetchHuggingFaceFiles fetches the file list for a model from HuggingFace.
func FetchHuggingFaceFiles(modelID string) ([]ModelFile, error) {
	url := fmt.Sprintf("%s/api/models/%s", huggingFaceAPIBase, modelID)

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "SwanProvider/2.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from HuggingFace: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HuggingFace API returned %d: %s", resp.StatusCode, string(body))
	}

	var info HFModelInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("failed to parse HuggingFace response: %w", err)
	}

	files := make([]ModelFile, 0, len(info.Siblings))
	for _, s := range info.Siblings {
		f := ModelFile{
			Filename: s.RFilename,
			URL:      fmt.Sprintf("%s/%s/resolve/main/%s", huggingFaceAPIBase, modelID, s.RFilename),
		}
		if s.LFS != nil {
			f.Hash = s.LFS.SHA256
			f.Algorithm = "sha256"
			f.SizeBytes = s.LFS.Size
		} else {
			f.SizeBytes = s.Size
		}
		files = append(files, f)
	}

	return files, nil
}

// HuggingFaceModelSize returns the total size of all files.
func HuggingFaceModelSize(files []ModelFile) int64 {
	var total int64
	for _, f := range files {
		total += f.SizeBytes
	}
	return total
}
