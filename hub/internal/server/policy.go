package server

import (
	"fmt"
	"log/slog"
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
// Example policy.yaml (note: `kinds:` is the ADR-030 governed-actions
// block — see KindPolicy below):
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
//	kinds:
//	  deliverable.set_state:
//	    default_tier: principal
//	    commits: true
//	    override_allowed: true
//	    quorum:
//	      principal: { m: 1 }
//	  task.set_status:
//	    default_tier: project-steward
//	    commits: false
//	    override_allowed: true
type Policy struct {
	Tiers     map[string]string           `yaml:"tiers"`
	Approvers map[string][]string         `yaml:"approvers"`
	Quorum    map[string]int              `yaml:"quorum"`
	Escalate  map[string]EscalationPolicy `yaml:"escalation"`
	// Kinds is the ADR-030 governed-actions block. Keys are propose
	// kinds (e.g. "deliverable.set_state", "task.set_status"); values
	// describe the addressing + quorum + override + escalation policy
	// for that kind. Missing key → permissive default (see KindFor).
	Kinds map[string]KindPolicy `yaml:"kinds"`
}

// KindPolicy is the per-(propose-kind) policy block from ADR-030.
// Read by the propose handler (W4) at attention-row insert time and by
// the decide handler (W5/W6/W7/W8) at approve/reject time. Every field
// is optional in the YAML; the parser populates zero values and KindFor
// applies the documented permissive default for the kind as a whole if
// the key is absent.
type KindPolicy struct {
	// DefaultTier is the addressee tier when the caller doesn't pin one
	// via the propose verb's `addressee_tier` argument. One of
	// 'worker' | 'project-steward' | 'general-steward' | 'principal'.
	// Empty → permissive default of "principal" (the safest fall-through:
	// route to the human director if the policy is silent).
	DefaultTier string `yaml:"default_tier" json:"default_tier"`
	// Quorum is per-tier M-of-N. Each entry's M is the number of
	// approvals required to resolve the row at that tier. Empty or
	// missing → 1.
	Quorum map[string]QuorumPolicy `yaml:"quorum" json:"quorum,omitempty"`
	// Commits flips true for kinds whose apply function mutates the
	// canonical project record (deliverable state, project phase). The
	// distinction matters for audit emphasis and for the MVP rule that
	// non-commit kinds skip the lint-governed-actions same-tier check.
	Commits bool `yaml:"commits" json:"commits"`
	// OverrideAllowed governs whether a principal can override a
	// lower-tier resolution on this kind (ADR-030 D-8 + W9). Default
	// false; only kinds whose apply functions have a defined rollback
	// path should opt in.
	OverrideAllowed bool `yaml:"override_allowed" json:"override_allowed"`
	// EscalateOnReject (post-MVP) — fire ADR-034 escalation signal
	// when this kind's row is rejected. MVP: always false.
	EscalateOnReject bool `yaml:"escalate_on_reject" json:"escalate_on_reject"`
	// EscalateOnTimeout (post-MVP) — fire ADR-034 escalation signal
	// when this kind's row sits past `inactivity_deadline`. MVP:
	// always false. Linter (W3) requires DefaultTier strictly below
	// `principal` when true, so the signal has somewhere to walk.
	EscalateOnTimeout bool `yaml:"escalate_on_timeout" json:"escalate_on_timeout"`
}

// QuorumPolicy is the per-tier quorum entry inside KindPolicy.Quorum.
// Wrapped in its own type so future M-of-N extensions (e.g. N>=M)
// don't require a YAML-shape migration.
type QuorumPolicy struct {
	M int `yaml:"m" json:"m"`
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

// Governance tiers — the ADR-030 ladder, distinct from the legacy
// significance tiers (auto/low/moderate/critical) above. The two
// vocabularies coexist: legacy tiers gate the `tool:*` / `spawn` /
// `template_propose` rows; governance tiers gate the `propose` rows.
// Migration 0045 enforces the value set on `attention_items.assigned_tier`
// via CHECK constraint.
const (
	GovTierWorker         = "worker"
	GovTierProjectSteward = "project-steward"
	GovTierGeneralSteward = "general-steward"
	GovTierPrincipal      = "principal"
)

// permissiveKindPolicy is the fall-through KindPolicy returned by
// KindFor when a kind isn't configured. Routes to principal with M=1,
// allows override (so the principal can self-correct in either
// direction), and leaves both escalation flags off. Documented at
// ADR-030 plan §2.2 W2.
func permissiveKindPolicy() KindPolicy {
	return KindPolicy{
		DefaultTier:       GovTierPrincipal,
		Quorum:            map[string]QuorumPolicy{GovTierPrincipal: {M: 1}},
		OverrideAllowed:   true,
		EscalateOnReject:  false,
		EscalateOnTimeout: false,
	}
}

type policyStore struct {
	mu     sync.RWMutex
	path   string
	loaded *Policy
	log    *slog.Logger
}

func newPolicyStore(dataRoot string) *policyStore {
	return newPolicyStoreWithLogger(dataRoot, slog.Default())
}

// newPolicyStoreWithLogger constructs a store with an explicit logger
// — used by the server constructor so KindFor's permissive-default WARN
// lands in the daemon's structured log. Tests pass a discard logger to
// keep test output quiet.
func newPolicyStoreWithLogger(dataRoot string, log *slog.Logger) *policyStore {
	if log == nil {
		log = slog.Default()
	}
	p := &policyStore{path: filepath.Join(dataRoot, "team", "policy.yaml"), log: log}
	p.reload()
	return p
}

// parsePolicy parses the raw policy.yaml bytes. Surfaced as a free
// function so tests can assert typed errors without poking at
// policyStore's last-known-good fallback in reload. Callers that want
// the structural-error-but-keep-running runtime semantic should use
// policyStore.reload instead.
func parsePolicy(data []byte) (*Policy, error) {
	var out Policy
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("policy.yaml: %w", err)
	}
	return &out, nil
}

func (p *policyStore) reload() {
	p.mu.Lock()
	defer p.mu.Unlock()
	data, err := os.ReadFile(p.path)
	if err != nil {
		p.loaded = &Policy{} // missing file → permissive
		return
	}
	parsed, err := parsePolicy(data)
	if err != nil {
		// Keep the last-known-good policy if the file is malformed. A
		// misparse shouldn't brick approvals; the operator's next
		// SIGHUP after the fix re-loads it.
		p.log.Warn("policy.yaml parse failed; keeping last-known-good",
			"path", p.path, "err", err)
		return
	}
	p.loaded = parsed
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

// KindFor returns the per-kind governed-actions policy for `kind`,
// falling back to a permissive default (route to principal, M=1,
// override allowed, escalation off) and logging a WARN when the kind
// is not configured. The bool reports whether the kind was found in
// the file — callers that want to differentiate "configured" from
// "default" can branch on it; callers that just want a usable policy
// can ignore it.
//
// Read by the propose handler (W4) and the decide handler. Called on
// every governed-action MCP call; the get() RLock makes it safe under
// SIGHUP reload.
func (p *policyStore) KindFor(kind string) (KindPolicy, bool) {
	pol := p.get()
	if k, ok := pol.Kinds[kind]; ok {
		return k, true
	}
	p.log.Warn("propose kind not configured in policy.yaml; using permissive default",
		"kind", kind, "default_tier", GovTierPrincipal)
	return permissiveKindPolicy(), false
}
