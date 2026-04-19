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
	Tiers     map[string]string           `yaml:"tiers"`
	Approvers map[string][]string         `yaml:"approvers"`
	Quorum    map[string]int              `yaml:"quorum"`
	Escalate  map[string]EscalationPolicy `yaml:"escalation"`
}

// EscalationPolicy describes how to widen an attention item that hasn't been
// resolved in time. After `After` elapses since created_at, the item's
// current_assignees is replaced with `WidenTo` and an entry is appended to
// escalation_history. One escalation per item — if a human still hasn't
// acted after that, the item sits open until they do.
type EscalationPolicy struct {
	After   string   `yaml:"after"`    // duration string, e.g. "5m"
	WidenTo []string `yaml:"widen_to"` // new assignee handle list
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

// EscalationFor returns the escalation rule for a tier, or (_, false) if
// the tier has no rule configured — in which case open items in that tier
// simply stay open until a human acts.
func (p *policyStore) EscalationFor(tier string) (EscalationPolicy, bool) {
	pol := p.get()
	rule, ok := pol.Escalate[tier]
	if !ok || rule.After == "" || len(rule.WidenTo) == 0 {
		return EscalationPolicy{}, false
	}
	return rule, true
}
