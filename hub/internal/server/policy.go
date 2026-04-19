package server

import (
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// Policy is the current resolved team policy. It's loaded from
// <dataRoot>/team/policy.yaml if present, otherwise a permissive default
// is used (everything is 'auto'). The file format is intentionally minimal
// — action→tier plus a list of approvers per tier. The real policy engine
// (Cedar/OPA) plugs in behind PolicyDecide().
//
// Example policy.yaml:
//
//	tiers:
//	  spawn: moderate
//	  tool:write_file: low
//	  tool:shell: critical
//	approvers:
//	  moderate: ["@steward", "@principal"]
//	  critical: ["@principal"]
//	quorum:
//	  moderate: 1
//	  critical: 1
type Policy struct {
	Tiers     map[string]string   `yaml:"tiers"`
	Approvers map[string][]string `yaml:"approvers"`
	Quorum    map[string]int      `yaml:"quorum"`
}

const (
	TierAuto     = "auto"
	TierLow      = "low"
	TierModerate = "moderate"
	TierCritical = "critical"
)

type policyStore struct {
	mu     sync.RWMutex
	path   string
	loaded *Policy
}

func newPolicyStore(dataRoot string) *policyStore {
	p := &policyStore{path: filepath.Join(dataRoot, "team", "policy.yaml")}
	p.reload()
	return p
}

func (p *policyStore) reload() {
	p.mu.Lock()
	defer p.mu.Unlock()
	data, err := os.ReadFile(p.path)
	if err != nil {
		p.loaded = &Policy{} // missing file → permissive
		return
	}
	var out Policy
	if err := yaml.Unmarshal(data, &out); err != nil {
		// Keep the last-known-good policy if the file is malformed. A
		// misparse shouldn't brick approvals.
		return
	}
	p.loaded = &out
}

func (p *policyStore) get() *Policy {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.loaded == nil {
		return &Policy{}
	}
	cp := *p.loaded
	return &cp
}

// Decide returns the tier for a given action key (e.g. "spawn",
// "tool:write_file"). Falls back to "auto" when unspecified.
func (p *policyStore) Decide(action string) string {
	pol := p.get()
	if t, ok := pol.Tiers[action]; ok && t != "" {
		return t
	}
	return TierAuto
}

// ApproversFor returns the handle list configured to approve a tier.
func (p *policyStore) ApproversFor(tier string) []string {
	return p.get().Approvers[tier]
}

func (p *policyStore) QuorumFor(tier string) int {
	pol := p.get()
	if n, ok := pol.Quorum[tier]; ok && n > 0 {
		return n
	}
	return 1
}
