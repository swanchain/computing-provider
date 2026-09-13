package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// modelsFileMode is the permission new models.json files are created with. It
// can carry a backend's api_key, so it is not world-readable.
const modelsFileMode = 0o600

// ModelsPath returns the path of the models.json in a repo directory.
func ModelsPath(cpRepoPath string) string {
	return filepath.Join(cpRepoPath, "models.json")
}

// readModels loads models.json as raw JSON per model.
//
// Entries are kept as json.RawMessage rather than being decoded into a struct
// so that rewriting one model's entry cannot drop a field this binary does not
// know about from another model's. models.json is an operator-authored file
// and has gained fields more than once; a read-modify-write that decodes it
// through a fixed struct silently erases whatever it was not compiled to
// understand.
func readModels(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]json.RawMessage{}, nil
		}
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]json.RawMessage{}, nil
	}

	models := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &models); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	return models, nil
}

// writeModels writes models.json atomically.
//
// Atomically because the running daemon watches this file with fsnotify: a
// truncate-then-write would be observed as an empty or half-written file, and
// the registry would deregister every model for as long as that took.
func writeModels(path string, models map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(models, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".models.json.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, modelsFileMode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// RegisteredEndpoint returns the endpoint models.json currently declares for a
// model, and whether there is an entry at all.
func RegisteredEndpoint(cpRepoPath, modelID string) (string, bool, error) {
	models, err := readModels(ModelsPath(cpRepoPath))
	if err != nil {
		return "", false, err
	}
	raw, ok := models[modelID]
	if !ok {
		return "", false, nil
	}
	var entry struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return "", true, nil
	}
	return entry.Endpoint, true, nil
}

// RegisterModel points models.json at a freshly started server.
//
// Fields the existing entry carried and this function does not set are kept:
// an operator who set a context_length override or an api_key by hand does not
// lose it because the model was restarted.
func RegisterModel(cpRepoPath string, spec *ServeSpec, inst *Instance) error {
	path := ModelsPath(cpRepoPath)
	models, err := readModels(path)
	if err != nil {
		return err
	}

	entry := map[string]json.RawMessage{}
	if existing, ok := models[spec.ModelID]; ok {
		if err := json.Unmarshal(existing, &entry); err != nil {
			// An entry that is not an object is not something to merge
			// into; replace it rather than fail, since the model is now
			// genuinely served from a known endpoint.
			entry = map[string]json.RawMessage{}
		}
	}

	set := func(key string, value interface{}) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		entry[key] = raw
		return nil
	}

	if err := set("endpoint", inst.Endpoint); err != nil {
		return err
	}
	if err := set("category", spec.category()); err != nil {
		return err
	}
	if spec.GPUMemory > 0 {
		if err := set("gpu_memory", spec.GPUMemory); err != nil {
			return err
		}
	}
	if spec.ContextLength > 0 {
		if err := set("context_length", spec.ContextLength); err != nil {
			return err
		}
	}

	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	models[spec.ModelID] = raw

	return writeModels(path, models)
}

// DeregisterModel removes a model's entry from models.json.
//
// The endpoint is checked first: if models.json has since been pointed at a
// different server for this model — by hand, or by a second manager — then
// this entry is not describing the container being stopped, and removing it
// would deregister something still running. In that case the file is left
// alone.
func DeregisterModel(cpRepoPath, modelID, endpoint string) error {
	path := ModelsPath(cpRepoPath)
	models, err := readModels(path)
	if err != nil {
		return err
	}
	raw, ok := models[modelID]
	if !ok {
		return nil
	}

	if endpoint != "" {
		var entry struct {
			Endpoint string `json:"endpoint"`
		}
		if err := json.Unmarshal(raw, &entry); err == nil && entry.Endpoint != "" && entry.Endpoint != endpoint {
			return nil
		}
	}

	delete(models, modelID)
	return writeModels(path, models)
}
