package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ConfigPath returns the path of the config.toml in a repo directory.
func ConfigPath(cpRepoPath string) string {
	return filepath.Join(cpRepoPath, "config.toml")
}

var (
	// A table header at the start of a line: "[Inference]", "[API]", and so on.
	tomlTableRe = regexp.MustCompile(`(?m)^\s*\[[^\]]+\]\s*$`)
	// The Models key, up to the opening bracket. The array may then run onto
	// further lines, so the closing bracket is found by scanning, not by regex.
	modelsKeyRe = regexp.MustCompile(`(?m)^([ \t]*)Models[ \t]*=[ \t]*\[`)
)

// SyncConfigModels rewrites the Models list under [Inference] in config.toml so
// it names exactly the models in models.json.
//
// The two files disagreeing is what the self-check reports as "config/models.json
// agreement". models.json is the file that actually drives routing — the daemon
// watches it with fsnotify and registers from it — but config.toml carries the
// list the audit compares against, and nothing else keeps it in step. Without
// this, every `models serve` and `models stop` leaves the node one model further
// out of agreement with itself.
//
// Only an existing `Models = [...]` inside an existing [Inference] table is
// rewritten. A config.toml that never declared the key is left untouched rather
// than having one invented: this edits an operator's file, and adding keys they
// did not write is not this function's business.
//
// The array is replaced in place, so comments, key order and formatting
// elsewhere in the file survive. Callers should treat a failure here as a
// warning: the server is already running and models.json already points at it,
// so a stale list in config.toml is a hygiene problem, not a serving fault.
func SyncConfigModels(cpRepoPath string) error {
	models, err := readModels(ModelsPath(cpRepoPath))
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(models))
	for id := range models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return writeConfigModels(ConfigPath(cpRepoPath), ids)
}

func writeConfigModels(path string, ids []string) error {
	original, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No config.toml at all: nothing to keep in step.
			return nil
		}
		return err
	}
	updated, changed, err := replaceInferenceModels(string(original), ids)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	// Written via a temp file and renamed so an interrupted write cannot leave
	// the operator with a truncated config.toml, which the daemon would fail to
	// parse on its next start.
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config.toml.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(updated); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// replaceInferenceModels returns the config with the [Inference] Models array
// set to ids, and whether anything changed.
func replaceInferenceModels(config string, ids []string) (string, bool, error) {
	start, end, ok := inferenceSection(config)
	if !ok {
		return config, false, nil
	}
	section := config[start:end]

	loc := modelsKeyRe.FindStringSubmatchIndex(section)
	if loc == nil {
		return config, false, nil
	}
	indent := section[loc[2]:loc[3]]

	// Scan from the opening bracket for its match. Model IDs cannot contain a
	// bracket, so a depth count over the raw text is enough here.
	openAt := loc[1] - 1
	depth := 0
	closeAt := -1
	for i := openAt; i < len(section); i++ {
		switch section[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				closeAt = i
			}
		}
		if closeAt >= 0 {
			break
		}
	}
	if closeAt < 0 {
		return config, false, fmt.Errorf("config.toml: unterminated Models array under [Inference]")
	}

	quoted := make([]string, 0, len(ids))
	for _, id := range ids {
		quoted = append(quoted, fmt.Sprintf("%q", id))
	}
	replacement := indent + "Models = [" + strings.Join(quoted, ", ") + "]"

	newSection := section[:loc[0]] + replacement + section[closeAt+1:]
	if newSection == section {
		return config, false, nil
	}
	return config[:start] + newSection + config[end:], true, nil
}

// inferenceSection locates the body of the [Inference] table: everything after
// its header up to the next table header. [Inference.AutoSwitch] is a separate
// table and so ends the span, which is what we want — its keys are not ours.
func inferenceSection(config string) (int, int, bool) {
	headers := tomlTableRe.FindAllStringIndex(config, -1)
	for i, h := range headers {
		if strings.TrimSpace(config[h[0]:h[1]]) != "[Inference]" {
			continue
		}
		start := h[1]
		end := len(config)
		if i+1 < len(headers) {
			end = headers[i+1][0]
		}
		return start, end, true
	}
	return 0, 0, false
}
