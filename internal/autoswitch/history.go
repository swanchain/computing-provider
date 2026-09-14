package autoswitch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// History is the scheduler's memory.
//
// Every method is nil-safe. A scheduler whose history could not be loaded must
// refuse to switch — which EvaluateWithHistory makes it do — rather than crash
// the daemon it is running inside. It is what makes the three rules a single
// cycle cannot judge — dwell, consecutive agreement, and the daily cap —
// judgeable at all.
//
// Persisted, because all three are defeated by a restart. A node that forgot
// its last switch on every restart would honour no dwell time, and a crash loop
// would turn into a switch loop.
type History struct {
	// Switches are the switches that were executed, newest last.
	Switches []SwitchRecord `json:"switches"`

	// Proposals are the recent allowed proposals, used to decide whether
	// enough consecutive cycles agree. Only the last few matter.
	Proposals []ProposalRecord `json:"proposals"`

	path string
	mu   sync.Mutex
}

// SwitchRecord is one executed switch and what it was worth.
type SwitchRecord struct {
	At              time.Time `json:"at"`
	Served          []string  `json:"served"`
	Stopped         []string  `json:"stopped"`
	Reason          string    `json:"reason"`
	ProjectedDayUSD float64   `json:"projected_daily_usd"`

	// BaselineDayUSD is what the node was earning when the switch was made,
	// so the outcome can be judged against the right starting point.
	BaselineDayUSD float64 `json:"baseline_daily_usd"`
}

// ProposalRecord is one cycle's decision, kept so consecutive agreement can be
// measured.
type ProposalRecord struct {
	At     time.Time `json:"at"`
	Action string    `json:"action"`

	// Fingerprint identifies the plan, so "agreeing" means proposing the same
	// change rather than merely proposing some change. Two cycles wanting
	// different models are not agreement, and treating them as such is how a
	// node flaps while appearing to deliberate.
	Fingerprint string `json:"fingerprint"`
}

// proposalsKept bounds the proposal log. Only the most recent few are ever
// consulted, and an unbounded list would grow forever on a node that never
// switches.
const proposalsKept = 16

// switchesKept bounds the switch log at rather more than a day's worth, since
// the daily cap is the only rule that reads back through it.
const switchesKept = 64

// LoadHistory reads the scheduler's memory, returning an empty one when there
// is nothing to read.
//
// A corrupt file is an error rather than a silent reset: starting from an empty
// history would quietly discard the dwell time and the daily cap, which is the
// state that exists to stop the node acting too often.
func LoadHistory(cpRepoPath string) (*History, error) {
	path := filepath.Join(cpRepoPath, "autoswitch-history.json")
	h := &History{path: path}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return h, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, h); err != nil {
		return nil, fmt.Errorf("%s is unreadable: %w", path, err)
	}
	h.path = path
	return h, nil
}

// Save writes the history atomically, so a crash mid-write cannot leave a file
// that the next start refuses to parse.
func (h *History) Save() error {
	if h == nil || h.path == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.Proposals) > proposalsKept {
		h.Proposals = h.Proposals[len(h.Proposals)-proposalsKept:]
	}
	if len(h.Switches) > switchesKept {
		h.Switches = h.Switches[len(h.Switches)-switchesKept:]
	}

	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(h.path), ".autoswitch-history.*")
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
	return os.Rename(name, h.path)
}

// RecordProposal notes what a cycle decided.
func (h *History) RecordProposal(now time.Time, decision *Decision) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Proposals = append(h.Proposals, ProposalRecord{
		At:          now,
		Action:      decision.Action,
		Fingerprint: Fingerprint(decision),
	})
}

// RecordSwitch notes a switch that was executed.
func (h *History) RecordSwitch(now time.Time, decision *Decision, baselineUSD float64) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Switches = append(h.Switches, SwitchRecord{
		At:              now,
		Served:          decision.Serve,
		Stopped:         decision.Stop,
		Reason:          decision.Reason,
		ProjectedDayUSD: decision.ExpectedDailyUSD,
		BaselineDayUSD:  baselineUSD,
	})
}

// LastSwitch returns the most recent switch, or nil.
func (h *History) LastSwitch() *SwitchRecord {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.Switches) == 0 {
		return nil
	}
	last := h.Switches[len(h.Switches)-1]
	return &last
}

// SwitchesSince counts switches made after a point in time.
func (h *History) SwitchesSince(t time.Time) int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, s := range h.Switches {
		if s.At.After(t) {
			n++
		}
	}
	return n
}

// ConsecutiveAgreement counts how many of the most recent proposals, ending
// with the latest, carry the same fingerprint.
//
// Counted backwards from the newest and stopping at the first disagreement: a
// plan proposed, abandoned, and proposed again is not two consecutive cycles of
// agreement, and treating it as such defeats the rule.
func (h *History) ConsecutiveAgreement(fingerprint string) int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	n := 0
	for i := len(h.Proposals) - 1; i >= 0; i-- {
		if h.Proposals[i].Fingerprint != fingerprint {
			break
		}
		n++
	}
	return n
}

// Fingerprint identifies a plan by what it would change.
//
// Only the models matter: the same switch proposed with a differently-worded
// reason, or a projection that moved by a cent, is the same plan. Fingerprinting
// the prose would mean a planner never agreed with itself twice.
func Fingerprint(d *Decision) string {
	if d == nil {
		return ""
	}
	if d.Action != ActionSwitch {
		return ActionKeep
	}
	serve := append([]string{}, dedupe(d.Serve)...)
	stop := append([]string{}, dedupe(d.Stop)...)
	sortStrings(serve)
	sortStrings(stop)
	return fmt.Sprintf("switch|serve=%v|stop=%v", serve, stop)
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// writeFile is a small helper used by tests to plant a history file.
func writeFile(dir, name, body string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600)
}
