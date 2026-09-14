package modelmem

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// StoreFile is the machine-readable source of truth.
const StoreFile = "model-memory.json"

// MarkdownFile is a rendering of the store for people and for agents that read
// prose. It is generated on every write and never read back, so it cannot drift
// from the store and cannot be corrupted by being edited.
const MarkdownFile = "cp.md"

// Store is the node's model memory, persisted to $CP_PATH.
type Store struct {
	Records map[string]*Record `json:"records"`

	dir  string
	node NodeContext
	mu   sync.Mutex
}

// Load reads the store, returning an empty one when there is nothing to read.
//
// A corrupt file is an error rather than a silent reset. This is the record of
// configurations that took real effort to find; discarding it quietly because
// one byte went wrong is not an acceptable failure mode.
func Load(cpRepoPath string) (*Store, error) {
	s := &Store{Records: map[string]*Record{}, dir: cpRepoPath}

	data, err := os.ReadFile(filepath.Join(cpRepoPath, StoreFile))
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("%s is unreadable: %w", StoreFile, err)
	}
	if s.Records == nil {
		s.Records = map[string]*Record{}
	}
	s.dir = cpRepoPath
	return s, nil
}

// Get returns what the node remembers about a model.
func (s *Store) Get(modelID string) *Record {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Records[modelID]
}

// All returns every record, ordered by model ID.
func (s *Store) All() []*Record {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*Record, 0, len(s.Records))
	for _, r := range s.Records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModelID < out[j].ModelID })
	return out
}

// RecordSuccess notes that a model ran, merging into whatever was already
// known about it.
//
// Merging rather than replacing, because the caller usually knows only part of
// the story. A start that did not measure VRAM must not erase a measurement
// taken last week, and one that did not know the quantisation must not blank a
// field the operator filled in by hand.
func (s *Store) RecordSuccess(now time.Time, update Record) *Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing := s.Records[update.ModelID]
	if existing == nil {
		existing = &Record{ModelID: update.ModelID, FirstSeen: now}
		s.Records[update.ModelID] = existing
	}

	if existing.Pinned {
		// The operator has said this record is correct. Count the success and
		// leave everything else alone.
		existing.Successes++
		existing.LastSeen = now
		return existing
	}

	existing.Status = StatusWorking
	existing.LastSeen = now
	existing.Successes++
	if update.Note != "" {
		existing.Note = update.Note
	}

	mergeWeights(&existing.Weights, update.Weights)
	mergeServing(&existing.Serving, update.Serving)
	if update.Hardware.GPUModel != "" {
		existing.Hardware = update.Hardware
	}
	if update.Measured != nil {
		existing.Measured = update.Measured
	}
	return existing
}

// RecordFailure notes that a model did not run.
//
// A failure never erases a measurement that succeeded before. The most likely
// cause of one failed start is something transient — an upstream hiccup, a busy
// GPU — and throwing away a known-good configuration over it would make the
// memory worse than useless.
func (s *Store) RecordFailure(now time.Time, modelID, note string) *Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing := s.Records[modelID]
	if existing == nil {
		existing = &Record{ModelID: modelID, FirstSeen: now}
		s.Records[modelID] = existing
	}

	existing.Failures++
	existing.LastSeen = now
	if note != "" {
		existing.Note = note
	}

	// Only a model that has never worked here is marked failed. One that has
	// keeps its status and its measurement, with the failure counted.
	if existing.Successes == 0 && !existing.Pinned {
		existing.Status = StatusFailed
	}
	return existing
}

// Forget removes a model.
func (s *Store) Forget(modelID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Records[modelID]; !ok {
		return false
	}
	delete(s.Records, modelID)
	return true
}

// Pin marks a record as the operator's, so automatic writes leave it alone.
func (s *Store) Pin(modelID string, pinned bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.Records[modelID]
	if !ok {
		return false
	}
	r.Pinned = pinned
	return true
}

// Save writes the store and regenerates the markdown view.
func (s *Store) Save() error {
	if s == nil || s.dir == "" {
		return nil
	}
	s.mu.Lock()
	data, err := json.MarshalIndent(s, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}

	if err := writeAtomic(filepath.Join(s.dir, StoreFile), append(data, '\n')); err != nil {
		return err
	}

	// Regenerated from the store on every write, so the two can never
	// disagree. Nothing reads it back.
	return writeAtomic(filepath.Join(s.dir, MarkdownFile), []byte(s.Markdown(s.node)))
}

// SetNodeContext records what the agent should know about the machine, for the
// next time cp.md is written.
func (s *Store) SetNodeContext(node NodeContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.node = node
}

// writeAtomic replaces a file in one step, so a crash mid-write cannot leave a
// file the next start refuses to parse.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func mergeWeights(dst *Weights, src Weights) {
	if src.Repo != "" {
		dst.Repo = src.Repo
	}
	if src.Revision != "" {
		dst.Revision = src.Revision
	}
	if src.URL != "" {
		dst.URL = src.URL
	}
	if src.LocalPath != "" {
		dst.LocalPath = src.LocalPath
	}
	if src.Format != "" {
		dst.Format = src.Format
	}
	if src.Quantisation != "" {
		dst.Quantisation = src.Quantisation
	}
	if src.SizeBytes > 0 {
		dst.SizeBytes = src.SizeBytes
	}
}

func mergeServing(dst *Serving, src Serving) {
	if src.Backend != "" {
		dst.Backend = src.Backend
	}
	if src.Image != "" {
		dst.Image = src.Image
	}
	if src.GPUs != "" {
		dst.GPUs = src.GPUs
	}
	if src.ContextLength > 0 {
		dst.ContextLength = src.ContextLength
	}
	if src.Concurrency > 0 {
		dst.Concurrency = src.Concurrency
	}
	if src.KVQuant != "" {
		dst.KVQuant = src.KVQuant
	}
	if len(src.ExtraArgs) > 0 {
		dst.ExtraArgs = src.ExtraArgs
	}
}
